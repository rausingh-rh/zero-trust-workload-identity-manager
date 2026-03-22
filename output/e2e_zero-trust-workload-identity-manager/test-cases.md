# E2E Test Cases: zero-trust-workload-identity-manager

## Operator Information
- **Repository**: github.com/openshift/zero-trust-workload-identity-manager
- **Framework**: controller-runtime
- **API Group**: operator.openshift.io/v1alpha1
- **Managed CRDs**: ZeroTrustWorkloadIdentityManager, SpireServer, SpireAgent, SpiffeCSIDriver, SpireOIDCDiscoveryProvider
- **Operator Namespace**: zero-trust-workload-identity-manager
- **Changes Analyzed**: git diff ai-staging...HEAD (SPIRE Federation support - EP-1863)

## Prerequisites
- OpenShift cluster with admin access
- `oc` CLI installed and authenticated
- Operator pre-installed via OLM or manual deployment
- ZeroTrustWorkloadIdentityManager CR created with valid trustDomain and clusterName

## Test Cases

### 1. Federation API Validation

#### 1.1 Create SpireServer with federation using https_spiffe profile
- **Test**: Create a SpireServer CR with federation configured using the default https_spiffe bundle endpoint profile
- **Steps**:
  1. Create SpireServer CR with `spec.federation.bundleEndpoint.profile: https_spiffe`
  2. Add a `federatesWith` entry with a remote trust domain using https_spiffe profile
  3. Wait for SpireServer to become Ready
- **Expected**: SpireServer CR is accepted, federation Service is created, conditions are healthy

#### 1.2 Create SpireServer with federation using https_web/acme profile
- **Test**: Create a SpireServer CR with federation using https_web profile with ACME configuration
- **Steps**:
  1. Create SpireServer CR with `spec.federation.bundleEndpoint.profile: https_web`
  2. Configure httpsWeb.acme with directoryUrl, domainName, and email
  3. Wait for SpireServer conditions
- **Expected**: SpireServer CR is accepted with ACME configuration, federation Service is created

#### 1.3 Create SpireServer with federation using https_web/servingCert profile
- **Test**: Create a SpireServer CR with federation using https_web profile with serving certificate
- **Steps**:
  1. Create SpireServer CR with `spec.federation.bundleEndpoint.profile: https_web`
  2. Configure httpsWeb.servingCert with fileSyncInterval and externalSecretRef
  3. Wait for SpireServer conditions
- **Expected**: SpireServer CR is accepted, StatefulSet gets TLS volume mount, Route has external certificate

#### 1.4 Reject invalid federation configuration
- **Test**: Verify that invalid federation configurations are rejected by the API server
- **Steps**:
  1. Attempt to create SpireServer with invalid bundleEndpoint profile
  2. Attempt to create with httpsWeb missing when profile is https_web
  3. Attempt to create with both acme and servingCert set
- **Expected**: API server rejects all invalid configurations with appropriate error messages

### 2. Federation Service Reconciliation

#### 2.1 Federation Service is created when federation is configured
- **Test**: Verify that the spire-server-federation Service is created when federation is enabled
- **Steps**:
  1. Create SpireServer with federation enabled
  2. Wait for Service `spire-server-federation` to appear
  3. Verify Service has port 8443 and correct labels/selectors
- **Expected**: Service exists with port 8443, protocol TCP, and service-ca TLS annotation

#### 2.2 Federation Service is not created when federation is not configured
- **Test**: Verify no federation Service exists when federation is disabled
- **Steps**:
  1. Create SpireServer without federation
  2. Check that Service `spire-server-federation` does not exist
- **Expected**: No federation Service is found

### 3. Federation Route Reconciliation

#### 3.1 Federation Route is created when managedRoute is true
- **Test**: Verify that a Route is created for the federation endpoint when managedRoute is "true"
- **Steps**:
  1. Create SpireServer with federation and managedRoute: "true" (default)
  2. Wait for Route `spire-server-federation` to appear
  3. Verify Route spec (TLS reencrypt, target service)
- **Expected**: Route exists with TLS reencrypt, targeting spire-server-federation Service

#### 3.2 Federation Route is not created when managedRoute is false
- **Test**: Verify no federation Route exists when managedRoute is "false"
- **Steps**:
  1. Create SpireServer with federation and managedRoute: "false"
  2. Check that Route `spire-server-federation` does not exist
- **Expected**: No federation Route is found

#### 3.3 Federation Route has external certificate when servingCert is configured
- **Test**: Verify Route TLS externalCertificate is set when servingCert with externalSecretRef is configured
- **Steps**:
  1. Create SpireServer with https_web/servingCert and externalSecretRef
  2. Get the federation Route
  3. Verify Route TLS externalCertificate references the secret
- **Expected**: Route TLS externalCertificate has the correct secret name

### 4. SPIRE Server ConfigMap Federation Integration

#### 4.1 Federation config is injected into SPIRE server ConfigMap
- **Test**: Verify that the spire-server ConfigMap includes federation configuration
- **Steps**:
  1. Create SpireServer with federation and federatesWith entries
  2. Get ConfigMap `spire-server`
  3. Parse server.conf and verify federation keys exist
- **Expected**: server.conf contains `bundle_endpoint` and `federates_with` sections

### 5. StatefulSet Federation Port

#### 5.1 Federation port is added to StatefulSet when federation is configured
- **Test**: Verify that the spire-server container in the StatefulSet has port 8443 when federation is enabled
- **Steps**:
  1. Create SpireServer with federation
  2. Get StatefulSet `spire-server`
  3. Check spire-server container ports
- **Expected**: Container has port 8443 named "federation"

#### 5.2 TLS volume is mounted when servingCert is configured
- **Test**: Verify TLS volume mount is present when https_web with servingCert is used
- **Steps**:
  1. Create SpireServer with https_web/servingCert federation
  2. Get StatefulSet `spire-server`
  3. Check volume mounts and volumes
- **Expected**: Volume `federation-tls` mounted at `/run/spire/federation-tls`

### 6. Federation Immutability

#### 6.1 Federation cannot be removed once configured
- **Test**: Verify that removing the federation field from SpireServer is rejected
- **Steps**:
  1. Create SpireServer with federation
  2. Attempt to update SpireServer by removing the federation field
- **Expected**: API server rejects the update with "spec.federation cannot be removed once configured"

#### 6.2 Bundle endpoint profile is immutable
- **Test**: Verify that changing the bundle endpoint profile is rejected
- **Steps**:
  1. Create SpireServer with https_spiffe profile
  2. Attempt to update to https_web profile
- **Expected**: API server rejects with "profile is immutable and cannot be changed once set"

### 7. FederatesWith Dynamic Updates

#### 7.1 Adding new federated trust domains
- **Test**: Verify that new federatesWith entries can be added dynamically
- **Steps**:
  1. Create SpireServer with one federatesWith entry
  2. Update SpireServer to add a second federatesWith entry
  3. Verify ConfigMap is updated with both entries
- **Expected**: ConfigMap server.conf contains both federated trust domains

#### 7.2 Removing federated trust domains
- **Test**: Verify that federatesWith entries can be removed
- **Steps**:
  1. Create SpireServer with two federatesWith entries
  2. Update SpireServer to remove one entry
  3. Verify ConfigMap is updated with only the remaining entry
- **Expected**: ConfigMap server.conf contains only one federated trust domain

## Verification
```bash
oc get spireserver cluster -o jsonpath='{.status.conditions}' | jq .
oc get service spire-server-federation -n zero-trust-workload-identity-manager
oc get route spire-server-federation -n zero-trust-workload-identity-manager
oc get configmap spire-server -n zero-trust-workload-identity-manager -o jsonpath='{.data.server\.conf}' | jq .
oc get statefulset spire-server -n zero-trust-workload-identity-manager -o jsonpath='{.spec.template.spec.containers[0].ports}'
oc logs -l app.kubernetes.io/name=zero-trust-workload-identity-manager -n zero-trust-workload-identity-manager
```

## Cleanup
```bash
oc delete spireserver cluster --ignore-not-found
oc delete zerotrustworkloadidentitymanager cluster --ignore-not-found
```
