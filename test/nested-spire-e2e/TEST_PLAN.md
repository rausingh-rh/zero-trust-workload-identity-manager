# Nested SPIRE E2E Test Plan — Sidecar + x509pop

## Clusters

| Role | Kubeconfig | Purpose |
|------|-----------|---------|
| **Hub (upstream)** | `/home/rausingh/Documents/gcp_cluster/14Jul202613/auth/kubeconfig` | Root CA, `exportGRPCRoute: true` |
| **Spoke (downstream)** | `/home/rausingh/Documents/gcp_cluster/14Jul202614/auth/kubeconfig` | Intermediate CA via `upstreamAuthority.spire` |

```bash
export HUB_KUBECONFIG=/home/rausingh/Documents/gcp_cluster/14Jul202613/auth/kubeconfig
export SPOKE_KUBECONFIG=/home/rausingh/Documents/gcp_cluster/14Jul202614/auth/kubeconfig
alias hub="oc --kubeconfig=$HUB_KUBECONFIG"
alias spoke="oc --kubeconfig=$SPOKE_KUBECONFIG"
```

## Pre-flight

```bash
# Verify both clusters are reachable
hub get nodes
spoke get nodes

# Derive the apps domain and trust domain for the hub cluster
HUB_APP_DOMAIN=apps.$(hub get dns cluster -o jsonpath='{.spec.baseDomain}')
HUB_TRUST_DOMAIN=$HUB_APP_DOMAIN
HUB_JWT_ISSUER=oidc-discovery.${HUB_APP_DOMAIN}
echo "Hub apps domain:  $HUB_APP_DOMAIN"
echo "Hub trust domain: $HUB_TRUST_DOMAIN"
echo "Hub JWT issuer:   https://$HUB_JWT_ISSUER"
```

---

## Phase 1: Build & Push Operator

```bash
cd /home/rausingh/Documents/oape/ztwim-poc/zero-trust-workload-identity-manager

export IMG=quay.io/rh-ee-rausingh/ztwim-operator:nested-spire
export BUNDLE_IMG=quay.io/rh-ee-rausingh/ztwim-operator-bundle:nested-spire
export CONTAINER_TOOL=podman

make docker-build docker-push IMG=$IMG
make bundle IMG=$IMG
make bundle-build bundle-push BUNDLE_IMG=$BUNDLE_IMG
```

---

## Phase 2: Deploy Operator on Hub Cluster

```bash
# Create namespace
hub new-project zero-trust-workload-identity-manager

# Deploy via OLM
KUBECONFIG=$HUB_KUBECONFIG operator-sdk run bundle $BUNDLE_IMG \
  --namespace zero-trust-workload-identity-manager --timeout 5m

# Approve InstallPlan if needed
hub get installplan -n zero-trust-workload-identity-manager
# hub patch installplan <name> -n zero-trust-workload-identity-manager \
#   --type merge -p '{"spec":{"approved":true}}'

# Verify operator is running
hub get pods -n zero-trust-workload-identity-manager -l control-plane=controller-manager
```

---

## Phase 3: Deploy Hub SPIRE Operands

### 3.1 Create all operand CRs (hub — self-signed root CA, exportGRPCRoute enabled)

```bash
hub apply -f - <<EOF
apiVersion: operator.openshift.io/v1alpha1
kind: ZeroTrustWorkloadIdentityManager
metadata:
  name: cluster
spec:
  trustDomain: $HUB_TRUST_DOMAIN
  clusterName: hub-cluster
  bundleConfigMap: spire-bundle
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  caSubject:
    commonName: $HUB_TRUST_DOMAIN
    country: "US"
    organization: "RH"
  persistence:
    size: "2Gi"
    accessMode: ReadWriteOncePod
  datastore:
    databaseType: sqlite3
    connectionString: "/run/spire/data/datastore.sqlite3"
    maxOpenConns: 100
    maxIdleConns: 2
    connMaxLifetime: 3600
  jwtIssuer: https://$HUB_JWT_ISSUER
  exportGRPCRoute: true
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireAgent
metadata:
  name: cluster
spec:
  nodeAttestor:
    k8sPSATEnabled: "true"
  workloadAttestors:
    k8sEnabled: "true"
    workloadAttestorsVerification:
      type: "auto"
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpiffeCSIDriver
metadata:
  name: cluster
spec: {}
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireOIDCDiscoveryProvider
metadata:
  name: cluster
spec:
  jwtIssuer: https://$HUB_JWT_ISSUER
EOF
```

### 3.2 Wait for hub SPIRE to be healthy

```bash
hub get pods -n zero-trust-workload-identity-manager -w
# Wait for: spire-server-0, spire-agent-*, spire-spiffe-csi-driver-* all Running

# Verify the gRPC Route was created
hub get route spire-server-grpc -n zero-trust-workload-identity-manager
HUB_ROUTE=$(hub get route spire-server-grpc -n zero-trust-workload-identity-manager -o jsonpath='{.spec.host}')
echo "Hub gRPC Route: $HUB_ROUTE"

# Verify agents are attested
hub exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server agent list
```

---

## Phase 4: Generate x509pop Certificates

```bash
mkdir -p /tmp/nested-spire-certs && cd /tmp/nested-spire-certs

# 4.1 Generate Agent CA (used by the upstream server to verify downstream agents)
openssl genrsa -out agent-ca.key 2048
openssl req -new -x509 -key agent-ca.key -out agent-ca.crt -days 365 \
    -subj "/C=US/O=SPIRE Test/CN=Agent CA" \
    -addext "keyUsage=keyCertSign,cRLSign,digitalSignature" \
    -addext "basicConstraints=critical,CA:TRUE"

# 4.2 Generate Agent certificate (for the downstream sidecar)
openssl genrsa -out agent.key 2048
openssl req -new -key agent.key -out agent.csr \
    -subj "/C=US/O=SPIRE Test/CN=Downstream Agent"

cat > agent-ext.cnf <<EXTEOF
keyUsage = critical,digitalSignature,keyEncipherment
basicConstraints = CA:FALSE
EXTEOF

openssl x509 -req -in agent.csr -CA agent-ca.crt -CAkey agent-ca.key \
    -CAcreateserial -out agent.crt -days 365 -extfile agent-ext.cnf

# 4.3 Verify the cert has digitalSignature
openssl x509 -in agent.crt -text -noout | grep -A1 "Key Usage"

echo "Certificates generated in /tmp/nested-spire-certs/"
ls -la /tmp/nested-spire-certs/
```

---

## Phase 5: Patch Hub Server for x509pop (CREATE_ONLY_MODE)

The hub server needs the `x509pop` NodeAttestor added to its `server.conf` and the CA cert mounted. Since the CRD doesn't support configuring server-side NodeAttestors for x509pop yet, we use CREATE_ONLY_MODE.

### 5.1 Enable CREATE_ONLY_MODE on the operator

```bash
hub patch subscription openshift-zero-trust-workload-identity-manager \
  -n zero-trust-workload-identity-manager --type=merge \
  -p '{"spec":{"config":{"env":[{"name":"CREATE_ONLY_MODE","value":"true"}]}}}'

# Wait for operator pod to restart with the env
hub get pods -n zero-trust-workload-identity-manager -l control-plane=controller-manager -w
```

### 5.2 Create the x509pop CA ConfigMap on hub

```bash
hub create configmap x509pop-ca-bundle \
  -n zero-trust-workload-identity-manager \
  --from-file=agent-ca.crt=/tmp/nested-spire-certs/agent-ca.crt
```

### 5.3 Patch hub server.conf to add x509pop NodeAttestor

```bash
# Get current server.conf
hub get cm spire-server -n zero-trust-workload-identity-manager -o jsonpath='{.data.server\.conf}' > /tmp/hub-server.conf

# You need to add x509pop to the NodeAttestor list in plugins:
# "NodeAttestor": [
#   {"k8s_psat": {...}},
#   {"x509pop": {"plugin_data": {"ca_bundle_path": "/run/spire/x509pop-ca/agent-ca.crt"}}}
# ]
# 
# Edit /tmp/hub-server.conf manually or use jq to add the x509pop entry,
# then update the ConfigMap:

hub create configmap spire-server \
  -n zero-trust-workload-identity-manager \
  --from-file=server.conf=/tmp/hub-server.conf \
  --dry-run=client -o yaml | hub apply -f -
```

### 5.4 Mount x509pop CA cert into hub StatefulSet

```bash
hub patch statefulset spire-server -n zero-trust-workload-identity-manager --type=json -p='[
  {"op":"add","path":"/spec/template/spec/volumes/-","value":{"name":"x509pop-ca","configMap":{"name":"x509pop-ca-bundle"}}},
  {"op":"add","path":"/spec/template/spec/containers/0/volumeMounts/-","value":{"name":"x509pop-ca","mountPath":"/run/spire/x509pop-ca","readOnly":true}}
]'
```

### 5.5 Wait for hub server to restart and verify

```bash
hub get pods -n zero-trust-workload-identity-manager -l app.kubernetes.io/name=spire-server -w
# Wait for spire-server-0 to be Running

# Verify agents are still attested
hub exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server agent list
```

### 5.6 Create the downstream registration entry

```bash
# Get one of the attested agent SPIFFE IDs (for parentID)
PARENT_ID=$(hub exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server agent list -output json | \
  python3 -c "import sys,json; agents=json.load(sys.stdin)['agents']; print(agents[0]['id']['path'])" 2>/dev/null)

echo "Parent agent path: $PARENT_ID"

# Create the downstream entry
hub exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server entry create \
  -parentID "spiffe://$HUB_TRUST_DOMAIN$PARENT_ID" \
  -spiffeID "spiffe://$HUB_TRUST_DOMAIN/downstream/spire-server" \
  -selector "unix:uid:0" \
  -downstream

# Note: The unix:uid selector needs to match the UID of the sidecar process
# On OpenShift, the UID is assigned by the SCC (e.g., 1000730000).
# You may need to check the actual UID after the spoke is deployed and update:
#   hub exec spire-server-0 -c spire-server -- \
#     /spire-server entry update -entryID <id> -selector "unix:uid:<actual-uid>"
```

---

## Phase 6: Get Hub Trust Bundle

```bash
# Extract the hub's root CA cert (needed by the spoke's upstream-agent sidecar)
hub get configmap spire-bundle -n zero-trust-workload-identity-manager \
  -o jsonpath='{.data.bundle\.crt}' > /tmp/nested-spire-certs/hub-bundle.crt

# Verify it's a valid cert
openssl x509 -in /tmp/nested-spire-certs/hub-bundle.crt -text -noout | head -10
```

---

## Phase 7: Deploy Operator on Spoke Cluster

```bash
spoke new-project zero-trust-workload-identity-manager

KUBECONFIG=$SPOKE_KUBECONFIG operator-sdk run bundle $BUNDLE_IMG \
  --namespace zero-trust-workload-identity-manager --timeout 5m

# Approve InstallPlan if needed
spoke get installplan -n zero-trust-workload-identity-manager
```

---

## Phase 8: Create Secrets on Spoke Cluster

```bash
# 8.1 x509pop agent cert + key
spoke create secret generic x509pop-agent-cert \
  -n zero-trust-workload-identity-manager \
  --from-file=agent.crt=/tmp/nested-spire-certs/agent.crt \
  --from-file=agent.key=/tmp/nested-spire-certs/agent.key

# 8.2 Upstream trust bundle (hub's root CA)
spoke create secret generic upstream-bundle \
  -n zero-trust-workload-identity-manager \
  --from-file=bundle.crt=/tmp/nested-spire-certs/hub-bundle.crt
```

---

## Phase 9: Deploy Spoke SPIRE Operands (Downstream)

**Critical:** The spoke must use the **same trust domain** as the hub. This is a fundamental requirement for nested SPIRE.

```bash
# Derive the spoke's own apps domain (for JWT issuer)
SPOKE_APP_DOMAIN=apps.$(spoke get dns cluster -o jsonpath='{.spec.baseDomain}')
SPOKE_JWT_ISSUER=oidc-discovery.${SPOKE_APP_DOMAIN}
echo "Spoke apps domain: $SPOKE_APP_DOMAIN"
echo "Using trust domain from hub: $HUB_TRUST_DOMAIN"
```

### 9.1 Create all operand CRs (downstream with nested SPIRE)

```bash
spoke apply -f - <<EOF
apiVersion: operator.openshift.io/v1alpha1
kind: ZeroTrustWorkloadIdentityManager
metadata:
  name: cluster
spec:
  trustDomain: $HUB_TRUST_DOMAIN
  clusterName: spoke-cluster
  bundleConfigMap: spire-bundle
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  caSubject:
    commonName: $HUB_TRUST_DOMAIN
    country: "US"
    organization: "RH"
  persistence:
    size: "2Gi"
    accessMode: ReadWriteOncePod
  datastore:
    databaseType: sqlite3
    connectionString: "/run/spire/data/datastore.sqlite3"
    maxOpenConns: 100
    maxIdleConns: 2
    connMaxLifetime: 3600
  jwtIssuer: https://$SPOKE_JWT_ISSUER
  upstreamAuthority:
    spire:
      upstreamServerAddress: "$HUB_ROUTE"
      upstreamServerPort: 443
      trustBundle:
        secretRef:
          name: upstream-bundle
          key: bundle.crt
      nodeAttestor:
        x509pop:
          certificateSecretName: x509pop-agent-cert
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireAgent
metadata:
  name: cluster
spec:
  nodeAttestor:
    k8sPSATEnabled: "true"
  workloadAttestors:
    k8sEnabled: "true"
    workloadAttestorsVerification:
      type: "auto"
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpiffeCSIDriver
metadata:
  name: cluster
spec: {}
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireOIDCDiscoveryProvider
metadata:
  name: cluster
spec:
  jwtIssuer: https://$SPOKE_JWT_ISSUER
EOF
```

---

## Phase 10: Verify

### 10.1 Check spoke StatefulSet has 3 containers

```bash
spoke get statefulset spire-server -n zero-trust-workload-identity-manager \
  -o jsonpath='{.spec.template.spec.containers[*].name}'
# Expected: spire-server spire-controller-manager upstream-agent
```

### 10.2 Check upstream-agent sidecar logs

```bash
spoke logs spire-server-0 -n zero-trust-workload-identity-manager \
  -c upstream-agent --tail=30
# Look for:
#   "Node attestation was successful"
#   "reattestable=true"
```

### 10.3 Check downstream server activated intermediate CA

```bash
spoke logs spire-server-0 -n zero-trust-workload-identity-manager \
  -c spire-server --tail=30
# Look for:
#   "X509 CA activated"
#   "self_signed=false"
#   "upstream_authority_id=..."
```

### 10.4 Verify on hub: downstream agent is listed

```bash
hub exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server agent list
# Should see an x509pop-attested agent from the spoke
```

### 10.5 Verify spoke agents are attested

```bash
spoke exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server agent list
```

### 10.6 Verify trust bundle contains hub's root CA

```bash
spoke exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server bundle show -format spiffe
# The x509-svid key should match the hub's root CA
```

### 10.7 Deploy a test workload on spoke and verify SVID chain

```bash
spoke apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: svid-demo
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: demo-workload
  namespace: svid-demo
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo-workload
  namespace: svid-demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: demo-workload
  template:
    metadata:
      labels:
        app: demo-workload
    spec:
      serviceAccountName: demo-workload
      containers:
      - name: workload
        image: registry.access.redhat.com/ubi9/ubi-minimal:latest
        command: ["sleep", "infinity"]
        volumeMounts:
        - name: spiffe-workload-api
          mountPath: /spiffe-workload-api
          readOnly: true
      volumes:
      - name: spiffe-workload-api
        csi:
          driver: csi.spiffe.io
          readOnly: true
EOF

# Wait for pod to be running
spoke get pods -n svid-demo -w

# Check the SVID chain (should be Workload -> Intermediate -> Root)
# The root should be from the hub cluster
```

---

## Troubleshooting

### Upstream-agent can't connect to hub Route
```bash
# Test connectivity from spoke cluster
spoke exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c upstream-agent -- sh -c "echo | openssl s_client -connect $HUB_ROUTE:443 -servername $HUB_ROUTE 2>/dev/null | head -5"
```

### Attestation fails — wrong UID in selector
```bash
# Check the UID of processes in the spoke's spire-server-0 pod
spoke exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c upstream-agent -- id
# Note the uid (e.g., 1000730000)

# Update the registration entry on the hub with the correct UID
hub exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server entry show
# Find the entry ID, then:
hub exec -n zero-trust-workload-identity-manager spire-server-0 \
  -c spire-server -- /spire-server entry update \
  -entryID <ENTRY_ID> \
  -selector "unix:uid:<ACTUAL_UID>"
```

### ConfigMap not generated
```bash
spoke get cm upstream-agent-config -n zero-trust-workload-identity-manager -o yaml
```

### Server.conf missing UpstreamAuthority block
```bash
spoke get cm spire-server -n zero-trust-workload-identity-manager \
  -o jsonpath='{.data.server\.conf}' | python3 -m json.tool | grep -A5 UpstreamAuthority
```
