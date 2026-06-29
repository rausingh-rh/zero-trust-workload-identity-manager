# Integration Guidelines

## Architecture Overview

The ZTWIM operator manages upstream SPIFFE/SPIRE components on OpenShift without embedding or forking upstream code. It deploys upstream container images and manages Kubernetes resources through two mechanisms:

1. **Static YAML manifests (bindata)** — RBAC, ServiceAccounts, Services, CSIDriver objects, and ValidatingWebhookConfigurations are compiled into Go via go-bindata from `bindata/`, decoded at runtime, mutated (namespace, labels, owner refs), and applied imperatively.
2. **Programmatically generated resources** — StatefulSets, DaemonSets, Deployments, ConfigMaps, Routes, and ClusterSPIFFEIDs are built entirely in Go from CR spec fields. No bindata template is used for these.

Five controllers run in a single binary (`cmd/zero-trust-workload-identity-manager/main.go`), each constructed with `New(mgr)` and wired with `SetupWithManager(mgr)`.

## CRD Hierarchy and Singleton Pattern

All five CRDs are cluster-scoped singletons named `"cluster"` (enforced by CEL: `self.metadata.name == 'cluster'`).

```
ZeroTrustWorkloadIdentityManager ("cluster")      ← parent, status aggregator
  ├── SpireServer ("cluster")                      ← server StatefulSet + controller-manager sidecar
  ├── SpireAgent ("cluster")                       ← agent DaemonSet
  ├── SpiffeCSIDriver ("cluster")                  ← CSI driver DaemonSet
  └── SpireOIDCDiscoveryProvider ("cluster")       ← OIDC Deployment + Route + ClusterSPIFFEIDs
```

The ZTWIM CR does **not** create operand CRs. Users create them independently. ZTWIM only aggregates their status.

## Parent-Child Owner References

Every operand reconciler fetches the ZTWIM singleton at `types.NamespacedName{Name: "cluster"}` and sets it as the controller owner:

```go
if utils.NeedsOwnerReferenceUpdate(&server, &ztwim) {
    controllerutil.SetControllerReference(&ztwim, &server, r.scheme)
    r.ctrlClient.Update(ctx, &server)
}
```

`NeedsOwnerReferenceUpdate` (`pkg/controller/utils/utils.go`) checks by UID, name, and kind to prevent unnecessary updates. The child resources (StatefulSet, DaemonSet, etc.) in turn have the operand CR as their controller owner via a second `SetControllerReference` call.

## Static Manifest (Bindata) Pattern

Bindata resources are organized under `bindata/<component>/`:

```
bindata/
├── spiffe-csi/          (CSIDriver, ServiceAccount, privileged RoleBinding)
├── spire-agent/         (ClusterRole, ClusterRoleBinding, ServiceAccount, Service)
├── spire-bundle/        (Role, RoleBinding)
├── spire-controller-manager/ (ClusterRole, ClusterRoleBinding, leader election Role/Binding, webhook Service, ValidatingWebhookConfiguration)
├── spire-oidc-discovery-provider/ (ServiceAccount, Service, external cert Role/RoleBinding)
└── spire-server/        (ClusterRole, ClusterRoleBinding, ServiceAccount, Service, external cert Role/RoleBinding)
```

The reconciliation pattern for bindata resources:

1. Load bytes with `assets.MustAsset(constantPath)`.
2. Decode with typed decoders in `utils.go` (e.g., `DecodeServiceAccountObjBytes`, `DecodeClusterRoleObjBytes`).
3. Mutate: set namespace via `utils.GetOperatorNamespace()`, merge labels via component label functions (e.g., `utils.SpireServerLabels(config.Labels)`), set `managed-by` label.
4. Set controller reference from operand CR.
5. Get → IsNotFound → Create, else compare with `utils.ResourceNeedsUpdate()` → Update (respecting create-only mode).

Constants for all asset paths live in `pkg/controller/utils/constants.go` (e.g., `SpireServerServiceAccountAssetName = "spire-server/spire-server-service-account.yaml"`).

## Reconciliation Order

### SpireServer Controller

1. Get `SpireServer` CR → Get `ZeroTrustWorkloadIdentityManager` CR
2. Set ZTWIM → SpireServer controller reference
3. Handle create-only mode
4. Validate configuration (common config, proxy, JWT issuer, federation, upstream authority)
5. Validate TTL durations
6. Reconcile ServiceAccount (bindata)
7. Reconcile Services — `spire-server` and `spire-controller-manager-webhook` (bindata)
8. Reconcile RBAC — server, bundle, controller-manager ClusterRoles/Bindings + Roles/Bindings (bindata)
9. Reconcile ValidatingWebhookConfiguration (bindata)
10. Reconcile SPIRE server ConfigMap (generated, returns config hash)
11. Reconcile controller-manager ConfigMap (generated, returns config hash)
12. Reconcile bundle ConfigMap (create-only, never updated — SPIRE server populates it)
13. Reconcile StatefulSet (generated, annotated with both config hashes)
14. Reconcile federation Route (generated, if `federation.managedRoute == "true"`)

### SpireAgent Controller

1. Get `SpireAgent` CR → Get ZTWIM CR
2. Set ZTWIM → SpireAgent controller reference
3. Handle create-only mode, validate configuration (proxy, common config)
4. Reconcile ServiceAccount, Service, RBAC (bindata)
5. Reconcile SCC — `spire-agent` SecurityContextConstraints (bindata)
6. Reconcile ConfigMap (generated, returns config hash)
7. Reconcile DaemonSet (generated, annotated with config hash)

### SpiffeCSIDriver Controller

1. Get `SpiffeCSIDriver` CR → Get ZTWIM CR
2. Set ZTWIM → SpiffeCSIDriver controller reference
3. Handle create-only mode, validate common config
4. Reconcile ServiceAccount (bindata)
5. Reconcile CSIDriver object (bindata)
6. Reconcile privileged SCC RoleBinding (bindata)
7. Reconcile DaemonSet (generated)

### SpireOIDCDiscoveryProvider Controller

1. Get `SpireOIDCDiscoveryProvider` CR → Get ZTWIM CR
2. Set ZTWIM → SpireOIDCDiscoveryProvider controller reference
3. Handle create-only mode, validate configuration (proxy, JWT issuer, common config)
4. Reconcile ServiceAccount, Service (bindata)
5. Reconcile ClusterSPIFFEIDs — OIDC provider + default fallback (generated)
6. Reconcile ConfigMap (generated, returns config hash)
7. Reconcile Deployment (generated, annotated with config hash)
8. Reconcile external certificate RBAC (bindata, conditional on `externalSecretRef`)
9. Reconcile Route (generated, if enabled)

## ConfigMap Generation Patterns

ConfigMaps are generated programmatically from CR spec fields (not from bindata templates).

**SPIRE Server ConfigMap** (`spire-server`): Built by `generateServerConfMap()` as a nested Go `map[string]interface{}`, marshaled to JSON. Contains server config (bind address, trust domain, CA settings, TTLs, JWT issuer), plugins (DataStore, KeyManager, NodeAttestor/k8s_psat, Notifier/k8sbundle), health checks, and telemetry. Federation and UpstreamAuthority plugin blocks are conditionally added.

**Controller-Manager ConfigMap** (`spire-controller-manager`): Built by `generateControllerManagerConfig()` as a `ControllerManagerConfigYAML` struct (embedding `spiffev1alpha.ControllerManagerConfig`), marshaled to YAML. Key fields: `className: "zero-trust-workload-identity-manager-spire"`, `parentIDTemplate`, `clusterName`, `trustDomain`, `entryIDPrefix`, `ignoreNamespaces`.

**Bundle ConfigMap** (name from `ztwim.Spec.BundleConfigMap`, default `spire-bundle`): Created empty with labels. Never updated by the operator — the SPIRE server's `k8sbundle` Notifier plugin populates it at runtime with the trust bundle.

**Agent ConfigMap** (`spire-agent`): Built by `generateAgentConfig()` as a nested map, marshaled to JSON. Contains agent config (trust domain, server address, SDS settings), plugins (KeyManager/memory, NodeAttestor/k8s_psat, WorkloadAttestor/k8s with kubelet verification settings).

All generated ConfigMaps produce a SHA256 config hash that is stored as a pod template annotation (e.g., `ztwim.openshift.io/spire-server-config-hash`) to trigger rolling updates when config changes.

## Federation Integration

Federation is configured through `SpireServer.spec.federation`:

- **Route generation**: `generateFederationRoute()` creates an OpenShift Route named `spire-server-federation` with host `federation.<trustDomain>`. TLS termination depends on profile: `https_spiffe` → passthrough, `https_web` with ACME → passthrough, `https_web` with `servingCert` → re-encrypt.
- **Bundle endpoint**: Added to SPIRE server config under `server.federation.bundle_endpoint` with profile-specific settings (`https_spiffe` or `https_web` with ACME/serving_cert_file).
- **Federated trust domains**: Added under `server.federation.federates_with` with per-domain endpoint URL and profile.
- **TLS for ServingCert**: The service CA annotation (`service.beta.openshift.io/serving-cert-secret-name: spire-server-serving-cert`) on the server Service causes OpenShift to generate a `spire-server-serving-cert` Secret, mounted at `/run/spire/server-tls/` in the StatefulSet.
- **External certificates**: When `externalSecretRef` is set, RBAC Role/RoleBinding are created from bindata to grant the router ServiceAccount read access to the Secret. The Route's `spec.tls.externalCertificate` references this Secret.

Federation configuration is immutable once set (CEL-enforced).

## SPIRE Controller-Manager Integration

The controller-manager runs as a sidecar container in the SPIRE server StatefulSet, sharing the SPIRE server socket via an `emptyDir` volume at `/tmp/spire-server/private/`. Key integration points:

- **ControllerManagerConfig YAML**: Generated with `className: "zero-trust-workload-identity-manager-spire"`, `parentIDTemplate` using k8s_psat format, `watchClassless: false`.
- **ClusterSPIFFEID reconciliation**: The OIDC controller creates two ClusterSPIFFEIDs — one for the OIDC provider pods (with `ClassName: "zero-trust-workload-identity-manager-spire"` and a `PodSelector` matching OIDC provider labels), and a default fallback for all other workloads (with `Fallback: true`).

## OpenShift Platform Integrations

- **Routes** (`routev1`): Federation Route on SpireServer, OIDC discovery Route on SpireOIDCDiscoveryProvider. Scheme registered in `main.go`.
- **SCCs** (`securityv1`): SpireAgent gets a custom SCC. SpiffeCSIDriver uses a RoleBinding to the `privileged` SCC. Scheme registered in `main.go`.
- **Service CA**: The `service.beta.openshift.io/serving-cert-secret-name` annotation on Services triggers automatic TLS certificate generation by the OpenShift service CA operator.
- **OLM OperatorCondition**: ZTWIM controller updates the `Upgradeable` condition on the OLM `OperatorCondition` resource based on operand readiness and create-only mode.

## Image Management

Container images are resolved at runtime from `RELATED_IMAGE_*` environment variables (set by OLM from the CSV). Each image has a constant, a getter function, and usage site:

| Env var | Getter | Used in |
|---|---|---|
| `RELATED_IMAGE_SPIRE_SERVER` | `utils.GetSpireServerImage()` | StatefulSet container |
| `RELATED_IMAGE_SPIRE_CONTROLLER_MANAGER` | `utils.GetSpireControllerManagerImage()` | StatefulSet sidecar container |
| `RELATED_IMAGE_SPIRE_AGENT` | `utils.GetSpireAgentImage()` | DaemonSet container |
| `RELATED_IMAGE_SPIFFE_CSI_DRIVER` | `utils.GetSpiffeCSIDriverImage()` | DaemonSet container |
| `RELATED_IMAGE_SPIRE_OIDC_DISCOVERY_PROVIDER` | `utils.GetSpireOIDCDiscoveryProviderImage()` | Deployment container |
| `RELATED_IMAGE_NODE_DRIVER_REGISTRAR` | `utils.GetNodeDriverRegistrarImage()` | CSI DaemonSet sidecar |
| `RELATED_IMAGE_SPIFFE_CSI_INIT_CONTAINER` | `utils.GetSpiffeCsiInitContainerImage()` | CSI DaemonSet init container |

Images are set directly on programmatically generated workloads (e.g., `Image: utils.GetSpireServerImage()` in `GenerateSpireServerStatefulSet`). They are never substituted in bindata templates.

## Client Architecture

`pkg/client/client.go` defines `CustomCtrlClient` wrapping controller-runtime's `client.Client` with retry helpers. The cache is built with `NewCacheBuilder()`:

- **Label-filtered cache** (`cacheResources`): StatefulSet, DaemonSet, Deployment, ConfigMap, Service, ServiceAccount, RBAC, ValidatingWebhookConfiguration, Route, ClusterSPIFFEID, CSIDriver — all filtered by `app.kubernetes.io/managed-by=zero-trust-workload-identity-manager`. Creating a resource **without this label** means the cache won't see it.
- **Unfiltered cache** (`cacheResourceWithoutReqSelectors`): ZTWIM, SpireServer, SpireAgent, SpiffeCSIDriver, SpireOIDCDiscoveryProvider, OperatorCondition — no label filter since these are the CRs themselves.

Each controller creates its own `CustomCtrlClient` instance via `customClient.NewCustomClient(mgr)`, but all share the manager's unified cache.

## Create-Only Mode

When `CREATE_ONLY_MODE=true` (case-insensitive), every operand reconciler:
1. Calls `utils.IsInCreateOnlyMode()` to check the env var.
2. Sets a `CreateOnlyMode` status condition on the operand CR.
3. Passes `createOnlyMode bool` to each `reconcile*` sub-function.
4. Each sub-function skips the `Update` path when `createOnlyMode` is true — resources are created if absent but never modified.
5. The ZTWIM aggregator sets `Upgradeable=False` on the OLM `OperatorCondition` when create-only mode is active.

## How to Add a New Bindata Resource to an Existing Operand

1. Create the YAML manifest in `bindata/<component>/` (e.g., `bindata/spire-server/my-new-resource.yaml`).
2. Add an asset path constant in `pkg/controller/utils/constants.go`.
3. Run `make update-bindata` to regenerate `pkg/operator/assets/bindata.go`.
4. Add a `reconcileNewResource()` method in the operand controller package, following the decode → mutate → SetControllerReference → Get → Create/Update pattern.
5. Call the new reconcile method in the controller's `Reconcile()` at the correct position in the ordering.
6. Add a status condition constant for the new resource.
7. Add a `Watches` entry in `SetupWithManager()` for the new resource type if it's a new GVK.
8. Ensure the resource type is registered in `NewCacheBuilder()` (`pkg/client/client.go`) under `cacheResources`.
9. Add the `managed-by` label to the YAML manifest or set it programmatically.
10. Add the resource type to `ResourceNeedsUpdate()` in `pkg/controller/utils/` if using field-level comparison.
11. Run `make manifests generate update-bindata && make verify`.

## How to Add a New Operand

1. Define the CRD type in `api/v1alpha1/<operand>_types.go` with `+kubebuilder:resource:scope=Cluster`, singleton CEL validation, and `ConditionalStatus` embedding.
2. Create the controller package under `pkg/controller/<operand>/` with `controller.go`, and resource-specific files.
3. Follow the standard reconciler structure: `Reconciler` struct, `New(mgr)`, `SetupWithManager(mgr)`, `Reconcile(ctx, req)` with the 9-step flow.
4. Register the scheme in `cmd/zero-trust-workload-identity-manager/main.go`.
5. Wire the controller in `main.go` with `New(mgr)` → `SetupWithManager(mgr)`.
6. Add the new CR type to `cacheResourceWithoutReqSelectors` in `pkg/client/client.go` and to `informerResources`.
7. Add a `get<Operand>Status()` method to the ZTWIM controller and include it in `aggregateOperandStatus()`.
8. Add a `Watches` entry for the new operand CR in the ZTWIM controller's `SetupWithManager()` with `operandStatusChangedPredicate`.
9. Add RBAC markers on the ZTWIM controller for the new CRD.
10. Add component label constant and label generator function in `pkg/controller/utils/labels.go`.
11. Add image env var constant and getter in `pkg/controller/utils/constants.go` and `relatedImages.go`.
12. Add bindata YAML under `bindata/<operand>/`, constants, and run `make update-bindata`.
13. Run `make manifests generate update-bindata && make verify && make test`.
