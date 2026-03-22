# E2E Execution Steps: zero-trust-workload-identity-manager

## Prerequisites

```bash
# Verify tools
which oc
oc version
oc whoami
oc get nodes
oc get clusterversion
```

## Environment Variables

```bash
export OPERATOR_NAMESPACE="zero-trust-workload-identity-manager"
export APP_DOMAIN="apps.$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')"
export JWT_ISSUER="https://oidc-discovery.${APP_DOMAIN}"
export CLUSTER_NAME="test01"
export BUNDLE_CONFIGMAP="spire-bundle"
```

## Step 1: Verify Operator Installation

```bash
# Wait for operator deployment
oc wait deployment/zero-trust-workload-identity-manager-controller-manager \
  -n $OPERATOR_NAMESPACE --for=condition=Available --timeout=120s

# Verify CRDs
oc get crd spireservers.operator.openshift.io
oc get crd zerotrustworkloadidentitymanagers.operator.openshift.io

# Check operator pods
oc get pods -n $OPERATOR_NAMESPACE -l name=zero-trust-workload-identity-manager
```

## Step 2: Create ZeroTrustWorkloadIdentityManager

```bash
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: ZeroTrustWorkloadIdentityManager
metadata:
  name: cluster
spec:
  bundleConfigMap: ${BUNDLE_CONFIGMAP}
  trustDomain: ${APP_DOMAIN}
  clusterName: ${CLUSTER_NAME}
EOF

oc wait zerotrustworkloadidentitymanager/cluster --for=condition=Ready --timeout=120s
```

## Step 3: Deploy SpireServer with Federation (https_spiffe)

```bash
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  jwtIssuer: "${JWT_ISSUER}"
  caSubject:
    country: "US"
  persistence:
    size: "1Gi"
    accessMode: ReadWriteOnce
  datastore:
    databaseType: sqlite3
    connectionString: "/run/spire/data/datastore.sqlite3"
    maxOpenConns: 100
    maxIdleConns: 2
    connMaxLifetime: 0
  federation:
    bundleEndpoint:
      profile: https_spiffe
      refreshHint: 300
    managedRoute: "true"
    federatesWith:
      - trustDomain: ${APP_DOMAIN}.remote
        bundleEndpointUrl: "https://spire-server-federation.${APP_DOMAIN}.remote"
        bundleEndpointProfile: https_spiffe
        endpointSpiffeId: "spiffe://${APP_DOMAIN}.remote/spire/server"
EOF
```

## Step 4: Verify Federation Resources

```bash
echo "=== SpireServer Status ==="
oc get spireserver cluster -o jsonpath='{.status.conditions}' | python3 -m json.tool

echo "=== Federation Service ==="
oc get service spire-server-federation -n $OPERATOR_NAMESPACE -o wide

echo "=== Federation Route ==="
oc get route spire-server-federation -n $OPERATOR_NAMESPACE -o wide

echo "=== Federation port in StatefulSet ==="
oc get statefulset spire-server -n $OPERATOR_NAMESPACE \
  -o jsonpath='{.spec.template.spec.containers[0].ports}' | python3 -m json.tool

echo "=== SPIRE Server ConfigMap (federation section) ==="
oc get configmap spire-server -n $OPERATOR_NAMESPACE \
  -o jsonpath='{.data.server\.conf}' | python3 -c "
import json, sys
conf = json.load(sys.stdin)
server = conf.get('server', {})
print('bundle_endpoint:', json.dumps(server.get('bundle_endpoint', 'NOT FOUND'), indent=2))
print('federates_with:', json.dumps(server.get('federates_with', 'NOT FOUND'), indent=2))
"
```

## Step 5: Test Adding Additional Federated Trust Domain

```bash
# Patch SpireServer to add a second federatesWith entry
oc patch spireserver cluster --type='merge' -p '{
  "spec": {
    "federation": {
      "federatesWith": [
        {
          "trustDomain": "'${APP_DOMAIN}'.remote",
          "bundleEndpointUrl": "https://spire-server-federation.'${APP_DOMAIN}'.remote",
          "bundleEndpointProfile": "https_spiffe",
          "endpointSpiffeId": "spiffe://'${APP_DOMAIN}'.remote/spire/server"
        },
        {
          "trustDomain": "'${APP_DOMAIN}'.remote2",
          "bundleEndpointUrl": "https://spire-server-federation.'${APP_DOMAIN}'.remote2",
          "bundleEndpointProfile": "https_web"
        }
      ]
    }
  }
}'

# Verify ConfigMap updated
sleep 10
oc get configmap spire-server -n $OPERATOR_NAMESPACE \
  -o jsonpath='{.data.server\.conf}' | python3 -c "
import json, sys
conf = json.load(sys.stdin)
federates = conf.get('server', {}).get('federates_with', {})
print('Federated trust domains:', list(federates.keys()))
assert len(federates) == 2, 'Expected 2 federated trust domains'
print('PASS: Two federated trust domains found')
"
```

## Step 6: Test Federation Immutability

```bash
echo "=== Test: Cannot remove federation once configured ==="
# This should fail
oc patch spireserver cluster --type='json' -p '[{"op": "remove", "path": "/spec/federation"}]' 2>&1 || echo "PASS: Federation removal correctly rejected"

echo "=== Test: Cannot change bundle endpoint profile ==="
# This should fail
oc patch spireserver cluster --type='merge' -p '{
  "spec": {
    "federation": {
      "bundleEndpoint": {
        "profile": "https_web",
        "httpsWeb": {
          "servingCert": {
            "fileSyncInterval": 86400
          }
        }
      }
    }
  }
}' 2>&1 || echo "PASS: Profile change correctly rejected"
```

## Step 7: Test Managed Route Toggle

```bash
echo "=== Test: Disable managed route ==="
oc patch spireserver cluster --type='merge' -p '{"spec":{"federation":{"managedRoute":"false"}}}'
sleep 10
oc get route spire-server-federation -n $OPERATOR_NAMESPACE 2>&1 && echo "FAIL: Route still exists" || echo "PASS: Route removed when managedRoute=false"

echo "=== Test: Re-enable managed route ==="
oc patch spireserver cluster --type='merge' -p '{"spec":{"federation":{"managedRoute":"true"}}}'
sleep 15
oc get route spire-server-federation -n $OPERATOR_NAMESPACE -o name && echo "PASS: Route recreated"
```

## Step 8: Cleanup

```bash
oc delete spireserver cluster --ignore-not-found --timeout=60s
oc delete zerotrustworkloadidentitymanager cluster --ignore-not-found --timeout=60s
```
