# processtest

Package `processtest` is an in-memory test harness for driving **your** workflow
definitions to completion — without hand-rolling the park/deliver loop, without
Docker, and deterministically.

Import path: `github.com/zakyalvan/krtlwrkflw/processtest`

`processtest` is the consumer-facing counterpart to the engine: you build a
process definition with `definition`, then use a `processtest.Harness` to run an
instance and drive it to a terminal status while asserting on what happened. It is
error-returning and imports neither `testing` nor a third-party clock, so it also
powers godoc `Example`s.

---

## Why it exists

A started instance does **not** run straight to completion. It advances
synchronously until it *parks* on an external stimulus — a human task, a timer, a
signal, a message, or an async child — then waits. Driving it yourself means:

1. discover *why* it parked (inspect `state.Tokens` / `state.Tasks` / `state.Timers`),
2. build the matching trigger (`Claim`/`Complete`, `TimerFired`, `SignalReceived`, …),
3. call `Deliver`,
4. repeat until `state.Status` is terminal.

`processtest` absorbs that loop. You supply a **park handler** that says *what to
do* at each park; the harness does the classification and delivery.

---

## 60-second quick start

A human-task approval flow, driven to completion:

```go
func TestExpenseApproval(t *testing.T) {
    def, err := definition.NewBuilder("expense-approval", 1).
        Add(event.NewStart("start")).
        Add(activity.NewUserTask("approve", []string{"manager"})).
        Add(event.NewEnd("end")).
        Connect("start", "approve").
        Connect("approve", "end").
        Build()
    require.NoError(t, err)

    h, err := processtest.New()
    require.NoError(t, err)

    _, err = h.Start(t.Context(), def, "inst-1", map[string]any{"amount": 4200})
    require.NoError(t, err)

    // decide how each open task is handled.
    approve := func(tsk humantask.HumanTask) (authz.Actor, map[string]any, bool) {
        return authz.Actor{ID: "alice", Roles: []string{"manager"}},
            map[string]any{"approved": true}, true
    }

    final, err := h.DriveToCompletion(t.Context(), def, "inst-1", h.CompleteTasks(approve))
    require.NoError(t, err)
    assert.Equal(t, engine.StatusCompleted, final.Status)
}
```

That's the whole shape: **`New` → `Start` → `DriveToCompletion(..., handler)`**.

---

## Core concepts

### `Harness`

`processtest.New(opts...)` wires the entire in-memory stack and owns it:

| Collaborator | Concrete | Accessor |
|---|---|---|
| store | `kernel.MemStore` | `h.Store()` |
| clock | `processtest.FakeClock` (shared) | `h.Clock()` |
| scheduler | `kernel.MemScheduler` (clock-shared) | `h.Scheduler()` |
| action catalog | `*SpyCatalog` | `h.Catalog()` |
| authorizer | `*SpyAuthorizer` (allow-all) | `h.Authorizer()` |
| human-task store | `humantask.MemTaskStore` | `h.Tasks()` |
| task service | `runtime/task.TaskService` | `h.TaskService()` |
| signal bus | `signal.SignalBus` (opt-in) | `h.Bus()` |
| driver | `runtime.ProcessDriver` | `h.Driver()` |

Options: `WithAction(name, a)`, `WithActionFunc(name, fn)`, `WithActions(map)`,
`WithAuthorizer(decideFn)`, `WithActorResolver(r)`, `WithSignalBus()`,
`WithDefinitions(reg)` (call activities), `WithDriveLimit(n)`, `WithClockStart(t)`.

### `ParkHandler` and `Decision`

```go
type ParkHandler func(ctx context.Context, p Park) (Decision, error)
```

At each park the harness calls your handler with a classified `Park` and expects a
`Decision`:

| `Decision` | Meaning |
|---|---|
| `Deliver(trigger)` | feed an arbitrary `engine.Trigger` to the driver |
| `AdvanceTimers()` | jump the fake clock to the next due timer and fire it *(Harness only)* |
| `Stop()` | stop driving; return the current (possibly non-terminal) state, `nil` error |
| `Abort(err)` | stop driving; return `err` |
| `Pass()` | "I didn't handle this" — the zero `Decision`; defers under `Chain` |

If a top-level handler returns `Pass()` for a non-terminal park, the drive fails
with `ErrUnhandledPark` (the instance is stuck — nothing resolved it).

### `Park` and `Reason`

The handler never has to re-derive *why* an instance parked. The harness classifies
it and fills a rich `Park`:

```go
type Park struct {
    State            engine.InstanceState // full state — inspect anything
    Reason           Reason               // primary classification (convenience)
    Node             string               // parked node id
    OpenTasks        []humantask.HumanTask
    AwaitingSignals  []string
    AwaitingMessages []string
    HasArmedTimers   bool
    Incidents        []engine.Incident
}
```

`Reason` is one of `ReasonTerminal`, `ReasonHumanTask`, `ReasonIncident`,
`ReasonSignal`, `ReasonMessage`, `ReasonTimer`, `ReasonAsyncChild`, `ReasonUnknown`
(that is also the priority order when several apply). Switch on `Reason` for the
common case, or read the discrete fields to handle a *secondary* park (e.g. fire a
reminder timer while a task is still open).

---

## Ready-made handlers

Compose these instead of writing a handler by hand:

```go
processtest.AutoTimers()                       // Reason==ReasonTimer → AdvanceTimers()
h.CompleteTasks(decide)                         // claim + complete open human tasks
h.PublishSignal("market-open", payload)         // resolve a signal park
h.DeliverMessage("PaymentReceived", key, payload) // resolve a message park
processtest.Chain(h1, h2, ...)                  // first non-Pass decision wins
```

`AutoTimers` and `Chain` are **package functions**; `CompleteTasks`,
`PublishSignal`, and `DeliverMessage` are **`Harness` methods** (they need the
harness's clock and task service — see [Quirks](#quirks--gotchas)).

### Recipes

```go
// Fully automatic (timers only):
h.DriveToCompletion(ctx, def, id, processtest.AutoTimers())

// Approval flow (timers, if any, then tasks):
h.DriveToCompletion(ctx, def, id,
    processtest.Chain(processtest.AutoTimers(), h.CompleteTasks(approve)))

// Signal flow:
h.DriveToCompletion(ctx, def, id, h.PublishSignal("go", nil))

// Message correlation:
h.DriveToCompletion(ctx, def, id, h.DeliverMessage("PaymentReceived", "order-1", nil))

// Custom: advance a reminder timer twice, THEN complete the task.
reminders := 0
custom := func(_ context.Context, p processtest.Park) (processtest.Decision, error) {
    if p.HasArmedTimers && reminders < 2 { // a task with an in-wait reminder timer
        reminders++
        return processtest.AdvanceTimers(), nil
    }
    if len(p.OpenTasks) > 0 {
        trg, err := h.TaskService().Claim(ctx, p.OpenTasks[0].TaskToken, actor)
        if err != nil { return processtest.Abort(err), nil }
        return processtest.Deliver(trg), nil
    }
    return processtest.Pass(), nil
}
```

---

## Fakes & assertions

All three fakes are exported for standalone use too.

### `SpyCatalog` — assert which actions ran

```go
h, _ := processtest.New(processtest.WithActionFunc("charge",
    func(_ context.Context, in map[string]any) (map[string]any, error) {
        return map[string]any{"charged": true}, nil
    }))
// ... drive ...
assert.Equal(t, 1, h.Catalog().Count("charge"))
inv := h.Catalog().InvocationsOf("charge")[0]
assert.Equal(t, 4200, inv.In["amount"])
assert.True(t, inv.Out["charged"].(bool))
```

### `SpyAuthorizer` — program and record authz

```go
h.Authorizer().Deny(authz.ErrNotAuthorized) // reject every actor
// ...
h.Authorizer().Allow()                        // back to allow-all
calls := h.Authorizer().Calls()               // every (Spec, Actor, Vars, Err)
```

Or program it up front: `processtest.New(processtest.WithAuthorizer(fn))`.

### `CaptureSender` — assert emails without SMTP

Wire it into the **real** `action/email` action:

```go
cap := processtest.NewCaptureSender()
mail := email.NewEmail(
    email.WithSender(cap.SenderFunc()), // <- the seam
    email.WithFrom("ops@example.com"),
    email.WithTo("alice@example.com"),
    email.WithSubjectTemplate("Notice"),
    email.WithBodyTemplate("Hello {{.name}}"),
)
h, _ := processtest.New(processtest.WithAction("notify", mail))
// ... drive ...
sent := cap.Sent()
assert.Len(t, sent, 1)
assert.Equal(t, "ops@example.com", sent[0].From)
```

---

## Driving your own driver (free function)

If you already built a `runtime.ProcessDriver` yourself, use the package-level
function instead of the fixture:

```go
state, _ := driver.Run(ctx, def, "id", nil)
final, err := processtest.DriveToCompletion(ctx, driver, def, state, handler)
```

It runs the same loop. It cannot `AdvanceTimers()` (it owns no scheduler) — see below.

---

## Quirks & gotchas

These are the things that trip people up. Read them before your first timer test.

### 1. Everything is deterministic — the clock is fake and frozen

The harness never reads the wall clock. It starts at a **fixed instant**
(`2026-01-01T00:00:00Z` by default; override with `WithClockStart(t)`) and only
moves when *you* advance it (`AdvanceTimers`, or `h.Clock().Advance(d)`). Two runs
of the same test produce byte-identical timestamps. Signals, messages, and timers
are all stamped from this shared fake clock — including bus-published signals.

### 2. Timer-driving handlers only work under a `Harness`

`AdvanceTimers()` needs a clock and scheduler. The **free** `DriveToCompletion`
owns neither, so an `AdvanceTimers()` decision there returns
`ErrAdvanceTimersUnsupported`. For timer flows, use the `Harness` (its
`DriveToCompletion` honours `AdvanceTimers`). In the free path, build and deliver
the `TimerFired` trigger yourself.

### 3. `AutoTimers` keys on `Reason == ReasonTimer`, **not** "a timer is armed"

This is deliberate. A user task can carry a **deadline/boundary timer**: it parks
as `ReasonHumanTask` *and* `HasArmedTimers == true`. If `AutoTimers` fired on
"any armed timer", `Chain(AutoTimers(), CompleteTasks(...))` would trip the
deadline and complete the task by *timeout* instead of letting your actor act.
Because `AutoTimers` checks the *primary* `Reason`, that ordering is safe: it only
fires a park whose primary reason is a timer. To intentionally breach a deadline,
read `p.HasArmedTimers` in a custom handler and return `AdvanceTimers()` yourself.

### 4. `Chain` order matters

`Chain` returns the **first non-`Pass`** decision. Put the handler you want to win
first. `Chain(AutoTimers(), CompleteTasks(decide))` advances timers when the park
*is* a timer, otherwise falls through to the task handler. A `nil` handler in the
chain is skipped.

### 5. The clock and scheduler are **shared across all instances** on one harness

Advancing timers while driving instance A moves the single fake clock and fires
**every globally-due timer**, including instance B's. For isolated timer assertions
across unrelated instances, **use one `Harness` per instance**. (Multi-instance
*signal/message* fan-out on a shared harness is fine and routes per definition —
see #7.)

### 6. Intermediate timer catches are invisible in `state.Timers`

The engine records deadline/boundary timers in `state.Timers`, but an **intermediate
timer catch** (`event.WithCatchTimer`) parks as a bare command-wait whose armed
timer lives only in the *scheduler*. Consequences:

- The **pure** `processtest.Classify(state)` (state-only) reports such a park as
  `ReasonAsyncChild` — it cannot see the scheduler.
- The **harness** enriches this: it matches the parked token's `AwaitCommand`
  against the scheduler and reports `ReasonTimer` precisely — so a genuine async
  call-activity park that merely coexists with an unrelated timer is *not*
  misclassified.

So inside a `DriveToCompletion` your handler sees `ReasonTimer` for timer catches;
`Classify` called standalone does not. Prefer driving through the harness.

### 7. The signal bus is opt-in and routes per definition

`h.Bus()` is `nil` unless you pass `WithSignalBus()`. When enabled, a single
`h.Bus().Publish(ctx, name, payload)` resumes **all** instances awaiting that
signal, each against its own definition (multiple definitions on one harness route
correctly), stamped with the fake clock. `Publish` only reaches instances that were
`Start`ed, so publishing before any `Start` is a no-op, not a panic.

### 8. `CompleteTasks`: one decision per task, first accepted task wins

`decide` is invoked **at most once per task token** — the verdict (actor, output,
accept) is memoized and reused for the claim and the completion, so completion
always uses the actor that claimed. Claiming and completing are two separate
deliveries, so it takes two drive steps per task. The handler acts on the **first
open task `decide` accepts**, skipping declined ones (a later actionable task is
never stranded behind a declined one). If every open task is declined, it returns
`Pass()` → `ErrUnhandledPark`.

### 9. Progress bounds and error sentinels

- The drive stops after `DriveLimit` steps (default **1000**; `WithDriveLimit(n)`)
  with `ErrDriveLimitExceeded` — this catches non-terminating definitions and
  handlers that never make progress.
- `ErrUnhandledPark` — a real (non-terminal) park your handler `Pass()`ed on.
- `ErrNoPendingTimer` — `AdvanceTimers()` with no timer armed.
- `ErrAdvanceTimersUnsupported` — `AdvanceTimers()` on the free-function path.

### 10. No `testing`, no Docker

The package imports neither, so you can use it inside `Example` functions and CI
without a container runtime. Terminal detection is exposed as
`processtest.IsTerminal(status)` (completed / failed / terminated).

---

## Terminal statuses

`DriveToCompletion` stops at a terminal status: `StatusCompleted`, `StatusFailed`,
or `StatusTerminated`. `StatusRunning` and `StatusCompensating` are non-terminal.
Read `final.Status`, `final.Variables`, `final.Incidents`, `final.Tasks`, etc. from
the returned `engine.InstanceState`.

---

See the `Example_*` functions in this package for runnable end-to-end samples
(timer flow, approval flow, email capture), and ADR-0092 / the design spec under
`docs/` for the rationale.
