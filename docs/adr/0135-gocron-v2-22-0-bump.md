# 135. Bump the gocron hard pin v2.21.2 → v2.22.0

- Status: Accepted
- Date: 2026-07-22

## Context

The tech stack locks `go-co-op/gocron` to a hard-pinned version (v2.21.2,
ADR-0009); changing it requires an ADR. The scheduler-owned durable jobs work
(ADR-0134) rebuilds the gocron engine layer (`scheduler/internal/gocron`), which is
the right moment to take a pin bump if one is warranted.

gocron v2.22.0 (released ~2026-07-09) was researched against wrkflw's exact usage
profile (one-shot `OneTimeJob` timers with `WithLimitedRuns`, recurring
cron/interval jobs, singleton mode, Monitor/EventListener hooks, distributed
elector):

- **OneTimeJob CPU-spin fix (#943)** — wrkflw's dominant job shape is the one-shot
  timer; the spin affects idle schedulers holding past-due one-shots.
- **Shared-mutable-state-across-jobs isolation fix** — removes a class of
  cross-job interference in the scheduler internals.
- **"Skipped runs no longer consume `WithLimitedRuns` budget"** — directly
  load-bearing for ADR-0134's overrun protection: a singleton-mode
  skip/reschedule can no longer burn a one-shot timer's single allowed run.
- **`ErrSchedulerBusy` + typed `LimitMode`** — better error surface and the typed
  constant ADR-0134's `WithSingletonMode(LimitModeReschedule)` default uses.
- **Monitor/EventListener surface unchanged** — the ADR-0134 observability plan
  (`MonitorStatus`, `AfterJobRunsWithError/WithPanic`, `AfterLockError`) ports
  without modification.
- **Async executor behaviour unchanged** vs 2.21.2 (verified) — the
  fire-before-commit analysis in ADR-0134 is version-independent; the bump neither
  causes nor fixes it.

> **Verified at bump time (Task 2):** re-checked against the fetched
> `$(go env GOMODCACHE)/github.com/go-co-op/gocron/v2@v2.22.0/` source, diffed
> against the prior `v2.21.2` copy in the same module cache.
>
> - **OneTimeJob CPU-spin fix (#943)** — **CONFIRMED.** `v2.21.2`
>   `scheduler.go` had three inlined `for next.Before(s.now()) { next =
>   j.next(next) }` loops with no guard against a non-advancing `next` (a
>   custom/degenerate `Cron.Next` returning the zero time or a
>   non-strictly-increasing value spins the scheduler goroutine forever).
>   `v2.22.0` factors this into `scheduler.advancePastNow` (`scheduler.go:405`),
>   which explicitly returns `ok=false` when `n.IsZero() || !n.After(next)` so
>   the caller removes the job instead of spinning.
> - **Shared-mutable-state-across-jobs isolation fix** — **CONFIRMED.**
>   `v2.22.0` `job.go:189` documents that "the default [Cron] implementation is
>   cloned per job automatically to avoid aliasing when the same JobDefinition
>   is reused across NewJob calls" — no `clone`/`Clone` reference exists
>   anywhere in `v2.21.2/job.go`.
> - **"Skipped runs no longer consume `WithLimitedRuns` budget"** —
>   **CONFIRMED.** `v2.22.0` adds a `jobOutCompleted{id, skipped bool}` struct
>   (replacing the bare `uuid.UUID` the executor sent in `v2.21.2`) and
>   `scheduler.selectExecJobsOutCompleted` (`scheduler.go:556`) now returns
>   early on `completed.skipped` before touching `limitRunsTo.runCount`.
>   `v2.22.0` ships a dedicated regression test,
>   `TestScheduler_WithLimitedRuns_SkippedRunsDoNotConsumeBudget`
>   (`scheduler_test.go:3393`), absent from `v2.21.2`.
> - **`ErrSchedulerBusy`** — **CONFIRMED new.** Defined at `v2.22.0`
>   `errors.go:46`, returned by `requestJob` (`util.go:54`) on a scheduler-busy
>   timeout; no match anywhere in `v2.21.2`.
> - **Typed `LimitMode`** — **CONFIRMED but NOT new to this bump.**
>   `type LimitMode int` (with the `LimitModeReschedule`/`LimitModeWait`
>   constants) already existed, already typed, in `v2.21.2` (`scheduler.go:1088`)
>   — it merely moved line position in `v2.22.0` (`scheduler.go:1200`). The
>   ADR's characterization of this as a v2.22.0-introduced improvement is
>   corrected here: it is pre-existing behaviour, not a new fix. Does not
>   affect the Decision (ADR-0134's `WithSingletonMode(LimitModeReschedule)`
>   default was never contingent on this being new).
> - **`MonitorStatus`/EventListener signatures unchanged** — **CONFIRMED.**
>   `scheduler_monitor.go` is byte-identical between `v2.21.2` and `v2.22.0`
>   (`diff` empty). `BeforeJobRuns`, `BeforeJobRunsSkipIfBeforeFuncErrors`,
>   `AfterJobRuns`, `AfterJobRunsWithError`, `AfterJobRunsWithPanic`,
>   `AfterLockError` in `job.go` are identical signatures at both versions
>   (only line numbers shifted).
> - **Async executor dispatch shape unchanged** — **CONFIRMED.** The
>   `jobsOutCompleted` channel + `select`-based executor loop that drives async
>   job dispatch is structurally the same in both versions; the only change is
>   the payload type carried on that channel (`uuid.UUID` → `jobOutCompleted`)
>   to support the skip-budget fix above, plus an unrelated executor-shutdown
>   busy-wait fix (`time.Now().Before(timeout)` polling replaced by a blocking
>   `e.clock.NewTimer` select in the stop-timeout wait). The fire-before-commit
>   analysis in ADR-0134 remains version-independent.
>
> All load-bearing claims (skip-budget fix in particular) are CONFIRMED. The
> singleton-overrun default (Task 6) may proceed.

## Decision

Bump the hard pin in `go.mod` from `github.com/go-co-op/gocron/v2 v2.21.2` to
**`v2.22.0`**, and update the Tech Stack table in `CLAUDE.md` accordingly. The bump
lands as an early phase of the ADR-0134 implementation (before the gocron engine
rewrite), accompanied by regression verification of the behaviours wrkflw relies
on: one-shot `WithLimitedRuns` semantics, singleton-mode overrun handling, and
Monitor/EventListener hook signatures.

## Consequences

**Easier:**

- The overrun-protection default in ADR-0134 is safe for one-shot timers only
  because of the skip-budget fix — without the bump, singleton mode could consume
  a timer's single run on a skip.
- Idle-scheduler CPU behaviour improves for the dominant one-shot workload.
- Typed `LimitMode` removes a stringly/int constant from the singleton wiring.

**Harder / trade-offs:**

- Any pin bump can carry unnoticed behaviour changes; mitigated by the targeted
  regression checks above plus the full existing scheduler test suite running
  against the new pin before the engine rewrite starts.
- The hard-pin policy means future gocron fixes still require an ADR each; this
  ADR does not soften the policy.

**Cross-references:** ADR-0009 (gocron adoption/pin), ADR-0134 (scheduler-owned
durable jobs — the consumer of every fix listed above).
