# CancelInstance + definition-level cancel actions — design

**Date:** 2026-06-22
**Status:** Proposed (awaiting user approval)
**Track:** Transports / feature-completeness (consolidated-backlog top pick)
**ADR:** 0028 (CancelInstance surfacing + fire-and-forget cancel actions)

## Context

`CancelInstance` is the most-requested missing operation: there is no way for a consumer to cancel
a running process instance through the public API. The engine **already** implements cancellation
internally — `engine.CancelRequested` + the `engine.Step` handler clear all tokens, set
`StatusTerminated`, emit `FailInstance{Err:"cancelled"}`, and cancel outstanding timers/arms
(idempotent on an already-terminal instance, tested in `TestCancelRequestedTerminates`). What is
missing is the **surface**: a runner method, a service operation, and REST + gRPC endpoints.
`ResolveIncident` is the exact end-to-end precedent (runner method → service method+request → admin
REST route), though it has no gRPC RPC today (admin ops are REST-only).

Additionally, cancellation must optionally **execute business-related tasks** — e.g. release
resources, notify external systems, issue refunds — when an instance is cancelled. Per the chosen
design (over a runtime callback hook), these are **definition-level cancel actions** run by the
engine, executed **best-effort** (failures logged, not state-affecting; cancellation still reports
success).

### Engine mechanics that shape the design (verified)

- **Every `InvokeAction` result is fed back** into `deliverLoop` and re-applied via `engine.Step`
  (`runner.go` perform→queue→Step loop). If `CancelRequested` emitted plain `InvokeAction`s, their
  `ActionCompleted`/`ActionFailed` results would be re-delivered against the now-**terminated**
  instance → `ErrInvalidTransition` (no token awaiting), failing the cancel `Deliver`. Therefore
  cancel actions need a **fire-and-forget** path that does not feed results back.
- The reminder/SLA actions are conceptually fire-and-forget, but still emit `InvokeAction`; a
  dedicated command makes the no-feedback contract explicit and safe.
- `cloneState` deep-copies `InstanceState` only; `CancelActions` lives on the immutable
  `ProcessDefinition` (`Step`'s `def` argument) and is never cloned.

## Goals

1. Cancel a running instance through engine → runtime → service → REST + gRPC.
2. Optionally run definition-declared business actions on cancellation, best-effort.
3. Preserve `Step` determinism and purity (no clock; output a pure function of `(def, state)`).
4. Cancelling an already-terminal instance is a wrong-state error (`ErrConflict` → 422 /
   `FailedPrecondition`).

## Non-goals

- **No runtime callback hook** (the rejected alternative). Cancel actions are definition-level.
- **No per-node cancel handlers** — `CancelActions` is process-level (matches "cancellation of
  process"); per-active-node cancel handlers are a deferred follow-up.
- **No cancel "reason" parameter** — `CancelRequested` carries no reason; adding one is out of scope.
- **No threading of cancel-action results back into state** — they are side effects only.
- **No compensation change** — cancel does not trigger `CompensateRequested`; cancel actions are a
  separate, simpler mechanism.

## Design

### 1. Model — `ProcessDefinition.CancelActions`

```go
type ProcessDefinition struct {
    ID            string
    Version       int
    Nodes         []Node
    Flows         []SequenceFlow
    CancelActions []string // optional, ordered action names run (best-effort) on cancellation
}
```

`model.Validate` rejects empty-string entries: a new sentinel `ErrEmptyCancelAction` returned when
any `CancelActions[i] == ""`. (The catalog is not available at validate time, so action-name
existence is not checked here — an unresolved name is handled best-effort at runtime: logged, cancel
still succeeds.) Empty/nil `CancelActions` ⇒ no behavior change (opt-in per definition).

`cloneState` is **untouched** (`CancelActions` is on the definition, not `InstanceState`).

### 2. Engine — `InvokeCancelAction` command + `CancelRequested` emission

A new fire-and-forget command joins the sealed `Command` set:

```go
// InvokeCancelAction asks the runtime to run a named ServiceAction as a
// best-effort side effect during cancellation. Unlike InvokeAction it carries no
// CommandID and its result is never fed back into the engine — the instance is
// already terminal. A failure is logged by the runtime; it never fails the cancel.
type InvokeCancelAction struct {
    Name  string
    Input map[string]any
}
func (InvokeCancelAction) isCommand() {}
var _ Command = InvokeCancelAction{}
```

The `CancelRequested` handler (engine/step.go) gains the cancel-action emission, alongside its
existing terminal commands, in **definition order**:

```go
case CancelRequested:
    ended := t.OccurredAt()
    s.Status = StatusTerminated
    s.EndedAt = &ended
    for i := range s.Tokens {
        tok := &s.Tokens[i]
        s.closeVisit(tok.ID, tok.NodeID, t.OccurredAt())
    }
    s.Tokens = nil

    var cmds []Command
    for _, name := range def.CancelActions { // NEW — fire-and-forget business tasks
        cmds = append(cmds, InvokeCancelAction{Name: name, Input: copyVars(s.Variables)})
    }
    cmds = append(cmds, FailInstance{Err: "cancelled"})
    cmds = append(cmds, s.cancelAllTimers()...)
    cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
    return StepResult{State: s, Commands: cmds}, nil
```

`Step` stays deterministic (commands are a pure function of `(def, state)`) and pure (uses
`t.OccurredAt()` and `copyVars`, no clock). Empty `CancelActions` ⇒ identical commands to today, so
the existing `TestCancelRequestedTerminates` cases stay green.

### 3. Runtime — best-effort `perform` + `Runner.CancelInstance`

```go
case engine.InvokeCancelAction:
    if r.cat == nil {
        r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action skipped: no catalog",
            slog.String("action", cmd.Name))
        return nil, nil
    }
    a, ok := r.cat.Resolve(cmd.Name)
    if !ok {
        r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action not found",
            slog.String("action", cmd.Name))
        return nil, nil
    }
    if _, err := a.Do(ctx, cmd.Input); err != nil {
        r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: cancel action failed",
            slog.String("action", cmd.Name), slog.Any("error", err))
    }
    return nil, nil // ALWAYS (nil, nil): no result fed back, never fails the cancel Deliver
```

```go
// CancelInstance terminates a running instance by delivering CancelRequested. Any
// definition-level CancelActions run best-effort inside the same deliverLoop.
func (r *Runner) CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error) {
    return r.Deliver(ctx, def, instanceID, engine.NewCancelRequested(r.clk.Now()))
}
```

The terminal-transition metric (`instCompleted{status="terminated"}`) already fires in
`deliverLoop`; no new counter.

### 4. Service — `CancelInstance` with already-terminal guard

```go
type CancelInstanceRequest struct {
    InstanceID string
}

func (e *Engine) CancelInstance(ctx context.Context, req CancelInstanceRequest) (engine.InstanceState, error) {
    def, st, err := e.resolveDefinition(ctx, req.InstanceID)
    if err != nil {
        return engine.InstanceState{}, fmt.Errorf("workflow-service: cancel instance: %w", err)
    }
    if isTerminal(st.Status) {
        return engine.InstanceState{}, fmt.Errorf("%w: instance %q is already terminal", ErrConflict, req.InstanceID)
    }
    st, err = e.runner.CancelInstance(ctx, def, req.InstanceID)
    if err != nil {
        return engine.InstanceState{}, fmt.Errorf("workflow-service: cancel instance: %w", err)
    }
    return st, nil
}
```

Added to the `Service` interface. `ErrInstanceNotFound`/`ErrDefinitionNotFound` propagate via
`resolveDefinition`. (The engine's idempotent re-cancel is intentionally fenced off here so the API
reports a clear conflict.)

### 5. REST — admin-gated route

`POST /admin/instances/{id}/cancel` behind the admin middleware (default-deny), mirroring
`handleResolveIncident`. No request body. Success → 200 with the mapped instance via
`renderInstance`. Errors via `WriteHTTPError`: `ErrConflict`→422 `conflict_state`,
`ErrInstanceNotFound`→404 `not_found` (both mappings already present). Route + doc-comment added to
`handler.go`.

### 6. gRPC — new RPC

```protobuf
rpc CancelInstance(CancelInstanceRequest) returns (InstanceResponse);

message CancelInstanceRequest {
  string instance_id = 1;
}
```

Regenerate `workflowpb` via `go generate ./transport/grpc/...` (needs `protoc` + the go/go-grpc
plugins; generated files are committed). `server.CancelInstance` mirrors `StartInstance`
(call `svc.CancelInstance` → `instanceToProto` → `mapToGRPCStatus`). No error-mapping change
(`ErrConflict`→`FailedPrecondition`, `ErrInstanceNotFound`→`NotFound` already mapped). gRPC has no
admin-middleware seam — the RPC is exposed and the consumer secures it via interceptors (inherent
REST/gRPC asymmetry; documented in the ADR).

## Data flow

```
REST POST /admin/instances/{id}/cancel  ─┐
gRPC CancelInstance ─────────────────────┤→ service.CancelInstance
                                          │     resolveDefinition → isTerminal? → ErrConflict (422/FailedPrecondition)
                                          │     runner.CancelInstance → Deliver(CancelRequested)
                                          │        engine.Step: terminate + emit InvokeCancelAction[] (def order) + FailInstance + CancelTimer[]
                                          │        deliverLoop perform: InvokeCancelAction → catalog.Do (best-effort, slog on failure, no feedback)
                                          └→ terminated InstanceState (StatusTerminated)
```

## Testing strategy

- **model:** `Validate` rejects an empty `CancelActions` entry (`ErrEmptyCancelAction`); a valid list
  passes; nil/empty is fine.
- **engine:** `CancelRequested` with `def.CancelActions=["a","b"]` emits `InvokeCancelAction{a}`,
  `InvokeCancelAction{b}` (in order) **before** `FailInstance`, sets `StatusTerminated`, clears
  tokens; with empty `CancelActions` the command set is unchanged (existing tests stay green);
  `InvokeCancelAction` satisfies the `Command` sealed set.
- **runtime:** cancelling an instance with `CancelActions` runs the actions (observe a recorder
  action's side effect); a **failing** cancel action and a **missing/unresolved** action are logged
  and the cancel **still returns `StatusTerminated` with nil error** (no `ErrInvalidTransition`
  leak); `Runner.CancelInstance` on a parked instance terminates it.
- **service:** parked instance → `CancelInstance` → `StatusTerminated`, empty tokens; already-terminal
  → `ErrConflict`; unknown id → `ErrInstanceNotFound`.
- **transport/rest:** default-deny (no admin middleware) → 403; admin success → 200 + terminated;
  already-terminal → 422 `conflict_state`; unknown → 404.
- **transport/grpc:** bufconn — success → `InstanceResponse` (status terminated); already-terminal →
  `codes.FailedPrecondition`; unknown → `codes.NotFound`.
- All black-box where exported; table-driven `assert`-closure; `t.Context()`.

## Verification gate

- `go test -race -p 1 ./...` green (Postgres `-p 1`).
- Touched packages ≥ 85% line coverage.
- `golangci-lint run ./...` clean.
- Engine/model **do** change this track (the new command + `CancelActions` field) — but `Step`
  determinism + purity preserved; no transport/storage/bus/time-vendor imports added to engine/model.
- `workflowpb` regenerated and committed; build does not require `protoc`.

## Risks & mitigations

- **Cancel-action result leaks into the terminal instance** → prevented by the dedicated
  `InvokeCancelAction` command whose `perform` returns `(nil, nil)` always (no follow-up trigger).
  A runtime test asserts cancel succeeds even when an action fails.
- **`protoc` unavailable for regen** → the generated `workflowpb` is committed; the implementer needs
  `protoc` + plugins only to regenerate (the Transports sub-project established this toolchain).
- **Sealed-set churn** → adding `InvokeCancelAction` is a deliberate, localized `engine/command.go`
  edit with a compile-time `var _ Command` assertion; the `perform` switch gets one new case.

## Deferred follow-ups

1. **Per-active-node cancel handlers** (BPMN-native, run the cancel handler of whatever activity is
   live) — `CancelActions` is process-level for v1.
2. **Cancel reason / audit** — carry an optional reason on `CancelRequested`/`CancelInstanceRequest`
   and record it (engine change).
3. **Cancel-action observability** — a span/counter per cancel action (align with the observability
   track); today failures are slog-only.
4. **gRPC admin interceptor guidance** — document/ship a sample interceptor mirroring the REST admin
   gate.
