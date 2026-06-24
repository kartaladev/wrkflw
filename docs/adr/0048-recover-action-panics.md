# 48. Recover panics in service-action execution

- Status: Accepted
- Date: 2026-06-25

## Context

A `ServiceAction` is consumer-supplied code invoked by the runtime when the engine
emits an `InvokeAction` (or `InvokeCancelAction`) command. The runtime called
`a.Do(ctx, input)` directly. A panic inside an action — a nil-map write, an
out-of-range index, a `panic()` on a malformed payload, a dependency that panics —
propagated up through `perform` → `deliverLoop` → `Runner.Deliver`/`Run` and
crashed the whole goroutine. In an embedded deployment that is the consumer's
request goroutine or the replica's driver; **one buggy action takes down every
in-flight instance on the replica.** Worse, a panic mid-compensation-walk leaves
compensation records uncleared, so a redelivery re-walks them — a double-effect
(e.g. double-refund) hazard.

This was the highest-severity finding of the production-readiness review: low
effort to fix, very large blast radius if not.

## Decision

Wrap every action invocation in a `recover` shim, `safeActionDo`, in `runtime`:

```go
func safeActionDo(ctx, a, in) (out, err) {
    defer func() {
        if rec := recover(); rec != nil {
            out, err = nil, fmt.Errorf("workflow-runtime: action panicked: %v", rec)
        }
    }()
    return a.Do(ctx, in)
}
```

A recovered panic is surfaced as an **ordinary action error**, so it flows through
each caller's existing failure path with no new branching:

- **`InvokeAction`** → the existing `err != nil` arm builds a retryable, jittered
  `ActionFailed`. A panicking action therefore behaves exactly like an erroring
  action: it honours the node's `RetryPolicy`, and with no policy it drives the
  instance to `StatusFailed` (a clean terminal state) instead of crashing.
- **`InvokeCancelAction`** → the existing best-effort arm logs the error and never
  feeds a result back (ADR-0028); a panicking cancel action is logged and the
  cancellation still reports success (`StatusTerminated`).

## Consequences

- A buggy or hostile action can no longer crash the runner or take out sibling
  instances; the failure is contained to its own instance.
- Panics and returned errors are now indistinguishable to the engine. That is the
  intended unification — both are "the action did not succeed." The panic value is
  preserved in the error message (`action panicked: <value>`) and the span already
  records it, so diagnosis is not lost.
- A pathological action that panics on every attempt under a `RetryPolicy` will
  retry-then-exhaust like any always-failing action — bounded by `MaxAttempts`,
  not infinite.
- Engine/model diff is **ZERO**; the change is confined to `runtime`.
