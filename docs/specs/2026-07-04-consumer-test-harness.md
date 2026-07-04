# Consumer test harness (`processtest`) — design

- **Status:** Approved (brainstorming, 2026-07-04)
- **Slug:** `consumer-test-harness`
- **ADR:** 0092 (records the new public package + the two supporting seam changes)

## Problem

A consumer embedding this engine has to hand-roll a delivery loop to test a
definition: `Run` parks the instance, then the consumer must discover *why* it
parked (inspect `state.Tokens`, `state.Tasks`, `state.Timers`), build the right
trigger (human `Claim`/`Complete`, `TimerFired`, `SignalReceived`,
`MessageReceived`), call `Deliver`, and repeat until a terminal status. That
boilerplate is duplicated across every `examples/scenarios/*/main.go`
(`human_task_approval`, `inwait_reminder`, `boundary_timer`, `signal_broadcast`,
`message_correlation`, …).

Today only `kernel.MemStore`, `kernel.MemScheduler`, `authz.AllowAll`,
`action.MapCatalog`/`Registry`, and the in-memory `humantask` store ship as
reusable doubles. There is **no** consumer-facing drive helper, **no** capturing
spies for the catalog/authorizer, and **no** externally-injectable email fake
(the `action/email` sender seam is unexported).

## Goal

Ship a public, root-level `processtest` package that lets a consumer unit-test a
definition without hand-rolling delivery — plus the fakes the audit named
(action-catalog spy, authorizer spy, email sender capture). Error-returning, no
`testing` dependency, so it also powers godoc `Example`s.

## Non-goals

- No new engine-core behaviour. `engine`/`definition/model`/`action/email` stay zero-diff.
- No Docker/testcontainers — the harness is fully in-memory and deterministic.
- Not a load/integration harness; unit-level definition testing only.

## Package & naming

- New package `github.com/zakyalvan/krtlwrkflw/processtest` (repo root, no `pkg/`).
- Error-returning API; **no import of `testing`** and **no import of `clockwork`**
  (a public package must not leak a vendor clock — see FakeClock below).
- The driver type is `runtime.ProcessDriver` (not "Runner"; that name only
  survives in stale comments).

## Architecture

Two entry points over one shared drive loop:

### 1. `Harness` fixture (primary, ergonomic)

```go
func New(opts ...Option) (*Harness, error)
```

`New` wires and owns the whole in-memory stack:

| Collaborator      | Concrete                                   |
|-------------------|--------------------------------------------|
| store             | `kernel.MemStore`                          |
| clock             | `processtest.FakeClock` (shared)            |
| scheduler         | `kernel.MemScheduler` (clock-shared)       |
| catalog           | `*SpyCatalog` (wraps an inner map)         |
| authorizer        | `*SpyAuthorizer` (allow-all default)       |
| task store        | `humantask.MemTaskStore`                   |
| actor resolver    | `humantask.StaticActorResolver` (permissive default) |
| task service      | `runtime/task.TaskService`                 |
| signal bus        | `signal.SignalBus` (opt-in via option)     |

Accessors for assertions: `Driver()`, `Store()`, `Clock()`, `Scheduler()`,
`Catalog() *SpyCatalog`, `Authorizer() *SpyAuthorizer`, `Tasks()`,
`TaskService()`, `Bus()`.

Options: `WithActions(map[string]action.Action)`, `WithAction(name, fn)`,
`WithAuthorizer(func(ctx, spec, actor, vars) error)` (programs the spy),
`WithActorResolver(humantask.ActorResolver)`, `WithSignalBus()`,
`WithDefinitions(kernel.DefinitionRegistry)`, `WithDriveLimit(int)`,
`WithClockStart(time.Time)`.

Methods:
```go
func (h *Harness) Start(ctx, def *model.ProcessDefinition, id string, vars map[string]any) (engine.InstanceState, error)
func (h *Harness) DriveToCompletion(ctx, def *model.ProcessDefinition, id string, handler ParkHandler) (engine.InstanceState, error)
```
`DriveToCompletion` loads the current state from the owned store, then runs the
shared loop (§3). Because the fixture owns the clock+scheduler, `AdvanceTimers()`
works here.

### 2. Free function (secondary, lower-level)

```go
func DriveToCompletion(ctx, driver *runtime.ProcessDriver, def *model.ProcessDefinition, state engine.InstanceState, handler ParkHandler) (engine.InstanceState, error)
```

Same loop against a consumer-built driver. Takes the current `state` (as returned
by the consumer's `Run`) rather than an id, because `ProcessDriver` exposes no
state accessor. `AdvanceTimers()` is **unsupported** here (no owned scheduler) and
returns a descriptive error pointing to the fixture; timer flows via the free
function require the handler to build/deliver its own triggers. The fixture method
delegates to this function after loading initial state.

## The park model (pluggable, classification provided)

The loop never pushes token/task/timer classification onto the consumer: it
classifies each park and hands the handler a rich `Park`. The handler decides.

```go
type Reason int
const (
    ReasonTerminal Reason = iota
    ReasonHumanTask
    ReasonTimer
    ReasonSignal
    ReasonMessage
    ReasonIncident
    ReasonAsyncChild
    ReasonUnknown
)
func (Reason) String() string // fmt.Stringer, for clear errors

type Park struct {
    State            engine.InstanceState // full state — handler may inspect anything
    Reason           Reason               // best-effort PRIMARY classification (convenience)
    Node             string               // parked node id (best-effort)
    OpenTasks        []humantask.HumanTask // Unclaimed/Claimed tasks
    AwaitingSignals  []string             // distinct AwaitSignal names
    AwaitingMessages []string             // distinct AwaitMessage names
    HasArmedTimers   bool                 // len(State.Timers) > 0
    Incidents        []engine.Incident    // parked incidents
}

type ParkHandler func(ctx context.Context, p Park) (Decision, error)
```

`Reason` priority when multiple apply (a user task may also have a boundary
timer): HumanTask > Incident > Signal > Message > Timer > AsyncChild > Unknown.
`Reason` is a convenience for simple `switch` handlers; the discrete slices/flags
let a handler act on a secondary park (e.g. fire a reminder timer while a task is
open) without re-deriving anything.

### Decision

An opaque value built by constructors; the zero value is `Pass` ("I didn't handle
this — try the next handler / it's stuck"):

```go
type Decision struct { /* unexported */ }
func Deliver(t engine.Trigger) Decision // feed an arbitrary trigger to the driver
func AdvanceTimers() Decision           // advance clock to the next due timer + Tick (fixture only)
func Stop() Decision                    // stop driving; return current (possibly non-terminal) state, nil
func Abort(err error) Decision          // stop driving; return err
func Pass() Decision                    // explicit no-op (== zero value)
```

### Ready-made handler combinators

```go
func AutoTimers() ParkHandler // Reason == ReasonTimer → AdvanceTimers(); else Pass
func (h *Harness) CompleteTasks(decide DecideTaskFunc) ParkHandler
    // first OpenTask decide accepts → Claim(+Complete); decide memoized per token; declined tasks skipped
func (h *Harness) PublishSignal(name string, payload map[string]any) ParkHandler // stamps with the fake clock
func (h *Harness) DeliverMessage(name, correlationKey string, payload map[string]any) ParkHandler
func Chain(handlers ...ParkHandler) ParkHandler // first non-Pass decision wins; all Pass → Pass
```

Review-driven refinements: `AutoTimers` keys on `Reason == ReasonTimer` (not "any
timer armed"), so `Chain(AutoTimers(), CompleteTasks(...))` never fires a user
task's deadline out from under the task handler. `PublishSignal`/`DeliverMessage`
are **`Harness` methods** stamping triggers with the shared fake clock (kept
deterministic); the signal bus is likewise wired with the fake clock. The harness
classifies an intermediate timer catch precisely — matching the parked token's
`AwaitCommand` against `MemScheduler.Pending` — so an async call-activity park
coexisting with an unrelated timer is not misclassified, and `advanceTimers` only
moves the clock forward.

`CompleteTasks` needs the `TaskService` to turn a decision into a trigger. In the
fixture path the loop injects the owned `TaskService`; the combinator is
constructed by the harness so it closes over it. (For the free-function path a
`CompleteTasksWith(svc *task.TaskService, decide …)` variant takes an explicit
service.)

Typical calls:
```go
h.DriveToCompletion(ctx, def, id, processtest.AutoTimers())                       // fully automatic + timers
h.DriveToCompletion(ctx, def, id, h.Chain(h.AutoTimers(), h.CompleteTasks(dec))) // approval flow
```

## Drive loop (shared)

```
state := initial
for step := 0; step < limit; step++ {
    if isTerminal(state.Status) { return state, nil }   // Completed | Failed | Terminated
    park := classify(state)
    decision, err := handler(ctx, park)
    if err != nil { return state, err }
    switch decision {
      Pass:          return state, fmt.Errorf("%w: %s at node %q", ErrUnhandledPark, park.Reason, park.Node)
      Stop:          return state, nil
      Abort(e):      return state, e
      Deliver(trg):  state, err = driver.Deliver(ctx, def, id, trg); if err != nil { return state, err }
      AdvanceTimers: fireAt, ok := sched.NextFireAt(); if !ok { return state, ErrNoPendingTimer }
                     clock.Set(fireAt); _ = sched.Tick(ctx)  // fire callback calls driver.Deliver internally
                     state, err = store.Load(ctx, id); if err != nil { return state, err }
    }
}
return state, ErrDriveLimitExceeded
```

- `isTerminal` mirrors the engine's unexported predicate (Completed/Failed/Terminated).
- `DriveLimit` default 1000 guards non-terminating definitions and no-progress
  handlers; `ErrDriveLimitExceeded` names the last park reason.
- Sentinels: `ErrUnhandledPark`, `ErrNoPendingTimer`, `ErrDriveLimitExceeded`,
  `ErrAdvanceTimersUnsupported` (free-function path).

## Fakes (also usable standalone)

- **`SpyCatalog`** implements `action.Catalog`; wraps an inner `action.Catalog`
  (or map). Records every resolved invocation `[]Invocation{Name, In, Out, Err}`
  by wrapping resolved actions. Helpers: `Invocations()`, `InvocationsOf(name)`,
  `Count(name)`.
- **`SpyAuthorizer`** implements `authz.Authorizer`; programmable `decide` func
  (default allow-all). Records `[]AuthzCall{Spec, Actor, Vars, Err}`. Helpers:
  `Calls()`, `Deny(err)`, `Allow()`.
- **`CaptureSender`** records `[]SentEmail{Addr, From, To, Msg}` and exposes an
  `email.SenderFunc` (via a `SenderFunc()` accessor) that a consumer passes to
  `email.WithSender(...)`. Helpers: `Sent()`, `Last()`. **No change to
  `action/email`** — the existing exported `email.SenderFunc` adapter already lets
  an external package inject a capturing sender (the current `email_test` black-box
  tests do exactly this), so the real email action (template render, CRLF guard,
  per-recipient fan-out) is already testable through it.

## Supporting seam change (additive, no external breakage) — ADR-0092

1. **Add `MemScheduler.NextFireAt() (time.Time, bool)`** to `runtime/kernel` —
   returns the earliest pending timer's fire time (`false` if none). Needed by
   `AdvanceTimers()` because `state.Timers`' element type is unexported and the
   scheduler is the only place fire times are visible externally. Reference
   in-memory scheduler only; the `Scheduler` interface is unchanged.

## FakeClock

`processtest.FakeClock` implements `clock.Clock` (`Now() time.Time`) plus
`Advance(d time.Duration)` and `Set(t time.Time)`. Trivial (a mutex + a
`time.Time`); MemScheduler only reads `Now()`, so clockwork's timer/after
machinery is unnecessary. Keeps the public harness free of a `clockwork`
dependency and honours "depend on `clock.Clock`, don't import a vendor clock".

## Testing

- Strict TDD; every new symbol gets a failing test first. All deterministic and
  in-memory (no Docker).
- Table tests per the project `table-test` skill (assert-closure form).
- Black-box `processtest_test` package.
- Ship godoc `Example`s: automatic flow, timer flow, approval flow, email capture
  — these double as the harness's executable documentation.
- Reuse an existing simple definition builder (`definition/build`) to construct
  test definitions; classification is exercised against real parked states, not
  synthesised ones.
- Target ≥85% line coverage on `processtest`; `email`/`kernel` deltas keep their
  existing coverage.

## Risks / open points

- **Multi-park nodes** (user task + boundary timer). Handled by exposing discrete
  slices/flags on `Park`; `Reason` is only a convenience. Reminder-style flows are
  a consumer-written handler (advance N times, then complete) — documented in an
  Example.
- **AsyncChild / call-activity parks** classify as `ReasonAsyncChild`; v1 does not
  auto-drive children (the child instance is driven by its own link notifier).
  A handler that hits one and can't proceed gets `ErrUnhandledPark` — acceptable
  for v1; note it in godoc.
```
