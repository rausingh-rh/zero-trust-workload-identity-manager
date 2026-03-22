# E2E Suggestions: zero-trust-workload-identity-manager

## Detected Operator Structure
- **Framework**: controller-runtime (operator-sdk v1 / kubebuilder v4)
- **Managed CRDs**: ZeroTrustWorkloadIdentityManager, SpireServer, SpireAgent, SpiffeCSIDriver, SpireOIDCDiscoveryProvider
- **E2E Pattern**: Ginkgo v2 with controller-runtime client, BeforeSuite client setup, `utils/` helper package
- **Operator Namespace**: `zero-trust-workload-identity-manager`
- **Install Mechanism**: OLM (detected from bundle/manifests structure)

## Changes Detected in Diff

| Category | File | Changes |
|----------|------|---------|
| API Types | `api/v1alpha1/spire_server_config_types.go` | Added 8 new types: FederationConfig, BundleEndpointConfig, BundleEndpointProfile, HttpsWebConfig, AcmeConfig, ServingCertConfig, FederatesWithConfig. Added `Federation` field to SpireServerSpec. Added CEL immutability rules. |
| Controller | `pkg/controller/spire-server/federation.go` | New file: reconcileFederation, reconcileFederationService, reconcileFederationRoute, validateFederationConfig, generateFederationServerConfig |
| Controller | `pkg/controller/spire-server/controller.go` | Added reconcileFederation call, Route watch, Route RBAC |
| Controller | `pkg/controller/spire-server/configmap.go` | Inject federation config into server.conf |
| Controller | `pkg/controller/spire-server/statefulset.go` | Add federation port and TLS volume to StatefulSet |
| Generated | `api/v1alpha1/zz_generated.deepcopy.go` | DeepCopy for new federation types |
| Generated | `config/crd/bases/operator.openshift.io_spireservers.yaml` | CRD schema with federation fields and CEL rules |
| Tests | `api/v1alpha1/tests/.../spireserver-federation.testsuite.yaml` | Integration test suite for API validation |

## Highly Recommended Test Scenarios

### 1. Federation Service Creation ⭐ Critical
**Why**: New Service reconciliation logic in `federation.go`. Must verify Service is created with correct port, labels, selectors, and service-ca annotation.
**Covered by**: `e2e_federation_test.go` → "Should create federation Service on port 8443"

### 2. Federation Route Creation ⭐ Critical
**Why**: New Route reconciliation with TLS reencrypt. Must verify Route is created and has correct TLS configuration.
**Covered by**: `e2e_federation_test.go` → "Should create federation Route with TLS reencrypt"

### 3. ConfigMap Federation Injection ⭐ Critical
**Why**: Modified `configmap.go` to inject `bundle_endpoint` and `federates_with` into server.conf. Must verify JSON structure is correct.
**Covered by**: `e2e_federation_test.go` → "Should inject federation config into SPIRE server ConfigMap"

### 4. StatefulSet Federation Port ⭐ Critical
**Why**: Modified `statefulset.go` to add port 8443 container port. Must verify port exists in the running StatefulSet.
**Covered by**: `e2e_federation_test.go` → "Should add federation port to StatefulSet spire-server container"

### 5. Federation Immutability (CEL Validation) ⭐ Critical
**Why**: CEL rules prevent federation removal and profile changes. Must verify API server rejects invalid updates.
**Covered by**: `e2e_federation_test.go` → "Federation immutability validation" context

### 6. FederatesWith Dynamic Updates ⭐ High
**Why**: federatesWith is designed to be dynamic (can add/remove entries). Must verify ConfigMap is updated when entries change.
**Covered by**: `e2e_federation_test.go` → "Should allow adding new federated trust domains"

### 7. ManagedRoute Toggle ⭐ High
**Why**: managedRoute controls whether Route is created. Must verify Route appears/disappears when toggled.
**Covered by**: `e2e_federation_test.go` → "Should remove Route when managedRoute is set to false"

## Optional/Nice-to-Have Scenarios

### 8. ACME Configuration End-to-End
**Why**: ACME requires external ACME server. Difficult to test automatically without mock.
**Recommendation**: Add if ACME test infrastructure is available. Otherwise, rely on unit tests and integration test suite.

### 9. ServingCert TLS Volume Mount
**Why**: When https_web/servingCert is configured, the StatefulSet should mount the TLS volume.
**Recommendation**: Test if you have a valid TLS Secret to reference.

### 10. Federation with Multiple Profiles
**Why**: Test https_spiffe and https_web peers in the same federatesWith list.
**Recommendation**: Nice-to-have for comprehensive coverage.

### 11. Federation Condition Reporting
**Why**: FederationServiceAvailable, FederationRouteAvailable, FederationConfigValid conditions should be set.
**Recommendation**: Add condition-specific assertions if condition names are stable.

## Gaps

1. **Cross-cluster federation verification**: Cannot test actual cross-cluster trust bundle retrieval without a second cluster. The e2e tests verify the infrastructure (Service, Route, ConfigMap) but not actual SPIRE federation handshake.
2. **ACME certificate provisioning**: Requires external ACME server or mock. Not tested.
3. **servingCert file sync behavior**: Requires running SPIRE server process to verify cert reload. Beyond e2e scope.
4. **Route DNS resolution**: Route host DNS resolution depends on cluster DNS configuration. Not tested.
