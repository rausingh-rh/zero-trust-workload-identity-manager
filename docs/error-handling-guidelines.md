# Error Handling Guidelines

## Error Wrapping

Use `fmt.Errorf("context: %w", err)` when propagating errors up the call stack. Use `errors.New(...)` for sentinel errors (e.g., `errors.New("OperatorCondition not found")`). Always include actionable context in the message — what operation failed, not why.

```go
return fmt.Errorf("failed to update OperatorCondition status: %w", err)
return fmt.Errorf("failed to fetch latest %q for update: %w", key, err)
```

Never wrap with `%v` — always use `%w` so callers can unwrap.

## ReconcileError Type

`pkg/controller/utils/errors.go` defines `ReconcileError` with three classifications:

| Reason | Meaning | When to use |
|--------|---------|-------------|
| `IrrecoverableError` | Retrying will not help | RBAC denied, bad request, invalid spec, service unavailable |
| `RetryRequiredError` | Transient failure, retry likely to succeed | Conflict, timeout, not-found on dependent resources |
| `MultipleInstanceError` | Singleton constraint violated | Multiple CRs exist where only `cluster` is allowed |

### Constructors

- `NewIrrecoverableError(err, msg, args...)` — permanent failure
- `NewRetryRequiredError(err, msg, args...)` — transient failure
- `FromClientError(err, msg, args...)` — auto-classifies Kubernetes API errors
- `FromError(err, msg, args...)` — preserves existing classification, defaults to retry

### FromClientError Classification

| API error type | Classification |
|---|---|
| `Unauthorized`, `Forbidden`, `Invalid`, `BadRequest`, `ServiceUnavailable` | Irrecoverable |
| `NotFound`, `Conflict`, `Timeout`, `ServerTimeout`, all others | Retry required |

All constructors return `nil` when passed a `nil` error — safe to call without nil-checking.

## Error-to-Reconcile-Result Mapping

Every operand controller `Reconcile` method uses these exact patterns:

### Primary CR not found (self)

```go
if err := r.ctrlClient.Get(ctx, req.NamespacedName, &server); err != nil {
    if kerrors.IsNotFound(err) {
        r.log.Info("resource not found, ignoring")
        return ctrl.Result{}, nil  // no requeue
    }
    return ctrl.Result{}, err  // requeue with backoff
}
```

### Parent ZTWIM CR not found

```go
if err := r.ctrlClient.Get(ctx, types.NamespacedName{Name: "cluster"}, &ztwim); err != nil {
    if kerrors.IsNotFound(err) {
        r.log.Error(err, "failed to get ZeroTrustWorkloadIdentityManager")
        statusMgr.AddCondition(v1alpha1.Ready, v1alpha1.ReasonFailed, "...", metav1.ConditionFalse)
        return ctrl.Result{}, nil  // no requeue — cannot proceed without parent
    }
    return ctrl.Result{}, err  // transient — requeue
}
```

### Validation failures

```go
if err := r.validateConfiguration(ctx, &server, statusMgr, &ztwim); err != nil {
    return ctrl.Result{}, nil  // no requeue — user must fix spec
}
```

Validation sets a status condition and returns a non-nil error to the caller, but the caller returns `nil` to controller-runtime (no requeue). A generation change on the CR will re-trigger reconciliation.

### Sub-reconciler step failures

```go
if err := r.reconcileServiceAccount(ctx, &server, statusMgr, createOnlyMode); err != nil {
    return ctrl.Result{}, err  // requeue with backoff
}
```

Sub-reconcilers set conditions internally before returning the error.

### Summary table

| Scenario | Return | Effect |
|---|---|---|
| Primary CR `IsNotFound` | `(Result{}, nil)` | Stop, no requeue |
| Parent ZTWIM `IsNotFound` | `(Result{}, nil)` | Stop, set `Ready=False/Failed` |
| Validation failure | `(Result{}, nil)` | Stop, set condition, wait for spec change |
| API transient error | `(Result{}, err)` | Requeue with exponential backoff |
| Sub-reconciler failure | `(Result{}, err)` | Requeue, condition already set by sub-reconciler |
| Successful reconciliation | `(Result{}, nil)` | Done |
| Recreate deleted singleton | `(Result{Requeue: true}, nil)` | Immediate re-reconcile |

## Status Condition Update Rules

1. Create `status.NewManager(r.ctrlClient)` early in `Reconcile`.
2. **Always** `defer statusMgr.ApplyStatus(...)` — status writes happen even if reconciliation returns early.
3. Each sub-reconciler calls `statusMgr.AddCondition(...)` for its component-specific condition type.
4. `ApplyStatus` auto-derives `Ready` from all other conditions unless `Ready` was explicitly set.
5. Status is only written to the API server when conditions actually changed (semantic equality check).
6. `ApplyStatus` errors are logged but never propagated — reconciliation result is not affected.

```go
defer func() {
    if err := statusMgr.ApplyStatus(ctx, &server, func() *v1alpha1.ConditionalStatus {
        return &server.Status.ConditionalStatus
    }); err != nil {
        r.log.Error(err, "failed to update status")
    }
}()
```

### Condition conventions

- Types: `Ready`, `Degraded`, `Upgradeable` (global); per-controller types like `StatefulSetAvailable`, `ConfigMapAvailable`, etc.
- Reasons: `Failed`, `Ready`, `Progressing`, `OperandsNotReady`, `ResourceConflict`.
- Only flip a condition from `False` to `True` if it was previously `False` — avoid no-op transitions.

## Retry Patterns

### UpdateWithRetry

Used for spec/metadata updates. Wraps `retry.RetryOnConflict` — re-fetches the object to get latest `resourceVersion`, then retries the update.

```go
err := r.ctrlClient.UpdateWithRetry(ctx, obj)
```

### StatusUpdateWithRetry

Same pattern for status subresource updates. Used by `ApplyStatus` and `updateOperatorCondition`.

```go
err := r.ctrlClient.StatusUpdateWithRetry(ctx, obj)
```

Both methods wrap the final error with context: `"failed to update %q resource: %w"` / `"failed to update %q status: %w"`.

### When NOT to retry

Direct `Update` (without retry) is used when the caller just fetched the object in the same reconcile loop and a conflict is unlikely (e.g., persisting owner references immediately after `Get`). If the update fails, return the error and let controller-runtime requeue the entire reconciliation.

## Logging Conventions for Errors

| Scenario | Pattern |
|---|---|
| Error with requeue | `r.log.Error(err, "failed to <action>")` then `return (Result{}, err)` |
| Error without requeue (validation) | `r.log.Error(err, "descriptive message", "key", value)` then set condition |
| Informational not-found | `r.log.Info("resource not found, ignoring")` — never log at Error level |
| Debug-level detail | `r.log.V(1).Info("skipping update", "reason", "...")` |
| Status update failure in defer | `r.log.Error(err, "failed to update status")` — log only, do not propagate |
| Best-effort OLM integration | `r.log.Error(err, "..., continuing (operator may be running outside OLM)")` |

Never use `fmt.Printf` or `klog` directly.

## Event Recording

Events are reserved for **user-actionable warnings** — not routine reconciliation. The only current usage is TTL validation warnings on the SpireServer CR:

```go
r.eventRecorder.Event(server, corev1.EventTypeWarning, "TTLConfigurationWarning", warning)
```

Rules:
- Use `corev1.EventTypeWarning` for problems the user should address.
- Do not emit events for transient API errors or routine reconciliation.
- Event reason should be PascalCase (e.g., `TTLConfigurationWarning`).

## NotFound Handling: Primary vs Dependent

| Resource role | IsNotFound behavior |
|---|---|
| **Primary CR** (the CR this controller owns) | `return (Result{}, nil)` — CR deleted, nothing to do |
| **Parent ZTWIM CR** | Set `Ready=False/Failed`, `return (Result{}, nil)` — cannot proceed |
| **Dependent resource** (managed SA, ConfigMap, etc.) | Treat as "needs creation" — proceed to `Create` |
| **Operand CR** (in ZTWIM aggregator) | Record as `OperandMessageCRNotFound`, classify as progressing |
| **OperatorCondition** (OLM) | Log and continue — operator may run outside OLM |

## Kubernetes API Error Classification

Use `kerrors` (aliased from `k8s.io/apimachinery/pkg/api/errors`):

| Check | Use case |
|---|---|
| `kerrors.IsNotFound(err)` | CR deleted or not yet created |
| `kerrors.IsAlreadyExists(err)` | Resource conflict with pre-existing resource (see `HandleCreateConflict`) |
| `kerrors.IsConflict(err)` | Stale resourceVersion — handled by `RetryOnConflict` |
| `kerrors.IsUnauthorized(err)` | RBAC misconfiguration — irrecoverable |
| `kerrors.IsForbidden(err)` | Missing permissions — irrecoverable |
| `kerrors.IsInvalid(err)` | Spec rejected by API server — irrecoverable |
| `kerrors.IsBadRequest(err)` | Malformed request — irrecoverable |
| `kerrors.IsServiceUnavailable(err)` | API server down — irrecoverable (unusual) |

### AlreadyExists / Resource Conflict Pattern

When `Create` returns `IsAlreadyExists`, the resource exists outside the operator's label-filtered cache (a pre-existing resource with the same name). Use `HandleCreateConflict`:

```go
if err := r.ctrlClient.Create(ctx, desired); err != nil {
    if conflictErr := utils.HandleCreateConflict(err, desired, r.log, statusMgr, conditionType); conflictErr != nil {
        return conflictErr
    }
    // Handle other create errors...
}
```

This sets a `ResourceConflict` condition and returns the conflict error (which requeues).
