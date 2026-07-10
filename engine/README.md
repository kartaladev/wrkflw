# engine — core token state machine

> **Most consumers use [`runtime`](../runtime/) instead of this package directly.**
> `runtime.ProcessDriver` wraps the engine, persists state, executes commands, drives
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
goroutines, and imports nothing beyond `definition` and the internal expression
evaluator. The runtime executes the returned commands and persists the new state.

---

## 1. Overview and purity

`Step` is a **pure function**: given the same inputs it always produces the same
outputs. This makes process logic deterministic and straightforwardly testable
without mocking a database or scheduler.

Import-purity guarantees (enforced by `purity_test.go`):
- No transport packages (the HTTP adapters, …).
- No persistence packages (Postgres, Redis, …).
- No event-bus packages (watermill, …).
- No observability SDK (OpenTelemetry).
- No scheduler (gocron, …).
- No wall-clock reads (`time.Now` never called; `time.Time` values arrive through
  the trigger or from `InstanceState`).

The runtime is responsible for all side effects. It reads the `[]Command` slice
returned by `Step`, executes each command (invoke a service action, schedule a
timer, create a human task, etc.), and persists the resulting `InstanceState`.

### Why the engine holds no ports

Adding a database call or scheduler invocation to the engine would:
1. Make `Step` no longer a pure function — replay, debugging, and deterministic
   unit tests would all require real infrastructure.
2. Couple the engine to a specific broker or scheduler SDK — swapping dependencies
   would require engine changes.
3. Break testability — a test of a three-node process would need a real database.

Instead, the engine emits a **command** (a named value type) that describes *what*
must happen; the runtime decides *how* and *when* to perform it. The engine never
knows whether its `ScheduleTimer` command ends up in an in-memory map or a gocron
cluster. This separation is enforced by the import-purity test in `purity_test.go`.

### Trigger → Command vocabulary

Every external event enters as a **Trigger** and every side effect the engine
requests leaves as a **Command**. The two types are sealed interfaces; the only way
to construct them is through the provided constructors (never raw struct literals).

**Triggers** carry a timestamp (`OccurredAt`) — the engine's only time source:

| Trigger | Produced by |
|---|---|
| `StartInstance` | ProcessDriver.Drive (first step) |
| `ActionCompleted` / `ActionFailed` | Runtime (after `InvokeAction`) |
| `HumanClaimed` / `HumanCompleted` / `HumanReassigned` | TaskService |
| `TimerFired` | Scheduler callback |
| `SignalReceived` | SignalBus |
| `MessageReceived` | ProcessDriver.DeliverMessage |
| `SubInstanceCompleted` / `SubInstanceFailed` | ProcessDriver (sync) or CallNotifier (async) |
| `CancelRequested` / `CompensateRequested` | Admin path (ProcessDriver.CancelInstance / service layer) |
| `ResolveIncident` | Admin path (ProcessDriver.ResolveIncident) |

**Commands** are promises the runtime must fulfil before persisting:

| Command | Runtime obligation |
|---|---|
| `InvokeAction` | Call catalog action; return `ActionCompleted` or `ActionFailed`. |
| `ScheduleTimer` / `CancelTimer` | Arm or disarm a timer via the Scheduler port. |
| `AwaitHuman` / `UpdateTask` | Create or update a human-task record. |
| `CompleteInstance` / `FailInstance` | Mark the instance terminal. |
| `ThrowSignal` | Broadcast to the SignalBus. |
| `SendMessage` | Write to the transactional outbox (no trigger fed back). |
| `StartSubInstance` | Start a child instance (sync or async). |
| `InvokeCancelAction` | Best-effort cancel side effect (no result fed back). |

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
All gateway conditions, deadline duration expressions, and correlation-key expressions
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
func Step(
    def *model.ProcessDefinition, // 1. the process definition (template)
    st  InstanceState,            // 2. the current instance state
    trg Trigger,                  // 3. the external event to apply
    opt StepOptions,              // 4. optional behaviour
) (StepResult, error)
```

`Step` maps `(def, st, trg, opt) → (StepResult, error)`. It is pure: same inputs
always produce the same outputs, and the input `st` is never mutated. The
subsections below follow the signature left-to-right — the four inputs in
positional order, then the output.

### Input 1 — `def *model.ProcessDefinition`

The process definition the instance executes against (the immutable template).
`Step` assumes it has already passed `definition.Validate`; in particular, an
exclusive gateway is assumed to have at most one unconditional non-default
outgoing flow — the engine takes the first matching flow in definition order and
does not detect ambiguous multi-unconditional configurations. For a token inside
a sub-process scope, the effective definition is resolved from this top-level one.

### Input 2 — `st InstanceState`

The current state of the instance (see §6). `Step` treats it as immutable: it
clones internally and never mutates the argument. The **first** `Step` of an
instance's life receives a seed state with only `InstanceID`, `DefID`, and
`DefVersion` set (paired with a `StartInstance` trigger); every later `Step`
receives the `State` returned by the previous call.

### Input 3 — `trg Trigger`

The single external event being applied this step. Every trigger carries a
timestamp (`OccurredAt() time.Time`) — the engine's **only** source of time (it
never reads the wall clock). Construct triggers with the provided constructors;
never build the struct literals directly.

| Constructor | Purpose |
|---|---|
| `engine.NewStartInstance(at, vars)` | Begin a new process instance with initial variables. |
| `engine.NewActionCompleted(at, commandID, output)` | A service action finished successfully. |
| `engine.NewActionFailed(at, commandID, errMsg, retryable, opts...)` | A service action failed (optionally retryable). Pass `engine.WithJitter(fraction)` to record a backoff jitter fraction. |
| `engine.NewHumanClaimed(at, taskToken, actor)` | A human task was claimed. |
| `engine.NewHumanCompleted(at, taskToken, output, actor)` | A human task was completed. |
| `engine.NewHumanReassigned(at, taskToken, from, to, by)` | A human task was reassigned from one actor to another (e.g. by an admin). |
| `engine.NewTimerFired(at, timerID)` | A previously scheduled timer fired. |
| `engine.NewSignalReceived(at, name, payload)` | A named signal was broadcast (resumes all tokens awaiting it). |
| `engine.NewMessageReceived(at, name, correlationKey, payload)` | A named message arrived (resumes the single matching token). |
| `engine.NewSubInstanceCompleted(at, commandID, output)` | A child process instance completed successfully. |
| `engine.NewSubInstanceFailed(at, commandID, errMsg)` | A child process instance failed. |
| `engine.NewCancelRequested(at)` | Admin: immediately terminate the instance. |
| `engine.NewCompensateRequested(at, toNode)` | Admin: roll back completed activities in reverse order (empty `toNode` = full rollback). |
| `engine.NewResolveIncident(at, incidentID, addAttempts)` | Admin: clear a parked incident and optionally grant extra retry budget. |

### Input 4 — `opt StepOptions`

`StepOptions` controls optional behaviour of the call. The zero value is valid
(Macro mode, no default retry, pure evaluator).

| Field | Type | Description |
|---|---|---|
| `Mode` | `StepMode` | Step granularity: `Macro` (default) or `Micro` — see the table below. |
| `DefaultRetryPolicy` | `*definition.RetryPolicy` | Fallback retry policy applied when a node carries no `RetryPolicy` of its own. `nil` = retry disabled by default. |
| `Evaluator` | `ConditionEvaluator` | Overrides the expression evaluator used for gateway conditions, timer/deadline durations, and correlation keys. `nil` (default) uses the pure, wall-clock-free package-global evaluator, keeping `Step` deterministic for replay. A consumer evaluating **untrusted** definitions can supply a timeout-capable evaluator (e.g. `expreval.New(expreval.WithTimeout(d))`) to bound evaluation latency and guard against expression-DoS — trading the replay-determinism guarantee for that protection (ADR-0049, ADR-0056). |

| `StepMode` | Behaviour |
|------------|-----------|
| `Macro` (default) | `drive` loops until **all** active tokens are parked or consumed. One `Step` call fully advances the instance past any chain of auto-advancing nodes (start events, gateways, etc.) until every token parks at a wait node or the instance is terminal. |
| `Micro` | `drive` stops after the **first** token park or terminal event. Useful for single-step debugging or test cases that need to inspect intermediate states. Auto-advancing nodes (start events, gateway routing that produces new active tokens) do not count as stops; execution passes through them within the same call until a park or terminal is reached. |

### Output — `(StepResult, error)`

`Step` returns a `StepResult` and an error. The error is non-nil only for
wrong-state, gateway-no-match, or unknown-trigger conditions — see the error
sentinels in §6.

`StepResult`:

| Field | Type | Description |
|---|---|---|
| `State` | `InstanceState` | The new instance state. `Step` never mutates its input — this is a fresh clone. |
| `Commands` | `[]Command` | Side effects the runtime must perform, in order, before persisting `State`. May be nil on a no-op step (e.g. a stale `TimerFired` with no matching token) — use `len(result.Commands)`, not `Commands != nil`, to test for work. |

`StepResult.Commands` are returned in the order the engine emitted them; the
runtime executes them all before persisting the new state.

| Command | What the runtime must do |
|---|---|
| `InvokeAction{CommandID, Name, Inline, Scoped, Input, FireAndForget}` | Run an `action.Action`; return result as `ActionCompleted`/`ActionFailed` carrying the same `CommandID`. `Inline` (engine-resolved node-local action) and `Scoped` (scope-effective catalog) are set by the engine and take precedence over resolving `Name` against the global catalog. When `FireAndForget` is true (deadline-breach and reminder actions) the runtime runs the action for its side effect only and feeds **no** `ActionCompleted`/`ActionFailed` back. |
| `ScheduleTimer{TimerID, Token, Trigger, Kind}` | Schedule a timer; deliver `TimerFired{TimerID}` per the resolved `schedule.TriggerSpec` in `Trigger`. The engine emits the trigger verbatim (including native recurring/calendar forms) and the scheduler owns the firing math and any recurrence — there is no engine-computed `FireAt`. `Kind` is `TimerIntermediate`, `TimerDeadline`, `TimerInWait`, or `TimerRetry`. |
| `CancelTimer{TimerID}` | Cancel a previously scheduled timer. |
| `AwaitHuman{TaskToken, Eligibility}` | Create a human-task record; park until `HumanCompleted`. |
| `UpdateTask{Task}` | Persist an updated `HumanTask` record (e.g. after a claim or reassignment). |
| `CompleteInstance{Result}` | Mark the instance completed with a result variable map. |
| `FailInstance{Err}` | Mark the instance failed. |
| `ThrowSignal{Name, Payload}` | Broadcast a named signal to interested subscribers. |
| `SendMessage{Name, CorrelationKey, Payload}` | Emit an outbound message (from a `SendTask`) through the runtime's message sink. Fire-and-forget: the token auto-advances past the send node in the same `Step`; the sink routes it (intra-engine `DeliverMessage`, an external broker / the eventing outbox, or both). |
| `StartSubInstance{CommandID, DefRef, Input}` | Start a child process instance; return result as `SubInstanceCompleted`/`SubInstanceFailed` carrying the same `CommandID`. |
| `InvokeCancelAction{Name, Input}` | Run a best-effort cancel side-effect action (no result fed back; the instance is already terminal). |
| `Compensate{ScopeID, FromNode}` | Reserved — not yet emitted. Future scope-targeted compensation for BPMN compensation boundary/throw producers. |

### Minimal usage example

```go
def, _ := definition.NewDefinition("order", 1).
    Add(event.NewStart("start")).
    Add(activity.NewServiceTask("charge", "billing.charge")).
    Add(event.NewEnd("end")).
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
| `ExclusiveGateway` | XOR | Takes the **first** outgoing flow whose `Condition` is true (definition order); or the flow marked `AsDefault()` if none match. Multiple unconditional flows are undefined — use `definition.Validate` to catch this. | Pass-through (single incoming). |
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
| `step_nodes.go` | `nodeStrategy` interface + `nodeStrategies` registry (16 registered kinds) + one stateless strategy struct per kind. |
| `step_gateways.go` | XOR/AND/OR fork/join algorithms. |
| `step_boundaries.go` | Boundary-event arming and firing. |
| `step_eventsubprocess.go` | Event-subprocess arming, scope open/close. |
| `step_compensation.go` | Compensation walk cursor, `beginCompensation`, `stepCompensationFinish`. |
| `step_errors.go` | `propagateError` — scope-chain error propagation for error end events and boundary error handlers. |
| `step_timers.go` | deadline/reminder/retry timer sub-dispatch inside `handleTimerFired`. |
| `step_state.go` | Token/scope/variable utility helpers shared across trigger handlers. |
| `state.go` | `InstanceState`, `Token`, `Incident`, `NodeVisit`, `Scope`, `Status`, `TokenState`, `CompensationRecord` type definitions + `InstanceState` method set. |
| `command.go` | Sealed `Command` interface + all command types. |
| `trigger.go` | Sealed `Trigger` interface + all trigger types and constructors. |

### The `nodeStrategy` registry

`drive` dispatches each token's current node through:

```go
var nodeStrategies = map[definition.NodeKind]nodeStrategy{ ... }
```

Sixteen kinds are registered: `KindServiceTask`, `KindStartEvent`,
`KindEndEvent`, `KindSubProcess`, `KindUserTask`, `KindIntermediateCatchEvent`,
`KindErrorEndEvent`, `KindExclusiveGateway`, `KindParallelGateway`,
`KindInclusiveGateway`, `KindEventBasedGateway`, `KindCallActivity`,
`KindIntermediateThrowEvent`, `KindBusinessRuleTask`, `KindReceiveTask`,
`KindSendTask`.

Three kinds intentionally fall through to the post-dispatch parking logic (token
is set `TokenWaitingCommand`): `KindBoundaryEvent`,
`KindEventSubProcess`, `KindUnspecified`. `step_nodes_test.go` pins both sets as
a completeness check (replaces the compiler's switch-exhaustiveness guarantee).

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

### `InstanceState`

The full execution state of one process instance. Consumer-relevant fields:

| Field | Type | Description |
|---|---|---|
| `InstanceID` | `string` | Unique instance identifier. |
| `DefID` | `string` | Process definition ID this instance executes. |
| `DefVersion` | `int` | Process definition version. |
| `Status` | `Status` | Lifecycle status (see the `Status` table below). |
| `Variables` | `map[string]any` | Flat process variables; `expr` conditions evaluate against these by bare key name. |
| `Tokens` | `[]Token` | Live execution tokens (see the `Token` table below). |
| `StartedAt` | `time.Time` | When the instance started (from the `StartInstance` trigger). |
| `EndedAt` | `*time.Time` | When the instance reached a terminal status; `nil` while running. |
| `History` | `[]NodeVisit` | Ordered record of node visits, for audit/snapshot projections. |
| `Tasks` | `[]humantask.HumanTask` | Human-task records created for user-task nodes. |
| `Incidents` | `[]Incident` | Open incident records (see the `Incident` table below). |

The remaining fields are **internal bookkeeping** (not part of the consumer contract; may change): `Timers`, `Scopes`, `ArmedEvents`, `Boundaries`, `EventSubprocesses`, `RootCompensations`, `ArchivedCompensations`, `Compensating`, `PendingCancel`, `DeferredCompensationThrows` (the ADR-0071 serialized-throw queue), and the sequence counters (`TokenSeq`, `CmdSeq`, `IncidentSeq`, …).

Use `st.Clone()` to take a deep copy when you need to retain a state snapshot; note that `Step` already clones its input internally and never mutates it.

### `Status`

| Value | Meaning |
|---|---|
| `StatusRunning` | Instance is active. |
| `StatusCompleted` | Instance finished normally (`CompleteInstance` emitted). |
| `StatusFailed` | Instance failed with an unhandled error (`FailInstance` emitted). |
| `StatusCompensating` | Compensation walk is in progress. |
| `StatusTerminated` | Instance was cancelled or fully rolled back. |

### `Token`

A token marks one point of execution within the instance.

| Field | Type | Description |
|---|---|---|
| `ID` | `string` | Unique token identifier within the instance. |
| `NodeID` | `string` | The node the token currently sits at. |
| `ScopeID` | `string` | The execution scope (empty = root; non-empty = a sub-process scope). |
| `State` | `TokenState` | Active / parked / at-join / incident (see the `TokenState` table in §2). |
| `AwaitCommand` | `string` | The `CommandID` or human-task token the parked token is waiting on. |
| `AwaitSignal` | `string` | The signal name the token is waiting for (signal catch). |
| `AwaitMessage` | `string` | The message name the token is waiting for (message catch/receive). |
| `AwaitMessageKey` | `string` | The resolved correlation key that must match an incoming message. |
| `Payload` | `map[string]any` | Token-local variables carried across a transition (e.g. gateway branch data). |
| `EnteredAt` | `time.Time` | When the token entered its current node. |
| `RetryAttempts` | `int` | Number of retry attempts consumed at the current node. |
| `RetryStartedAt` | `time.Time` | When the current retry sequence began (for backoff bookkeeping). |

### `Incident`

Created when a token's retry budget is exhausted (or a non-retryable error occurs). Resolved via `NewResolveIncident`.

| Field | Type | Description |
|---|---|---|
| `ID` | `string` | Unique incident identifier within the instance. |
| `TokenID` | `string` | The token parked in the `TokenIncident` state. |
| `NodeID` | `string` | The node whose action failed. |
| `ScopeID` | `string` | The execution scope of the failed token. |
| `CommandID` | `string` | The `InvokeAction` command that failed (used to re-invoke on resolution). |
| `Error` | `string` | The failure message from the last attempt. |
| `Attempts` | `int` | Number of attempts made before the incident was raised. |
| `CreatedAt` | `time.Time` | When the incident was raised. |

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

Attach `activity.WithCompensateAction("undo-action")` to any activity. When the
activity completes, a `CompensationRecord` is appended (in completion order) to
the relevant scope's record list.

`NewCompensateRequested(at, toNode)` initiates a reverse-order walk: the engine
enters `StatusCompensating`, emits one `InvokeAction` per record from most-recent
to least-recent (down to `toNode`, exclusive). When the walk finishes (all
records processed, or `toNode` reached), the instance enters `StatusTerminated`
(full rollback) or resumes at `toNode` (partial rollback).

An `IntermediateThrowEvent` with `event.WithCompensateRef("nodeID")` triggers a
localized compensation walk over the archived records of the named sub-process
scope, then resumes execution past the throw event.

### Cancel

`NewCancelRequested` terminates the instance immediately: all live tokens are
consumed, all timers and boundary/gateway arms are cancelled (`CancelTimer`
commands emitted), and `Status` is set to `StatusTerminated`. Any
`CancelAction` actions registered on nodes are emitted as `InvokeCancelAction`
commands (best-effort; their results are never fed back into the engine).
