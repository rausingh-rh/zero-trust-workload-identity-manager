# Security Guidelines — ZTWIM Operator

Repo-specific security patterns for the Zero Trust Workload Identity Manager operator and its operands.

## Container Security

The operator image (`Dockerfile`) runs as non-root UID **65532:65532** on a `ubi9-minimal` base. Never change the `USER` directive without coordinating with cluster security policy. Operand containers (SPIRE server, controller-manager) set `ReadOnlyRootFilesystem: true` in their `SecurityContext` and use `EmptyDir` volumes for writable paths (`/tmp`, `/var/lib/spire`). Do not add writable volume mounts at sensitive paths without explicit justification.

## FIPS Build Requirements

Production and CI builds **must** use `hack/go-fips.sh`, which sets `GOEXPERIMENT=strictfipsruntime` and build tags `strictfipsruntime,openssl`. A build that prints `WARN: building without FIPS support` is not shippable. The Dockerfile uses `CGO_ENABLED=1` so the Go runtime links against OpenSSL for FIPS-validated crypto. Never set `CGO_ENABLED=0` in CI or production builds.

## Metrics Endpoint Protection

The metrics server binds to `:8443` with TLS enabled by default (`--metrics-secure=true`). Two layers of protection:

1. **TLS certificates** — supplied via `--metrics-cert-dir` (files `tls.crt` / `tls.key`) or self-signed fallback. Client verification uses the OpenShift service CA read from `/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt`.
2. **AuthN/AuthZ filter** — `filters.WithAuthenticationAndAuthorization` enforces RBAC. The `metrics-auth-role` ClusterRole permits `subjectaccessreviews` creation; `metrics-reader-role` grants GET on `/metrics`. Only service accounts bound to these roles can scrape.

When adding new metrics endpoints, ensure they are served through the existing `metricsserver.Options` pipeline, not via a separate HTTP listener.

## HTTP/2 Disabled by Default

HTTP/2 is disabled on both metrics and webhook servers (`--enable-http2=false`) via `tls.Config.NextProtos = []string{"http/1.1"}`. This mitigates HTTP/2 rapid-reset and similar protocol-level vulnerabilities. Do not enable HTTP/2 without a documented security review.

## RBAC Scoping

The operator manages two RBAC layers:

| Layer | Manifests | Scope |
|---|---|---|
| **Operator** | `config/rbac/role.yaml` | `manager-role` ClusterRole — grants the operator itself access to CRDs, workloads, RBAC, Routes, SCCs, SPIRE CRDs |
| **Operand** | `bindata/spire-*/`, `bindata/spiffe-csi/` | Per-component ClusterRoles/Roles bound to operand ServiceAccounts |

Operator RBAC uses `resourceNames` restrictions wherever possible (e.g., only ServiceAccounts named `spire-agent`, `spire-server`; only DaemonSets named `spire-agent`, `spire-spiffe-csi-driver`). When adding new managed resources, scope verbs and resource names as tightly as possible in the kubebuilder RBAC markers and regenerate with `make manifests`.

All operand CRs are restricted to the singleton name `cluster` via `resourceNames` in the operator ClusterRole. Never grant the operator blanket write access to arbitrary CR instances.

## SCC Management

Two distinct SCC strategies exist:

### SPIRE Agent — Custom SCC

The agent needs `AllowHostDirVolumePlugin` and `AllowHostPID` to access host-level workload info. A dedicated `spire-agent` SCC is created programmatically in `pkg/controller/spire-agent/scc.go` with:
- `AllowPrivilegedContainer: false`, `AllowPrivilegeEscalation: false`
- `RequiredDropCapabilities: ["ALL"]`
- `ReadOnlyRootFilesystem: true`
- `Users` limited to `system:serviceaccount:<namespace>:spire-agent`
- `AllowHostNetwork: false`, `AllowHostIPC: false`, `AllowHostPorts: false`

Never relax these constraints. If you need new volume types, add them to `Volumes` explicitly.

### SPIFFE CSI Driver — Privileged SCC

The CSI driver requires the platform `privileged` SCC. Access is granted via a namespace-scoped `RoleBinding` (`spire-csi-use-privileged-scc`) binding the `spire-spiffe-csi-driver` ServiceAccount to `system:openshift:scc:privileged`. This avoids granting cluster-wide privileged access.

## Service CA Annotations for TLS

Internal TLS between the OpenShift Route and the SPIRE server pod uses the annotation:

```
service.beta.openshift.io/serving-cert-secret-name: spire-server-serving-cert
```

This annotation is set on the `spire-server` Service **only when federation is enabled** (see `getSpireServerService`). The OpenShift service CA operator auto-provisions a TLS secret with the specified name. The secret is mounted into the StatefulSet at `/run/spire/server-tls` for the `https_web` + `servingCert` federation profile.

Constants are defined in `pkg/controller/utils/constants.go`:
- `ServiceCAAnnotationKey` = `service.beta.openshift.io/serving-cert-secret-name`
- `SpireServerServingCertName` = `spire-server-serving-cert`

## Federation TLS Patterns

Federation bundle endpoints support four TLS configurations, each mapping to a different Route TLS termination:

| Profile | Sub-config | Route TLS | Certificate source |
|---|---|---|---|
| `https_spiffe` | — | Passthrough | SPIRE's own SVID |
| `https_web` | `acme` | Passthrough | ACME (e.g., Let's Encrypt) managed by SPIRE |
| `https_web` | `servingCert` | Re-encrypt | Service CA for internal leg; optional `externalSecretRef` for Route edge cert |
| `https_web` | `servingCert` + `externalSecretRef` | Re-encrypt + ExternalCertificate | Admin-managed TLS secret referenced by Route |

CEL validations enforce immutability: `profile` cannot change after creation, and you cannot switch between `acme` and `servingCert`. The federation configuration block itself cannot be removed once set.

When `externalSecretRef` is provided, an external-cert-reader Role/RoleBinding is created so the OpenShift Ingress Operator can read the secret (`SpireServerExternalCertRoleName`). The secret must reside in the operator namespace.

## Workload Attestor Verification

The `WorkloadAttestorsVerification.Type` field controls how the SPIRE agent verifies the kubelet's TLS certificate:

| Type | Behavior | When to use |
|---|---|---|
| `skip` | Sets `skip_kubelet_verification: true` | Development / testing only |
| `auto` | Uses OpenShift default CA at `/etc/kubernetes/kubelet-ca.crt`; can be overridden with `hostCertBasePath` / `hostCertFileName` | Standard OpenShift clusters |
| `hostCert` | Requires explicit `hostCertBasePath` and `hostCertFileName` (CEL-enforced) | Custom PKI or non-standard kubelet CA location |

Default is `auto`. Production clusters should use `auto` or `hostCert`; `skip` bypasses TLS verification entirely and must not be used outside of testing environments.

## Leader Election Security

Leader election uses a Lease-based lock with ID `24a59323.operator.openshift.io`. The operator's RBAC includes `coordination.k8s.io/leases` with full CRUD verbs. `LeaderElectionReleaseOnCancel` is intentionally disabled — do not enable it if the binary performs cleanup after manager shutdown, as that can cause split-brain during rolling updates.

## Image References via RELATED_IMAGE

All operand images are resolved from environment variables, never hardcoded:

| Env var | Component |
|---|---|
| `RELATED_IMAGE_SPIRE_SERVER` | SPIRE server |
| `RELATED_IMAGE_SPIRE_AGENT` | SPIRE agent |
| `RELATED_IMAGE_SPIFFE_CSI_DRIVER` | SPIFFE CSI driver |
| `RELATED_IMAGE_SPIRE_OIDC_DISCOVERY_PROVIDER` | OIDC provider |
| `RELATED_IMAGE_SPIRE_CONTROLLER_MANAGER` | Controller manager sidecar |
| `RELATED_IMAGE_NODE_DRIVER_REGISTRAR` | CSI node driver registrar |
| `RELATED_IMAGE_SPIFFE_CSI_INIT_CONTAINER` | CSI init container |

These are set in the OLM CSV and resolved at runtime via `utils.Get*Image()` helpers. This ensures disconnected / air-gapped installs use mirrored images. Never reference a container image by literal string in Go code — always add a `RELATED_IMAGE_*` constant in `utils/constants.go` and read it from the environment.

## Cache Label Selectors

The controller cache (`pkg/client/client.go`) filters managed resources by label:

```
app.kubernetes.io/managed-by = zero-trust-workload-identity-manager
