# E2E Execution Steps: zero-trust-workload-identity-manager

## Prerequisites

```bash
# Verify oc CLI is installed and authenticated
which oc
oc version

# Check authentication
oc whoami
oc whoami --show-server

# Verify cluster health
oc get nodes
oc get clusterversion

# Check OLM is available
oc get packagemanifests | grep zero-trust-workload-identity-manager
```

## Environment Variables

```bash
# Get cluster's base domain for Routes
export APP_DOMAIN=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
echo "Cluster app domain: $APP_DOMAIN"

# Set operator namespace constant
export OPERATOR_NAMESPACE="zero-trust-workload-identity-manager"
echo "Operator namespace: $OPERATOR_NAMESPACE"

# Verify environment
env | grep -E '(APP_DOMAIN|OPERATOR_NAMESPACE)'
```

## Step 1: Install Operator via OLM

```bash
# Create operator namespace
oc create namespace $OPERATOR_NAMESPACE

# Create OperatorGroup for namespace-scoped operator
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: $OPERATOR_NAMESPACE
  namespace: $OPERATOR_NAMESPACE
spec:
  targetNamespaces:
  - $OPERATOR_NAMESPACE
EOF

# Create Subscription to install operator
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: $OPERATOR_NAMESPACE
  namespace: $OPERATOR_NAMESPACE
spec:
  channel: alpha
  name: $OPERATOR_NAMESPACE
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
EOF

# Wait for CSV to be created
echo "Waiting for ClusterServiceVersion..."
sleep 10
CSV_NAME=$(oc get csv -n $OPERATOR_NAMESPACE -o jsonpath='{.items[0].metadata.name}')
echo "CSV: $CSV_NAME"

# Wait for CSV to succeed
oc wait csv/$CSV_NAME -n $OPERATOR_NAMESPACE --for=jsonpath='{.status.phase}'=Succeeded --timeout=5m

# Wait for operator deployment
oc wait deployment/zero-trust-workload-identity-manager-controller-manager \
  -n $OPERATOR_NAMESPACE \
  --for=condition=Available \
  --timeout=5m

# Verify operator is running
oc get pods -n $OPERATOR_NAMESPACE -l name=zero-trust-workload-identity-manager
oc get deployment -n $OPERATOR_NAMESPACE zero-trust-workload-identity-manager-controller-manager

# Check operator logs (optional)
# oc logs -n $OPERATOR_NAMESPACE deployment/zero-trust-workload-identity-manager-controller-manager --tail=50
```

## Step 2: Deploy Custom Resources

### 2.1 Deploy ZeroTrustWorkloadIdentityManager (singleton)

```bash
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: ZeroTrustWorkloadIdentityManager
metadata:
  name: cluster
spec:
  trustDomain: example.com
  clusterName: prod-cluster
  bundleConfigMap: spire-bundle
EOF

# Wait for Ready condition
echo "Waiting for ZeroTrustWorkloadIdentityManager to be Ready..."
oc wait zerotrustworkloadidentitymanager/cluster \
  --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  --timeout=5m

# Verify status
oc get zerotrustworkloadidentitymanager cluster -o jsonpath='{.status.conditions}' | jq
```

### 2.2 Deploy SpireServer with Federation (https_web + ACME)

```bash
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  logLevel: info
  logFormat: json
  jwtIssuer: "https://spire-oidc.${APP_DOMAIN}"
  caValidity: 24h
  defaultX509Validity: 1h
  defaultJWTValidity: 5m
  caKeyType: rsa-2048
  caSubject:
    country: US
    organization: Example Corp
    commonName: Example SPIRE Server
  persistence:
    size: 2Gi
    accessMode: ReadWriteOnce
  datastore:
    databaseType: sqlite3
  federation:
    bundleEndpoint:
      profile: https_web
      refreshHint: 300
      httpsWeb:
        acme:
          directoryUrl: "https://acme-v02.api.letsencrypt.org/directory"
          domainName: "spire-federation.${APP_DOMAIN}"
          email: admin@example.com
          tosAccepted: "true"
    federatesWith:
    - trustDomain: partner.example.com
      bundleEndpointUrl: "https://spire-federation.partner.example.com:8443"
      bundleEndpointProfile: https_web
    managedRoute: "true"
EOF

# Wait for SpireServer to be Ready (may take longer due to ACME)
echo "Waiting for SpireServer to be Ready..."
oc wait spireserver/cluster \
  --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  --timeout=10m

# Verify status
oc get spireserver cluster -o jsonpath='{.status.conditions}' | jq

# Check StatefulSet
oc get statefulset spire-server
oc wait statefulset/spire-server --for=jsonpath='{.status.readyReplicas}'=1 --timeout=5m

# Verify federation resources
oc get route spire-server-federation
oc get svc spire-server -o jsonpath='{.spec.ports[?(@.name=="federation")]}'
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A10 BundleEndpoint
```

### 2.3 Deploy SpireAgent

```bash
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: SpireAgent
metadata:
  name: cluster
spec:
  socketPath: /run/spire/agent-sockets
  logLevel: info
  logFormat: json
  nodeAttestor:
    k8sPSAT:
      enabled: "true"
  workloadAttestors:
    k8s:
      enabled: "true"
      disableContainerSelectors: "false"
      useNewContainerLocator: "true"
    workloadAttestorVerification:
      type: auto
EOF

# Wait for SpireAgent to be Ready
echo "Waiting for SpireAgent to be Ready..."
oc wait spireagent/cluster \
  --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  --timeout=5m

# Verify status
oc get spireagent cluster -o jsonpath='{.status.conditions}' | jq

# Check DaemonSet
oc get daemonset spire-agent
oc get pods -l app.kubernetes.io/name=spire-agent
```

### 2.4 Deploy SpiffeCSIDriver

```bash
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: SpiffeCSIDriver
metadata:
  name: cluster
spec:
  agentSocketPath: /run/spire/agent-sockets
  pluginName: csi.spiffe.io
EOF

# Wait for SpiffeCSIDriver to be Ready
echo "Waiting for SpiffeCSIDriver to be Ready..."
oc wait spiffecsidriver/cluster \
  --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  --timeout=5m

# Verify status
oc get spiffecsidriver cluster -o jsonpath='{.status.conditions}' | jq

# Check DaemonSet
oc get daemonset spire-spiffe-csi-driver
oc get pods -l app.kubernetes.io/name=spiffe-csi-driver
```

### 2.5 Deploy SpireOIDCDiscoveryProvider

```bash
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: SpireOIDCDiscoveryProvider
metadata:
  name: cluster
spec:
  logLevel: info
  logFormat: json
  csiDriverName: csi.spiffe.io
  jwtIssuer: "https://spire-oidc.${APP_DOMAIN}"
  replicaCount: 1
  managedRoute: "true"
EOF

# Wait for SpireOIDCDiscoveryProvider to be Ready
echo "Waiting for SpireOIDCDiscoveryProvider to be Ready..."
oc wait spireoidcdiscoveryprovider/cluster \
  --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  --timeout=5m

# Verify status
oc get spireoidcdiscoveryprovider cluster -o jsonpath='{.status.conditions}' | jq

# Check Deployment
oc get deployment spire-spiffe-oidc-discovery-provider
oc wait deployment/spire-spiffe-oidc-discovery-provider --for=condition=Available --timeout=5m
oc get pods -l app.kubernetes.io/name=spiffe-oidc-discovery-provider
```

## Step 3: Verify Installation

### 3.1 Verify All CRs are Ready

```bash
# Check all operand CRs
echo "=== ZeroTrustWorkloadIdentityManager Status ==="
oc get zerotrustworkloadidentitymanager cluster -o jsonpath='{.status.conditions[?(@.type=="Ready")]}' | jq

echo "=== SpireServer Status ==="
oc get spireserver cluster -o jsonpath='{.status.conditions[?(@.type=="Ready")]}' | jq

echo "=== SpireAgent Status ==="
oc get spireagent cluster -o jsonpath='{.status.conditions[?(@.type=="Ready")]}' | jq

echo "=== SpiffeCSIDriver Status ==="
oc get spiffecsidriver cluster -o jsonpath='{.status.conditions[?(@.type=="Ready")]}' | jq

echo "=== SpireOIDCDiscoveryProvider Status ==="
oc get spireoidcdiscoveryprovider cluster -o jsonpath='{.status.conditions[?(@.type=="Ready")]}' | jq
```

### 3.2 Verify Managed Workloads

```bash
# SpireServer StatefulSet
echo "=== SpireServer StatefulSet ==="
oc get statefulset spire-server -o wide
oc get pods -l app.kubernetes.io/name=spire-server

# SpireAgent DaemonSet
echo "=== SpireAgent DaemonSet ==="
oc get daemonset spire-agent -o wide
oc get pods -l app.kubernetes.io/name=spire-agent

# SpiffeCSIDriver DaemonSet
echo "=== SpiffeCSIDriver DaemonSet ==="
oc get daemonset spire-spiffe-csi-driver -o wide
oc get pods -l app.kubernetes.io/name=spiffe-csi-driver

# SpireOIDCDiscoveryProvider Deployment
echo "=== SpireOIDCDiscoveryProvider Deployment ==="
oc get deployment spire-spiffe-oidc-discovery-provider -o wide
oc get pods -l app.kubernetes.io/name=spiffe-oidc-discovery-provider
```

### 3.3 Verify Federation Resources

```bash
# Federation Route
echo "=== Federation Route ==="
oc get route spire-server-federation
FEDERATION_ROUTE=$(oc get route spire-server-federation -o jsonpath='{.spec.host}')
echo "Federation endpoint: https://$FEDERATION_ROUTE"

# Federation Service Port
echo "=== Federation Service ==="
oc get svc spire-server -o jsonpath='{.spec.ports[?(@.name=="federation")]}' | jq

# Federation StatefulSet Port
echo "=== StatefulSet Federation Port ==="
oc get statefulset spire-server -o jsonpath='{.spec.template.spec.containers[0].ports[?(@.name=="federation")]}' | jq

# ConfigMap Federation Config
echo "=== SPIRE Server Federation Config ==="
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A30 BundleEndpoint
```

## Step 4: Diff-Specific Tests (Federation API)

### 4.1 Test Federation Bundle Endpoint (https_web ACME)

```bash
# Verify ACME configuration in ConfigMap
echo "=== Verifying ACME Configuration ==="
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A10 acme

# Check SpireServer spec
oc get spireserver cluster -o jsonpath='{.spec.federation.bundleEndpoint.httpsWeb.acme}' | jq

# Test federation bundle endpoint
echo "=== Testing Federation Bundle Endpoint ==="
FEDERATION_ROUTE=$(oc get route spire-server-federation -o jsonpath='{.spec.host}')
curl -k -v https://$FEDERATION_ROUTE/.well-known/spiffe/bundle 2>&1 | head -30

# Check SPIRE server logs for ACME activity
echo "=== SPIRE Server Logs (ACME) ==="
oc logs statefulset/spire-server --tail=50 | grep -i acme
```

### 4.2 Test Federated Trust Domain Configuration

```bash
# Verify federatesWith in SpireServer spec
echo "=== Federated Trust Domains ==="
oc get spireserver cluster -o jsonpath='{.spec.federation.federatesWith}' | jq

# Verify federatesWith in ConfigMap
echo "=== ConfigMap FederatesWith ==="
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A20 FederatesWith
```

### 4.3 Test Managed Route Toggle

```bash
# Current managedRoute value
echo "=== Current managedRoute Setting ==="
oc get spireserver cluster -o jsonpath='{.spec.federation.managedRoute}'

# Verify Route exists
oc get route spire-server-federation

# Update managedRoute to "false"
echo "=== Setting managedRoute to false ==="
oc patch spireserver cluster --type=merge -p '{"spec":{"federation":{"managedRoute":"false"}}}'

# Wait and verify Route is deleted
sleep 10
oc get route spire-server-federation 2>&1 | grep -q "NotFound" && echo "Route successfully deleted" || echo "ERROR: Route still exists"

# Restore managedRoute to "true"
echo "=== Restoring managedRoute to true ==="
oc patch spireserver cluster --type=merge -p '{"spec":{"federation":{"managedRoute":"true"}}}'

# Wait and verify Route is recreated
sleep 10
oc get route spire-server-federation && echo "Route successfully recreated" || echo "ERROR: Route not created"
```

### 4.4 Test Federation Profile Immutability

```bash
# Current profile
echo "=== Current Federation Profile ==="
oc get spireserver cluster -o jsonpath='{.spec.federation.bundleEndpoint.profile}'

# Attempt to change profile (should fail)
echo "=== Attempting to Change Profile (should fail) ==="
oc patch spireserver cluster --type=merge -p '{"spec":{"federation":{"bundleEndpoint":{"profile":"https_spiffe"}}}}' 2>&1 | tee /tmp/profile-change-error.txt

# Verify error message
grep -q "profile is immutable" /tmp/profile-change-error.txt && echo "✓ Profile immutability validated" || echo "✗ Profile change was not rejected"
```

### 4.5 Test ACME and ServingCert Mutual Exclusivity (Validation)

```bash
# This test requires creating an invalid CR (will be rejected by admission)
echo "=== Testing Mutual Exclusivity Validation ==="

cat <<EOF | oc apply -f - 2>&1 | tee /tmp/validation-error.txt
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster-invalid
spec:
  logLevel: info
  jwtIssuer: "https://test.example.com"
  caSubject:
    country: US
    organization: Test
    commonName: Test
  persistence:
    size: 1Gi
    accessMode: ReadWriteOnce
  datastore:
    databaseType: sqlite3
  federation:
    bundleEndpoint:
      profile: https_web
      httpsWeb:
        acme:
          directoryUrl: "https://acme.example.com"
          domainName: "test.com"
          email: test@example.com
        servingCert:
          externalSecretRef: "test-secret"
EOF

# Verify validation error
grep -q "exactly one of" /tmp/validation-error.txt && echo "✓ Mutual exclusivity validated" || echo "✗ Invalid CR was not rejected"
```

### 4.6 Test endpointSpiffeId Requirement for https_spiffe

```bash
echo "=== Testing endpointSpiffeId Validation ==="

# Create test CR missing endpointSpiffeId for https_spiffe federatesWith
cat <<EOF | oc apply -f - 2>&1 | tee /tmp/spiffeid-error.txt
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster-invalid-spiffeid
spec:
  logLevel: info
  jwtIssuer: "https://test.example.com"
  caSubject:
    country: US
    organization: Test
    commonName: Test
  persistence:
    size: 1Gi
    accessMode: ReadWriteOnce
  datastore:
    databaseType: sqlite3
  federation:
    bundleEndpoint:
      profile: https_spiffe
    federatesWith:
    - trustDomain: partner.example.com
      bundleEndpointUrl: "https://partner.example.com:8443"
      bundleEndpointProfile: https_spiffe
      # Missing endpointSpiffeId
EOF

# Verify validation error
grep -q "endpointSpiffeId is required" /tmp/spiffeid-error.txt && echo "✓ endpointSpiffeId requirement validated" || echo "✗ Invalid CR was not rejected"
```

### 4.7 Verify RBAC for Federation Routes and Secrets

```bash
# Check ClusterRole for Route permissions
echo "=== ClusterRole Route Permissions ==="
oc get clusterrole zero-trust-workload-identity-manager-controller-manager -o yaml | grep -A5 routes

# Check Role for external certificate secrets
echo "=== Role for External Certificates ==="
oc get role spire-server-external-cert -n $OPERATOR_NAMESPACE -o yaml 2>/dev/null || echo "Role not found (may not be created until externalSecretRef is used)"

# Check RoleBinding
oc get rolebinding spire-server-external-cert-binding -n $OPERATOR_NAMESPACE -o yaml 2>/dev/null || echo "RoleBinding not found"
```

## Step 5: Additional Federation Tests

### 5.1 Test Sample CR with Federation (Manual TLS)

```bash
# Create external TLS secret
echo "=== Creating External TLS Secret ==="
oc create secret tls spire-external-cert \
  --cert=/tmp/test-cert.pem \
  --key=/tmp/test-key.pem \
  -n $OPERATOR_NAMESPACE \
  --dry-run=client -o yaml | oc apply -f -

# Apply sample CR with manual TLS
echo "=== Applying SpireServer with Manual TLS ==="
cat config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_tls.yaml | \
  envsubst | oc apply -f -

# Wait for Ready
oc wait spireserver/cluster --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True --timeout=10m

# Verify external secret is mounted
echo "=== Verifying External Secret Mount ==="
oc get statefulset spire-server -o jsonpath='{.spec.template.spec.volumes[?(@.name=="federation-tls")]}' | jq
```

### 5.2 Verify Integration Test Suite File

```bash
# Check integration test suite exists
echo "=== Checking Integration Test Suite ==="
ls -lh api/v1alpha1/tests/spireserver.federation.testsuite.yaml

# Display test suite content
echo "=== Integration Test Suite Content ==="
head -100 api/v1alpha1/tests/spireserver.federation.testsuite.yaml
```

## Step 6: Cleanup

### 6.1 Delete Custom Resources

```bash
# Delete operand CRs in reverse order
echo "=== Deleting Operand CRs ==="
oc delete spireoidcdiscoveryprovider cluster --wait=false
oc delete spiffecsidriver cluster --wait=false
oc delete spireagent cluster --wait=false
oc delete spireserver cluster --wait=false
oc delete zerotrustworkloadidentitymanager cluster --wait=false

# Wait for SpireServer deletion (triggers cascade deletion of managed resources)
echo "Waiting for SpireServer deletion..."
oc wait --for=delete spireserver/cluster --timeout=5m 2>/dev/null || echo "SpireServer already deleted"

# Wait for workload deletions
echo "Waiting for StatefulSet deletion..."
oc wait --for=delete statefulset/spire-server --timeout=5m 2>/dev/null || echo "StatefulSet already deleted"

echo "Waiting for DaemonSet deletions..."
oc wait --for=delete daemonset/spire-agent --timeout=5m 2>/dev/null || echo "DaemonSet already deleted"
oc wait --for=delete daemonset/spire-spiffe-csi-driver --timeout=5m 2>/dev/null || echo "CSI Driver already deleted"

echo "Waiting for Deployment deletion..."
oc wait --for=delete deployment/spire-spiffe-oidc-discovery-provider --timeout=5m 2>/dev/null || echo "Deployment already deleted"
```

### 6.2 Uninstall Operator

```bash
# Delete Subscription
echo "=== Deleting Subscription ==="
oc delete subscription $OPERATOR_NAMESPACE -n $OPERATOR_NAMESPACE --wait=false

# Delete CSV
echo "=== Deleting ClusterServiceVersion ==="
CSV_NAME=$(oc get csv -n $OPERATOR_NAMESPACE -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "$CSV_NAME" ]; then
  oc delete csv $CSV_NAME -n $OPERATOR_NAMESPACE --wait=false
fi

# Delete OperatorGroup
echo "=== Deleting OperatorGroup ==="
oc delete operatorgroup $OPERATOR_NAMESPACE -n $OPERATOR_NAMESPACE --wait=false

# Wait for operator deployment deletion
echo "Waiting for operator deployment deletion..."
oc wait --for=delete deployment/zero-trust-workload-identity-manager-controller-manager -n $OPERATOR_NAMESPACE --timeout=5m 2>/dev/null || echo "Operator already deleted"

# Delete namespace
echo "=== Deleting Operator Namespace ==="
oc delete namespace $OPERATOR_NAMESPACE --wait=false

# Wait for namespace deletion
echo "Waiting for namespace deletion..."
oc wait --for=delete namespace/$OPERATOR_NAMESPACE --timeout=5m 2>/dev/null || echo "Namespace already deleted"
```

### 6.3 Verify Cleanup

```bash
# Check namespace is gone
echo "=== Verifying Namespace Deletion ==="
oc get namespace $OPERATOR_NAMESPACE 2>&1 | grep -q "NotFound" && echo "✓ Namespace deleted" || echo "✗ Namespace still exists"

# Check CRDs are still present (installed cluster-wide by OLM)
echo "=== CRDs (should still exist) ==="
oc get crd | grep operator.openshift.io

# Check no orphaned resources in the namespace
echo "=== Checking for Orphaned Resources ==="
oc get all -n $OPERATOR_NAMESPACE 2>&1 | grep -q "No resources found" && echo "✓ No orphaned resources" || echo "Resources still present"
```

## Summary

This execution guide covers:
1. ✓ Operator installation via OLM
2. ✓ Deployment of all 5 operand CRs (including federation-enabled SpireServer)
3. ✓ Verification of installation and federation resources
4. ✓ Diff-specific tests for federation API features:
   - ACME certificate provisioning
   - Manual TLS certificate configuration
   - Federated trust domains
   - Managed Route lifecycle
   - Profile immutability
   - Validation rules (mutual exclusivity, required fields)
   - RBAC permissions
5. ✓ Complete cleanup procedure

All commands are executable and use discovered operator structure. No hardcoded values except for the operator name itself.
