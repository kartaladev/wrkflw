# 9. gocron-backed Scheduler sharing the engine's clockwork time source

- Status: Accepted
- Date: 2026-06-21

## Context

The engine core emits `ScheduleTimer`/`CancelTimer` commands and consumes
`TimerFired` triggers; the runtime arms timers through the `runtime.Scheduler`
port (`Schedule(timerID, fireAt, fire)` / `Cancel(timerID)`). Until now the only
implementation is `MemScheduler`, an in-memory `Tick`-driven scheduler: tests
advance a fake clock and call `Tick` to fire due timers. That is perfect for
deterministic engine tests but is **not a production scheduler** — it fires only
when something calls `Tick`.

The locked tech stack (CLAUDE.md) mandates `go-co-op/gocron`, **hard-pinned to
v2.21.2**, as the production scheduler for timers, SLA waiters, and in-wait
actions. Two constraints shape how it must be introduced:

- **Vendor isolation** — like watermill, casbin, and clockwork, gocron must never
  be imported from engine/workflow code; it is reached only behind an in-repo
  abstraction (here, the `runtime.Scheduler` port) so it stays swappable.
- **Deterministic time** — ADR-0003 established that time flows through the
  in-repo `clock.Clock`, implemented by clockwork at the edge, and that "the
  scheduling sub-project constructs gocron with a `clockwork.Clock` and hands the
  same instance to our components as a `clock.Clock`, so a single
  `clockwork.NewFakeClock()` advances both engine logic and the real scheduler
  together under test." gocron v2 accepts exactly such a clock via
  `gocron.WithClock(clockwork.Clock)`.

ADR-0008 established the **façade-over-internal** layout (`persistence/` over
`internal/persistence/postgres/`) as the template for the gocron, watermill, and
casbin sub-projects, which all face the identical "concrete impl must be in
`internal/` yet every feature must be reachable from a root package" tension.

## Decision

Implement a **production `Scheduler` backed by gocron v2.21.2**, behind the
existing `runtime.Scheduler` port, sharing the engine's clockwork time source —
following the ADR-0008 façade/internal split:

- `internal/scheduling/gocron/` holds the concrete `*GocronScheduler` wrapping
  `gocron.Scheduler`. It owns all gocron and `google/uuid` imports. It keeps a
  `sync.Mutex`-guarded `map[string]uuid.UUID` (timerID → job ID) so `Schedule`
  can replace an existing timer, `Cancel` can remove one, and an `AfterJobRuns`
  event listener prunes the map entry once a one-time job fires. Construction
  `Start()`s gocron's executor; `Close()` calls `Shutdown()` (graceful, no
  restart) so the consumer can release the goroutine.
- `scheduling/` (module root) is the consumer-facing façade: `NewScheduler(clk
  clockwork.Clock) (*Scheduler, error)` returns a `*Scheduler` that satisfies
  `runtime.Scheduler` **and** `io.Closer`, delegating to the internal impl and
  re-exporting nothing internal-concrete. No functional-options variadic is added
  until a real knob exists.
- **One shared clock.** The consumer (or example/test) constructs a single
  `clockwork.Clock` — `NewRealClock()` in production, `NewFakeClock()` in tests —
  and passes that same instance both to `runtime.NewRunner` (as `clock.Clock`,
  which clockwork satisfies structurally) and to `scheduling.NewScheduler` (as
  `clockwork.Clock`, via `gocron.WithClock`). Advancing the fake clock drives
  engine timestamps and timer firing in lockstep. The scheduling adapter and
  consumer/example wiring are the only places (besides `_test.go`) allowed to
  import clockwork; `engine`/`model`/`runtime` keep depending on `clock.Clock`
  alone.
- `MemScheduler` is **retained** as the in-memory/`Tick` reference impl; it is not
  deleted (existing timer + SLA e2e tests depend on it). The two are
  interchangeable behind the port.
- **Timer rehydration on restart is deferred.** It needs a persistence enumeration
  query (`ListPendingTimers`) that does not exist yet; v1 ships only the firing
  mechanism and lifecycle, and does not add a speculative rehydration API.

## Consequences

**Easier:** the production scheduler is a true drop-in for `MemScheduler` — the
runtime, engine, and model are untouched; only new packages are added. Because the
gocron scheduler shares the fake clock, timer behaviour stays deterministically
testable end-to-end (one `Advance` fires both the engine's logical time and the
real scheduler), with no real waiting and no flakiness — provided the mandatory
`BlockUntilContext` arm barrier is used before advancing. The façade keeps the
public surface small and stable (a `Scheduler` type + `NewScheduler`), and the
gocron vendor stays swappable behind the port, honouring the vendor-isolation rule
and reusing the ADR-0008 template verbatim.

**Harder / trade-offs:** the `AfterJobRuns` listener runs on gocron's executor
goroutine, so the timerID→job-ID map needs mutex discipline (concurrency the
`MemScheduler` did not have). gocron must be `Shutdown()` to avoid a leaked
executor goroutine, pushing a lifecycle obligation onto the consumer (surfaced as
`io.Closer`). The arm-vs-advance race makes the `BlockUntilContext(ctx, n)` barrier
mandatory in every timer test — a subtlety that must be documented and enforced in
review. Finally, until the deferred rehydration lands, a process restart loses
in-memory timer arming for in-flight instances; this is the known v1 operational
caveat and a tracked follow-up gated on a new persistence query.
