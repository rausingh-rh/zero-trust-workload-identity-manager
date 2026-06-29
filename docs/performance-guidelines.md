# Performance Guidelines

Repo-specific patterns for reducing API server load, minimizing reconciliation loops,
and keeping the controller-runtime informer footprint tight.

## Cache Architecture

### Manager-Level Label-Filtered Cache (`NewCacheBuilder`)

The operator runs a **single unified cache** configured via `pkg/client/client.go:NewCacheBuilder`.
All controllers share one set of informers — there is no per-reconciler cache.

Two resource lists control what the cache watches:

| List | Selector | Purpose |
|------|----------|---------|
| `cacheResources` | `app.kubernetes.io/managed-by=zero-trust-workload-identity-manager` | Managed operand resources (RBAC, workloads, ConfigMaps, Routes, etc.) |
| `cacheResourceWithoutReqSelectors` | *(none)* | Operator CRs and OperatorCondition — watched without label filtering |

The label selector ensures the cache only tracks resources this operator created.
Resources without the `managed-by` label are invisible to the cache and to all `Get`/`List` calls that go through it.

### `ReaderFailOnMissingInformer: true`

Set at cache construction time. If a controller attempts to read a type that was never
registered in `informerResources`, the read fails immediately instead of silently
starting a cluster-wide informer. This prevents accidental memory/connection leaks
when a new resource type is introduced without updating the cache lists.

### Registering New Resources in the Cache

To add a new resource type that the operator manages:

1. Add to `cacheResources` (if it should be filtered by managed-by label) or
   `cacheResourceWithoutReqSelectors` (if it's an operator-owned CR or external CR).
2. Add to `informerResources` so the informer is pre-registered at startup.
3. Ensure all instances the operator creates carry `app.kubernetes.io/managed-by: zero-trust-workload-identity-manager`.
   Without this label, the resource will not appear in cache reads.

## Predicate Strategy

Predicates are the primary mechanism for reducing unnecessary reconciliation.
Each controller uses a layered approach:

### Primary CR (the `For` resource)

| Controller | Predicate | Effect |
|------------|-----------|--------|
| ZTWIM | `GenerationChangedPredicate` | Ignores status-only updates to the parent CR |
| Operand controllers (SpireServer, SpireAgent, etc.) | `GenerationOrOwnerReferenceChangedPredicate` | Reconciles on spec changes OR owner-ref mutations |

### Managed Resources (secondary watches)

All operand controllers use `ControllerManagedResourcesForComponent(component)` on managed resource watches.
This predicate checks **both** labels at the event level:

- `app.kubernetes.io/managed-by == zero-trust-workload-identity-manager`
- `app.kubernetes.io/component == <component>` (e.g., `control-plane`, `node-agent`, `csi`, `discovery`)

This ensures each controller only reacts to drift in **its own** resources, not resources
managed by sibling controllers — even though all controllers share the same informer.

### Operand Status Watches (ZTWIM controller only)

The ZTWIM controller watches operand CRs using `operandStatusChangedPredicate`:
it reconciles **only** when an operand's `.status` changes (using `equality.Semantic.DeepEqual`).
Spec-only changes to operand CRs do not trigger ZTWIM reconciliation.

### ZTWIM Spec Watch (operand controllers)

Operand controllers watch the `ZeroTrustWorkloadIdentityManager` CR with `ZTWIMSpecChangedPredicate`,
which fires on Create and Delete but suppresses Update events. This avoids reconciling operands
on every ZTWIM status write.

### Adding Watches for New Resource Types

When adding a new managed resource type to an existing controller:

```go
Watches(&newv1.MyResource{}, handler.EnqueueRequestsFromMapFunc(mapFunc), controllerManagedResourcePredicates).
```

Always use the existing `controllerManagedResourcePredicates` (component-scoped).
Never add a watch without a predicate — that causes reconciliation on every change
to every instance of that type cluster-wide.

## Drift Detection (`ResourceNeedsUpdate`)

`pkg/controller/utils/resource_comparison.go` provides **type-specific** comparison
instead of `reflect.DeepEqual` on the full object. This avoids spurious updates caused
by server-set fields (UIDs, timestamps, defaulted values).

### Design principles

1. **Labels/annotations**: Only desired keys are checked. Extra keys added by admission
   controllers or other operators are ignored.
2. **Immutable fields skipped**: `Service.Spec.ClusterIP`, `Service.Spec.ClusterIPs` are
   never compared.
3. **Semantic equality**: Uses `k8s.io/apimachinery/pkg/api/equality.Semantic.DeepEqual`
   for slices/maps (handles nil vs empty correctly).
4. **Container comparison by name**: Containers in workloads are matched by name, not
   by position. This tolerates reordering by mutating webhooks.
5. **Volume comparison by name**: Volumes matched by name with source-type-specific checks.

### When to use

Call `ResourceNeedsUpdate(existing, desired)` before issuing an `Update` call.
If it returns `false`, skip the update entirely.

For workloads with config hash annotations (StatefulSet), use the controller-local
`needsUpdate` wrapper that first checks annotation hashes before falling through
to `ResourceNeedsUpdate`.

### Adding a new type

Add a case to the `switch` in `ResourceNeedsUpdate` and implement a
`<Type>NeedsUpdate(existing, desired *<Type>) bool` function comparing only
the fields the operator sets. Always use `equality.Semantic.DeepEqual` for
sub-structures rather than `reflect.DeepEqual`.

## Status Update Optimization

### Semantic Equality Check

`status.Manager.ApplyStatus` computes a deep copy of the status before applying
conditions and compares old vs new with `equality.Semantic.DeepEqual`.
If the status is semantically identical, the `StatusUpdateWithRetry` call is skipped.
This eliminates no-op status writes that would otherwise bump `resourceVersion` and
trigger watch events.

### `StatusUpdateWithRetry`

Wraps `retry.RetryOnConflict(retry.DefaultRetry, ...)`:

1. Fetches the latest `resourceVersion` from the API server.
2. Copies it onto the local object.
3. Calls `Status().Update(...)`.
4. Retries on conflict (409).

Use this for all status writes. Never call `StatusUpdate` (non-retry variant) from
reconcilers — it will fail silently on conflicts from concurrent reconciliations.

### `UpdateWithRetry`

Same pattern for spec/metadata updates. Fetches latest, sets `resourceVersion`, retries
on conflict. Used for owner-reference persistence and other non-status writes that
can race with concurrent actors.

## Create-Only Mode

When `CREATE_ONLY_MODE=true`, controllers create resources but **skip all Update calls**.
Performance implications:

- Drift is not corrected. `ResourceNeedsUpdate` still runs for status condition reporting,
  but no API write occurs.
- Reduces API server writes to zero after initial resource creation.
- The `Upgradeable` OLM condition is set to `False`, blocking automatic upgrades.

Controllers check this flag once at the top of reconciliation and pass the boolean
down to each `reconcile*` sub-function to conditionally skip updates.

## Summary of API Call Reduction Layers

| Layer | Mechanism | Avoids |
|-------|-----------|--------|
| Cache label selector | `managed-by` filter on informer | Watching unrelated resources cluster-wide |
| Component predicate | `ControllerManagedResourcesForComponent` | Cross-controller reconcile storms |
| Generation predicate | `GenerationChangedPredicate` | Reconciling on status-only writes |
| Status predicate | `operandStatusChangedPredicate` | ZTWIM reconciling on operand spec changes |
| `ResourceNeedsUpdate` | Type-specific field comparison | No-op Update API calls |
| Status semantic equality | `equality.Semantic.DeepEqual` on status | No-op Status Update API calls |
| Retry wrappers | `RetryOnConflict` | Failed writes due to stale resourceVersion |
| Create-only mode | Skip all Updates | All update writes post-creation |
