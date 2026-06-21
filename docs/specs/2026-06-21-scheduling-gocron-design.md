# Scheduling (gocron) — design spec

- Status: Accepted
- Date: 2026-06-21
- Related: ADR-0003 (clockwork as time source), ADR-0008 (consumer façade over
  internal impl), ADR-0009 (gocron-backed `Scheduler`)
- Plan: `docs/plans/2026-06-21-scheduling-gocron.md`

## 1. Goal & scope

Ship a **production `Scheduler`** for the wrkflw engine, backed by
[`go-co-op/gocron` v2.21.2](https://github.com/go-co-op/gocron) (the locked,
hard-pinned scheduler in CLAUDE.md). It implements the *existing*
`runtime.Scheduler` port — the engine core and runtime do not change. The new
scheduler shares the engine's time source (clockwork, per ADR-0003) so that a
**single fake-clock advance deterministically drives both engine timestamps and
timer firing** under test.

In scope (v1):

- A concrete `*GocronScheduler` wrapping `gocron.Scheduler`, living in
  `internal/scheduling/gocron/` (consumers never import it).
- A consumer-facing root façade `scheduling.NewScheduler(...)` returning a type
  that satisfies `runtime.Scheduler` + `io.Closer` (for graceful shutdown), per
  the ADR-0008 façade-over-internal template.
- The lifecycle (`Start` on construct, graceful `Shutdown` on `Close`) wired so a
  consumer can attach it to their own lifecycle without leaking a goroutine.
- A capstone e2e proving the gocron scheduler drives a real `runtime.Runner`
  (timer-intermediate process) identically to `MemScheduler`.

Explicitly **out of scope** (see §8): timer rehydration on process restart.

Non-goals carried over from the engine-core contract:

- The engine core stays pure of the time vendor and the scheduler vendor. gocron
  and clockwork are **never** imported from `engine`/`model`/`runtime`/workflow
  code — only from `scheduling/` and `internal/scheduling/` (and `_test.go`
  files). This is the same rule ADR-0003 already enforces for clockwork.
- `MemScheduler` is **kept**, not deleted. It remains the in-memory / `Tick`
  reference impl that existing timer + SLA e2e tests rely on
  (`runtime/timer_example_test.go`). The two impls are interchangeable behind the
  port.

## 2. The port being implemented (unchanged)

`runtime/scheduler.go` already defines:

```go
type Scheduler interface {
    // Schedule registers a timer with the given timerID that calls fire at or
    // after fireAt. If a timer with the same timerID already exists it is replaced.
    Schedule(timerID string, fireAt time.Time, fire func())

    // Cancel removes a pending timer. No-op if the timer does not exist or has
    // already fired.
    Cancel(timerID string)
}
```

The runtime arms timers through this port only: `runtime/runner.go` `perform`
handles `engine.ScheduleTimer` by calling
`r.sched.Schedule(timerID, fireAt, fireCallback)` (the callback `Deliver`s a
`engine.NewTimerFired(...)`), and `engine.CancelTimer` by calling
`r.sched.Cancel(timerID)`. The gocron scheduler is a drop-in replacement: nothing
in `runtime` changes.

## 3. Layout (ADR-0008 template)

Mirrors the persistence façade split exactly:

- `internal/scheduling/gocron/` — the concrete `*GocronScheduler` wrapping
  `gocron.Scheduler`. All gocron and `google/uuid` imports live here. Consumers
  must not import it; it may change without a public-semver impact.
- `scheduling/` (module root) — the consumer-facing product surface: a thin
  `Scheduler` type and a `NewScheduler` constructor that delegate to the internal
  impl, plus the compile-time assertion that the façade satisfies
  `runtime.Scheduler`. It re-exports **nothing internal-concrete**; the façade
  type is the stable public type.

## 4. Clock sharing — the core mechanism

This is the heart of ADR-0009 and the reason the engine and scheduler stay in
lockstep under test.

- gocron v2 accepts a clock via `gocron.WithClock(clock clockwork.Clock)`. It
  takes the **full** `clockwork.Clock` (it needs `After`, `NewTimer`,
  `BlockUntilContext`, etc. — not just `Now()`).
- The engine's `clock.Clock` is minimal (`Now() time.Time`). A `clockwork.Clock`
  satisfies `clock.Clock` **structurally** (ADR-0003), so one clockwork instance
  can be both.
- The **consumer** (or an example, or a test) constructs **one** `clockwork.Clock`
  — `clockwork.NewRealClock()` in production, `clockwork.NewFakeClock()` in tests
  — and passes that *same instance* to:
  - the runtime, as a `clock.Clock` (`runtime.NewRunner(cat, clk, store, ...)`),
  - and `scheduling.NewScheduler(clk, ...)`, as a `clockwork.Clock`.

  Same instance ⇒ shared logical time. Advancing the fake clock advances both
  `Trigger.OccurredAt` (engine side) and gocron's internal timers (scheduler side)
  together — removing the entire class of "logical time and scheduled time
  disagree" bugs.

This is the *edge*: the scheduling adapter and consumer/example wiring may import
clockwork; `engine`/`model`/`runtime` still depend only on `clock.Clock`. This is
fully consistent with ADR-0003 ("the scheduling sub-project constructs gocron with
a `clockwork.Clock` and hands the same instance to our components as a
`clock.Clock`").

## 5. `GocronScheduler` design (gocron v2.21.2 API mapping)

State: a `*gocron.Scheduler`, a `sync.Mutex`, and a
`map[string]uuid.UUID` (timerID → gocron job ID).

**Construct** — `NewGocronScheduler(clk clockwork.Clock) (*GocronScheduler, error)`:

```
s, err := gocron.NewScheduler(gocron.WithClock(clk))
// on err: return nil, err
s.Start() // non-blocking; starts gocron's executor goroutine
```

**`Schedule(timerID, fireAt, fire)`** — under the mutex:

1. If `timerID` is already in the map, remove the existing job
   (`_ = s.RemoveJob(existingID)`, ignoring `gocron.ErrJobNotFound`) and delete
   the map entry. This gives the port's "replace existing" semantics.
2. Create a one-time job firing at `fireAt`:
   ```
   job, err := s.NewJob(
       gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(fireAt)),
       gocron.NewTask(fire), // gocron.NewTask accepts a bare func()
       gocron.WithEventListeners(gocron.AfterJobRuns(func(uuid.UUID, string) {
           // delete map[timerID] under the mutex — runs on gocron's executor goroutine
       })),
   )
   ```
3. Store `job.ID()` in the map under `timerID`.

`Schedule` returns no error (the port signature has none); a `NewJob` error is
logged via `slog` and the timer is simply not armed (documented as best-effort —
the engine's outer resilience layer owns retry).

**`Cancel(timerID)`** — under the mutex: look up `timerID`; if absent, no-op
(cancel of an unknown id is safe). If present, delete the map entry and call
`s.RemoveJob(id)`; treat `errors.Is(err, gocron.ErrJobNotFound)` as a safe no-op
(the timer may already have fired and been pruned).

**`Close()` / `Shutdown()`** — `s.Shutdown()` (graceful; gocron cannot be
restarted afterward). The façade exposes this as `io.Closer` so the consumer wires
it into their lifecycle. **Not calling Shutdown leaks gocron's executor
goroutine** — the e2e + unit tests use `t.Cleanup(func() { _ = sched.Close() })`.

**Map cleanup** happens via the `AfterJobRuns` listener after a one-time job runs
(so a fired timer's entry doesn't linger). That listener executes on gocron's
executor goroutine, so it takes the same mutex — hence the mutex guards the map
against concurrent `Schedule`/`Cancel`/cleanup.

## 6. Root façade (`scheduling/`)

```go
// Scheduler is the consumer-facing gocron-backed scheduler. It satisfies
// runtime.Scheduler and io.Closer.
type Scheduler struct { impl *gocron.GocronScheduler } // internal/scheduling/gocron

func NewScheduler(clk clockwork.Clock) (*Scheduler, error)

func (s *Scheduler) Schedule(timerID string, fireAt time.Time, fire func())
func (s *Scheduler) Cancel(timerID string)
func (s *Scheduler) Close() error

var _ runtime.Scheduler = (*Scheduler)(nil)
var _ io.Closer        = (*Scheduler)(nil)
```

No `...Option` variadic is added in v1: there is no real configurable knob yet,
and an unusable unexported-only option list was a mistake learned from the
persistence façade. If/when a real knob appears (e.g. a custom `slog.Logger`, a
gocron limit-mode), add it then, as an exported functional option.

## 7. The mandatory deterministic test pattern

Every timer test uses the **shared fake clock + barrier + WaitGroup** pattern. The
barrier is non-negotiable: without `BlockUntilContext` the test races the
clock-`Advance` against gocron arming its internal `AfterFunc`, producing a flaky
"sometimes fires, sometimes doesn't" test.

```go
fakeClock := clockwork.NewFakeClock()
sched, _ := NewScheduler(fakeClock) // or gocron.NewGocronScheduler(fakeClock)
t.Cleanup(func() { _ = sched.Close() })

var wg sync.WaitGroup
wg.Add(1)
sched.Schedule("t1", fakeClock.Now().Add(5*time.Second), func() { wg.Done() })

// MANDATORY barrier: wait until gocron has armed its timer (1 waiter) before
// advancing — otherwise Advance can outrun the arm and the timer never fires.
require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
fakeClock.Advance(5 * time.Second) // fire
wg.Wait()                          // executor goroutine actually ran the task
```

`BlockUntilContext(ctx, n)` blocks until `n` timers are armed against the fake
clock (`n` = number of expected armed timers). `Advance` returning is **not** the
same as the task having run — the task runs on gocron's executor goroutine, so the
`WaitGroup` (or a similar synchronization) proves the callback executed.

Test cases v1 must cover:

- **fires-at-time** — schedule, barrier, advance, `wg.Wait()` proves it fired.
- **cancel-prevents-fire** — schedule, barrier(1), `Cancel`, `Advance`; assert the
  fire did **not** run (e.g. an atomic counter stays 0; assert after a *second*
  control timer fires, or with a short `require.Never`).
- **replace-reschedules** — schedule `t1` at T+5, re-`Schedule` `t1` at T+10;
  advance to T+5 → assert NOT fired; advance to T+10 → assert fired **once**.
- **cancel-unknown-is-noop** — `Cancel("nope")` does not panic / error.
- **fire-callback-runs-once** — a fired one-time timer runs its callback exactly
  once (counter == 1).

## 8. Durability / rehydration — explicitly deferred

Timers persist in the instance snapshot (`InstanceState.Timers`) via the
persistence sub-project. **Full auto-rehydration on restart** — re-arming the
gocron jobs for every in-flight instance when the process boots — requires a
persistence *enumeration* query (e.g. `StateStore.ListPendingTimers()` /
`Store.ListPendingTimers(ctx)` returning the pending `(instanceID, timerID,
fireAt)` triples across all running instances). **That query does not exist
today.**

v1 therefore ships the *firing mechanism* + *lifecycle* only. Rehydration-on-
startup is a documented follow-up that **depends on a new persistence query**; we
deliberately do **not** build a speculative rehydration API in v1 (consistent with
the engine-core discipline of not shipping reserved-but-inert surfaces beyond what
is honestly wired). Until then, a restart loses in-memory timer arming — the
documented operational caveat for v1; instances parked on a timer resume only once
their timer is re-armed by a future rehydration mechanism.

## 9. Dependencies & version pins

- `github.com/go-co-op/gocron/v2@v2.21.2` — **hard pin** (CLAUDE.md locked stack).
  Add with `go get github.com/go-co-op/gocron/v2@v2.21.2`.
- It pins `github.com/jonboulle/clockwork v0.5.0` — already the repo's version, and
  the one that has `BlockUntilContext` (required by the test pattern).
- `github.com/google/uuid` arrives transitively (gocron job IDs are
  `uuid.UUID`); used only inside `internal/scheduling/gocron`.

## 10. Verification gate

Per CLAUDE.md: `go test -race ./...` green; ≥85% line coverage on the touched
packages (`internal/scheduling/gocron`, `scheduling`); `golangci-lint run ./...`
clean; and an import-boundary check that gocron is imported **only** under
`scheduling/` + `internal/scheduling/` (never `engine`/`model`/`runtime`/workflow),
mirroring the persistence sub-project's forbidden-import check.
