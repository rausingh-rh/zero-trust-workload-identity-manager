# CLAUDE.md

@AGENTS.md

## Build and Test Commands

When working in this repository, use these commands:

### Before Committing

```bash
make manifests generate update-bindata && make verify
```

This regenerates all CRDs, deepcopy, and bindata, then runs verification checks. CI will reject PRs with stale generated files.

### Development Workflow

```bash
make build            # Full build with codegen
make build-operator   # Fast rebuild (binary only, FIPS-aware)
make test             # Run unit tests with envtest (no cluster needed)
make lint             # Run golangci-lint
```

### Testing

```bash
make test             # Unit tests with envtest
make test-e2e         # E2E tests (requires live OpenShift cluster, 45min timeout)
```

### Dependency Management

```bash
make vendor           # go mod tidy + go mod vendor (after go.mod changes)
```

## Claude Code Preferences

- Always run `make manifests generate update-bindata && make verify` after code changes that affect:
  - CRD definitions (`api/v1alpha1/`)
  - Kubebuilder markers (`+kubebuilder:*`)
  - Bindata YAML (`bindata/`)
  - RBAC markers

- Use `make lint` to check for linting issues before suggesting manual fixes

- The repository vendors all dependencies. Never suggest installing tools globally

- Container builds default to `docker`. Override with `CONTAINER_TOOL=podman` if needed

- All build-time tools (controller-gen, golangci-lint, kustomize, go-bindata) are vendored via `tools/tools.go` and built from source
