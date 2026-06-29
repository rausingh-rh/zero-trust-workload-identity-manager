# API Contracts Guidelines

## API Group and Versioning

| Property | Value |
|----------|-------|
| Group | `operator.openshift.io` |
| Version | `v1alpha1` |
| Full GVK prefix | `operator.openshift.io/v1alpha1` |
| Go package | `github.com/openshift/zero-trust-workload-identity-manager/api/v1alpha1` |
| Kubebuilder layout | `go.kubebuilder.io/v4` |
| CRD version | `v1` |

The operator also deploys CRDs from `spire.spiffe.io/v1alpha1` (vendored from `spire-controller-manager`).

## CRD Resources

### Operator-Owned CRDs (`operator.openshift.io/v1alpha1`)

| Kind | Scope | Singleton | Plural | Source |
|------|-------|-----------|--------|--------|
| ZeroTrustWorkloadIdentityManager | Cluster | `cluster` | zerotrustworkloadidentitymanagers | `zero_trust_workload_identity_manager_types.go` |
| SpireServer | Cluster | `cluster` | spireservers | `spire_server_config_types.go` |
| SpireAgent | Cluster | `cluster` | spireagents | `spire_agent_config_types.go` |
| SpiffeCSIDriver | Cluster | `cluster` | spiffecsidrivers | `spiffe_csi_config_types.go` |
| SpireOIDCDiscoveryProvider | Cluster | `cluster` | spireoidcdiscoveryproviders | `spire_oidc_discovery_provider_types.go` |

### SPIRE Controller-Manager CRDs (`spire.spiffe.io/v1alpha1`)

| Kind | Scope | Purpose |
|------|-------|---------|
| ClusterSPIFFEID | Cluster | Template-based SPIFFE ID assignment to workloads |
| ClusterFederatedTrustDomain | Cluster | Federation relationship with remote trust domains |
| ClusterStaticEntry | Cluster | Static SPIRE registration entries |

These CRDs are installed from `config/crd/bases/*-spiffe-crd.yaml` and managed by the spire-controller-manager sidecar. The operator does **not** reconcile them directly.

## Required Kubebuilder Markers on CRD Types

Every operator-owned CRD type **must** carry this exact marker set:

```go
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="<Kind> is a singleton, .metadata.name must be 'cluster'"
// +operator-sdk:csv:customresourcedefinitions:displayName="<Kind>"
```

The List type requires only:

```go
// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
```

## Singleton Enforcement via CEL XValidation

All operator CRDs are cluster-scoped singletons named `cluster`. This is enforced at admission time via a top-level CEL rule on the type:

```go
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="<Kind> is a singleton, .metadata.name must be 'cluster'"
```

This produces a `x-kubernetes-validations` entry in the OpenAPI schema. No webhook is needed.

## Immutability Enforcement via CEL

Use field-level or type-level `XValidation` with `self == oldSelf` or `oldSelf.<field> == self.<field>`:

| Pattern | Scope | Example |
|---------|-------|---------|
| Field immutability | Field marker | `+kubebuilder:validation:XValidation:rule="self == oldSelf",message="trustDomain is immutable"` |
| Nested field immutability | Type marker | `+kubebuilder:validation:XValidation:rule="oldSelf.spec.persistence.size == self.spec.persistence.size"` |
| Cannot-remove-once-set | Type marker | `+kubebuilder:validation:XValidation:rule="oldSelf == null \|\| !has(oldSelf.spec.federation) \|\| has(self.spec.federation)"` |
| Enum switch lock | Nested type marker | `+kubebuilder:validation:XValidation:rule="!has(oldSelf.profile) \|\| oldSelf.profile == self.profile"` |

Immutable fields in this repo: `trustDomain`, `clusterName`, `bundleConfigMap`, `persistence.size`, `persistence.accessMode`, `persistence.storageClass`, federation removal, `bundleEndpoint.profile`, `httpsWeb` acme/servingCert switch.

## Field-Level Validation Conventions

| Technique | Marker | Example |
|-----------|--------|---------|
| Enum | `+kubebuilder:validation:Enum=a;b;c` | `Enum=debug;info;warn;error` |
| String pattern | `+kubebuilder:validation:Pattern=` | `^[a-z0-9]([a-z0-9\-\.]*[a-z0-9])?$` |
| Min/Max length | `+kubebuilder:validation:MinLength=` / `MaxLength=` | `MinLength=1`, `MaxLength=255` |
| Numeric range | `+kubebuilder:validation:Minimum=` / `Maximum=` | `Minimum=60`, `Maximum=3600` |
| List bounds | `+kubebuilder:validation:MinItems=` / `MaxItems=` | `MaxItems=50` |
| Map bounds | `+kubebuilder:validation:MaxProperties=` | `MaxProperties=64` |
| Default | `+kubebuilder:default:=<value>` | `+kubebuilder:default:="info"` |
| Duration | `+kubebuilder:validation:Format=duration` | Used with `metav1.Duration` |
| Required | `+kubebuilder:validation:Required` | Plus `+required` for openapi-gen |
| Boolean-as-string | `Enum:="true";"false"` | Used for booleans needing explicit tri-state |
| Cross-field exclusive | CEL on parent struct | `(has(self.a) && !has(self.b)) \|\| (!has(self.a) && has(self.b))` |
| Conditional required | CEL on parent struct | `self.profile == 'https_web' ? has(self.httpsWeb) : true` |

## CommonConfig Embedding Pattern

All operand specs embed `CommonConfig` as an inline struct to provide uniform pod-scheduling fields:

```go
type SomeOperandSpec struct {
    // ... operand-specific fields ...
    CommonConfig `json:",inline"`
}
```

`CommonConfig` provides: `labels` (map, max 64), `resources` (`*corev1.ResourceRequirements`), `affinity` (`*corev1.Affinity`), `tolerations` (`[]*corev1.Toleration`, max 50, atomic list), `nodeSelector` (map, max 50, atomic map).

**Rules:**
- Always embed as the **last** field in the spec struct.
- Use `json:",inline"` tag -- never give it a JSON key.
- `CommonConfig` is defined once in `zero_trust_workload_identity_manager_types.go` alongside the ZTWIM type.

## ConditionalStatus Embedding Pattern

All operand status structs embed `ConditionalStatus` for uniform condition handling:

```go
type SomeOperandStatus struct {
    ConditionalStatus `json:",inline,omitempty"`
}
```

`ConditionalStatus` (defined in `meta.go`) holds `Conditions []metav1.Condition` with merge-patch list semantics (`+listType=map`, `+listMapKey=type`).

Every operand type must also implement a `GetConditionalStatus() ConditionalStatus` method.

The ZTWIM status extends this with an `Operands []OperandStatus` field (list-map keyed by `kind`).

## Status Subresource Patterns

### Condition Types

| Constant | Used By | Status Values | Reasons |
|----------|---------|---------------|---------|
| `Ready` | All CRDs | True/False | `Ready`, `Progressing`, `Failed` |
| `Degraded` | All CRDs | True/False | `Failed` |
| `Upgradeable` | ZTWIM only | True/False | `Ready`, `OperandsNotReady` |

### Reasons (constants in `conditions.go`)

`ReasonFailed`, `ReasonReady`, `ReasonInProgress`, `ReasonOperandsNotReady`, `ReasonResourceConflict`

### OperandStatus (ZTWIM-specific)

The ZTWIM `.status.operands[]` array uses `kind` as the list-map key and carries: `name`, `kind` (enum-validated), `ready` (pattern `^(true|false)$`), `message` (max 32768 chars), and nested `conditions`.

## Code Generation Pipeline

```bash
# After modifying *_types.go files:
make generate    # Regenerates zz_generated.deepcopy.go
make manifests   # Regenerates config/crd/bases/*.yaml and RBAC

# Both in one shot:
make build       # Runs manifests + generate + fmt + vet + compile
```

Generated outputs -- **never hand-edit**:
- `api/v1alpha1/zz_generated.deepcopy.go`
- `config/crd/bases/operator.openshift.io_*.yaml`

## How to Add a New API Field

1. Add the field to the appropriate `*Spec` or `*Status` struct in `api/v1alpha1/`.
2. Add kubebuilder validation markers (Required/Optional, Enum, Pattern, Min/Max, Default).
3. If immutable, add a CEL `XValidation` rule.
4. Run `make generate` (deepcopy).
5. Run `make manifests` (CRD YAML).
6. Update controller logic to read/use the new field.
7. Update bindata templates if the field affects rendered YAML.
8. Run `make update-bindata` if templates changed.
9. Add unit tests covering validation (valid values, invalid values, immutability).
10. Run `make verify` to confirm lint/fmt pass.

## How to Add a New CRD

1. Create `api/v1alpha1/<name>_types.go` with the required marker set (cluster-scoped, singleton, status subresource).
2. Define `<Name>Spec` (embed `CommonConfig` inline as last field) and `<Name>Status` (embed `ConditionalStatus` inline).
3. Implement `GetConditionalStatus() ConditionalStatus` on the type.
4. Add `SchemeBuilder.Register(&<Name>{}, &<Name>List{})` in `func init()`.
5. Run `make generate && make manifests`.
6. Add the new CRD YAML path to `config/crd/kustomization.yaml`.
7. Create controller in `pkg/controller/<name>/` following the reconciler pattern.
8. Register controller in `cmd/zero-trust-workload-identity-manager/main.go`.
9. Add the type to the ZTWIM controller's `OperandStatus` enum and aggregation logic.
10. Update RBAC markers on the new controller, then `make manifests`.
11. Add the new type to `pkg/client/client.go` cache lists if applicable.
12. Run `make bundle` to update OLM metadata.

## spire.spiffe.io CRD Integration

The operator deploys three CRDs from the spire-controller-manager (`spire.spiffe.io/v1alpha1`):

| CRD YAML | Installed By |
|-----------|-------------|
| `config/crd/bases/clusterspiffeids-spiffe-crd.yaml` | Operator (kustomize) |
| `config/crd/bases/clusterfederatedtrustdomains-spiffe-crd.yaml` | Operator (kustomize) |
| `config/crd/bases/clusterstaticentries-spiffe-crd.yaml` | Operator (kustomize) |

**Integration rules:**
- These CRD YAMLs are **vendored snapshots** -- update by bumping the `spire-controller-manager` dependency and copying new CRD manifests.
- Go types are imported from `github.com/spiffe/spire-controller-manager/api/v1alpha1` (vendored).
- The scheme is registered in `main.go` alongside the operator's own types.
- The SpireServer controller creates `ClusterFederatedTrustDomain` CRs when federation is configured.
- Users create `ClusterSPIFFEID` and `ClusterStaticEntry` CRs directly; the spire-controller-manager sidecar reconciles them against the SPIRE Server API.
- The operator never modifies or reconciles `ClusterSPIFFEID` or `ClusterStaticEntry` resources.
