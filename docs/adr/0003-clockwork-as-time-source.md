# 3. Time flows through an in-repo Clock interface, implemented by clockwork

- Status: Accepted
- Date: 2026-06-20

## Context

The engine is pervasively time-dependent: timer events, SLA deadlines (e.g. a
human task due in 3 working days), and in-wait actions (reminders sent during a
wait) all reason about "now" and "fire at". ADR-0002 keeps the engine core pure,
so the core itself must not read the wall clock. The scheduling sub-project will
use gocron (pinned to v2.21.2 per the locked tech stack), and gocron v2 accepts
a `github.com/jonboulle/clockwork` `Clock`. Tests cannot wait real durations
(three days, an SLA window), so time must be fast-forwardable deterministically,
and the engine's logical time must advance in lockstep with the scheduler's.

CLAUDE.md already mandates that vendor libraries (watermill, casbin, gocron) are
reached only through in-repo abstractions so they stay swappable. The same rule
applies to the time vendor: the module must not import `clockwork` from engine
logic.

## Decision

We will define an **in-repo `Clock` interface** as the engine's sole time
abstraction, and use **`github.com/jonboulle/clockwork`** as its implementation
at the wiring/scheduler edge.

```go
// clock/clock.go
type Clock interface { Now() time.Time }

func System() Clock // standard-library-backed real clock (no clockwork import)
```

- Stateful components that need "now" depend on `clock.Clock`, never on
  `time.Now()` directly and never on `clockwork` directly.
- The engine core (ADR-0002) does not use `Clock` at all: it reads no clock and
  emits `ScheduleTimer` commands with a `FireAt`/duration. **Time enters the core
  only as `Trigger.OccurredAt`**, which the runtime produces by calling
  `Clock.Now()` at the boundary.
- `clockwork.Clock` satisfies `clock.Clock` structurally (it has
  `Now() time.Time`), so `clockwork` is imported only by tests and by the
  scheduling sub-project's wiring — not by `engine`/`model`/`runtime` logic.
- The scheduling sub-project constructs gocron with a `clockwork.Clock` and hands
  the **same instance** to our components as a `clock.Clock`, so a single
  `clockwork.NewFakeClock()` advances both engine logic and the real scheduler
  together under test.
- `clockwork` remains a locked dependency alongside the gocron v2.21.2 hard pin.

## Consequences

- The time vendor is swappable: replacing `clockwork` touches only the wiring
  edge and tests, never engine logic, mirroring the watermill/casbin/gocron rule.
- Time-based behaviour (timers, SLA breach, in-wait reminders) is
  deterministically testable: pass a `clockwork` fake (it satisfies `clock.Clock`)
  to both our components and gocron, advance it, and assert — no real waiting, no
  flakiness.
- One shared clock instance between engine and scheduler removes the class of
  bugs where logical time and scheduled time disagree.
- Stateful constructors that touch time gain a `clock.Clock` parameter (with
  `clock.System()` as the real default), a small, consistent ergonomic cost.
- An import-boundary test enforces that `engine`/`model` never import `clockwork`
  and never call `time.Now()` in logic (the `clock` package's real impl is the
  single allowed adapter).
```
