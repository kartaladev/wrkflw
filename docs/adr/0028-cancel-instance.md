# 28. CancelInstance surface + definition-level cancel actions via fire-and-forget command

- Status: Accepted
- Date: 2026-06-22

## Context

`CancelInstance` was the most-requested missing operation in the public API: no surface existed
for a consumer to cancel a running process instance. The engine **already** handled cancellation
internally — `engine.CancelRequested` + `engine.Step` clear all tokens, set `StatusTerminated`,
emit `FailInstance{Err:"cancelled"}`, and cancel outstanding timers/arms (idempotent on an
already-terminal instance, covered by `TestCancelRequestedTerminates`). What was absent was the
**surface**: a runner method, a service operation, and REST + gRPC endpoints.

`ResolveIncident` was the closest end-to-end precedent (runner → service → admin REST route), used
as the structural template. `ResolveIncident` has no gRPC RPC (admin ops were REST-only); adding
one for `CancelInstance` introduces a mild REST/gRPC asymmetry (see Decision §5 and Consequences).

An additional requirement: cancellation must optionally **run business-related tasks** — e.g.
release a resource, notify an external system, issue a refund — when an instance is cancelled.

### Engine mechanics that constrain the design

Every `InvokeAction` result is fed back into `deliverLoop` and re-applied via `engine.Step`
(`runtime/runner.go` perform→queue→Step loop). If `CancelRequested` emitted plain `InvokeAction`
commands for the cancel tasks, their `ActionCompleted`/`ActionFailed` results would be re-delivered
against the now-**terminal** instance — no token is awaiting them, so the engine returns
`ErrTokenNotFound` (wrapping `ErrInvalidTransition`, ADR-0026), causing `Deliver` to fail and the
cancel to surface as an error. Cancel actions therefore need a **fire-and-forget path** that never
feeds results back into the state machine.

SLA/reminder in-wait actions are conceptually fire-and-forget but still emit `InvokeAction`; a
dedicated command makes the no-feedback contract explicit, type-safe, and testable.

`cloneState` deep-copies `InstanceState` only; `CancelActions` lives on the immutable
`ProcessDefinition` (the `def` argument to `Step`) and is never cloned — no structural change to
state copying.

The rejected alternative was a **runtime callback hook** (`WithCancelHook`): the user chose
definition-level cancel actions because they are declared alongside the process definition, are
version-controlled with it, and require no runtime-wiring change to enable.

## Decision

### 1. Model — `ProcessDefinition.CancelActions []string`

We add an optional `CancelActions []string` field to `model.ProcessDefinition` (verified:
`model/definition.go` line 170). It carries ordered ServiceAction names to run best-effort on
cancellation. `model.Validate` rejects empty-string entries via a new sentinel
`model.ErrEmptyCancelAction` (`model/validate.go`). Action-name existence against the catalog is
**not** checked at validate time (the catalog is unavailable); an unresolved name at runtime is
handled best-effort (logged, cancel still succeeds). Nil/empty `CancelActions` preserves existing
behavior exactly.

### 2. Engine — `InvokeCancelAction` command + `CancelRequested` emission

We introduce a new fire-and-forget command in the sealed `Command` set
(`engine/command.go`):

```go
type InvokeCancelAction struct {
    Name  string
    Input map[string]any
}
func (InvokeCancelAction) isCommand() {}
var _ Command = InvokeCancelAction{}
```

Unlike `InvokeAction`, `InvokeCancelAction` carries no `CommandID`. Its perform handler always
returns `(nil, nil)` — no follow-up command is queued, no result re-enters the state machine.

The `CancelRequested` handler in `engine/step.go` emits `InvokeCancelAction` entries — one per
`def.CancelActions` entry, in definition order, with a snapshot of instance variables via
`copyVars` — **before** `FailInstance` and the timer/arm cancellations. `Step` remains
deterministic (commands are a pure function of `(def, state)`) and pure (uses `t.OccurredAt()`
and `copyVars`; no clock, no I/O). With empty `CancelActions` the command set is byte-identical
to the pre-change output, so all existing `TestCancelRequestedTerminates` cases stay green.

### 3. Runtime — best-effort `perform` + `Runner.CancelInstance`

`runtime/runner.go` gains a `case engine.InvokeCancelAction` branch in `perform`. It resolves
the action from the catalog; if the catalog is nil or the name is unresolved it logs a warning
via `slog` and returns `(nil, nil)`. If the action's `Do` call fails it logs an error via `slog`
and still returns `(nil, nil)`. The `(nil, nil)` return is unconditional — a failing cancel
action **never** fails the enclosing `deliverLoop` call and never reaches the terminal instance
as a follow-up trigger.

`Runner.CancelInstance` is a thin delegator:

```go
func (r *Runner) CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error) {
    return r.Deliver(ctx, def, instanceID, engine.NewCancelRequested(r.clk.Now()))
}
```

The existing `instCompleted{status="terminated"}` metric counter fires inside `deliverLoop`; no
new counter is needed.

### 4. Service — `CancelInstance` with already-terminal guard

We add `CancelInstance(ctx, CancelInstanceRequest) (engine.InstanceState, error)` to the
`Service` interface. The concrete `Engine` implementation resolves the definition via
`resolveDefinition`, guards against already-terminal instances with `isTerminal(st.Status)` →
`ErrConflict`, then delegates to `Runner.CancelInstance`. This guard intentionally fences off
the engine's idempotent re-cancel at the API boundary, so callers receive a clear 422 instead of
a silent no-op. `ErrInstanceNotFound`/`ErrDefinitionNotFound` propagate from `resolveDefinition`
unchanged.

### 5. REST — admin-gated route

`POST /admin/instances/{id}/cancel` is added behind the existing admin middleware (default-deny),
mirroring `handleResolveIncident`. No request body. Success → HTTP 200 with the mapped instance
via `renderInstance`. Error mapping via the shared `WriteHTTPError` classifier:
`ErrConflict` → 422 `conflict_state`, `ErrInstanceNotFound` → 404 `not_found` (both mappings
already present from ADR-0026).

### 6. gRPC — new RPC, consumer-secured

We add `rpc CancelInstance(CancelInstanceRequest) returns (InstanceResponse)` to the proto
service definition (`transport/grpc/proto/workflow.proto`). `server.CancelInstance` mirrors
`StartInstance` (call `svc.CancelInstance` → `instanceToProto` → `mapToGRPCStatus`).
`ErrConflict` → `codes.FailedPrecondition` and `ErrInstanceNotFound` → `codes.NotFound` are
already present in `mapToGRPCStatus`. The generated `workflowpb` is regenerated via
`go generate ./transport/grpc/...` and committed.

The gRPC transport has no admin-middleware seam equivalent to the REST `adminMiddleware` option
(ADR-0011: transports are consumer-mounted). This `CancelInstance` RPC is therefore exposed
without automatic admin gating; the consumer is responsible for securing it via an interceptor.
This asymmetry is inherent to the consumer-mounted transport model and is documented here.

## Consequences

**Easier / better:**

- Consumers can cancel a running instance through the REST admin route or the gRPC RPC, closing
  the most-requested API gap.
- Definition-level cancel actions enable business side effects (resource release, notifications,
  refunds) to be declared with the process and run automatically — no runtime wiring change
  required per definition.
- `Step` determinism and purity are preserved: the new `InvokeCancelAction` command is a pure
  output of `(def, state)` with no clock, I/O, or vendor import touching `engine/` or `model/`.
  Import purity invariants (no transport/storage/bus/vendor in engine or model) are intact.
- Best-effort semantics mean a missing or failing cancel action never poisons the cancel
  operation itself. A runtime test asserts cancellation returns `StatusTerminated` / nil error
  even when a cancel action fails.
- Already-terminal cancel surfaces a typed `ErrConflict` → 422 / `codes.FailedPrecondition`
  rather than a silent no-op or an opaque error, consistent with ADR-0026.

**Harder / trade-offs:**

- **engine/ and model/ do change** on this track (the new `InvokeCancelAction` command and
  `CancelActions` field), unlike ADR-0027 (timer rehydration) which left the engine untouched.
  The changes are strictly additive and localized to `engine/command.go`, `engine/step.go`, and
  `model/definition.go` / `model/validate.go`.
- **REST/gRPC admin asymmetry:** REST gates `CancelInstance` behind the admin middleware;
  gRPC exposes the RPC without a built-in gate. Consumers mounting the gRPC transport must add
  an interceptor for equivalent protection. A sample interceptor is a documented follow-up.
- Cancel actions are **process-level** (`CancelActions` on `ProcessDefinition`), not
  per-active-node. A BPMN-native per-node cancel handler (run only the compensation/cancel
  handler of the node that was active at cancellation) is deferred.
- Cancel actions run as **best-effort side effects**; their results never re-enter instance
  state. They are not compensation (which follows `CompensateRequested`, ADR-0013) and carry no
  state-machine guarantees. A cancel action that must succeed before the instance is considered
  cancelled cannot be expressed in this model.
- `CancelRequested` carries no reason parameter. Cancel reason / audit trail is deferred.
- Cancel-action observability is limited to `slog` logging; no span or counter per action.
  Alignment with the observability track (ADR-0019) is a deferred follow-up.

**Deferred follow-ups:**

1. **Per-active-node cancel handlers** — run only the live activity's cancel handler (BPMN
   native); `CancelActions` is process-level v1.
2. **Cancel reason / audit** — carry an optional reason on `CancelRequested` /
   `CancelInstanceRequest` and record it (engine change).
3. **Cancel-action observability** — a span and counter per cancel action; today
   failures are slog-only.
4. **gRPC admin interceptor sample** — document or ship a reference interceptor mirroring the
   REST admin gate.
5. **`//go:generate` path-with-spaces quirk** in `transport/grpc/errors.go` — quote the path
   or move the directive to a Makefile to guard against shells that do not handle spaces in
   `go generate` argument paths.

**Cross-references:** ADR-0011 (consumer-mounted transports — REST/gRPC asymmetry is inherent
here), ADR-0013 (compensation — cancel does NOT trigger `CompensateRequested`; cancel actions
are a separate, simpler mechanism), ADR-0026 (`ErrConflict` / `ErrInvalidTransition` sentinels
used here), ADR-0009 (gocron scheduling — cancel clears all outstanding timers/arms).
