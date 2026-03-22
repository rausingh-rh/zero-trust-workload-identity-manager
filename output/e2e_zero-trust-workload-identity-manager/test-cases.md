# E2E Test Cases: zero-trust-workload-identity-manager

## Operator Information
- **Repository**: github.com/openshift/zero-trust-workload-identity-manager
- **Framework**: controller-runtime
- **API Group**: operator.openshift.io
- **Managed CRDs**:
  - ZeroTrustWorkloadIdentityManager
  - SpireServer
  - SpireAgent
  - SpiffeCSIDriver
  - SpireOIDCDiscoveryProvider
- **Operator Namespace**: zero-trust-workload-identity-manager
- **Changes Analyzed**: git diff origin/ai-staging...HEAD (102 files changed, 30336 insertions, 2492 deletions)

## Prerequisites
- OpenShift cluster with admin access (version 4.18+)
- `oc` CLI installed and authenticated
- `APP_DOMAIN` environment variable (cluster's base domain for Routes)
- Cluster must have:
  - Storage provisioner for PersistentVolumeClaims
  - OpenShift Route capability
  - RBAC enabled

## Installation

### Install Operator via OLM

```bash
# Create operator namespace
oc create namespace zero-trust-workload-identity-manager

# Create OperatorGroup
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: zero-trust-workload-identity-manager
  namespace: zero-trust-workload-identity-manager
spec:
  targetNamespaces:
  - zero-trust-workload-identity-manager
EOF

# Create Subscription
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: zero-trust-workload-identity-manager
  namespace: zero-trust-workload-identity-manager
spec:
  channel: alpha
  name: zero-trust-workload-identity-manager
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF

# Wait for operator deployment to be ready
oc wait --for=condition=Available deployment/zero-trust-workload-identity-manager-controller-manager \
  -n zero-trust-workload-identity-manager --timeout=5m

# Verify operator pod is running
oc get pods -n zero-trust-workload-identity-manager -l name=zero-trust-workload-identity-manager
```

## CR Deployment

### 1. Deploy ZeroTrustWorkloadIdentityManager (singleton)

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
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  zerotrustworkloadidentitymanager/cluster --timeout=5m
```

### 2. Deploy SpireServer (with Federation - NEW)

```bash
# Get cluster's app domain
export APP_DOMAIN=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')

cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  logLevel: info
  logFormat: json
  jwtIssuer: "https://spire-oidc.\${APP_DOMAIN}"
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
  # NEW: Federation configuration
  federation:
    bundleEndpoint:
      profile: https_web
      refreshHint: 300
      httpsWeb:
        acme:
          directoryUrl: "https://acme-v02.api.letsencrypt.org/directory"
          domainName: "spire-federation.\${APP_DOMAIN}"
          email: admin@example.com
          tosAccepted: "true"
    federatesWith:
    - trustDomain: partner.example.com
      bundleEndpointUrl: "https://spire-federation.partner.example.com:8443"
      bundleEndpointProfile: https_web
    managedRoute: "true"
EOF

# Wait for SpireServer Ready
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  spireserver/cluster --timeout=10m
```

### 3. Deploy SpireAgent

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

# Wait for SpireAgent Ready
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  spireagent/cluster --timeout=5m
```

### 4. Deploy SpiffeCSIDriver

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

# Wait for SpiffeCSIDriver Ready
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  spiffecsidriver/cluster --timeout=5m
```

### 5. Deploy SpireOIDCDiscoveryProvider

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
  jwtIssuer: "https://spire-oidc.\${APP_DOMAIN}"
  replicaCount: 1
  managedRoute: "true"
EOF

# Wait for SpireOIDCDiscoveryProvider Ready
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  spireoidcdiscoveryprovider/cluster --timeout=5m
```

## Test Cases

### API Type Changes - SpireServer Federation API

#### Test 1: Federation Bundle Endpoint Configuration (https_spiffe profile)

**Purpose**: Verify SpireServer can configure federation bundle endpoint with SPIFFE authentication

**Steps**:
1. Create SpireServer with `federation.bundleEndpoint.profile: https_spiffe`
2. Verify StatefulSet includes federation port configuration (8443)
3. Verify Service exposes federation port
4. Verify Route is created if `managedRoute: "true"`
5. Verify SPIRE server config includes bundle endpoint plugin

**Expected**:
- SpireServer status becomes Ready
- StatefulSet pod exposes port 8443
- Service includes port 8443 named `federation`
- Route exists with TLS passthrough to port 8443
- ConfigMap spire-server contains `bundle_endpoint_profile = "https_spiffe"`

**Commands**:
```bash
oc get spireserver cluster -o jsonpath='{.spec.federation.bundleEndpoint.profile}'
oc get statefulset spire-server -o jsonpath='{.spec.template.spec.containers[0].ports[?(@.name=="federation")]}'
oc get svc spire-server -o jsonpath='{.spec.ports[?(@.name=="federation")]}'
oc get route spire-server-federation -o jsonpath='{.spec.tls.termination}'
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep bundle_endpoint_profile
```

---

#### Test 2: Federation Bundle Endpoint Configuration (https_web with ACME)

**Purpose**: Verify https_web profile with ACME certificate provisioning

**Steps**:
1. Create SpireServer with `federation.bundleEndpoint.profile: https_web` and ACME config
2. Verify ACME directory URL, domain, email are configured
3. Verify SPIRE server pod has ACME plugin enabled
4. Check for certificate provisioning initiation (logs)

**Expected**:
- SpireServer status Ready
- ConfigMap contains ACME configuration: `directory_url`, `domain_name`, `email`, `tos_accepted`
- SPIRE server logs show ACME certificate request

**Commands**:
```bash
oc get spireserver cluster -o jsonpath='{.spec.federation.bundleEndpoint.httpsWeb.acme}'
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A5 acme
oc logs statefulset/spire-server | grep -i acme
```

---

#### Test 3: Federation Bundle Endpoint Configuration (https_web with manual TLS)

**Purpose**: Verify https_web profile with manual TLS certificate from Secret

**Steps**:
1. Create a Secret with tls.crt and tls.key
2. Create SpireServer with `federation.bundleEndpoint.profile: https_web` and `servingCert.externalSecretRef`
3. Verify SPIRE server mounts the secret
4. Verify Route references the external secret for TLS termination

**Expected**:
- SpireServer status Ready
- StatefulSet mounts external TLS secret
- Route configured with edge termination using externalSecretRef
- ConfigMap contains servingCert configuration with fileSyncInterval

**Commands**:
```bash
# Create test secret
oc create secret tls spire-federation-tls --cert=test.crt --key=test.key -n zero-trust-workload-identity-manager

oc get spireserver cluster -o jsonpath='{.spec.federation.bundleEndpoint.httpsWeb.servingCert.externalSecretRef}'
oc get statefulset spire-server -o jsonpath='{.spec.template.spec.volumes[?(@.name=="federation-tls")]}'
oc get route spire-server-federation -o jsonpath='{.spec.tls.certificate}'
```

---

#### Test 4: Federated Trust Domain Configuration

**Purpose**: Verify SpireServer can configure federation with remote trust domains

**Steps**:
1. Create SpireServer with `federation.federatesWith` containing remote trust domain
2. Verify SPIRE server config includes `federates_with` plugin
3. Verify bundleEndpointUrl, bundleEndpointProfile, endpointSpiffeId are configured
4. Test federation with https_spiffe (requires endpointSpiffeId)
5. Test federation with https_web (endpointSpiffeId optional)

**Expected**:
- SpireServer status Ready
- ConfigMap contains `federates_with` entries with:
  - `trust_domain`
  - `bundle_endpoint_url`
  - `bundle_endpoint_profile`
  - `endpoint_spiffe_id` (if profile is https_spiffe)

**Commands**:
```bash
oc get spireserver cluster -o jsonpath='{.spec.federation.federatesWith}'
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A10 federates_with
```

---

#### Test 5: Managed Route Creation for Federation

**Purpose**: Verify automatic Route creation when `managedRoute: "true"`

**Steps**:
1. Create SpireServer with `federation.managedRoute: "true"`
2. Verify Route `spire-server-federation` is created
3. Verify Route exposes federation port 8443
4. Update to `managedRoute: "false"` and verify Route is deleted

**Expected**:
- When `managedRoute: "true"`: Route exists with host based on cluster domain
- When `managedRoute: "false"`: Route is deleted, allowing manual configuration
- Route points to Service spire-server port 8443

**Commands**:
```bash
oc get spireserver cluster -o jsonpath='{.spec.federation.managedRoute}'
oc get route spire-server-federation -o jsonpath='{.spec.host}'
oc get route spire-server-federation -o jsonpath='{.spec.port.targetPort}'

# Update managedRoute to false
oc patch spireserver cluster --type=merge -p '{"spec":{"federation":{"managedRoute":"false"}}}'
oc get route spire-server-federation 2>&1 | grep NotFound
```

---

#### Test 6: Federation Configuration Immutability

**Purpose**: Verify federation profile cannot be changed after creation

**Steps**:
1. Create SpireServer with `federation.bundleEndpoint.profile: https_spiffe`
2. Attempt to update profile to `https_web`
3. Verify validation webhook rejects the change

**Expected**:
- Admission webhook returns error: "profile is immutable and cannot be changed once set"
- SpireServer remains unchanged

**Commands**:
```bash
# Create with https_spiffe
oc apply -f spireserver-https-spiffe.yaml

# Attempt to change to https_web
oc patch spireserver cluster --type=merge -p '{"spec":{"federation":{"bundleEndpoint":{"profile":"https_web"}}}}' 2>&1 | grep "profile is immutable"
```

---

#### Test 7: Federation Configuration Cannot Be Removed

**Purpose**: Verify federation config cannot be deleted once set

**Steps**:
1. Create SpireServer with federation configuration
2. Attempt to remove federation field entirely
3. Verify validation webhook rejects the removal

**Expected**:
- Admission webhook returns error: "Federation configuration cannot be removed once set"
- Federation configuration remains

**Commands**:
```bash
oc patch spireserver cluster --type=json -p='[{"op":"remove","path":"/spec/federation"}]' 2>&1 | grep "cannot be removed"
```

---

#### Test 8: ACME and ServingCert Mutual Exclusivity

**Purpose**: Verify acme and servingCert cannot coexist in httpsWeb

**Steps**:
1. Attempt to create SpireServer with both `acme` and `servingCert` configured
2. Verify validation webhook rejects

**Expected**:
- Admission webhook returns error: "exactly one of acme or servingCert must be set"

**Commands**:
```bash
# CR with both acme and servingCert should fail validation
oc apply -f spireserver-acme-and-cert.yaml 2>&1 | grep "exactly one of"
```

---

#### Test 9: Switching between ACME and ServingCert is Forbidden

**Purpose**: Verify cannot switch from ACME to manual cert or vice versa

**Steps**:
1. Create SpireServer with `acme` configuration
2. Attempt to remove acme and add servingCert
3. Verify validation webhook rejects

**Expected**:
- Admission webhook returns error: "cannot switch from acme to servingCert configuration"

**Commands**:
```bash
oc apply -f spireserver-acme.yaml
oc patch spireserver cluster --type=json -p='[{"op":"remove","path":"/spec/federation/bundleEndpoint/httpsWeb/acme"},{"op":"add","path":"/spec/federation/bundleEndpoint/httpsWeb/servingCert","value":{"externalSecretRef":"tls-secret"}}]' 2>&1 | grep "cannot switch"
```

---

#### Test 10: EndpointSpiffeId Required for https_spiffe Profile in FederatesWith

**Purpose**: Verify endpointSpiffeId is required when federating with https_spiffe

**Steps**:
1. Attempt to create SpireServer with `federatesWith` using `https_spiffe` but no `endpointSpiffeId`
2. Verify validation webhook rejects

**Expected**:
- Admission webhook returns error: "endpointSpiffeId is required when bundleEndpointProfile is https_spiffe"

**Commands**:
```bash
oc apply -f spireserver-federation-missing-spiffeid.yaml 2>&1 | grep "endpointSpiffeId is required"
```

---

### API Type Changes - Updated Documentation and Field Descriptions

#### Test 11: Verify Field Documentation Updates

**Purpose**: Ensure API documentation improvements are reflected in CRD

**Steps**:
1. Check CRD for SpireServer
2. Verify field descriptions are improved (e.g., "SpireServerSpec defines the specifications...")
3. Verify marker comments are updated

**Expected**:
- CRD contains updated descriptions for spec fields
- OpenAPI schema includes detailed field documentation

**Commands**:
```bash
oc get crd spireservers.operator.openshift.io -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.description}'
oc explain spireserver.spec.federation
```

---

### Controller Changes - SpireServer Controller

#### Test 12: Federation ConfigMap Generation

**Purpose**: Verify SpireServer controller generates correct SPIRE server.conf with federation

**Steps**:
1. Create SpireServer with federation enabled
2. Check ConfigMap `spire-server` for federation plugin configuration
3. Verify bundle_endpoint and federates_with sections

**Expected**:
- ConfigMap contains `BundleEndpoint` plugin with correct profile
- ConfigMap contains `FederatesWith` entries for each remote trust domain
- Plugin configurations are syntactically valid HCL

**Commands**:
```bash
oc get configmap spire-server -o yaml
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A20 BundleEndpoint
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A20 FederatesWith
```

---

#### Test 13: Federation StatefulSet Port Configuration

**Purpose**: Verify SpireServer StatefulSet exposes federation port

**Steps**:
1. Create SpireServer with federation
2. Check StatefulSet for port 8443 named `federation`
3. Verify containerPort and service port match

**Expected**:
- StatefulSet container exposes port 8443
- Port name is "federation"

**Commands**:
```bash
oc get statefulset spire-server -o jsonpath='{.spec.template.spec.containers[0].ports[?(@.name=="federation")]}'
```

---

#### Test 14: Federation Service Configuration

**Purpose**: Verify Service exposes federation port

**Steps**:
1. Create SpireServer with federation
2. Check Service `spire-server` for federation port
3. Verify port 8443 is exposed

**Expected**:
- Service has port 8443 named `federation`
- TargetPort matches container port

**Commands**:
```bash
oc get svc spire-server -o jsonpath='{.spec.ports[?(@.name=="federation")]}'
```

---

#### Test 15: Federation Route Creation and Management

**Purpose**: Verify Route creation, TLS configuration, and lifecycle

**Steps**:
1. Create SpireServer with `managedRoute: "true"` and https_spiffe profile
2. Verify Route created with passthrough TLS
3. Create SpireServer with https_web profile
4. Verify Route created with edge TLS (for ACME or manual cert)
5. Update managedRoute to "false" and verify Route is deleted

**Expected**:
- Route name: `spire-server-federation`
- For https_spiffe: TLS termination is passthrough
- For https_web: TLS termination is edge
- Route host based on cluster domain
- Route deleted when managedRoute is "false"

**Commands**:
```bash
oc get route spire-server-federation
oc get route spire-server-federation -o jsonpath='{.spec.tls.termination}'
oc get route spire-server-federation -o jsonpath='{.spec.host}'
```

---

#### Test 16: RBAC for External Certificate Secret

**Purpose**: Verify controller has RBAC permissions to read external TLS secrets

**Steps**:
1. Check Role/ClusterRole for operator
2. Verify permissions include `get`, `list`, `watch` on secrets

**Expected**:
- Role includes rules for secrets with verbs: get, list, watch
- Operator can mount and read externalSecretRef secrets

**Commands**:
```bash
oc get clusterrole zero-trust-workload-identity-manager-controller-manager -o yaml | grep -A5 secrets
```

---

### Integration Test Suite Changes

#### Test 17: Federation Integration Test Suite Execution

**Purpose**: Verify new integration test suite for federation runs successfully

**Steps**:
1. Locate `api/v1alpha1/tests/spireserver.federation.testsuite.yaml`
2. Run the test suite using integration test runner
3. Verify all test cases pass

**Expected**:
- Test suite file exists and is valid YAML
- All test scenarios defined in the suite pass
- Coverage includes: bundle endpoint profiles, ACME, manual TLS, federatesWith

**Commands**:
```bash
cat api/v1alpha1/tests/spireserver.federation.testsuite.yaml
# Integration test execution (depends on test framework)
```

---

### Sample CR Changes

#### Test 18: Sample CR for SpireServer with Federation (ACME)

**Purpose**: Verify sample CR with ACME federation deploys successfully

**Steps**:
1. Apply `config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_acme.yaml`
2. Verify SpireServer becomes Ready
3. Check federation endpoint is accessible

**Expected**:
- SpireServer Ready condition is True
- ACME certificate provisioning starts
- Route is created and responds

**Commands**:
```bash
oc apply -f config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_acme.yaml
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True spireserver/cluster --timeout=10m
oc get route spire-server-federation
curl -k https://$(oc get route spire-server-federation -o jsonpath='{.spec.host}')/.well-known/spiffe/bundle
```

---

#### Test 19: Sample CR for SpireServer with Federation (Manual TLS)

**Purpose**: Verify sample CR with manual TLS certificate deploys successfully

**Steps**:
1. Create TLS secret
2. Apply `config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_tls.yaml`
3. Verify SpireServer becomes Ready
4. Check Route uses external certificate

**Expected**:
- SpireServer Ready condition is True
- Route TLS certificate matches externalSecretRef
- Federation endpoint serves over TLS

**Commands**:
```bash
oc create secret tls spire-external-cert --cert=cert.pem --key=key.pem -n zero-trust-workload-identity-manager
oc apply -f config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_tls.yaml
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True spireserver/cluster --timeout=10m
oc get route spire-server-federation -o jsonpath='{.spec.tls}'
```

---

### RBAC Changes

#### Test 20: RBAC for Federation Routes

**Purpose**: Verify operator has permissions to create/update/delete Routes for federation

**Steps**:
1. Check ClusterRole for routes permissions
2. Verify verbs include create, get, list, update, delete, watch

**Expected**:
- ClusterRole includes Route resource permissions
- Operator can manage federation Routes

**Commands**:
```bash
oc get clusterrole zero-trust-workload-identity-manager-controller-manager -o yaml | grep -A5 routes
```

---

#### Test 21: RBAC for External Certificate Secrets

**Purpose**: Verify operator can access external TLS secrets for federation

**Steps**:
1. Check Role for secrets in operator namespace
2. Verify `spire-server-external-cert-role.yaml` is created
3. Verify RoleBinding exists

**Expected**:
- Role `spire-server-external-cert` includes secret permissions
- RoleBinding binds role to operator ServiceAccount

**Commands**:
```bash
oc get role spire-server-external-cert -n zero-trust-workload-identity-manager -o yaml
oc get rolebinding spire-server-external-cert-binding -n zero-trust-workload-identity-manager -o yaml
```

---

### CRD Schema Validation Changes

#### Test 22: CRD Validation for Federation Fields

**Purpose**: Verify CRD includes proper validation for federation fields

**Steps**:
1. Check CRD OpenAPI schema for SpireServer
2. Verify federation field validations:
   - `profile` enum: https_spiffe, https_web
   - `refreshHint` min: 60, max: 3600
   - `trustDomain` pattern validation
   - `bundleEndpointUrl` pattern: ^https://
   - `email` pattern for ACME
3. Attempt to create SpireServer with invalid values and verify rejection

**Expected**:
- CRD schema includes all validation rules
- Invalid CRs are rejected with clear error messages

**Commands**:
```bash
oc get crd spireservers.operator.openshift.io -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.federation}'

# Test invalid profile
oc apply -f - <<EOF
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  ...
  federation:
    bundleEndpoint:
      profile: invalid_profile  # Should fail
EOF
```

---

### Bundle Manifest and OLM Changes

#### Test 23: Bundle Manifest Includes Federation CRD

**Purpose**: Verify OLM bundle includes updated CRD with federation support

**Steps**:
1. Check `bundle/manifests/operator.openshift.io_spireservers.yaml`
2. Verify federation fields are present in schema
3. Verify CSV includes updated permissions for Routes and Secrets

**Expected**:
- Bundle CRD matches config/crd/bases CRD
- CSV includes Route and Secret RBAC

**Commands**:
```bash
cat bundle/manifests/operator.openshift.io_spireservers.yaml | grep -A50 federation
cat bundle/manifests/zero-trust-workload-identity-manager.clusterserviceversion.yaml | grep -A10 routes
```

---

### End-to-End Federation Flow

#### Test 24: Complete Federation Flow (Single Cluster)

**Purpose**: Verify complete federation setup within a single cluster

**Steps**:
1. Deploy ZeroTrustWorkloadIdentityManager
2. Deploy SpireServer with federation enabled (https_web + ACME)
3. Deploy SpireAgent
4. Verify bundle endpoint is reachable
5. Check SPIRE server logs for bundle refresh

**Expected**:
- All components become Ready
- Federation bundle endpoint serves bundle JSON
- SPIRE server logs show bundle endpoint plugin active

**Commands**:
```bash
# Full deployment
oc apply -f config/samples/operator.openshift.io_v1alpha1_zerotrustworkloadidentitymanager.yaml
oc apply -f config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_acme.yaml
oc apply -f config/samples/operator.openshift.io_v1alpha1_spireagent.yaml

# Wait for readiness
oc wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True spireserver/cluster --timeout=10m

# Test bundle endpoint
ROUTE_HOST=$(oc get route spire-server-federation -o jsonpath='{.spec.host}')
curl -k https://$ROUTE_HOST/.well-known/spiffe/bundle

# Check logs
oc logs statefulset/spire-server | grep -i "bundle endpoint"
```

---

## Verification

### Operator Health
```bash
# Check operator deployment
oc get deployment zero-trust-workload-identity-manager-controller-manager -n zero-trust-workload-identity-manager
oc get pods -n zero-trust-workload-identity-manager -l name=zero-trust-workload-identity-manager

# Check operator logs
oc logs -n zero-trust-workload-identity-manager deployment/zero-trust-workload-identity-manager-controller-manager --tail=100
```

### Operand CR Status
```bash
# Check all CR conditions
oc get zerotrustworkloadidentitymanager cluster -o jsonpath='{.status.conditions}' | jq
oc get spireserver cluster -o jsonpath='{.status.conditions}' | jq
oc get spireagent cluster -o jsonpath='{.status.conditions}' | jq
oc get spiffecsidriver cluster -o jsonpath='{.status.conditions}' | jq
oc get spireoidcdiscoveryprovider cluster -o jsonpath='{.status.conditions}' | jq
```

### Managed Workloads
```bash
# SpireServer
oc get statefulset spire-server -o wide
oc get pods -l app.kubernetes.io/name=spire-server
oc wait --for=condition=Ready pod -l app.kubernetes.io/name=spire-server --timeout=5m

# SpireAgent
oc get daemonset spire-agent -o wide
oc get pods -l app.kubernetes.io/name=spire-agent
oc wait --for=condition=Ready pod -l app.kubernetes.io/name=spire-agent --timeout=5m

# SpiffeCSIDriver
oc get daemonset spire-spiffe-csi-driver -o wide
oc get pods -l app.kubernetes.io/name=spiffe-csi-driver

# SpireOIDCDiscoveryProvider
oc get deployment spire-spiffe-oidc-discovery-provider -o wide
oc get pods -l app.kubernetes.io/name=spiffe-oidc-discovery-provider
oc wait --for=condition=Available deployment/spire-spiffe-oidc-discovery-provider --timeout=5m
```

### Federation Resources
```bash
# Federation Route
oc get route spire-server-federation
oc describe route spire-server-federation

# Federation Service Port
oc get svc spire-server -o jsonpath='{.spec.ports[?(@.name=="federation")]}'

# Federation ConfigMap
oc get configmap spire-server -o jsonpath='{.data.server\.conf}' | grep -A30 BundleEndpoint
```

### Configuration
```bash
# SpireServer ConfigMap
oc get configmap spire-server -o yaml

# SpireAgent ConfigMap
oc get configmap spire-agent -o yaml

# SpireOIDCDiscoveryProvider ConfigMap
oc get configmap spire-spiffe-oidc-discovery-provider -o yaml
```

## Cleanup

### Delete Custom Resources (reverse order)
```bash
# Delete operand CRs
oc delete spireoidcdiscoveryprovider cluster
oc delete spiffecsidriver cluster
oc delete spireagent cluster
oc delete spireserver cluster
oc delete zerotrustworkloadidentitymanager cluster

# Wait for resources to be cleaned up
oc wait --for=delete spireserver/cluster --timeout=5m
oc wait --for=delete statefulset/spire-server --timeout=5m
oc wait --for=delete daemonset/spire-agent --timeout=5m
```

### Uninstall Operator (OLM)
```bash
# Delete Subscription
oc delete subscription zero-trust-workload-identity-manager -n zero-trust-workload-identity-manager

# Delete CSV
CSV_NAME=$(oc get csv -n zero-trust-workload-identity-manager -o jsonpath='{.items[0].metadata.name}')
oc delete csv $CSV_NAME -n zero-trust-workload-identity-manager

# Delete OperatorGroup
oc delete operatorgroup zero-trust-workload-identity-manager -n zero-trust-workload-identity-manager

# Delete namespace
oc delete namespace zero-trust-workload-identity-manager
```

### Verify Cleanup
```bash
# Check CRDs are still present (installed by OLM)
oc get crd | grep operator.openshift.io

# Check no orphaned resources
oc get all -n zero-trust-workload-identity-manager 2>&1 | grep "No resources found"
```

---

## Notes

- All CRs use singleton pattern with `metadata.name: cluster`
- Federation configuration is immutable once set (profile cannot change)
- ACME and manual TLS certificate provisioning are mutually exclusive
- Managed Routes can be disabled by setting `managedRoute: "false"`
- Federation requires external connectivity for ACME or manual certificate setup
- The operator manages all RBAC, Routes, Services automatically
- Integration tests are located in `api/v1alpha1/tests/spireserver.federation.testsuite.yaml`
