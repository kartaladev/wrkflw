# runtime

Package `runtime` is the reference driver that executes `engine.Step` commands,
persists state, and feeds external results back as triggers. It is the package
most library consumers use — the layer between the pure functional engine and
the rest of the application.

Import path: `github.com/zakyalvan/krtlwrkflw/runtime`

## Overview

`engine.Step` is a pure function: it takes a definition, a current state, and a
trigger, and returns a list of commands and a new state. Nothing is persisted,
nothing is invoked. `runtime.Runner` is the driver that makes it real:

- Calls `engine.Step` in a loop until the instance reaches a terminal state or
  parks at a wait point (user task, catch event, async call activity).
- Executes each returned command: invokes service actions, schedules timers,
  creates human-task records, throws signals, starts sub-instances.
- Persists every applied step atomically via `Store` (snapshot + journal +
  outbox in one transaction).
- Delivers follow-up triggers produced by those commands (action results, timer
  fires, signal deliveries) back through the loop.

The package also provides:

- `MemStore` — in-memory `Store` for development and testing.
- `CachingStore` — write-through, single-writer LRU cache in front of any
  `Store`.
- `MemScheduler` — clock-driven in-memory `Scheduler` for tests.
- `SignalBus` — fan-out signal delivery to parked instances.
- `NewTaskService` — human-task authorization and trigger production.
- `NewInstanceSnapshot` / `NewActionableView` — JSON-safe DTOs for reading
  instance state.

## Quickstart

Wire a `MapCatalog`, an in-memory store, and a runner; call `Run`.

```go
import (
    "context"

    "github.com/zakyalvan/krtlwrkflw/action"
    "github.com/zakyalvan/krtlwrkflw/engine"
    "github.com/zakyalvan/krtlwrkflw/model"
    "github.com/zakyalvan/krtlwrkflw/runtime"
)

cat := action.NewMapCatalog(map[string]action.ServiceAction{
    "greet": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
        return map[string]any{"greeting": "hi " + in["name"].(string)}, nil
    }),
})
store := runtime.NewMemStore()
r := runtime.NewRunner(cat, store) // clock defaults to clock.System()

def := &model.ProcessDefinition{
    ID: "greeting", Version: 1,
    Nodes: []model.Node{
        model.NewStartEvent("start"),
        model.NewServiceTask("greet", model.WithActionName("greet")),
        model.NewEndEvent("end"),
    },
    Flows: []model.SequenceFlow{
        {ID: "f1", Source: "start", Target: "greet"},
        {ID: "f2", Source: "greet", Target: "end"},
    },
}

st, err := r.Run(context.Background(), def, "inst-1", map[string]any{"name": "Ada"})
// st.Status == engine.StatusCompleted
// st.Variables["greeting"] == "hi Ada"
```

Pass `nil` for `cat` when the process has no service tasks.

## Runner construction and options

```go
func NewRunner(
    cat   action.Catalog, // 1. required — service-action catalog
    store Store,          // 2. required — transactional persistence port
    opts  ...Option,      //    optional capabilities (functional options)
) *Runner
```

**Positional argument 1 — `cat action.Catalog`.** The service-action catalog
resolving action names to `ServiceAction` implementations. May be `nil` if the
process has no service or business-rule tasks.

**Positional argument 2 — `store Store`.** The transactional persistence port
(snapshot + journal + outbox committed atomically per applied trigger). Use
`NewMemStore()` for dev/tests; wire the Postgres/MySQL store via the
`persistence` package for production.

> The **clock is no longer positional** — it defaults to `clock.System()` and is
> overridden with the `WithRunnerClock` option (inject a fake clock in tests for
> deterministic timers).

**Optional capabilities (functional options).** The complete set of `With*`
functions accepted by `NewRunner`:

| Option | What it enables |
|---|---|
| `WithHumanTasks(resolver, taskStore, az)` | User-task support: candidate resolution, task persistence, authorization. Without this, any user-task node returns an error. |
| `WithScheduler(sched)` | Timer support: `ScheduleTimer`/`CancelTimer` commands are armed. Without this, any timer node returns an error. |
| `WithSignalBus(bus)` | Signal throw support: `ThrowSignal` commands are broadcast. Without this, any signal-throw node returns an error. |
| `WithDefinitions(reg)` | Definition registry for resolving call-activity `DefRef` strings. Required when any `CallActivity` node is present. |
| `WithCallLinks(store)` | Enables the async (non-blocking) call-activity path. Without this, call activities run the child synchronously to completion in-process. |
| `WithTimerStore(store)` | Persists armed timers so `RehydrateTimers` can re-arm them after a restart. Without this, timers are in-memory only. |
| `WithDefaultRetryPolicy(p)` | Fallback `model.RetryPolicy` for action-bearing nodes that declare none. Without this, a failed action goes straight to incident or error-boundary. |
| `WithActionTimeout(d)` | Per-invocation timeout applied to every service action (default **30s**; `0` disables). A hung action that honours ctx is cancelled and surfaces as a retryable failure. |
| `WithExpressionTimeout(d)` | Wraps the engine's expression evaluator with a per-eval timeout (guards against expression-DoS from untrusted definitions). |
| `WithConditionEvaluator(eval)` | Replaces the engine's expression evaluator entirely (advanced; supersedes `WithExpressionTimeout`). |
| `WithJitterSource(src)` | Custom jitter for retry-backoff de-synchronization; inject a deterministic source in tests. |
| `WithRunnerClock(clk)` | Overrides the time source (default `clock.System()`); inject a fake clock in tests. |
| `WithLogger(l)` | Structured logger (`*slog.Logger`); defaults to `slog.Default()`. |
| `WithTracerProvider(tp)` | OTel tracer provider; defaults to the OTel global. |
| `WithMeterProvider(mp)` | OTel meter provider; defaults to the OTel global. |

## Driving an instance

### `Run` — start and drive

```go
st, err := r.Run(ctx, def, instanceID, vars)
```

Creates a new instance and drives it through the engine's command loop until it
either reaches a terminal status (`StatusCompleted`, `StatusFailed`,
`StatusTerminated`) or parks at a wait point:
- a user task (`WithHumanTasks` required),
- an intermediate catch event (timer, signal, message),
- a call-activity child that has not yet completed (async path only).

Returns `engine.InstanceState` reflecting the state at the point execution
stopped. A parked instance has `Status == engine.StatusRunning` with one or
more tokens holding a non-zero `AwaitCommand` or `AwaitSignal` value.

### `Deliver` — external trigger

```go
st, err := r.Deliver(ctx, def, instanceID, trg)
```

Loads the instance, applies one external trigger via `engine.Step`, persists the
result, and drives forward until the instance parks or completes again. Use this
to resume a parked instance:
- user-task claim / completion (via `TaskService`),
- signal arrival,
- timer fire (handled internally by the scheduler callback),
- message correlation,
- compensation or cancel.

### `DeliverMessage` — message correlation

```go
err := r.DeliverMessage(ctx, def, messageName, correlationKey, payload)
```

Finds the single instance currently waiting for a `ReceiveTask` or message catch
event with the given name and correlation key and delivers a `MessageReceived`
trigger to it. No-op if no matching waiter is found.

### `ResolveIncident` — admin recovery

```go
st, err := r.ResolveIncident(ctx, def, instanceID, incidentID, addAttempts)
```

Grants `addAttempts` additional retry attempts on the incident's node and
re-invokes the parked action. Call this when retry exhaustion has produced an
incident and an operator has corrected the underlying problem.

### `CancelInstance` — terminate

```go
st, err := r.CancelInstance(ctx, def, instanceID)
```

Delivers a `CancelRequested` trigger. Any definition-level cancel actions (see
`model.CancelActions`) run best-effort inside the same loop. When `WithCallLinks`
and `WithDefinitions` are both configured, running async child instances are
cancelled recursively (best-effort; errors are logged, never returned). Returns
the terminated `InstanceState`.

### `RehydrateTimers` — restart recovery

```go
err := r.RehydrateTimers(ctx)
```

Re-arms every persisted armed timer on the scheduler. Call once at startup,
after constructing the runner, to recover timers that were lost when the process
restarted. Requires `WithScheduler`, `WithTimerStore`, and `WithDefinitions`.
A timer whose `FireAt` is already in the past fires immediately; a re-fire of an
already-consumed timer is an idempotent engine no-op.

## Human tasks

Wire `WithHumanTasks` with a task store, an actor resolver, and an authorizer.
Use `NewTaskService` to authorize claim/complete interactions and produce the
triggers to feed back into the runner.

```go
manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}

taskStore := humantask.NewMemTaskStore()
resolver  := humantask.NewStaticActorResolver(map[string][]authz.Actor{
    "manager": {manager},
})
az  := authz.RoleAuthorizer{}

r := runtime.NewRunner(
    nil, // no service actions for this example
    runtime.NewMemStore(),
    runtime.WithHumanTasks(resolver, taskStore, az),
)

def := &model.ProcessDefinition{
    ID: "approval", Version: 1,
    Nodes: []model.Node{
        model.NewStartEvent("start"),
        model.NewUserTask("approve", []string{"manager"}),
        model.NewEndEvent("end"),
    },
    Flows: []model.SequenceFlow{
        {ID: "f1", Source: "start", Target: "approve"},
        {ID: "f2", Source: "approve", Target: "end"},
    },
}

// Run parks at the user task.
parked, err := r.Run(ctx, def, "inst-1", nil)
// parked.Status == engine.StatusRunning

// List tasks claimable by the manager.
claimable, err := taskStore.ClaimableBy(ctx, manager)
taskToken := claimable[0].TaskToken

// Authorize and produce a HumanClaimed trigger.
svc := runtime.NewTaskService(taskStore, az)

claimTrg, err := svc.Claim(ctx, taskToken, manager)
r.Deliver(ctx, def, "inst-1", claimTrg)

// Complete the task (output is merged into process variables).
completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"approved": true})
final, err := r.Deliver(ctx, def, "inst-1", completeTrg)
// final.Status == engine.StatusCompleted
// final.Variables["approved"] == true
```

Authorization happens in `TaskService` so the engine core remains pure. The
runner snapshots process variables into `HumanTask.Vars` at task-creation time
so attribute-based eligibility predicates (e.g. `vars["region"] == "EU"` via
`model.WithEligibilityExpr`) are evaluated against the correct state at claim
time.

## Signals, messages, and timers

### Signals

Wire `WithSignalBus` to enable `ThrowSignal` commands and signal-catch events.
Construct the bus with a delivery function that routes to the right runner:

```go
bus := runtime.NewSignalBus(func(ctx context.Context, id string, trg engine.Trigger) error {
    _, err := r.Deliver(ctx, def, id, trg)
    return err
})
r2 := runtime.NewRunner(cat, store, runtime.WithSignalBus(bus))
```

After each `Run`/`Deliver` iteration the runner calls `SignalBus.Sync` to
register the instance's current awaited signals. A subsequent `bus.Publish` fans
the signal out to all registered waiters.

### Messages

For `ReceiveTask` and message catch events, use `DeliverMessage`. The runner
tracks message waiters internally; no extra configuration is needed beyond
constructing the runner.

### Timers and deadlines

Wire `WithScheduler` to enable timer nodes (`IntermediateCatchEvent` with
`WithTimerDuration`), deadlines (`WithDeadline` on any activity), and reminders
(`WithReminder`). Use `NewMemScheduler` for tests:

```go
fc   := clockwork.NewFakeClockAt(startAt)
sched := runtime.NewMemScheduler(runtime.WithMemSchedulerClock(fc))
r     := runtime.NewRunner(cat, store,
    runtime.WithScheduler(sched),
    runtime.WithRunnerClock(fc)) // share the fake clock for deterministic timers

// After Run parks at a timer node, advance the fake clock and call Tick.
fc.Advance(1*time.Hour + 1*time.Second)
sched.Tick(ctx) // fires registered callbacks → delivers TimerFired → instance resumes
```

For production, wire a `gocron`-backed scheduler from the `scheduling` package
via `WithScheduler`.

To survive process restarts, also wire `WithTimerStore` and call
`r.RehydrateTimers(ctx)` once during startup.

## Retries and incidents

Retry policy can be set per node with `model.WithRetryPolicy` or globally as a
runner-level fallback with `WithDefaultRetryPolicy`:

```go
p := model.RetryPolicy{
    MaxAttempts:     5,
    InitialInterval: 2 * time.Second,
    BackoffCoef:     2.0,
    MaxInterval:     30 * time.Second,
}
r := runtime.NewRunner(cat, store, runtime.WithDefaultRetryPolicy(p))
```

When retries are exhausted the engine creates an `engine.Incident` on the token.
The instance stays `StatusRunning` with that token in `TokenIncident` state. An
operator calls `ResolveIncident` to grant additional attempts and resume
execution.

## Reading instance state (DTOs)

Two read-only projections are available after `Run` or `Deliver` returns:

```go
// Full JSON-safe snapshot: status, variables, tokens, history, tasks, incidents.
snap := runtime.NewInstanceSnapshot(st, def)

// Actionable view: open tasks + allowed next outgoing flows per task.
view := runtime.NewActionableView(st, def)

// Human-readable status string ("running", "completed", "failed", etc.).
s := runtime.StatusString(st.Status)
```

#### `InstanceSnapshot`

The full JSON-safe projection. It omits engine bookkeeping fields (timers,
scopes, internal sequences) so it is safe to JSON-encode and return to API
consumers without leaking implementation details.

| Field | JSON key | Type | Description |
|---|---|---|---|
| `InstanceID` | `instance_id` | `string` | Unique process instance identifier. |
| `DefID` | `def_id` | `string` | Process-definition ID. |
| `DefVersion` | `def_version` | `int` | Process-definition version. |
| `Status` | `status` | `string` | Instance lifecycle state (see `StatusString`). |
| `Variables` | `variables` | `map[string]any` | Current process variables. |
| `Tokens` | `tokens` | `[]TokenView` | Current token positions. |
| `History` | `history` | `[]NodeVisitView` | Ordered audit trail of node visits. |
| `Tasks` | `tasks` | `[]TaskView` | In-flight human-task records. |
| `Incidents` | `incidents` | `[]IncidentView` | Open incident records. |
| `StartedAt` | `started_at` | `time.Time` | When the instance was created. |
| `EndedAt` | `ended_at` | `*time.Time` | When the instance reached a terminal state; `nil` while running. |
| `ScopedActions` | `scoped_actions` | `[]string` | Sorted names in the definition-scoped action catalog; `nil` when none are registered or no definition is available. |
| `ActionBindings` | `action_bindings` | `[]ActionBindingView` | Action wiring for each `ServiceTask`/`BusinessRuleTask`, sorted by node ID. Each `ActionBindingView` is `{NodeID, NodeKind, Action, Inline}`. |

#### `ActionableView`

Purpose-built for UI rendering: it exposes only open human tasks together with
the outgoing flows derived from the definition, so a frontend can offer
contextual action buttons without knowing the BPMN graph.

| Field | JSON key | Type | Description |
|---|---|---|---|
| `InstanceID` | `instance_id` | `string` | Unique process instance identifier. |
| `Status` | `status` | `string` | Instance lifecycle state. |
| `OpenTasks` | `open_tasks` | `[]ActionableTask` | Tasks currently open (Unclaimed or Claimed). |

Each `ActionableTask` (note: `AllowedActions` lives here, **per task**, not on the view):

| Field | JSON key | Type | Description |
|---|---|---|---|
| `TaskToken` | `task_token` | `string` | Unique task instance identifier. |
| `NodeID` | `node_id` | `string` | BPMN node that generated the task. |
| `State` | `state` | `string` | Task lifecycle state. |
| `ClaimedBy` | `claimed_by` | `string` | Actor ID that claimed the task; empty when unclaimed. |
| `Candidates` | `candidates` | `[]string` | Resolved actor IDs eligible to act on the task. |
| `AllowedActions` | `allowed_actions` | `[]NextAction` | Outgoing sequence flows from this task's node; `nil` when no definition is available. Each `NextAction` is `{FlowID, Target, Condition, IsDefault}`. |

Both DTOs are also exposed over the REST transport (`transport/rest`) at
`GET /instances/{id}/snapshot` and `GET /instances/{id}/actionable`.

## Stores and caching

### `MemStore` (development and tests)

`NewMemStore()` is an in-memory `Store` + `JournalReader` + `InstanceLister`
backed by a plain map with per-instance optimistic-CAS versioning. It is
concurrency-safe and does not require a database.

```go
store := runtime.NewMemStore()
r     := runtime.NewRunner(cat, store)
```

Access the trigger history for audit assertions in tests:
```go
entries, _ := store.Entries(ctx, instanceID) // []engine.Trigger
```

### `CachingStore` (production hot path)

`CachingStore` is a write-through, bounded LRU cache in front of any `Store`.
It is correct only when exactly one process writes each instance
(`Ownership` guarantees that invariant). `AlwaysOwn` is appropriate for
single-process embedding; multi-replica deployments need a real lease
(`persistence.NewAdvisoryLockOwnership`).

```go
store := runtime.NewCachingStore(
    pgStore,           // backing Store (e.g. a Postgres/MySQL/SQLite store from persistence; note: SQLite provides only the fail-loud persistence.NewSQLiteAdvisoryLockOwnership, so CachingStore on SQLite is single-process only)
    runtime.AlwaysOwn{},
    runtime.WithCacheTTL(5*time.Minute),
    runtime.WithCacheMaxEntries(1024),
    // clock defaults to clock.System(); override with runtime.WithCachingStoreClock(...)
)
r := runtime.NewRunner(cat, store)
```

### `CachingDefinitionRegistry`

For hot-path definition resolution, wrap any `DefinitionRegistry` with
`NewCachingDefinitionRegistry` to avoid repeated unmarshalling:

```go
// Second arg is the cache TTL (time.Duration); the clock defaults to
// clock.System() and is overridden with WithCachingDefinitionRegistryClock(...).
reg := runtime.NewCachingDefinitionRegistry(pgDefRegistry, 5*time.Minute)
r   := runtime.NewRunner(cat, store, runtime.WithDefinitions(reg))
```

### SQL store (production)

The production `Store` implementation lives in the neutral `internal/persistence/store`
parametrized by an `internal/persistence/dialect` (Postgres, MySQL, or SQLite — ADR-0081/0082).
It satisfies the `runtime.Store` interface. Wire it via the `persistence` package's exported
constructors — consumers do not import `internal/` directly:

- `persistence.OpenPostgres(ctx, pool, opts...)` — Postgres 17 (LISTEN/NOTIFY relay, advisory-lock ownership).
- `persistence.OpenMySQL(ctx, db, opts...)` — MySQL 8.0+ (poll-only relay, advisory-lock ownership).
- `persistence.OpenSQLite(ctx, db, opts...)` — SQLite (WAL, single-writer, single-node/test/embedded).
  `persistence.NewSQLiteAdvisoryLockOwnership` is fail-loud (every acquire returns `dialect.ErrUnsupported`),
  so `CachingStore` on SQLite is single-process only. Use Postgres or MySQL for multi-replica.

All three backends expose relay/lister/store constructors (`NewSQLite*`, `NewMySQL*`, `NewPostgres*`).

## Process-instance chaining

Chaining automatically starts a new, **independent** top-level instance when
another reaches a terminal state — completed, failed, or terminated (ADR-0045).
The predecessor fully ends and releases its resources; the successor is a fresh
root instance that outlives it. This is *not* the parent→child nesting of an
async call activity (`StartSubInstance`); it is sequential chaining of
independent instances, driven off the durable terminal outbox events.

The `SuccessorPolicy` callback receives a `ChainEvent` (the terminal predecessor)
and returns a `SuccessorDecision`:

**`ChainEvent`** (input to the policy):

| Field | Type | Description |
|---|---|---|
| `PredecessorID` | `string` | The instance that reached a terminal state. |
| `PredecessorDefinitionRef` | `string` | The `"defID:version"` of the predecessor, carried end-to-end through the outbox (ADR-0047) so a policy can route on the predecessor's definition. Empty only for pre-ADR-0047 events. |
| `Outcome` | `Outcome` | The terminal outcome that fired the event (`OutcomeCompleted` / `OutcomeFailed` / `OutcomeTerminated`). |
| `Result` | `map[string]any` | The event payload: terminal variables (completed) or `{"error": …}` (failed/terminated). |

**`SuccessorDecision`** (returned by the policy):

| Field | Type | Description |
|---|---|---|
| `Def` | `*model.ProcessDefinition` | The successor definition to start. A `nil` `Def` (or returning `ok=false`) ends the chain. |
| `Vars` | `map[string]any` | Seed variables for the successor instance. |

Three pieces:

- **`SuccessorPolicy`** — a Go callback `func(ctx, ChainEvent) (SuccessorDecision,
  bool)`. It decides the successor definition + seed variables for a terminal
  predecessor; returning `ok=false` (or a nil `Def`) ends the chain.
- **`Chainer`** — the broker-agnostic core. `Handle(ctx, ChainEvent)` applies the
  policy, records the lineage hop, then starts the successor with the
  deterministic id `<predecessor>-next-<outcome>`.
- **`ChainLinkStore`** — durable lineage (`MemChainLinkStore` for tests/embedded;
  `persistence.NewChainLinkStore` for Postgres). The unique `(predecessor,
  outcome)` key plus the deterministic successor id and `Store.Create`'s
  `ErrInstanceExists` give **exactly-once effect** under at-least-once delivery.

```go
policy := func(_ context.Context, ev runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
    if ev.Outcome != runtime.OutcomeCompleted {
        return runtime.SuccessorDecision{}, false
    }
    return runtime.SuccessorDecision{Def: fulfillmentDef, Vars: ev.Result}, true
}
chainer := runtime.NewChainer(runner, policy, runtime.WithChainLinks(links))
```

The terminal event reaches the `Chainer` over the broker: mount
`eventing.NewChainHandler(chainer)` on your own `message.Router`, or run the
turnkey `eventing.NewChainerRunner(chainer).Run(ctx, sub)` which subscribes the
`instance.completed` / `instance.failed` / `instance.terminated` topics. All
watermill stays in `eventing`; `runtime` never imports it. `Handle` is
idempotent — a redelivered terminal event is a clean no-op. See
[`ExampleChainer`](chainer_example_test.go).

> **Terminal events are status-accurate (ADR-0046).** Each terminal status emits
> exactly one event: completed→`instance.completed`, failed→`instance.failed`,
> terminated→`instance.terminated`. A cancelled instance now emits
> `instance.terminated` (previously `instance.failed`), and an admin full-rollback
> termination now emits `instance.terminated` (previously nothing). Consumers
> route on the topic; the `{"error": …}` payload is human-readable, not an enum.
