# 2. Engine core executes as a pure stepper returning commands as data

- Status: Accepted
- Date: 2026-06-20

## Context

The `wrkflw` engine core is the token state machine that advances a process
instance across BPMN nodes. CLAUDE.md makes one property load-bearing: the core
must stay **pure of transport, storage vendor, and event-bus specifics** — it
depends on interfaces (or plain data) only and must be consumable with no
Postgres, watermill, or gRPC imported at all. Persistence is the source of
truth, events publish through a transactional outbox, timers/SLAs run on gocron,
and time is controlled through `jonboulle/clockwork` (see ADR-0003).

We evaluated three execution models (full write-up:
`docs/specs/2026-06-20-engine-core-execution-model-options.html` and the
junior-oriented explainer alongside it):

- **A — Pure stepper + commands as data.** The core is a deterministic function
  `Step(def, state, trigger) -> (newState, []Command)`. It performs no I/O and
  spawns no goroutines; a surrounding runtime executes commands and feeds results
  back as the next trigger, correlated by a `CommandID`.
- **B — Active engine.** The core holds injected I/O ports and drives execution
  itself (calls actions inline, schedules timers, owns goroutines).
- **C — Hybrid.** Pure transitions, but the core calls a few narrow injected
  ports synchronously mid-step instead of returning a command list.

The forces that decided it: the purity rule above; the need for a transactional
outbox (state write + event write in one tx); deterministic crash-recovery and
replay; long-lived wait states (a human task due in 3 working days) that must
cost no memory or threads while parked; and first-class compensation/rollback.

## Decision

We will build the engine core as **Option A: a pure stepper that returns
commands as data**.

- The core's sole execution entry point is a deterministic, side-effect-free
  function of shape `Step(definition, state, trigger) -> (state, []Command)`.
- **Trigger** and **Command** are each a **closed (sealed) set of plain data
  types** — no functions or I/O handles inside them. A `Trigger` is "the thing
  that drives the next step": this includes both initiating causes (instance
  started, signal/message received) and *results coming back* (action
  completed/failed, timer fired, human completed, sub-instance completed). An
  `Command` describes "something to do" (invoke action, schedule timer, emit
  event, await human, start sub-instance, compensate, complete/fail instance).
- Every command that yields a result carries a **`CommandID`**; the corresponding
  result trigger echoes that same `CommandID`, and that correlation is how the
  core re-attaches a result to the parked token awaiting it.
- The core contains **no goroutines, no clock reads, no DB/network/bus access**.
  A separate **runtime** package owns the loop that persists state, performs
  commands, and re-delivers results as triggers. We will ship a default runtime
  so simple embedded use stays ergonomic, but it is not part of the core.

## Consequences

- The transactional outbox becomes natural: the runtime persists `state` and
  writes `EmitEvent` commands in one DB transaction, so events cannot diverge
  from state.
- Resilience (retry/backoff/poison) and **deterministic replay** fall out: a
  trigger can be re-delivered after a crash because `Step` is deterministic and
  idempotent on `(state, trigger)`.
- Long waits (human/SLA/timer) cost nothing while parked — the instance lives
  entirely in the DB until a trigger wakes it; in-wait reminders and SLA breach
  are just additional `ScheduleTimer` commands.
- Compensation/rollback is a first-class `Compensate` command rather than ad-hoc
  unwinding code.
- Cost: consumers do not call actions inline; they run (or embed) the runtime
  loop. We absorb this by shipping a default loop.
- Cost: the trigger/command taxonomy must be designed up front and kept closed;
  new node families (e.g. sub-processes, boundary events) may require new command
  variants, which is an explicit, reviewable change rather than a silent one.
- This decision constrains every later sub-project (persistence, eventing,
  scheduling, transports): they attach to the core through triggers and commands,
  never by reaching into its internals.
```
