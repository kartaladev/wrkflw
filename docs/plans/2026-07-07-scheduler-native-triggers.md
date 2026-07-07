# Plan 2/3 — scheduler port + gocron native triggers (`NextRun`-driven) + `scheduling` neutralization

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every `schedule.TriggerSpec` fire through gocron's native trigger types. Redesign the scheduler port to `Schedule(ctx, id, TriggerSpec, fire) (nextRun, err)`; map `TriggerSpec → gocron.JobDefinition` 1:1 in the adapter; support the reducible forms in `MemScheduler`; have the engine emit the `TriggerSpec` (not a `FireAt`); and neutralize the public `scheduling` package of DB-driver coupling.

**Architecture:** The engine emits `ScheduleTimer{Trigger}`. The runtime schedules it via the port and persists the returned `NextRun` as the timer's `next_run` (existing column reused). The gocron adapter maps each `Kind` to a `JobDefinition` (one-shot = `WithLimitedRuns(1)`), returns `job.NextRun()`, and owns recurrence natively — the engine no longer reschedules reminders. `MemScheduler` handles `OneTime`+`Every` for deterministic engine tests; cron/calendar require the gocron scheduler (fake-clock-driven). Durable descriptor persistence + JobStore + ambient-tx consistency are Plan 3.

**Tech Stack:** Go 1.25, `github.com/go-co-op/gocron/v2 v2.21.2` (behind the port), `clockwork` (shared fake clock). No `robfig/cron`.

## Global Constraints

- `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean before done.
- **Strict TDD**; black-box tests; `table-test` skill for 2+ cases; `t.Context()`.
- Never import `gocron`/`clockwork` from engine/definition code; gocron only inside `internal/scheduling/gocron` (+ the `scheduling` façade). Public `scheduling` must NOT import `pgx`/`database/sql` after this plan.
- Breaking API allowed (pre-v1.0); migrate all call sites in the breaking task.
- Touched packages ≥ 85% coverage; new public symbols carry godoc.
- Depends on **Plan 1** (`schedule.TriggerSpec`, `engine.ResolveTrigger`, `engine.ErrUnsupportedTrigger`). This is **Plan 2 of 3** for ADR-0102. Plan 3 = JobStore + descriptor persistence + consistency + self-rehydration + ADR write.

---

## File Structure

- **Modify** `engine/command.go` — `ScheduleTimer.FireAt time.Time` → `Trigger schedule.TriggerSpec`.
- **Modify** engine arm sites (`step_boundaries.go`, `step_nodes.go`, `step_eventsubprocess.go`) — emit `Trigger` (drop `triggerDelay`/`FireAt`); `step_timers.go` — drop reminder reschedule (recurrence is native).
- **Modify** `runtime/kernel/scheduler.go` — `Scheduler` interface new shape; `MemScheduler` (`OneTime`+`Every`, `NextRun`, `ErrUnsupportedTrigger`).
- **Modify** `runtime/kernel/timerstore.go` — `ArmedTimer.FireAt` → `NextRun` + `Trigger schedule.TriggerSpec`.
- **Modify** `runtime/timerops.go` — `armTimer` → `sched.Schedule(ctx, …)`; `timerOpsFor` recurring-aware.
- **Modify** `runtime/processdriver_action.go` — `perform(ScheduleTimer)` passes `Trigger`.
- **Create** `internal/scheduling/gocron/trigger.go` — `TriggerSpec → gocron.JobDefinition` mapping.
- **Modify** `internal/scheduling/gocron/scheduler.go` — new `Schedule` signature, `NextRun`, native jobs.
- **Modify** `scheduling/scheduler.go` — neutral `Locker`/`Elector` options; remove DB-driver options; new `Schedule` signature.
- **Create** `scheduling/backend/postgres/`, `scheduling/backend/mysql/` — thin public elector constructors.
- **Create** the persistence-lock bridge exposing `dialect.Locker` as `scheduling.Locker`.

---

## Task 1: `ScheduleTimer` carries a `TriggerSpec`; arm sites emit it

**Files:** `engine/command.go`; `engine/step_boundaries.go`, `engine/step_nodes.go`, `engine/step_eventsubprocess.go`, `engine/step_timers.go`; adjust engine tests.

**Interfaces — Produces:** `type ScheduleTimer struct{ TimerID, Token string; Trigger schedule.TriggerSpec; Kind TimerKind }` (FireAt removed).

- [ ] **Step 1: Failing test** — assert a cron boundary arms a `ScheduleTimer` carrying the cron `TriggerSpec` (no `FireAt`):

```go
// engine/step_boundaries_test.go (add)
func TestBoundaryEmitsTriggerNotFireAt(t *testing.T) {
	def := receiveTaskBoundaryDef(event.NewBoundary("bnd", "recv", event.WithBoundaryTimer(schedule.Cron(`0 9 * * *`))))
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"}, engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var st *engine.ScheduleTimer
	for _, c := range r1.Commands {
		if s, ok := c.(engine.ScheduleTimer); ok {
			st = &s
		}
	}
	require.NotNil(t, st)
	if c, ok := st.Trigger.CronExpr(); !ok || c != `0 9 * * *` {
		t.Fatalf("trigger = %+v", st.Trigger)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestBoundaryEmitsTriggerNotFireAt` → FAIL (`ScheduleTimer.Trigger` undefined; cron currently errors via `triggerDelay`).

- [ ] **Step 3: Implement** — in `engine/command.go`:

```go
type ScheduleTimer struct {
	TimerID string
	Token   string
	Trigger schedule.TriggerSpec
	Kind    TimerKind
}
```

At each arm site, drop `triggerDelay`/`FireAt` and emit the resolved trigger:

```go
spec, err := ResolveTrigger(eval, X.Timer /* or DeadlineOf/ReminderOf */, s.Variables)
if err != nil { return ...fmt.Errorf(...) }
if !spec.IsZero() {
	timerID := s.nextTimerID()
	cmds = append(cmds, ScheduleTimer{TimerID: timerID, Token: tok.ID, Trigger: spec, Kind: <Kind>})
	// bookkeeping (timerRecord / arm.TimerID / tok.AwaitCommand) unchanged
}
```

In `step_timers.go` `handleReminderFired`: **remove** the re-`ScheduleTimer` block — the recurring `Every`/`Cron` job fires natively; the handler only runs the reminder action + keeps the reminder arm. Delete `triggerDelay` (now unused) from `engine/trigger_resolve.go`.

- [ ] **Step 4: Run** — `go test ./engine/...`; adjust tests that asserted `ScheduleTimer{FireAt}` to assert `.Trigger` (compute expected via `schedule.AfterDuration`). Recurring reminder tests now assert a single arm + repeated fires driven by the scheduler (move those assertions to the scheduler tests in Task 3–4 if they relied on engine reschedule).

- [ ] **Step 5: Commit**

```bash
git add engine/
git commit -m "refactor(engine): ScheduleTimer carries TriggerSpec; native recurrence (no reschedule)"
```

---

## Task 2: Scheduler port + `MemScheduler` (OneTime + Every)

**Files:** `runtime/kernel/scheduler.go`; `runtime/kernel/scheduler_test.go`.

**Interfaces — Produces:**

```go
type Scheduler interface {
	Schedule(ctx context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (nextRun time.Time, err error)
	Cancel(ctx context.Context, timerID string)
	NextRun(timerID string) (time.Time, bool)
}
var ErrUnsupportedTrigger = errors.New("workflow-scheduler: trigger kind not supported by this scheduler")
```

`MemScheduler` supports `KindOneTime` (fire once at `now+d` or `At`) and `KindDuration` (re-arm at `last+d` on `Tick`); returns `ErrUnsupportedTrigger` for cron/calendar/random. `NextRun`/`NextFireAt`/`Pending` keep working.

- [ ] **Step 1: Failing test**

```go
func TestMemSchedulerTriggers(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(clk))
	ctx := t.Context()

	fired := 0
	if _, err := s.Schedule(ctx, "t1", schedule.AfterDuration(time.Hour), func() { fired++ }); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour + time.Second)
	_ = s.Tick(ctx)
	if fired != 1 {
		t.Fatalf("one-shot fired %d", fired)
	}

	rec := 0
	if _, err := s.Schedule(ctx, "t2", schedule.Every(time.Minute), func() { rec++ }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		clk.Advance(time.Minute + time.Second)
		_ = s.Tick(ctx)
	}
	if rec != 3 {
		t.Fatalf("recurring fired %d, want 3", rec)
	}

	if _, err := s.Schedule(ctx, "t3", schedule.Cron(`0 9 * * *`), func() {}); !errors.Is(err, kernel.ErrUnsupportedTrigger) {
		t.Fatalf("cron must be unsupported, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./runtime/kernel/... -run TestMemSchedulerTriggers` → FAIL (signature/behavior).

- [ ] **Step 3: Implement** — change the `Scheduler` interface + add `ErrUnsupportedTrigger`. Update `MemScheduler`:

```go
func (s *MemScheduler) Schedule(_ context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next time.Time
	switch trig.Kind() {
	case schedule.KindOneTime:
		if at, ok := trig.AbsTime(); ok {
			next = at
		} else {
			d, _ := trig.Duration()
			next = s.clk.Now().Add(d)
		}
		s.pending[timerID] = pendingTimer{timerID: timerID, fireAt: next, fire: fire, recurEvery: 0}
	case schedule.KindDuration:
		d, _ := trig.Duration()
		next = s.clk.Now().Add(d)
		s.pending[timerID] = pendingTimer{timerID: timerID, fireAt: next, fire: fire, recurEvery: d}
	default:
		return time.Time{}, ErrUnsupportedTrigger
	}
	return next, nil
}
```

Add `recurEvery time.Duration` to `pendingTimer`. In `Tick`, after firing a due timer, if `recurEvery > 0` re-arm at `fireAt + recurEvery` instead of deleting (preserve the existing "not re-fired in the same Tick" determinism). Add `Cancel(ctx, id)` (ignore ctx) and `NextRun(id)` (alias of `Pending`).

- [ ] **Step 4: Run** — `go test ./runtime/kernel/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/kernel/scheduler.go runtime/kernel/scheduler_test.go
git commit -m "feat(kernel): Scheduler port takes TriggerSpec; MemScheduler OneTime+Every"
```

---

## Task 3: gocron adapter — `TriggerSpec → JobDefinition` (1:1) + `NextRun`

**Files:** Create `internal/scheduling/gocron/trigger.go`, `internal/scheduling/gocron/trigger_test.go`; modify `internal/scheduling/gocron/scheduler.go`.

**Interfaces — Produces:** `func jobDefinition(t schedule.TriggerSpec) (gocron.JobDefinition, bool /*oneShot*/, error)`; new `GocronScheduler.Schedule(ctx, id, TriggerSpec, fire) (time.Time, error)`, `Cancel(ctx, id)`, `NextRun(id)`.

- [ ] **Step 1: Failing test** — a cron job fires ≥2× when the shared fake clock is advanced to successive `NextRun()`s; a one-shot fires once then disarms:

```go
func TestGocronNativeTriggers(t *testing.T) {
	clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 59, 0, 0, time.UTC))
	s, err := gocronsched.NewGocronScheduler(gocronsched.WithClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()

	var fired atomic.Int32
	nr, err := s.Schedule(ctx, "cron1", schedule.Cron(`0 9 * * *`), func() { fired.Add(1) })
	require.NoError(t, err)
	require.False(t, nr.IsZero(), "NextRun must be returned")

	for i := 0; i < 2; i++ {
		next, ok := s.NextRun("cron1")
		require.True(t, ok)
		clk.Advance(next.Sub(clk.Now()) + time.Millisecond)
		require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
	}
}
```

> gocron runs the task on its executor goroutine, so use `require.Eventually` (not a bare assert) after advancing the fake clock — this is the standard pattern for the existing gocron tests in this package.

- [ ] **Step 2: Run to verify it fails** — FAIL (new signature/mapping undefined).

- [ ] **Step 3: Implement `internal/scheduling/gocron/trigger.go`**

```go
package gocron

import (
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// jobDefinition maps a neutral TriggerSpec to a gocron JobDefinition and reports
// whether it is a one-shot (so the caller adds WithLimitedRuns(1)).
func jobDefinition(t schedule.TriggerSpec, now time.Time) (gocron.JobDefinition, bool, error) {
	switch t.Kind() {
	case schedule.KindOneTime:
		if at, ok := t.AbsTime(); ok {
			return gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(at)), true, nil
		}
		d, _ := t.Duration()
		return gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(now.Add(d))), true, nil
	case schedule.KindDuration:
		d, _ := t.Duration()
		return gocron.DurationJob(d), false, nil
	case schedule.KindDurationRand:
		mn, mx, _ := t.Random()
		return gocron.DurationRandomJob(mn, mx), false, nil
	case schedule.KindCron:
		expr, _ := t.CronExpr()
		return gocron.CronJob(expr, false), false, nil
	case schedule.KindDaily, schedule.KindWeekly, schedule.KindMonthly:
		interval, days, weekdays, at, _ := t.Calendar()
		ats := atTimes(at)
		switch t.Kind() {
		case schedule.KindDaily:
			return gocron.DailyJob(interval, ats), false, nil
		case schedule.KindWeekly:
			return gocron.WeeklyJob(interval, gocron.NewWeekdays(weekdays[0], weekdays[1:]...), ats), false, nil
		default:
			return gocron.MonthlyJob(interval, gocron.NewDaysOfTheMonth(days[0], days[1:]...), ats), false, nil
		}
	default:
		return nil, false, fmt.Errorf("workflow-scheduler: unschedulable trigger kind %d", t.Kind())
	}
}

func atTimes(cs []schedule.ClockTime) gocron.AtTimes {
	if len(cs) == 0 {
		return gocron.NewAtTimes(gocron.NewAtTime(0, 0, 0))
	}
	first := gocron.NewAtTime(cs[0].Hour, cs[0].Minute, cs[0].Second)
	rest := make([]gocron.AtTime, 0, len(cs)-1)
	for _, c := range cs[1:] {
		rest = append(rest, gocron.NewAtTime(c.Hour, c.Minute, c.Second))
	}
	return gocron.NewAtTimes(first, rest...)
}
```

> Confirm exact gocron helper names at impl time (`gocron.NewWeekdays(time.Weekday, ...time.Weekday)`, `gocron.NewDaysOfTheMonth(int, ...int)`) against v2.21.2 and adjust the calls; the mapping is 1:1 so only the helper spelling may differ.

In `internal/scheduling/gocron/scheduler.go`, replace `Schedule(timerID, fireAt, fire)` with:

```go
func (s *GocronScheduler) Schedule(_ context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.jobs[timerID]; ok {
		_ = s.sched.RemoveJob(existing)
		delete(s.jobs, timerID)
	}
	def, oneShot, err := jobDefinition(trig, s.clk.Now())
	if err != nil {
		return time.Time{}, err
	}
	opts := []gocron.JobOption{gocron.WithName(timerID), gocron.WithEventListeners(gocron.AfterJobRuns(func(jobID uuid.UUID, _ string) {
		s.mu.Lock()
		if oneShot { // one-shots auto-remove; drop bookkeeping
			if cur, ok := s.jobs[timerID]; ok && cur == jobID {
				delete(s.jobs, timerID)
			}
		}
		s.mu.Unlock()
	}))}
	if oneShot {
		opts = append(opts, gocron.WithLimitedRuns(1))
	}
	job, err := s.sched.NewJob(def, gocron.NewTask(fire), opts...)
	if err != nil {
		return time.Time{}, err
	}
	s.jobs[timerID] = job.ID()
	next, _ := job.NextRun()
	return next, nil
}
```

Update `Cancel` to `Cancel(_ context.Context, timerID string)`; add `NextRun(timerID)` scanning `s.jobs` → `gocron` job → `job.NextRun()`.

- [ ] **Step 4: Run** — `go test ./internal/scheduling/gocron/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduling/gocron/
git commit -m "feat(gocron): map TriggerSpec to native JobDefinitions; NextRun-driven Schedule"
```

---

## Task 4: Runtime wiring — `armTimer`/`perform` on the new port

**Files:** `runtime/kernel/timerstore.go` (ArmedTimer), `runtime/timerops.go`, `runtime/processdriver_action.go`; adjust runtime tests.

- [ ] **Step 1: Failing test** — a runtime e2e schedules a cron boundary via a fake-clock gocron scheduler and it fires (use the existing runtime e2e harness pattern with `require.Eventually`).

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** —
  - `kernel.ArmedTimer`: replace `FireAt time.Time` with `NextRun time.Time` and add `Trigger schedule.TriggerSpec` (persistence descriptor lands in Plan 3; the field exists now).
  - `armTimer(def, instanceID, timerID string, trig schedule.TriggerSpec)`: `nextRun, err := r.sched.Schedule(ctx, timerID, trig, fire)`; on error log + skip; keep the retrying `fire` closure delivering `TimerFired`.
  - `perform(engine.ScheduleTimer)`: call `r.armTimer(def, st.InstanceID, cmd.TimerID, cmd.Trigger)`.
  - `timerOpsFor`: build `ArmedTimer{Trigger: cmd.Trigger, NextRun: <set post-schedule or zero>}`; **do not** add a fired `TimerFired` timer to cancels when its armed `Trigger.Recurring()` (look up the armed trigger; recurring survives). One-shot fired timers cancel as today.

- [ ] **Step 4: Run** — `go test ./runtime/...` → PASS; adjust reminder tests to expect scheduler-driven recurrence (fake-clock gocron) rather than engine reschedule.

- [ ] **Step 5: Commit**

```bash
git add runtime/
git commit -m "feat(runtime): arm timers via TriggerSpec port; recurring survives fire"
```

---

## Task 5: Neutralize the public `scheduling` package

**Files:** `scheduling/scheduler.go` (+ new `scheduling/locker.go`, `scheduling/elector.go`); create `scheduling/backend/postgres/elector.go`, `scheduling/backend/mysql/elector.go`; a persistence-lock bridge.

**Interfaces — Produces:**
- `type Locker interface { … }` and `type Elector interface { … }` in `scheduling` (neutral; method sets matching what the gocron adapter needs — mirror `gocron.Locker`/`gocron.Elector` shapes but without importing gocron in the public signatures where avoidable; internally adapt).
- `func WithLocker(l Locker) Option`, `func WithElector(e Elector) Option`.
- Remove `WithDistributedTimerLock(*pgxpool.Pool)`, `WithTimerElector(*pgxpool.Pool,…)`, `WithMySQLTimerElector(*sql.DB,…)` and the `pool`/`mysqlElectorDB` fields.
- `scheduling/backend/postgres.NewElector(pool *pgxpool.Pool, opts…) scheduling.Elector` (+ mysql).
- A bridge exposing the persistence advisory lock as `scheduling.Locker` (reuse `internal/persistence/dialect.Locker` implementations; e.g. `persistence.NewSchedulerLocker(...)` returning `scheduling.Locker`).

- [ ] **Step 1: Failing test** — assert the public `scheduling` package has no DB-driver dependency:

```go
// scheduling/neutrality_test.go
func TestSchedulingHasNoDBDeps(t *testing.T) {
	pkg, err := packages.Load(&packages.Config{Mode: packages.NeedImports}, "github.com/zakyalvan/krtlwrkflw/scheduling")
	require.NoError(t, err)
	for _, p := range pkg {
		for imp := range p.Imports {
			if strings.Contains(imp, "jackc/pgx") || imp == "database/sql" {
				t.Fatalf("scheduling must not import %s", imp)
			}
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails** — FAIL (scheduling imports pgx/sql today).

- [ ] **Step 3: Implement** — define neutral `Locker`/`Elector` interfaces + `WithLocker`/`WithElector`; delete the DB-driver options/fields; the façade passes the neutral values to the internal gocron adapter (adapting to `gocron.Locker`/`gocron.Elector` inside `internal/scheduling/gocron`). Move `NewPostgresElector`/`NewMySQLElector` construction into `scheduling/backend/{postgres,mysql}` public constructors that return `scheduling.Elector`. Add the persistence-lock bridge (`scheduling.Locker` backed by `dialect.Locker`), so no PG/MySQL locker code lives in `scheduling`.

- [ ] **Step 4: Run** — `go test ./scheduling/... ./scheduling/backend/...` → PASS incl. the neutrality test; migrate wiring examples (`examples/{production,mysql}_wiring`) to the new options.

- [ ] **Step 5: Commit**

```bash
git add scheduling/ examples/
git commit -m "refactor(scheduling): neutral Locker/Elector; backend electors; reuse persistence lock"
```

---

## Self-Review (spec coverage for Plan 2)

- Native trigger firing (all kinds via gocron) → Tasks 1, 3, 4. Port `Schedule(ctx,id,TriggerSpec)(nextRun,err)` + `Cancel` + `NextRun` → Tasks 2–4. `MemScheduler` simple-only → Task 2. Engine emits `Trigger`, recurrence native (no reschedule) → Task 1. `scheduling` neutralization + persistence-lock reuse + backend electors → Task 5.
- **Deferred to Plan 3:** descriptor persistence (`trigger_kind`/`trigger_payload` migration), `JobStore` port + self-rehydration, ambient-tx consistency, ADR-0102 write (superseding ADR-0027 timer-write). In Plan 2, one-shot rehydration via `NextRun`/`At` works through the existing `TimerStore`; durable recurring rehydration needs Plan 3's descriptor column. Plan 2 delivers live native scheduling (in-process) end-to-end.
