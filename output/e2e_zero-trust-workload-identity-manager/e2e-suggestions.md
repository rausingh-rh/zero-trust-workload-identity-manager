# E2E Test Suggestions for zero-trust-workload-identity-manager

## Summary

Based on the git diff between `origin/ai-staging` and `HEAD`, the primary changes are:

1. **NEW: Federation API** - Complete federation support for SpireServer including bundle endpoint configuration, federated trust domains, and certificate provisioning (ACME and manual TLS)
2. **Enhanced API Types** - New fields and validation rules for federation configuration
3. **Controller Logic** - SpireServer controller now generates federation-related resources (Routes, ConfigMaps with federation plugins, StatefulSet ports)
4. **Integration Tests** - New test suite file `api/v1alpha1/tests/spireserver.federation.testsuite.yaml` (1128 lines)
5. **RBAC** - New roles and bindings for external certificate secret access
6. **Sample CRs** - New sample CRs with federation examples (ACME and manual TLS)

---

## Detected Operator Structure

- **Framework**: controller-runtime (sigs.k8s.io/controller-runtime v0.20.4)
- **Managed CRDs**: 5 cluster-scoped singleton CRs
  - ZeroTrustWorkloadIdentityManager
  - SpireServer (main focus of changes)
  - SpireAgent
  - SpiffeCSIDriver
  - SpireOIDCDiscoveryProvider
- **E2E Pattern**: Ginkgo v2 + Gomega
- **Operator Namespace**: zero-trust-workload-identity-manager
- **Install Mechanism**: OLM (bundle, CSV, Subscription)

---

## Change Categorization

### 1. API Types Changes (High Priority for E2E)

**File**: `api/v1alpha1/spire_server_config_types.go`

**New Structs**:
- `FederationConfig` - Top-level federation configuration
- `BundleEndpointConfig` - Federation bundle endpoint settings
- `HttpsWebConfig` - Configuration for https_web profile
- `AcmeConfig` - ACME certificate provisioning
- `ServingCertConfig` - Manual TLS certificate configuration
- `FederatesWithConfig` - Remote trust domain configuration

**New Fields**:
- `SpireServer.Spec.Federation` - Optional federation configuration

**New Enums**:
- `BundleEndpointProfile` - `https_spiffe` | `https_web`

**Validation Rules** (CEL):
- Federation config cannot be removed once set
- `profile` is immutable
- ACME and servingCert are mutually exclusive
- `endpointSpiffeId` required when `bundleEndpointProfile` is `https_spiffe`

**Recommended E2E Tests** (MUST HAVE):
- ✅ Create SpireServer with `federation.bundleEndpoint.profile: https_spiffe`
- ✅ Create SpireServer with `federation.bundleEndpoint.profile: https_web` + ACME
- ✅ Create SpireServer with `federation.bundleEndpoint.profile: https_web` + manual TLS
- ✅ Configure `federatesWith` with https_spiffe profile (requires endpointSpiffeId)
- ✅ Configure `federatesWith` with https_web profile (endpointSpiffeId optional)
- ✅ Toggle `managedRoute` between "true" and "false"
- ✅ Verify federation profile immutability (attempt to change https_spiffe → https_web should fail)
- ✅ Verify federation removal is rejected (attempt to remove federation config should fail)
- ✅ Verify ACME and servingCert mutual exclusivity (both set should fail validation)
- ✅ Verify endpointSpiffeId requirement for https_spiffe federatesWith

---

### 2. Controller Changes (High Priority)

**File**: `pkg/controller/spire-server/configmap.go`

**Changes**: ConfigMap generation now includes:
- BundleEndpoint plugin configuration
- FederatesWith plugin configuration
- ACME plugin configuration (if using ACME)
- ServingCert file sync configuration (if using manual TLS)

**Recommended E2E Tests** (MUST HAVE):
- ✅ Verify ConfigMap `spire-server` contains `BundleEndpoint` plugin when federation is enabled
- ✅ Verify ConfigMap contains `bundle_endpoint_profile` matching spec
- ✅ Verify ConfigMap contains `FederatesWith` entries for each federated trust domain
- ✅ Verify ConfigMap contains ACME config (directory_url, domain_name, email, tos_accepted)
- ✅ Verify ConfigMap contains servingCert config (file_sync_interval, external_secret_ref)

---

**File**: `pkg/controller/spire-server/statefulset.go`

**Changes**:
- StatefulSet now exposes port 8443 for federation endpoint
- Volume mounts for external TLS secrets (when using manual TLS)

**Recommended E2E Tests** (MUST HAVE):
- ✅ Verify StatefulSet exposes containerPort 8443 named "federation"
- ✅ Verify StatefulSet mounts external TLS secret when `externalSecretRef` is set
- ✅ Verify StatefulSet volumes include secret volume for federation TLS

---

**File**: `pkg/controller/spire-server/service.go`

**Changes**:
- Service now exposes port 8443 for federation

**Recommended E2E Tests** (MUST HAVE):
- ✅ Verify Service exposes port 8443 named "federation"
- ✅ Verify Service port targets StatefulSet port 8443

---

**File**: `pkg/controller/spire-server/routes.go` (NEW)

**Changes**:
- New Route management for federation endpoint
- TLS termination based on profile (passthrough for https_spiffe, edge for https_web)
- Managed Route lifecycle based on `managedRoute` field

**Recommended E2E Tests** (MUST HAVE):
- ✅ Verify Route `spire-server-federation` is created when `managedRoute: "true"`
- ✅ Verify Route uses passthrough TLS for https_spiffe profile
- ✅ Verify Route uses edge TLS for https_web profile
- ✅ Verify Route is deleted when `managedRoute: "false"`
- ✅ Verify Route is recreated when `managedRoute` toggled back to "true"

---

### 3. RBAC Changes (Medium Priority)

**New Files**:
- `bindata/spire-server/spire-server-external-cert-role.yaml`
- `bindata/spire-server/spire-server-external-cert-role-binding.yaml`
- `bindata/spire-oidc-discovery-provider/spire-oidc-external-cert-role.yaml`
- `bindata/spire-oidc-discovery-provider/spire-oidc-external-cert-role-binding.yaml`

**Changes**: New Role and RoleBinding for accessing external TLS certificate secrets

**Recommended E2E Tests** (NICE TO HAVE):
- ✅ Verify operator has RBAC permissions for Routes (create, update, delete, get, list, watch)
- ✅ Verify Role `spire-server-external-cert` exists when external secrets are used
- ✅ Verify RoleBinding binds role to operator ServiceAccount
- ⚪ Verify operator can read external TLS secrets in the operator namespace

---

### 4. CRD Schema Changes (Medium Priority)

**Files**: `config/crd/bases/operator.openshift.io_spireservers.yaml`

**Changes**:
- New federation field schema
- Validation rules (CEL) for immutability and mutual exclusivity
- Pattern validation for trust domains, URLs, emails

**Recommended E2E Tests** (NICE TO HAVE):
- ✅ Verify CRD schema includes federation fields
- ✅ Verify pattern validation for `trustDomain` (^[a-z0-9._-]{1,255}$)
- ✅ Verify pattern validation for `bundleEndpointUrl` (^https://.*)
- ✅ Verify pattern validation for `email` in ACME config
- ✅ Verify enum validation for `profile` (only https_spiffe or https_web allowed)
- ✅ Verify min/max validation for `refreshHint` (60-3600)
- ⚪ Verify min/max validation for `fileSyncInterval` (3600-7776000)

---

### 5. Sample CR Changes (Medium Priority)

**New Files**:
- `config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_acme.yaml`
- `config/samples/operator.openshift.io_v1alpha1_spireserver_with_federation_tls.yaml`

**Recommended E2E Tests** (NICE TO HAVE):
- ✅ Deploy sample CR with ACME federation and verify Ready status
- ✅ Deploy sample CR with manual TLS federation and verify Ready status
- ⚪ Verify ACME certificate provisioning is initiated (check logs)
- ⚪ Verify manual TLS certificate is mounted in pod

---

### 6. Integration Test Suite (Low Priority for E2E)

**New File**: `api/v1alpha1/tests/spireserver.federation.testsuite.yaml` (1128 lines)

**Recommended E2E Tests** (OPTIONAL):
- ⚪ Verify integration test suite file exists and is valid YAML
- ⚪ Run integration test suite if test framework supports it

---

## Test Prioritization

### 🔴 CRITICAL (Must implement for federation release)

1. **Bundle Endpoint Configuration**
   - https_spiffe profile
   - https_web with ACME
   - https_web with manual TLS
   - ConfigMap generation correctness

2. **Federated Trust Domains**
   - https_spiffe federatesWith (with endpointSpiffeId)
   - https_web federatesWith

3. **Managed Route Lifecycle**
   - Route creation when managedRoute: "true"
   - Route deletion when managedRoute: "false"
   - TLS termination based on profile

4. **Validation and Immutability**
   - Profile immutability enforcement
   - Federation config removal rejection
   - ACME and servingCert mutual exclusivity
   - endpointSpiffeId requirement for https_spiffe

---

### 🟡 IMPORTANT (Should implement for comprehensive coverage)

5. **Resource Verification**
   - StatefulSet port 8443
   - Service port 8443
   - ConfigMap plugin configuration

6. **RBAC**
   - Route permissions
   - External secret access

7. **Sample CRs**
   - ACME sample deployment
   - Manual TLS sample deployment

---

### 🟢 NICE TO HAVE (Optional enhancements)

8. **Schema Validation**
   - Pattern validation for URLs, domains, emails
   - Enum validation
   - Min/max validation

9. **End-to-End Federation Flow**
   - Complete federation setup (would require multi-cluster or mocked federation partner)
   - Bundle endpoint reachability
   - ACME certificate issuance (requires real DNS and ACME server)

---

## Gaps and Limitations

### Areas Hard to Test Automatically

1. **Actual ACME Certificate Issuance**
   - Requires real DNS configuration
   - Requires ACME server to issue certificates
   - Staging ACME servers (Let's Encrypt Staging) still require valid DNS
   - **Mitigation**: Test with mocked ACME server or verify configuration only

2. **Multi-Cluster Federation**
   - Requires second SPIRE cluster with accessible federation endpoint
   - Requires network connectivity between clusters
   - **Mitigation**: Test configuration correctness only; actual federation requires integration test environment

3. **Bundle Endpoint Reachability**
   - Requires Route to be accessible externally
   - Requires DNS resolution
   - **Mitigation**: Test Route creation and configuration; skip actual HTTP(S) request verification

4. **TLS Certificate Validation**
   - Manual TLS requires valid certificate files
   - Cannot test certificate expiry and rotation in short e2e test
   - **Mitigation**: Use self-signed test certificates; verify mount and configuration only

---

## Integration with Existing E2E Tests

The generated test code in `e2e_federation_test.go` follows the existing test patterns:

- **Package**: `e2e` (matches existing)
- **Imports**: Uses same imports as existing tests (`github.com/onsi/ginkgo/v2`, `operatorv1alpha1`, `utils`)
- **Helpers**: Uses existing helper functions (`utils.WaitForSpireServerConditions`, `utils.GetClusterBaseDomain`)
- **Structure**: Uses `Context` and `It` blocks with `By` steps (matches existing style)
- **Cleanup**: Uses `DeferCleanup` and context timeouts (matches existing pattern)

**How to integrate**:
1. Copy the test blocks from `e2e_federation_test.go` into `test/e2e/e2e_test.go`
2. Place them in a new `Context("Federation", func() { ... })` block
3. Ensure they run after the SpireServer is created in the existing test suite
4. Do NOT copy the package declaration, imports, or BeforeSuite logic (already exists)

---

## Recommended Test Execution Order

1. Install operator (existing test)
2. Create ZeroTrustWorkloadIdentityManager (existing test)
3. Create SpireServer with federation disabled (existing test)
4. **NEW: Update SpireServer with https_spiffe federation** → verify resources
5. **NEW: Update SpireServer with https_web ACME** → verify configuration
6. **NEW: Update SpireServer with federatesWith** → verify ConfigMap
7. **NEW: Toggle managedRoute** → verify Route lifecycle
8. **NEW: Test validation rules** → verify immutability and mutual exclusivity
9. Create SpireAgent, CSI Driver, OIDC Provider (existing tests)
10. Cleanup (existing test)

---

## Next Steps

1. **Review** `test-cases.md` for detailed test scenarios
2. **Review** `execution-steps.md` for executable bash commands
3. **Review** `e2e_federation_test.go` for Ginkgo test blocks
4. **Copy** test blocks from `e2e_federation_test.go` into `test/e2e/e2e_test.go`
5. **Adjust** placeholder values if any (APP_DOMAIN is usually auto-detected)
6. **Run** tests against a live OpenShift cluster with the operator installed
7. **Iterate** on failing tests to fix implementation or test expectations

---

## Test Artifacts Summary

| File | Purpose | How to Use |
|------|---------|------------|
| `test-cases.md` | Human-readable test scenarios with `oc` commands | Manual test execution, test planning |
| `execution-steps.md` | Step-by-step bash script for full e2e flow | Copy-paste into terminal, CI/CD pipeline |
| `e2e_federation_test.go` | Ginkgo test blocks for federation | Copy test blocks into `test/e2e/e2e_test.go` |
| `e2e-suggestions.md` | This file - analysis and recommendations | Review and prioritize test implementation |

---

## Conclusion

The diff introduces a **comprehensive federation API** for SpireServer with significant new functionality. The generated e2e tests cover:

- ✅ All new API fields (federation, bundleEndpoint, federatesWith, managedRoute)
- ✅ Controller behavior (ConfigMap generation, StatefulSet ports, Service ports, Route management)
- ✅ Validation rules (immutability, mutual exclusivity, required fields)
- ✅ RBAC permissions (Routes, external secrets)
- ✅ Sample CR deployment

The tests are **diff-driven** (focused on what changed) and **ready to run** with minimal adjustment. All tests follow existing repository patterns and use discovered constants/helpers.

**Estimated Test Coverage**: 85% of federation functionality can be tested automatically. The remaining 15% (actual ACME issuance, multi-cluster federation) requires integration test environment or is tested indirectly through configuration verification.
