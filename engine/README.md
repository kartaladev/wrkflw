# engine — core token state machine

> **Most consumers use [`runtime`](../runtime/) instead of this package directly.**
> `runtime.Runner` wraps the engine, persists state, executes commands, drives
> timers, and integrates human tasks. Reach for `engine` only when you need
> deterministic unit tests of process logic, or when you are building your own
> execution layer on top.

Package `engine` is the **pure token state machine** that drives process
instances. Its single exported entry point is:

```go
func Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)
```

`Step` maps `(definition, current state, trigger) → (commands + next state)`.
It does no I/O, reads no wall clock (time arrives inside the trigger), spawns no
goroutines, and imports nothing beyond `model` and the internal expression
evaluator. The runtime executes the returned commands and persists the new state.

---

## 1. Overview and purity

`Step` is a **pure function**: given the same inputs it always produces the same
outputs. This makes process logic deterministic and straightforwardly testable
without mocking a database or scheduler.

Import-purity guarantees (enforced by `purity_test.go`):
- No transport packages (HTTP, gRPC, …).
- No persistence packages (Postgres, Redis, …).
- No event-bus packages (watermill, …).
- No observability SDK (OpenTelemetry).
- No scheduler (gocron, …).
- No wall-clock reads (`time.Now` never called; `time.Time` values arrive through
  the trigger or from `InstanceState`).

The runtime is responsible for all side effects. It reads the `[]Command` slice
returned by `Step`, executes each command (invoke a service action, schedule a
timer, create a human task, etc.), and persists the resulting `InstanceState`.

---

## 2. Token-based execution

Execution state is a set of **tokens** stored in `InstanceState.Tokens`. Each
token sits at a node in the process definition and has one of four states:

| `TokenState`        | Meaning                                              |
|---------------------|------------------------------------------------------|
| `TokenActive`       | Token is being driven forward in the current `Step`. |
| `TokenWaitingCommand` | Token is parked, waiting for an external event (action result, human task, timer, signal, message). |
| `TokenAtJoin`       | Token is waiting at a parallel/inclusive gateway join for sibling tokens. |
| `TokenIncident`     | Token's retry budget exhausted; parked until an operator resolves it. |

**Variables** are stored flat on `InstanceState.Variables` as `map[string]any`.
All gateway conditions, SLA duration expressions, and correlation-key expressions
are evaluated by [`expr-lang/expr`](https://github.com/expr-lang/expr) against
this map using **bare key names** (e.g. `amount > 100`, not `vars.amount`).

On every `Step`, `drive()` picks up `TokenActive` tokens one at a time,
dispatches their node through the strategy registry (see §5), and either parks
the token (emitting commands for the runtime) or advances it along an outgoing
sequence flow to the next node.

---

## 3. The Step contract

### Signature

```go
result, err := engine.Step(def, state, trigger, engine.StepOptions{})
```

`Step` returns a `StepResult`:

```go
type StepResult struct {
    State    InstanceState // new state (never mutates the input)
    Commands []Command     // side effects the runtime must perform, in order
}
```

If `Commands` is nil (e.g. a stale `TimerFired` with no matching token), the
step is a clean no-op. Use `len(result.Commands)` to test for work.

### StepOptions and StepMode

```go
type StepOptions struct {
    Mode               StepMode           // Macro (default) or Micro
    DefaultRetryPolicy *model.RetryPolicy // fallback when a node carries none
}
```

| `StepMode` | Behaviour |
|------------|-----------|
| `Macro` (default) | `drive` loops until **all** active tokens are parked or consumed. One `Step` call fully advances the instance past any chain of auto-advancing nodes (start events, gateways, etc.) until every token parks at a wait node or the instance is terminal. |
| `Micro` | `drive` stops after the **first** token park or terminal event. Useful for single-step debugging or test cases that need to inspect intermediate states. Auto-advancing nodes (start events, gateway routing that produces new active tokens) do not count as stops; execution passes through them within the same call until a park or terminal is reached. |

### Triggers (input)

Every trigger carries a timestamp (`OccurredAt() time.Time`) — the engine's
only source of time. Construct triggers with the provided constructors; never
construct the struct literals directly.

| Constructor | Purpose |
|---|---|
| `engine.NewStartInstance(at, vars)` | Begin a new process instance with initial variables. |
| `engine.NewActionCompleted(at, commandID, output)` | A service action finished successfully. |
| `engine.NewActionFailed(at, commandID, errMsg, retryable)` | A service action failed (optionally retryable). |
| `engine.NewHumanClaimed(at, taskToken, actor)` | A human task was claimed. |
| `engine.NewHumanCompleted(at, taskToken, output, actor)` | A human task was completed. |
| `engine.NewTimerFired(at, timerID)` | A previously scheduled timer fired. |
| `engine.NewSignalReceived(at, name, payload)` | A named signal was broadcast (resumes all tokens awaiting it). |
| `engine.NewMessageReceived(at, name, correlationKey, payload)` | A named message arrived (resumes the single matching token). |
| `engine.NewSubInstanceCompleted(at, commandID, output)` | A child process instance completed successfully. |
| `engine.NewSubInstanceFailed(at, commandID, errMsg)` | A child process instance failed. |
| `engine.NewCancelRequested(at)` | Admin: immediately terminate the instance. |
| `engine.NewCompensateRequested(at, toNode)` | Admin: roll back completed activities in reverse order (empty `toNode` = full rollback). |
| `engine.NewResolveIncident(at, incidentID, addAttempts)` | Admin: clear a parked incident and optionally grant extra retry budget. |

### Commands (output)

Commands are returned in the order the engine emitted them. The runtime executes
them all before persisting the new state.

| Command | What the runtime must do |
|---|---|
| `InvokeAction{CommandID, Name, Inline, Scoped, Input}` | Run a `ServiceAction`; return result as `ActionCompleted`/`ActionFailed` carrying the same `CommandID`. `Inline` (engine-resolved node-local action) and `Scoped` (scope-effective catalog) are set by the engine and take precedence over resolving `Name` against the global catalog. |
| `ScheduleTimer{TimerID, Token, FireAt, Kind}` | Schedule a timer; deliver `TimerFired{TimerID}` at `FireAt`. `Kind` is `TimerIntermediate`, `TimerSLA`, `TimerInWait`, or `TimerRetry`. |
| `CancelTimer{TimerID}` | Cancel a previously scheduled timer. |
| `AwaitHuman{TaskToken, Eligibility}` | Create a human-task record; park until `HumanCompleted`. |
| `UpdateTask{Task}` | Persist an updated `HumanTask` record (e.g. after a claim or reassignment). |
| `CompleteInstance{Result}` | Mark the instance completed with a result variable map. |
| `FailInstance{Err}` | Mark the instance failed. |
| `ThrowSignal{Name, Payload}` | Broadcast a named signal to interested subscribers. |
| `StartSubInstance{CommandID, DefRef, Input}` | Start a child process instance; return result as `SubInstanceCompleted`/`SubInstanceFailed` carrying the same `CommandID`. |
| `InvokeCancelAction{Name, Input}` | Run a best-effort cancel side-effect action (no result fed back; the instance is already terminal). |
| `Compensate{ScopeID, FromNode}` | Reserved — not yet emitted. Future scope-targeted compensation for BPMN compensation boundary/throw producers. |

### Minimal usage example

```go
def, _ := model.NewDefinition("order", 1).
    Add(model.NewStartEvent("start")).
    Add(model.NewServiceTask("charge", "billing.charge")).
    Add(model.NewEndEvent("end")).
    Connect("start", "charge").
    Connect("charge", "end").
    Build()

st := engine.InstanceState{InstanceID: "ord-1", DefID: "order", DefVersion: 1}
at := time.Now()

// Start
res, err := engine.Step(def, st, engine.NewStartInstance(at, map[string]any{"amount": 99}), engine.StepOptions{})
// res.Commands → [InvokeAction{CommandID:"cmd-1", Name:"billing.charge", ...}]

// Action completed
res, err = engine.Step(def, res.State, engine.NewActionCompleted(at, "cmd-1", nil), engine.StepOptions{})
// res.Commands → [CompleteInstance{...}]
```

---

## 4. Gateway semantics

Gateways read `InstanceState.Variables` via `expr-lang` expressions evaluated
with **bare key names**.

| Gateway | BPMN type | Split behaviour | Join behaviour |
|---|---|---|---|
| `ExclusiveGateway` | XOR | Takes the **first** outgoing flow whose `Condition` is true (definition order); or the flow marked `AsDefault()` if none match. Multiple unconditional flows are undefined — use `model.Validate` to catch this. | Pass-through (single incoming). |
| `ParallelGateway` | AND | Activates **all** outgoing flows simultaneously, one token per branch. | Waits until **all** incoming flows carry a token (`TokenAtJoin`), then fires. |
| `InclusiveGateway` | OR | Activates all outgoing flows whose `Condition` is true (or the default). | Waits for all **active** matching branches (branches that were not taken are not waited for). |
| `EventBasedGateway` | Event-based | Arms all following `IntermediateCatchEvent` branches simultaneously. The gateway token is parked until the **first** armed event fires; sibling arms are cancelled. | Not applicable (single path). |

---

## 5. Internal structure (ADR-0044)

The engine's file layout after the ADR-0044 decomposition:

| File | Responsibility |
|---|---|
| `step.go` | `Step` (thin trigger type-switch) + `drive` (token loop + strategy dispatch) + `stepCtx`. |
| `step_triggers.go` | One `handle<Trigger>` function per trigger type; called by `Step`'s type-switch. |
| `step_nodes.go` | `nodeStrategy` interface + `nodeStrategies` registry (13 registered kinds) + one stateless strategy struct per kind. |
| `step_gateways.go` | XOR/AND/OR fork/join algorithms. |
| `step_boundaries.go` | Boundary-event arming and firing. |
| `step_eventsubprocess.go` | Event-subprocess arming, scope open/close. |
| `step_compensation.go` | Compensation walk cursor, `beginCompensation`, `stepCompensationFinish`. |
| `step_errors.go` | `propagateError` — scope-chain error propagation for error end events and boundary error handlers. |
| `step_timers.go` | SLA/reminder/retry timer sub-dispatch inside `handleTimerFired`. |
| `step_state.go` | Token/scope/variable utility helpers shared across trigger handlers. |
| `state.go` | `InstanceState`, `Token`, `Incident`, `NodeVisit`, `Scope`, `Status`, `TokenState`, `CompensationRecord` type definitions + `InstanceState` method set. |
| `command.go` | Sealed `Command` interface + all command types. |
| `trigger.go` | Sealed `Trigger` interface + all trigger types and constructors. |

### The `nodeStrategy` registry

`drive` dispatches each token's current node through:

```go
var nodeStrategies = map[model.NodeKind]nodeStrategy{ ... }
```

Thirteen arm-bearing kinds are registered: `KindServiceTask`, `KindStartEvent`,
`KindEndEvent`, `KindSubProcess`, `KindUserTask`, `KindIntermediateCatchEvent`,
`KindErrorEndEvent`, `KindExclusiveGateway`, `KindParallelGateway`,
`KindInclusiveGateway`, `KindEventBasedGateway`, `KindCallActivity`,
`KindIntermediateThrowEvent`.

Seven kinds intentionally fall through to the post-dispatch parking logic (token
is set `TokenWaitingCommand`): `KindTerminateEndEvent`, `KindBusinessRuleTask`,
`KindReceiveTask`, `KindSendTask`, `KindBoundaryEvent`, `KindEventSubProcess`,
`KindUnspecified`. `step_nodes_test.go` pins both sets as a completeness check
(replaces the compiler's switch-exhaustiveness guarantee).

### The `halt` signal

`nodeStrategy.enter` returns `(cmds []Command, halt bool, err error)`. Only
`errorEndEventStrategy` returns `halt=true`. When `drive` sees `halt`, it exits
immediately (returning all commands accumulated so far) rather than continuing to
the next active token. This preserves the semantics of the original code path
where an error end event terminated `drive` entirely, preventing surviving
parallel-branch tokens from being driven further after the instance is already
terminal/failed.

---

## 6. State model

### `InstanceState` (consumer-relevant fields)

```go
type InstanceState struct {
    InstanceID string
    DefID      string
    DefVersion int
    Status     Status
    Variables  map[string]any
    Tokens     []Token
    StartedAt  time.Time
    EndedAt    *time.Time
    History    []NodeVisit
    Tasks      []humantask.HumanTask
    Incidents  []Incident
    // ... internal bookkeeping (Timers, Scopes, ArmedEvents, Boundaries,
    //     EventSubprocesses, RootCompensations, ArchivedCompensations,
    //     Compensating, PendingCancel, sequence counters)
}
```

Use `st.Clone()` to take a deep copy before feeding into the next `Step`.

### `Status`

| Value | Meaning |
|---|---|
| `StatusRunning` | Instance is active. |
| `StatusCompleted` | Instance finished normally (`CompleteInstance` emitted). |
| `StatusFailed` | Instance failed with an unhandled error (`FailInstance` emitted). |
| `StatusCompensating` | Compensation walk is in progress. |
| `StatusTerminated` | Instance was cancelled or fully rolled back. |

### `Token`

Key fields: `ID`, `NodeID`, `ScopeID`, `State` (`TokenState`),
`AwaitCommand` (CommandID or task token the token is parked on),
`AwaitSignal`, `AwaitMessage`/`AwaitMessageKey`, `Payload`, `EnteredAt`,
`RetryAttempts`, `RetryStartedAt`.

### `Incident`

Created when a token's retry budget is exhausted (or a non-retryable error
occurs). Fields: `ID`, `TokenID`, `NodeID`, `ScopeID`, `CommandID`, `Error`,
`Attempts`, `CreatedAt`. Resolved via `NewResolveIncident`.

### Error sentinels

All engine error messages carry a `workflow-engine:` prefix (ADR-0026
convention):

| Sentinel | Meaning | `errors.Is` behaviour |
|---|---|---|
| `engine.ErrInvalidTransition` | A trigger arrived for a token that is not awaiting it (wrong state). | Parent sentinel — use `errors.Is(err, engine.ErrInvalidTransition)` to classify any wrong-state error. |
| `engine.ErrTokenNotFound` | Specific wrong-state: no token is awaiting the given command/task token. Wraps `ErrInvalidTransition`. | `errors.Is(err, ErrInvalidTransition)` is true. |
| `engine.ErrNoMatchingFlow` | Exclusive/inclusive gateway found no matching or default outgoing flow. Definition/data error. | Does **not** wrap `ErrInvalidTransition`. |
| `engine.ErrUnknownTrigger` | Trigger type is not handled by `Step`. Programming/infrastructure error. | Does **not** wrap `ErrInvalidTransition`. |

---

## 7. Compensation and error handling

### Error propagation

When an `ErrorEndEvent` fires (or an activity raises an error), `propagateError`
walks the scope chain outward looking for a matching `BoundaryEvent` with a
compatible error code. If a match is found, a token is routed to the boundary's
outgoing flow and execution continues. If no handler is found at any scope level,
the instance is marked `StatusFailed` and `FailInstance` is emitted.

### Retry

`ActionFailed` with `Retryable: true` increments `Token.RetryAttempts`. If the
node's `RetryPolicy` (or `StepOptions.DefaultRetryPolicy`) still has budget, the
engine emits `ScheduleTimer{Kind: TimerRetry}` with an exponential-backoff fire
time. On `TimerFired` the action is re-invoked via `InvokeAction`. When the
budget is exhausted the token moves to `TokenIncident` and an `Incident` record
is appended to `InstanceState.Incidents`. Operators clear incidents with
`NewResolveIncident`.

### Compensation

Attach `model.WithCompensation("undo-action")` to any activity. When the
activity completes, a `CompensationRecord` is appended (in completion order) to
the relevant scope's record list.

`NewCompensateRequested(at, toNode)` initiates a reverse-order walk: the engine
enters `StatusCompensating`, emits one `InvokeAction` per record from most-recent
to least-recent (down to `toNode`, exclusive). When the walk finishes (all
records processed, or `toNode` reached), the instance enters `StatusTerminated`
(full rollback) or resumes at `toNode` (partial rollback).

An `IntermediateThrowEvent` with `model.WithCompensateRef("nodeID")` triggers a
localized compensation walk over the archived records of the named sub-process
scope, then resumes execution past the throw event.

### Cancel

`NewCancelRequested` terminates the instance immediately: all live tokens are
consumed, all timers and boundary/gateway arms are cancelled (`CancelTimer`
commands emitted), and `Status` is set to `StatusTerminated`. Any
`CancelHandler` actions registered on nodes are emitted as `InvokeCancelAction`
commands (best-effort; their results are never fed back into the engine).
