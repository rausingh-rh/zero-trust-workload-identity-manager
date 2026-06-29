# AGENTS.md

## Docs Index

Detailed domain-specific guidelines are in these files -- read them before working in the corresponding area:

- [docs/error-handling-guidelines.md](docs/error-handling-guidelines.md) -- Error wrapping, status conditions, retry logic, ReconcileError classification
- [docs/testing-guidelines.md](docs/testing-guidelines.md) -- Unit test patterns, FakeCustomCtrlClient, E2E with Ginkgo, test helpers
- [docs/api-contracts-guidelines.md](docs/api-contracts-guidelines.md) -- CRD types, kubebuilder markers, CEL validation, CommonConfig, code generation
- [docs/security-guidelines.md](docs/security-guidelines.md) -- FIPS builds, RBAC, SCCs, TLS, federation security, metrics protection
- [docs/performance-guidelines.md](docs/performance-guidelines.md) -- Cache architecture, watch predicates, drift detection, status update optimization
- [docs/integration-guidelines.md](docs/integration-guidelines.md) -- Bindata pattern, ConfigMap generation, federation, SPIRE controller-manager, OpenShift platform

## Project Overview

This is a Kubernetes operator (built with controller-runtime, not `openshift/library-go`) that deploys and manages upstream [SPIFFE/SPIRE](https://github.com/spiffe/spiffe) components on OpenShift clusters. The operator does not embed upstream code -- it manages upstream resources as static YAML manifests (bindata) applied imperatively, and deploys upstream container images.

Five controllers run in a single binary:

| Controller | Package | Watches | Purpose |
|---|---|---|---|
| `zero-trust-workload-identity-manager-controller` | `pkg/controller/zero-trust-workload-identity-manager/` | ZTWIM CR + all operand CR statuses + `OperatorCondition` | Aggregates operand statuses, sets Ready/OperandsAvailable/CreateOnlyMode, syncs Upgradeable to OLM OperatorCondition |
| `zero-trust-workload-identity-manager-spire-server-controller` | `pkg/controller/spire-server/` | `SpireServer` CR + ZTWIM CR + managed resources | Reconciles SPIRE server StatefulSet, RBAC, ConfigMaps, webhooks, Routes, federation |
| `zero-trust-workload-identity-manager-spire-agent-controller` | `pkg/controller/spire-agent/` | `SpireAgent` CR + ZTWIM CR + managed resources | Reconciles SPIRE agent DaemonSet, RBAC, SCC, ConfigMap |
| `zero-trust-workload-identity-manager-spiffe-csi-driver-controller` | `pkg/controller/spiffe-csi-driver/` | `SpiffeCSIDriver` CR + ZTWIM CR + managed resources | Reconciles SPIFFE CSI driver DaemonSet, RoleBinding (privileged SCC), CSIDriver |
| `zero-trust-workload-identity-manager-spire-oidc-discovery-provider-controller` | `pkg/controller/spire-oidc-discovery-provider/` | `SpireOIDCDiscoveryProvider` CR + ZTWIM CR + managed resources | Reconciles OIDC SA, Service, ClusterSPIFFEIDs, ConfigMap, Deployment, RBAC, Route |

## Project Structure

```text
api/v1alpha1/          CRD type definitions, conditions, shared types, deepcopy
bindata/               Static operand YAML manifests (compiled into Go via go-bindata)
bundle/                OLM bundle (CSV, CRDs, metadata, scorecard)
cmd/                   Operator entrypoint (main.go)
config/                Kustomize manifests (CRDs, RBAC, manager, samples)
hack/                  go-fips.sh, boilerplate license header
pkg/controller/        Controller implementations
  zero-trust-workload-identity-manager/  Status aggregation controller
  spire-server/        SPIRE server operand controller
  spire-agent/         SPIRE agent operand controller
  spiffe-csi-driver/   SPIFFE CSI driver operand controller
  spire-oidc-discovery-provider/  OIDC discovery provider operand controller
  status/              Shared status management (condition collection, auto-Ready)
  utils/               Predicates, constants, validation, resource comparison, errors
pkg/client/            CustomCtrlClient interface + counterfeiter fakes + cache builder
pkg/operator/assets/   Generated bindata.go (never hand-edit)
pkg/version/           Build-time version info (ldflags)
test/e2e/              End-to-end tests (Ginkgo v2 + live OpenShift cluster)
tools/                 Go module for build-time tool dependencies
vendor/                Tracked vendored dependencies
```

## Upstream Operand Projects

The operator manages components from these separate upstream SPIRE repositories via container images and generated manifests:

| Upstream repo | Operator integration |
|---|---|
| `github.com/spiffe/spire` | Container images for server (StatefulSet), agent (DaemonSet), OIDC provider (Deployment) |
| `github.com/spiffe/spire-controller-manager` | Go module dependency for API types; image deployed as sidecar with SPIRE server |
| `github.com/spiffe/spiffe-csi` | Container image for CSI driver (DaemonSet) |

The operator imports `github.com/spiffe/spire-controller-manager` as a Go module for `ControllerManagerConfig` and CRD types (`ClusterSPIFFEID`, `ClusterFederatedTrustDomain`, `ClusterStaticEntry`).

## Release Process

Releases are managed through a separate repository: [`openshift/zero-trust-workload-identity-manager-release`](https://github.com/openshift/zero-trust-workload-identity-manager-release). This repo uses a dual-branch model:

| Branch | Purpose |
|---|---|
| `release-*` (e.g. `release-1.1`) | Builds operator + operand container images via Konflux; tracks submodules of ZTWIM and all operand forks |
| `main` | Maintains File-Based Catalog (FBC) declarative config for per-OCP-version OLM indexes (v4.18–v4.22) |

The release repo tracks these OpenShift forks as submodules (on release branches):

| Submodule | Upstream |
|---|---|
| `zero-trust-workload-identity-manager` | `openshift/zero-trust-workload-identity-manager` |
| `spiffe-spire` | `openshift/spiffe-spire` |
| `spiffe-spire-controller-manager` | `openshift/spiffe-spire-controller-manager` |
| `spiffe-spiffe-csi` | `openshift/spiffe-spiffe-csi` |
| `spiffe-spiffe-helper` | `openshift/spiffe-spiffe-helper` |

Key workflow: operand images built → digests pinned in `images_digest.conf` → bundle built → `make update-catalog` on main → FBC index images built per OCP version.

## Build System (Key Makefile Targets)

| Target | What it does |
|---|---|
| `make all` | `build verify` (default) |
| `make build` | Full build: manifests + generate + fmt + vet + compile binary |
| `make build-operator` | Compile binary only (FIPS-aware, vendor mode) |
| `make test` | Unit tests with envtest (K8s 1.31.0 assets) |
| `make test-e2e` | E2E tests against a live OpenShift cluster (45min timeout) |
| `make lint` | Run golangci-lint |
| `make verify` | vet + fmt check + golangci-lint |
| `make manifests` | Regenerate CRD/RBAC/webhook YAML from kubebuilder markers |
| `make generate` | Regenerate DeepCopy methods |
| `make update-bindata` | Regenerate `pkg/operator/assets/bindata.go` from `bindata/` YAML |
| `make vendor` | `go mod tidy && go mod vendor` |
| `make bundle` | Generate OLM bundle |
| `make docker-build` | Build container image |
| `make install` | Install CRDs into cluster |
| `make deploy` | Deploy operator into cluster |

After code changes to API types or bindata, run `make manifests generate update-bindata` then `make verify` to ensure generated files are consistent.

## Code Style and Formatting

### Import Order

Imports are grouped by `goimports` (enforced by CI):

1. Standard library
2. `k8s.io/*`, `sigs.k8s.io/*`
3. Third-party (`github.com/go-logr/logr`, `github.com/operator-framework/api`, etc.)
4. OpenShift (`github.com/openshift/api`, `github.com/openshift/client-go`)
5. This project (`github.com/openshift/zero-trust-workload-identity-manager/...`)

### Standard Import Aliases

| Alias | Package |
|---|---|
| `ctrl` | `sigs.k8s.io/controller-runtime` |
| `kerrors` | `k8s.io/apimachinery/pkg/api/errors` |
| `apimeta` | `k8s.io/apimachinery/pkg/api/meta` |
| `customClient` | `github.com/openshift/zero-trust-workload-identity-manager/pkg/client` |
| `routev1` | `github.com/openshift/api/route/v1` |

### Linting

The repo uses golangci-lint with a selective allowlist (see `.golangci.yml`). Key rules:
- `errcheck`, `govet`, `staticcheck`, `revive`, `ginkgolinter`, `gofmt`, `goimports` are enabled.
- `dupl` and `lll` are relaxed under `pkg/*` and `api/*`.
- `revive` enforces `comment-spacings`.
- Timeout: 5 minutes.

### File Headers

All `.go` files must include the Apache 2.0 license header from `hack/boilerplate.go.txt`.

### Vendoring

Dependencies are vendored and tracked in git. After any `go.mod` change: `make vendor` (runs `go mod tidy && go mod vendor`). Always commit the `vendor/` diff alongside `go.mod`/`go.sum`.

### FIPS Build

Production builds use `hack/go-fips.sh` which enables `GOEXPERIMENT=strictfipsruntime` and build tags `strictfipsruntime,openssl` when the Go compiler supports it. Local dev builds without FIPS work but are not suitable for CI/production.

## Naming Conventions

### Go Packages

Controller packages use `kebab-case` directories: `spire-server`, `spire-agent`, `spiffe-csi-driver`, `spire-oidc-discovery-provider`. Package names use `snake_case` Go identifiers: `spire_server`, `spire_agent`, etc.

### Constants

- Controller names: `"zero-trust-workload-identity-manager-<component>-controller"` (defined in `pkg/controller/utils/constants.go`).
- Asset path constants: `Spire*AssetName` or `Spiffe*AssetName` (e.g., `SpireServerServiceAssetName`).
- Image env var constants: `RELATED_IMAGE_*` suffix with `*ImageEnv` Go constant name (e.g., `SpireServerImageEnv`).
- Resource kind constants: `ResourceKind*` (e.g., `ResourceKindSpireServer`).

### CRD Object Names

All five operator CRDs (`ZeroTrustWorkloadIdentityManager`, `SpireServer`, `SpireAgent`, `SpiffeCSIDriver`, `SpireOIDCDiscoveryProvider`) are singletons named `"cluster"` (enforced by CEL). The operator namespace is `"zero-trust-workload-identity-manager"` (from `OPERATOR_NAMESPACE` env, required).

### Labels

All managed resources carry `app.kubernetes.io/managed-by: zero-trust-workload-identity-manager`. This label is used by the cache label selector.

## Architectural Patterns

### Reconciler Structure

Every operand controller follows the same pattern:

1. A `Reconciler` struct with `ctrlClient customClient.CustomCtrlClient`, `ctx context.Context`, `log logr.Logger`, `scheme *runtime.Scheme`, `eventRecorder record.EventRecorder`.
2. A `New(mgr)` constructor that builds the reconciler and custom client (sets `ctx` to `context.Background()`).
3. A `SetupWithManager(mgr)` method that wires watches, predicates, and map functions.
4. A `Reconcile(ctx, req)` method following the standard flow (see below).

### CustomCtrlClient Interface

All controllers interact with Kubernetes through `pkg/client.CustomCtrlClient`, not the raw controller-runtime `client.Client`. This interface adds `UpdateWithRetry`, `StatusUpdateWithRetry`, `CreateOrUpdateObject`, and `Exists` methods. Unit tests use counterfeiter-generated fakes (`pkg/client/fakes/`).

To regenerate fakes after changing the interface: `go generate ./pkg/client/...`

### Operand Reconciliation Flow

Every operand reconciler follows this flow. Do not deviate:

1. `Get` the operand CR (`cluster`); `IsNotFound` → return nil (no requeue).
2. Create `status.NewManager(...)` with **`defer statusMgr.ApplyStatus(...)`** (auto-calls `SetReadyCondition()` if Ready not explicitly set).
3. `Get` the parent `ZeroTrustWorkloadIdentityManager` (`cluster`); missing → `Ready=False/Failed`, return nil.
4. Set controller reference from ZTWIM → operand if needed.
5. Check `CREATE_ONLY_MODE` env via `handleCreateOnlyMode`.
6. **Validate configuration** (`validateConfiguration` / `validateCommonConfig`); if invalid → set condition, return nil (no requeue).
7. Run ordered `reconcile*` steps (SA → Service → RBAC → ConfigMaps → workload → Route...).
8. Each step adds a typed condition to the status manager.

Note: `ApplyStatus` uses semantic equality (`k8s.io/apimachinery/pkg/api/equality`) to skip no-op writes. If no controller explicitly set a `Ready` condition, `SetReadyCondition()` auto-derives it from all other conditions.

### ZTWIM Aggregator Pattern

The top-level `ZeroTrustWorkloadIdentityManager` controller does NOT create operand CRs. It reads each operand CR's status and aggregates into `status.operands`, then sets `Ready`, `OperandsAvailable`, and `CreateOnlyMode` conditions on the ZTWIM CR. It also syncs `Upgradeable` to the OLM `OperatorCondition` resource (best-effort, not on the ZTWIM CR itself).

Watches:
- Own CR with `predicate.GenerationChangedPredicate` (standard library, not custom).
- All four operand CRs with `operandStatusChangedPredicate` (status-only updates).
- `OperatorCondition` with `operandStatusChangedPredicate`.

### Watch and Predicate Conventions

- **Operand controllers** watch their own CR with `GenerationOrOwnerReferenceChangedPredicate` and managed resources with component-specific label predicates (`ControllerManagedResourcesForComponent` using component identifiers like `ComponentControlPlane`, `ComponentNodeAgent`, `ComponentCSI`, `ComponentDiscovery`).
- **All operand controllers** also watch the `ZeroTrustWorkloadIdentityManager` CR with `ZTWIMSpecChangedPredicate`, causing re-reconciliation when the parent CR spec changes.
- **ZTWIM controller** uses the standard `predicate.GenerationChangedPredicate` on itself (not the custom one used by operands).

### Static Manifest (Bindata) Pattern

All operand Kubernetes resources live as YAML in `bindata/` organized by component. They are compiled into `pkg/operator/assets/bindata.go` via go-bindata. At reconcile time:
1. Decode bytes with `assets.MustAsset(path)` and runtime deserialization.
2. Mutate the decoded object: set namespace, merge labels from `CommonConfig`, set owner references.
3. Check existence, compare with `ResourceNeedsUpdate()`, then `Create` or `UpdateWithRetry`.

When adding a new resource: add YAML to `bindata/`, add constant in `utils/constants.go`, run `make update-bindata`, and follow existing `reconcile*` patterns.

## Common Pitfalls

1. **Never edit generated files by hand**: `zz_generated.deepcopy.go`, `config/crd/bases/*.yaml`, `pkg/operator/assets/bindata.go`. Always use `make generate`, `make manifests`, `make update-bindata`.
2. **Always run `make verify`** after code changes. CI will reject PRs with stale generated files or lint failures.
3. **Vendor is tracked in git**: after dependency changes, always `make vendor` and commit the vendor directory.
4. **Never return both `RequeueAfter` and a non-nil error** from `Reconcile`. Return one or the other.
5. **Add new watched resources to `NewCacheBuilder`** in `pkg/client/client.go` with the correct label selector, or the cache won't see them.
6. **Add new resource types to `ResourceNeedsUpdate`** in `pkg/controller/utils/` with field-level comparison, not full `DeepEqual`.
7. **PROJECT file is stale** -- it references `ExternalSecrets` which no longer exists. Ignore it; actual CRDs are in `api/v1alpha1/`.
8. **README prereqs lag** -- says Go 1.23+ but `go.mod` requires 1.25+.
9. **Federation is one-way** -- `SpireServer` federation cannot be removed once set; persistence fields (size, accessMode, storageClass) are immutable. These are CEL-enforced.
10. **Create-only mode** (`CREATE_ONLY_MODE=true`) creates resources but skips all updates. Surfaced as `CreateOnlyMode` condition and blocks `Upgradeable` on OLM `OperatorCondition`.
11. **Scheme registration** -- `main.go` registers OpenShift SCC, Route, OLM, and SPIRE controller-manager schemes. If you add new external types, register them there.
12. **Cache label selectors** -- managed resources are cached with `managed-by` label selector. Creating a resource without that label means the cache won't see it.

## PR and Contribution Expectations

- Run `make verify` and `make test` locally before submitting.
- Add unit tests for new reconciliation logic using table-driven tests and `FakeCustomCtrlClient`.
- Follow existing error wrapping patterns: `fmt.Errorf("...: %w", err)` for wrapping, status conditions for validation failures.
- Each reconciler sub-function gets its own `*_test.go` file (e.g., `service_account_test.go`).
- PR reviewers/approvers are listed in `OWNERS`.

## Environment Variables

The operator reads these at runtime (typically set by OLM/CSV):

| Variable | Purpose |
|---|---|
| `OPERATOR_NAMESPACE` | Namespace the operator runs in (required) |
| `OPERATOR_CONDITION_NAME` | OLM OperatorCondition name for Upgradeable sync (required) |
| `CREATE_ONLY_MODE` | When `true`, skip updates to existing resources |
| `RELATED_IMAGE_SPIRE_SERVER` | SPIRE server container image |
| `RELATED_IMAGE_SPIRE_AGENT` | SPIRE agent container image |
| `RELATED_IMAGE_SPIFFE_CSI_DRIVER` | SPIFFE CSI driver container image |
| `RELATED_IMAGE_SPIRE_OIDC_DISCOVERY_PROVIDER` | OIDC discovery provider container image |
| `RELATED_IMAGE_SPIRE_CONTROLLER_MANAGER` | SPIRE controller manager container image |
| `RELATED_IMAGE_NODE_DRIVER_REGISTRAR` | CSI node driver registrar container image |
| `RELATED_IMAGE_SPIFFE_CSI_INIT_CONTAINER` | SPIFFE CSI init container image |
