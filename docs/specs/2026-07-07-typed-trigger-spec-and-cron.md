# Spec: scheduler subsystem — typed `schedule.TriggerSpec` → gocron native triggers, JobStore-owned lifecycle & rehydration

- **Date:** 2026-07-07 (revised after scheduler-trigger + reference-design analysis)
- **Status:** Approved (design)
- **ADR:** 0102 (scheduler subsystem: typed trigger spec, gocron-native
  scheduling, JobStore-owned durable lifecycle). **Supersedes the timer-write
  portion of ADR-0027** (transactional timer arms) — the guarantee is preserved
  via ambient-tx `JobStore` writes; the mechanism moves from engine-emitted
  `AppliedStep.TimerArms` to scheduler-owned `JobStore.Save/Update/Delete`.
  Lands FIRST, before `2026-07-07-boundary-event-enhancements.md` (ADR-0103/0104).
- **Library:** `github.com/go-co-op/gocron/v2 v2.21.2` (pinned). **No cron parser
  of our own** (no `robfig/cron` usage) — gocron owns all scheduling math.
- **Scope note:** this is now a scheduler-subsystem redesign (trigger type +
  scheduler port + JobStore + persistence + consistency), larger than the
  original "typed duration + cron". It reshapes the prior Plan A and replaces the
  prior Plan B; the implementation plan is re-derived from this revised spec.

## Context

Timer/deadline/reminder "durations" are today expr-lang strings the engine
evaluates to a `time.Duration`, then computes an absolute `FireAt = at.Add(dur)`
and schedules a gocron **`OneTimeJob`**. Recurrence (reminders) is faked by the
**engine rescheduling** on each fire. Three problems: no type safety for the
static case; the engine re-implements scheduling (and would have to re-implement
cron/calendar math); recurrence lives in the wrong layer.

gocron v2 natively supports seven trigger families and returns the next fire
instant for any scheduled job (`Job.NextRun()`). We will translate a typed,
neutral trigger spec **directly** onto those families and let gocron own all
timing — including `NextRun()` as the authoritative fire time.

## Goals

1. **Type-safe** static durations (`time.Duration`), **preserve** dynamic
   expr-lang durations, and expose gocron's **full trigger parity**.
2. **gocron owns scheduling & recurrence.** The engine emits a neutral trigger
   descriptor; the runtime schedules it and reads back `NextRun()`. No fire-time
   or cron math on our side. gocron stays **behind the scheduler port** (never
   imported from engine/definition code).
3. **Deterministic tests** via the shared `clockwork` fake clock + gocron
   `NextRun()`-driven clock advancement (no hand-rolled next-fire computation).

`RetryPolicy` already uses typed `time.Duration` — out of scope.

## `schedule.TriggerSpec` (full gocron parity)

New leaf package `definition/schedule`. `TriggerSpec` is a neutral, serializable
value with a `Kind` discriminator; it is used both to author definitions **and**
as the value the scheduler port consumes (the engine resolves the two `*Expr`
forms into concrete durations before the port; see Engine).

Constructors (one per gocron family + two dynamic-expr variants):

| Constructor | gocron `JobDefinition` | Recurs? |
|---|---|---|
| `AfterDuration(d time.Duration)` | `OneTimeJob(now+d)` | no |
| `At(t time.Time)` | `OneTimeJob(t)` (absolute; used on rehydrate) | no |
| `AfterExpr(code string)` | resolved → `AfterDuration` (dynamic one-shot) | no |
| `Every(d time.Duration)` | `DurationJob(d)` | yes |
| `EveryExpr(code string)` | resolved → `Every` (dynamic interval) | yes |
| `EveryRandom(min, max time.Duration)` | `DurationRandomJob(min,max)` | yes |
| `Cron(expr string)` | `CronJob(expr, false)` | yes |
| `Daily(interval uint, at ...ClockTime)` | `DailyJob(interval, atTimes)` | yes |
| `Weekly(interval uint, days []time.Weekday, at ...ClockTime)` | `WeeklyJob(...)` | yes |
| `Monthly(interval uint, days []int, at ...ClockTime)` | `MonthlyJob(...)` | yes |

Supporting value type: `schedule.ClockTime{Hour, Minute, Second uint}` (neutral;
maps to `gocron.NewAtTime`). Accessors: `Kind() Kind`, `IsZero() bool`,
`Recurring() bool`, plus typed getters per form for the adapter.

```go
package schedule

type Kind uint8
const (
	KindUnset Kind = iota
	KindOneTime      // AfterDuration/At
	KindDuration     // Every
	KindDurationRand // EveryRandom
	KindCron
	KindDaily
	KindWeekly
	KindMonthly
	// KindExpr / KindEveryExpr exist only pre-resolution (see Engine); after the
	// engine resolves them they become KindOneTime / KindDuration.
	KindExpr
	KindEveryExpr
)

type TriggerSpec struct { /* kind + typed fields; unexported */ }

func AfterDuration(d time.Duration) TriggerSpec
func At(t time.Time) TriggerSpec
func AfterExpr(code string) TriggerSpec
func Every(d time.Duration) TriggerSpec
func EveryExpr(code string) TriggerSpec
func EveryRandom(min, max time.Duration) TriggerSpec
func Cron(expr string) TriggerSpec
func Daily(interval uint, at ...ClockTime) TriggerSpec
func Weekly(interval uint, days []time.Weekday, at ...ClockTime) TriggerSpec
func Monthly(interval uint, days []int, at ...ClockTime) TriggerSpec

func (s TriggerSpec) Kind() Kind
func (s TriggerSpec) IsZero() bool
func (s TriggerSpec) Recurring() bool // false for OneTime/Expr; true otherwise
```

Usage:

```go
event.WithBoundaryTimer(schedule.AfterDuration(1 * time.Hour))
event.WithBoundaryTimer(schedule.Cron(`0 9 * * *`))
activity.WithReminder(schedule.Every(30*time.Minute), "nudge")
activity.WithReminder(schedule.Daily(1, schedule.ClockTime{Hour: 9}), "nudge")
activity.WithDeadline(schedule.AfterExpr(`slaHours * 3600`), "overdue", "notify")
```

## Options + model (as in the prior draft, now carrying full `TriggerSpec`)

Options change their duration/every parameter from `string` to
`schedule.TriggerSpec`: `event.WithBoundaryTimer`, `WithStartTimer`,
`WithCatchTimer`, `WithCatchDeadline`, `WithCatchReminder`;
`activity.WithDeadline`, `activity.WithReminder`. Model fields
(`StartEvent/IntermediateCatchEvent/BoundaryEvent.Timer`,
`WaitFields.DeadlineTimer`, `WaitFields.ReminderEvery`) become `TriggerSpec`.
`DeadlineOf`/`ReminderOf` return `TriggerSpec`.

(The `WithDeadline`→`WithDeadlineFlow`/`WithDeadlineAction` split and
`WithReminder`→`WithWaitReminder` rename are in the boundary spec; this spec sets
the parameter TYPE.)

## Wire / serialization

A `TriggerSpec` serializes as a nested, discriminated object per timer slot,
plus backward-compat for the old flat string form:

```go
// model.TriggerWire — one per timer slot (timer / deadline / reminder)
type TriggerWire struct {
	Kind        string       `json:"kind"`
	Nanos       int64        `json:"nanos,omitempty"`       // OneTime(after)/Duration
	At          *time.Time   `json:"at,omitempty"`          // OneTime(absolute)
	Expr        string       `json:"expr,omitempty"`        // Expr/EveryExpr
	Cron        string       `json:"cron,omitempty"`
	MinNanos    int64        `json:"minNanos,omitempty"`    // EveryRandom
	MaxNanos    int64        `json:"maxNanos,omitempty"`
	Interval    uint         `json:"interval,omitempty"`    // Daily/Weekly/Monthly
	AtTimes     []ClockTime  `json:"atTimes,omitempty"`
	Weekdays    []int        `json:"weekdays,omitempty"`
	DaysOfMonth []int        `json:"daysOfMonth,omitempty"`
}
// NodeWire gains: TimerTrigger, DeadlineTrigger, ReminderTrigger *TriggerWire.
```

**Backward compatibility:** the pre-existing flat string fields
(`timerDuration`, `deadlineDuration`, `reminderEvery`) are still decoded — when
the nested `*Trigger` is absent but the flat string is present, it loads as
`AfterExpr` (deadlines/timers) or `EveryExpr` (reminders), exactly today's
semantics. New definitions serialize the nested object.

## Engine

- `ResolveTrigger(eval ConditionEvaluator, spec schedule.TriggerSpec, env map[string]any) (schedule.TriggerSpec, error)` — resolves the dynamic forms and passes everything else through: `AfterExpr` → `AfterDuration(EvalDuration(code))`; `EveryExpr` → `Every(EvalDuration(code))`; all other kinds returned unchanged. This is the ONLY duration math left in the engine (reuses `EvalDuration` verbatim).
- The arm sites (`armBoundaries`, deadline/reminder in `step_nodes.go`, catch/start/event-gateway/event-subprocess) call `ResolveTrigger`, then emit a `ScheduleTimer` carrying the resolved `TriggerSpec` (NOT a computed `FireAt`).
- **Reminders no longer reschedule.** `handleReminderFired`'s re-`ScheduleTimer` logic is removed — a recurring `TriggerSpec` recurs natively on the scheduler. The reminder fire path only runs the reminder action.
- `ScheduleTimer` command: replace `FireAt time.Time` with `Trigger schedule.TriggerSpec`. `TimerFired`/`CancelTimer` unchanged.

## Scheduler subsystem: scheduler owns the job lifecycle via a workflow `JobStore`

The scheduler is the single authority for scheduled jobs. It calls a
**workflow-provided `JobStore`** (persistence + job-spec builder) and projects
onto gocron. Adapted from the reference design, improved for wrkflw's layering,
uniform handler, and transactional guarantees.

### Neutral job spec (persisted, gocron-free)

```go
// kernel.JobSpec — everything needed to persist a timer AND rebuild its
// executable handler on load. No Go closures, no gocron types.
type JobSpec struct {
	TimerID    string
	InstanceID string
	DefID      string
	DefVersion int
	Trigger    schedule.TriggerSpec
	NextRun    time.Time // authoritative, from the backend (gocron NextRun)
}
```

### `JobStore` port (implemented by workflow; CALLED by the scheduler)

```go
// kernel.JobStore is provided by the workflow runtime. It persists job specs
// AND rebuilds a fully-executable job (handler) from a persisted spec on load —
// this is where handler rebinding is solved (the uniform "deliver TimerFired"
// closure is reconstructed from the def registry). The SCHEDULER calls it.
type JobStore interface {
	// LoadScheduled returns every persisted job as an executable ScheduledJob
	// (spec + a rebuilt fire func). Used by the scheduler to self-rehydrate on start.
	LoadScheduled(ctx context.Context) ([]ScheduledJob, error)
	// Save / Update / Delete persist lifecycle changes. Implementations MUST run
	// on the ambient transaction in ctx when present (atomic with the state commit).
	Save(ctx context.Context, spec JobSpec) error
	Update(ctx context.Context, timerID string, nextRun time.Time) error
	Delete(ctx context.Context, timerID string) error
}

// ScheduledJob is a JobSpec plus its reconstructed handler.
type ScheduledJob struct {
	Spec JobSpec
	Fire func()
}
```

The concrete `JobStore` lives on the runtime/persistence side and closes over
the definition registry + the `ProcessDriver`'s deliver path to rebuild `Fire`
(a `TimerFired` delivery for `Spec.InstanceID`). The generic gocron adapter never
sees workflow types — it receives `Fire` already built.

### Consistency (preserving ADR-0027 atomicity)

Every timer lifecycle change coincides with a **state commit**: arming while
applying a step; a one-shot `Delete` and a recurring `Update(nextRun)` when
`TimerFired` is applied. The scheduler's `JobStore.Save/Update/Delete` therefore
run **within the ambient transaction of that concurrent commit** (the tx is
threaded via `ctx`), so the timer row and instance state land atomically. gocron
in-memory registration is a **projection** reconciled by `LoadScheduled` at
startup (and by idempotent no-op firing of orphaned jobs). This is the outbox
philosophy applied to timers; it supersedes the "engine emits arms fused into
AppliedStep" mechanics of ADR-0027 while keeping its guarantee (recorded in
ADR-0102, superseding the timer-write portion of ADR-0027).

### Scheduler port + gocron adapter (`NextRun`-driven)

```go
type Scheduler interface {
	// Schedule registers timerID with trigger; fire runs on each occurrence.
	// Returns the authoritative next fire instant (from the backend).
	Schedule(ctx context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (nextRun time.Time, err error)
	// Cancel removes a pending timer (no-op if absent/fired).
	Cancel(ctx context.Context, timerID string)
	// NextRun reports the next fire instant of a pending timer.
	NextRun(timerID string) (time.Time, bool)
}
```

- **gocron adapter** (`internal/scheduling/gocron`): maps `TriggerSpec.Kind()` →
  `JobDefinition` **1:1** (`OneTimeJob`/`DurationJob`/`DurationRandomJob`/
  `CronJob`/`DailyJob`/`WeeklyJob`/`MonthlyJob`); one-shot kinds add
  `gocron.WithLimitedRuns(1)`. Returns `job.NextRun()`. Given a `JobStore`, it
  **self-rehydrates on start** (`LoadScheduled` → re-register each) and, on
  lifecycle events, calls `Save` (schedule), `Delete` (one-shot completion /
  cancel), `Update` (recurring `AfterJobRuns` → new `NextRun`). All within the
  ambient ctx tx. gocron stays behind the port.
- **`scheduling.Scheduler` façade** delegates + owns the gocron lifecycle.
- **`MemScheduler`** (`runtime/kernel`): supports `KindOneTime` + `KindDuration`
  only (trivial next-run, no parser); `ErrUnsupportedTrigger` for
  cron/calendar/random; also accepts an optional in-mem `JobStore` for the same
  self-rehydration flow in tests. `Tick` fires due timers; `KindDuration` re-arms
  at `last+d`.

### Runtime

- The runtime constructs the workflow `JobStore` (with the def registry + its
  deliver path) and hands it to the scheduler; **no explicit `RehydrateTimers`
  call remains** — the scheduler self-rehydrates on start.
- `perform(engine.ScheduleTimer)` calls `sched.Schedule(ctx, …)` with the
  step's ambient-tx `ctx`; the adapter persists via `JobStore.Save`.
- **One-shot vs recurring:** the adapter deletes a one-shot after it fires
  (`WithLimitedRuns(1)` + `Delete`); a recurring job survives firing and its
  `next_run` is refreshed via `Update`. The engine no longer reschedules
  reminders, and `timerOpsFor`'s fused arm/cancel emission is retired in favor of
  the scheduler-owned `JobStore` writes.

### Persistence (`wrkflw_timers`)

- Columns: keep `instance_id`, `timer_id` (PK), `def_id`, `def_version`; replace
  `fire_at` with `next_run`; add `trigger_kind SMALLINT` + `trigger_payload`
  (JSON `TriggerWire`). One migration per dialect (PG/MySQL/SQLite) via the
  existing goose + `dialect.UpsertTimer()` pattern; the `UpsertTimer` conflict
  clauses gain the new columns.
- The workflow `JobStore` implementation wraps `store.TimerStore` (read →
  `LoadScheduled`, rebuilding `Fire`) and `Store.upsertTimer`/`deleteTimer`
  (write → `Save`/`Delete`/`Update`), all ambient-tx aware. `MemTimerStore`
  gets an in-mem `JobStore` sibling for tests.

## Package structure (neutralize the public `scheduling` package)

Today the public `scheduling` package hard-depends on DB drivers — it holds
`*pgxpool.Pool`/`*sql.DB` fields and exposes `WithDistributedTimerLock(pool
*pgxpool.Pool)`, `WithTimerElector(pool *pgxpool.Pool)`,
`WithMySQLTimerElector(db *sql.DB)`, constructing Postgres/MySQL electors
internally. This violates the library-first "core depends on interfaces only"
rule that persistence already follows (ADR-0081/0082). Neutralize it:

- **`scheduling` (public) becomes backend-neutral** — no `pgx`/`database/sql`
  imports. Options take small neutral capability interfaces:
  `WithLocker(Locker)` and `WithElector(Elector)` (multi-replica timer
  exclusivity), plus the existing `WithClock`/`WithLogger`. The DB-driver
  options are removed (breaking, pre-v1.0).
- **Reuse the persistence advisory lock.** The neutral `scheduling.Locker` is
  satisfied by the existing persistence advisory-lock capability
  (`internal/persistence/dialect.Locker` / its PG/MySQL implementations) — no
  duplicate `PostgresLocker`/`MySQLLocker` in scheduling. A thin public
  constructor exposes the persistence-backed locker for wiring; the consumer
  passes it to `scheduling.WithLocker(...)`.
- **Backend elector adapters move to public subpackages** the consumer imports
  and wires: `scheduling/backend/postgres` (`NewElector(pool)`) and
  `scheduling/backend/mysql` (`NewElector(db)`), each returning the neutral
  `Elector`. gocron internals + the concrete electors stay in
  `internal/scheduling/gocron`, re-exported through those thin backend packages.
- **Out of scope here:** rethinking multi-replica exclusivity as a JobStore
  concern (deferred; the JobStore source-of-truth model in Plan 3 may later
  subsume the elector, but this spec keeps the neutral Locker/Elector seam).

This neutralization ships in Plan 2 alongside the scheduler-port redesign.

## Testing (strict TDD)

- `schedule`: every constructor sets the right `Kind`/fields; `Recurring()`;
  `IsZero`; ClockTime.
- Wire: nested round-trip for each kind; old flat string loads as `AfterExpr`/
  `EveryExpr`; negative cases.
- Engine `ResolveTrigger`: expr→duration one-shot/recurring; pass-through for
  native kinds; reminder path no longer emits a reschedule `ScheduleTimer`.
- gocron adapter: each `Kind` → correct `JobDefinition`; `NextRun()` returned;
  one-shot disarms (LimitedRuns 1); recurring fires ≥2× under the fake clock
  advanced to successive `NextRun()`s; cron/daily fire at expected instants.
- `MemScheduler`: OneTime + Every fire deterministically via `Tick`; recurring
  Every re-arms; cron/calendar return `ErrUnsupportedTrigger`.
- **`JobStore` + self-rehydration:** scheduler calls `Save` on schedule, `Delete`
  on one-shot completion/cancel, `Update` on recurring fire; `LoadScheduled`
  rebuilds executable jobs; a scheduler constructed with a populated `JobStore`
  re-registers and fires them with no explicit rehydrate call.
- **Consistency:** `JobStore.Save/Delete/Update` run on the ambient tx — a timer
  row and its state commit are atomic (assert both-or-neither under a forced
  rollback); an orphaned in-memory job fires as an idempotent engine no-op.
- Durable e2e (SQLite/testcontainers): a recurring cron job persists, a fresh
  scheduler `LoadScheduled`-rehydrates it, and it fires at the expected instants.
- All ~30 existing call sites migrated; examples run.

## Non-goals

- No `robfig/cron` or any of our own cron/calendar math — gocron owns it.
- No change to `RetryPolicy`.
- Boundary-event features remain in the boundary spec (ADR-0103/0104).

## Verification checklist

- [ ] `schedule.TriggerSpec` with all 10 constructors + `ClockTime` + accessors.
- [ ] Options + model fields carry `TriggerSpec`; `DeadlineOf`/`ReminderOf` typed.
- [ ] Nested wire + backward-compat flat-string load (as `*Expr`).
- [ ] Engine `ResolveTrigger` (expr resolved; natives pass through); reminder
      reschedule removed.
- [ ] Port `Schedule(ctx, id, TriggerSpec) (nextRun, err)` + `Cancel(ctx,id)` +
      `NextRun`; gocron 1:1 mapping returning `NextRun()`; `MemScheduler` simple-only.
- [ ] `JobStore` (workflow-provided, scheduler-called): `LoadScheduled` rebuilds
      executable jobs; scheduler self-rehydrates on start; no `RehydrateTimers` call.
- [ ] Consistency: `JobStore.Save/Update/Delete` run on the ambient state-commit
      tx (atomic timer+state); gocron is a reconciled projection.
- [ ] Persistence: `next_run` + `trigger_kind` + `trigger_payload`; 3-dialect
      migration; `JobSpec` descriptor; scan/upsert; SQLite durable rehydrate test.
- [ ] All call sites migrated; examples run; godoc `Example`s.
- [ ] ADR-0102 written (Nygard); note it EXTENDS the expr-lang-for-durations
      decision and adds gocron-native triggers.
- [ ] `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean; ≥85%.
- [ ] Public `scheduling` package has NO `pgx`/`database/sql` imports; neutral
      `WithLocker(Locker)`/`WithElector(Elector)`; persistence advisory lock
      reused; PG/MySQL electors behind `scheduling/backend/{postgres,mysql}`.
- [ ] Boundary-enhancements spec ADR numbers remain 0103/0104.
