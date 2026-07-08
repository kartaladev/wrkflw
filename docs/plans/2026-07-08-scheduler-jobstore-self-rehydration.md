# Scheduler JobStore Self-Rehydration Implementation Plan (ADR-0102, revised)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Supersedes** the Task 3–5 portion of `docs/plans/2026-07-07-scheduler-jobstore-durability.md`. That plan predated the `ada9436` scheduler-redesign merge and its Task 4 premise is architecturally wrong for the current code: timer/state atomicity is **already** delivered by the fused `AppliedStep.TimerArms/TimerCancels` writes inside `Store.Commit`'s transaction (`internal/persistence/store/store_core.go:285`), and gocron `Schedule` runs post-commit in `perform` with its ctx ignored — so routing timer writes through a post-commit `JobStore.Save` would **regress** atomicity, not preserve it. This revised plan keeps the fused-write atomicity untouched and delivers the one genuinely net-new capability: **the scheduler self-rehydrates armed timers on start via a `LoadScheduled`-only `kernel.JobStore`**, so consumers no longer need an explicit `RehydrateTimers` call.

**Goal:** Armed timers persisted in the durable store (already implemented — `next_run`/`trigger_kind`/`trigger_payload`) are re-registered on the scheduler automatically when the scheduler starts, rebuilding each timer's executable `Fire` callback, with no explicit `ProcessDriver.RehydrateTimers` call and no change to the atomic timer-write path.

**Architecture:** A `kernel.JobStore` interface exposes `LoadScheduled(ctx) ([]ScheduledJob, error)`; each `ScheduledJob` carries a `JobSpec` (descriptor) plus a rebuilt `Fire func()`. The `runtime` implementation (`runtime.NewJobStore`) reads `TimerStore.ListArmed`, resolves each definition via the registry, and rebuilds `Fire` from the shared `ProcessDriver.timerFireFunc`. The `scheduling.Scheduler` façade accepts `WithJobStore(func() kernel.JobStore)` (a **provider thunk** — resolved at first Start, breaking the driver↔jobstore↔scheduler construction cycle the same way the codebase already forward-references the SignalBus) and, on first start, calls `LoadScheduled` and registers every job. The driver auto-wires this for its owned default scheduler. The fused-write atomicity mechanism (`AppliedStep.TimerArms/TimerCancels`) is unchanged; `RehydrateTimers` is retained (it already shares the fire closure via `armTimer`) as an explicit fallback for injected schedulers.

**Tech Stack:** Go 1.25; gocron behind `kernel.Scheduler`; `clockwork` fake clock (via `clock.Clock`) in tests; SQLite (`modernc.org/sqlite`, pure-Go) + Postgres/MySQL testcontainers for durable tests; `schedule.TriggerSpec` / `model.TriggerWire` descriptor already persisted.

## Global Constraints

- `go build ./...`, `go test -race ./...`, `golangci-lint run ./...` clean; ≥85% coverage on touched packages.
- **Strict TDD** (CLAUDE.md TDD Operational Discipline — observable RED before every new symbol). `use-testcontainers` for DB tests (`database.RunTestDatabase` for PG; SQLite via the existing SQLite test helper); `table-test` closure form; `use-mockgen` for any mocked interface; black-box tests (`_test` packages).
- **Never** import gocron/clockwork/watermill/casbin from engine/workflow code — go through `kernel.Scheduler` / `clock.Clock`.
- **Do NOT touch** `AppliedStep.TimerArms/TimerCancels`, `applyTimerOps`, or the `Store.Commit` timer-write path — atomicity (ADR-0027) is retained by leaving it alone.
- Never compare `dialect.Name` to `"sqlite"` — use `dialect.TimestampsAsText()` (ADR-0080). (Not expected to arise here; the descriptor read/write already landed.)
- Error sentinels use the `workflow-<pkg>:` prefix.
- ADR: **ADR-0102** (scheduler subsystem) + annotate **ADR-0027**. Nygard template (Status/Date, Context, Decision, Consequences), `docs/adr/0001-record-architecture-decisions.md` as the canonical format.
- Examples show engine INTERNAL mechanics, not testing — no `processtest`/test-helper wiring in `examples/`.

## File Structure

- **Modify** `runtime/timerops.go` — extract the inline fire closure (lines ~131–162) into `(driver *ProcessDriver) timerFireFunc(def, instanceID, timerID) func()`; repoint `armTimer`. `RehydrateTimers` unchanged (already routes through `armTimer` → `timerFireFunc`).
- **Modify** `runtime/kernel/timerstore.go` — add `JobSpec`, `ScheduledJob`, `JobStore` (port; `LoadScheduled` only).
- **Create** `runtime/jobstore.go` + `runtime/jobstore_test.go` — `NewJobStore(driver) kernel.JobStore` (+ `jobStore` impl).
- **Modify** `scheduling/scheduler.go` — `WithJobStore(func() kernel.JobStore)` option (thunk); `config.jobStoreProvider`; self-rehydrate once on first start.
- **Modify** `runtime/processdriver.go` — auto-wire the owned default scheduler with a `WithJobStore` thunk when the driver owns the scheduler and has `timerStore`+`defsReg`.
- **Create** `examples/scenarios/timer_durability/main.go` — the restart-survival demonstration (persistent SQLite + fake clock).
- **Create** `docs/adr/0102-scheduler-subsystem.md`; **modify** `docs/adr/0027-timer-rehydration.md` (annotation); **modify** `runtime/README.md` (self-rehydration note).

---

## Task 1: Extract the fire closure into `timerFireFunc` (pure refactor)

**Files:** `runtime/timerops.go` (modify `armTimer` ~L130–179).

**Interfaces — Produces:** `func (driver *ProcessDriver) timerFireFunc(def *model.ProcessDefinition, instanceID, timerID string) func()` — the reusable builder for a timer's fire callback (delivers `TimerFired` via `driver.ApplyTrigger` with the existing 5-attempt CAS-retry). Consumed by `armTimer` and (Task 2) the JobStore.

- [ ] **Step 1: Confirm existing timer tests are green (refactor baseline)**

Run: `go test ./runtime/... -run 'Timer|Rehydrate' -count=1`
Expected: PASS (these prove behavior; they must stay green before AND after — this is a behavior-preserving refactor, so no NEW test is added, per CLAUDE.md "pure refactor" rule).

- [ ] **Step 2: Add `timerFireFunc`, lifting the closure body verbatim**

In `runtime/timerops.go`, add the method (body is the exact closure currently inline in `armTimer` at ~L133–162 — `fireCtx := context.Background()`, build `engine.NewTimerFired(driver.clk.Now(), timerID)`, `driver.obs.timerFired.Add`, the `maxAttempts = 5` loop retrying only on `kernel.ErrConcurrentUpdate`, and the two error logs):

```go
// timerFireFunc builds the fire callback for a timer. The callback runs from the
// scheduler's goroutine when the timer becomes due, so it uses a background
// context (the arming request's context may be cancelled by fire time). It
// delivers a TimerFired trigger to the instance via ApplyTrigger, retrying up to
// maxAttempts times on an optimistic-CAS conflict (ErrConcurrentUpdate); any
// other error is logged and dropped. It is shared by armTimer and the JobStore's
// rehydration path so both build byte-identical fire behaviour.
func (driver *ProcessDriver) timerFireFunc(def *model.ProcessDefinition, instanceID, timerID string) func() {
	return func() {
		fireCtx := context.Background()
		trg := engine.NewTimerFired(driver.clk.Now(), timerID)
		driver.obs.timerFired.Add(fireCtx, 1)
		const maxAttempts = 5
		var err error
		for range maxAttempts {
			if _, err = driver.ApplyTrigger(fireCtx, def, instanceID, trg); err == nil {
				return
			}
			if !errors.Is(err, kernel.ErrConcurrentUpdate) {
				driver.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: ApplyTrigger failed",
					append(driver.obs.tel.LogAttrs(fireCtx),
						slog.String("timer_id", timerID),
						slog.String("instance_id", instanceID),
						slog.Any("error", err))...)
				return
			}
		}
		driver.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: ApplyTrigger permanently dropped after CAS conflicts",
			append(driver.obs.tel.LogAttrs(fireCtx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Int("attempts", maxAttempts),
				slog.Any("error", err))...)
	}
}
```

- [ ] **Step 3: Repoint `armTimer` to use it**

Replace the inline `func() { … }` argument to `driver.sched.Schedule(...)` in `armTimer` with `driver.timerFireFunc(def, instanceID, timerID)`:

```go
nextRun, err := driver.sched.Schedule(ctx, timerID, trig, driver.timerFireFunc(def, instanceID, timerID))
```

Leave the rest of `armTimer` (the `err != nil` skip-with-WARN and the DEBUG "scheduled" log) unchanged.

- [ ] **Step 4: Run the timer tests (still green — behavior identical)**

Run: `go test ./runtime/... -run 'Timer|Rehydrate' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/timerops.go
git commit -m "refactor(runtime): extract timer fire closure into timerFireFunc"
```

---

## Task 2: `kernel.JobStore` port + `runtime` implementation

**Files:** `runtime/kernel/timerstore.go` (add port + types), `runtime/jobstore.go` (create), `runtime/jobstore_test.go` (create).

**Interfaces — Produces:**
```go
// runtime/kernel
type JobSpec struct {
	TimerID, InstanceID, DefID string
	DefVersion int
	Trigger schedule.TriggerSpec // the trigger to (re)register with — faithful-fire-time resolved
	NextRun time.Time
}
type ScheduledJob struct {
	Spec JobSpec
	Fire func()
}
type JobStore interface {
	// LoadScheduled enumerates every armed timer and returns an executable
	// ScheduledJob (descriptor + rebuilt Fire) for each. Timers whose definition
	// cannot be resolved are skipped and counted in the returned error.
	LoadScheduled(ctx context.Context) ([]ScheduledJob, error)
}
// runtime
func NewJobStore(driver *ProcessDriver) kernel.JobStore
```

- [ ] **Step 1: Add the port + types to `runtime/kernel/timerstore.go`**

Append after the `TimerStore` interface (keep `TimerStore` unchanged):

```go
// JobSpec is the descriptor of one durable scheduled timer job.
type JobSpec struct {
	TimerID    string
	InstanceID string
	DefID      string
	DefVersion int
	// Trigger is the TriggerSpec to (re)register the job with. For a non-recurring
	// timer with a persisted NextRun it is schedule.At(NextRun) (faithful original
	// fire instant); otherwise it is the stored recurring Trigger.
	Trigger schedule.TriggerSpec
	NextRun time.Time
}

// ScheduledJob is an executable durable timer: its descriptor plus a rebuilt Fire
// callback that delivers the timer's TimerFired trigger when invoked.
type ScheduledJob struct {
	Spec JobSpec
	Fire func()
}

// JobStore is the read-side port a Scheduler uses to self-rehydrate armed timers
// on start. It rebuilds executable ScheduledJobs from the durable TimerStore; the
// write side remains the fused AppliedStep.TimerArms/TimerCancels on the state
// commit (ADR-0027) — JobStore never writes.
type JobStore interface {
	LoadScheduled(ctx context.Context) ([]ScheduledJob, error)
}
```

- [ ] **Step 2: Write the failing test** — `runtime/jobstore_test.go` (black-box, `package runtime_test`). Use the in-memory driver + `MemTimerStore` + a definition registry. Arm a one-shot timer on a parked instance through the normal path (so it lands in the `TimerStore`), then assert `NewJobStore(driver).LoadScheduled(ctx)` returns one `ScheduledJob` whose `Spec` matches and whose `Fire()` advances the parked instance to completion.

```go
func TestJobStoreLoadScheduledRebuildsExecutableFire(t *testing.T) {
	// Build an in-mem driver with a MemTimerStore + registered definition whose
	// single instance parks on an intermediate timer catch event.
	// (Mirror the setup in runtime/rehydrate_test.go.)
	// ... arm the timer via Drive so MemTimerStore records it ...
	js := runtime.NewJobStore(driver)
	jobs, err := js.LoadScheduled(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, timerID, jobs[0].Spec.TimerID)
	assert.Equal(t, instanceID, jobs[0].Spec.InstanceID)
	// Firing the rebuilt callback advances the instance.
	jobs[0].Fire()
	st, _, err := store.Load(t.Context(), instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}
```

Run: `go test ./runtime/... -run TestJobStoreLoadScheduled -count=1` → FAIL (`undefined: runtime.NewJobStore`).

- [ ] **Step 3: Implement `runtime/jobstore.go`**

```go
package runtime

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// jobStore is the runtime JobStore: it rebuilds executable ScheduledJobs from the
// durable TimerStore, resolving each timer's definition via the registry and
// rebuilding its Fire via the shared timerFireFunc.
type jobStore struct {
	driver *ProcessDriver
}

// NewJobStore returns a kernel.JobStore backed by driver's TimerStore + definition
// registry. Pass it (as a provider) to scheduling.WithJobStore so the scheduler
// self-rehydrates armed timers on start.
func NewJobStore(driver *ProcessDriver) kernel.JobStore { return &jobStore{driver: driver} }

func (j *jobStore) LoadScheduled(ctx context.Context) ([]kernel.ScheduledJob, error) {
	if j.driver.timerStore == nil || j.driver.defsReg == nil {
		return nil, nil
	}
	armed, err := j.driver.timerStore.ListArmed(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: LoadScheduled: list armed: %w", err)
	}
	jobs := make([]kernel.ScheduledJob, 0, len(armed))
	var unresolved int
	for _, a := range armed {
		defQ := model.Version(a.DefID, a.DefVersion)
		def, err := j.driver.defsReg.Lookup(ctx, defQ)
		if err != nil {
			unresolved++
			// log at ERROR, mirroring RehydrateTimers
			continue
		}
		jobs = append(jobs, kernel.ScheduledJob{
			Spec: kernel.JobSpec{
				TimerID: a.TimerID, InstanceID: a.InstanceID,
				DefID: a.DefID, DefVersion: a.DefVersion,
				Trigger: rehydrateTrigger(a), NextRun: a.NextRun,
			},
			Fire: j.driver.timerFireFunc(def, a.InstanceID, a.TimerID),
		})
	}
	if unresolved > 0 {
		return jobs, fmt.Errorf("workflow-runtime: LoadScheduled: %d timer(s) skipped (definition not found)", unresolved)
	}
	return jobs, nil
}
```

Note: `Spec.Trigger` uses `rehydrateTrigger(a)` (the existing helper in `timerops.go`) so a one-shot re-registers via `schedule.At(NextRun)` — the same faithful-fire-time semantics `RehydrateTimers` already gives. Add the ERROR log for the unresolved branch (copy the shape from `RehydrateTimers`).

- [ ] **Step 4: Run** — `go test ./runtime/... -run TestJobStoreLoadScheduled -count=1` → PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/kernel/timerstore.go runtime/jobstore.go runtime/jobstore_test.go
git commit -m "feat(runtime): kernel.JobStore port + LoadScheduled impl (rebuilds executable Fire)"
```

---

## Task 3: Scheduler self-rehydration on start (the integration crux)

**Files:** `scheduling/scheduler.go` (add `WithJobStore` + rehydrate-once); `runtime/processdriver.go` (auto-wire owned scheduler); durable e2e test `scheduling/jobstore_rehydrate_test.go` OR extend `runtime/rehydrate_durable_test.go`.

**Interfaces — Produces:** `scheduling.WithJobStore(provider func() kernel.JobStore) Option`. On the first `Start`/auto-start, the scheduler resolves the provider once, calls `LoadScheduled`, and registers every `ScheduledJob` via the underlying `Schedule`. Registration is idempotent w.r.t. a later duplicate arm (same `timerID` replaces).

**Construction-cycle note:** the provider is a **thunk** (`func() kernel.JobStore`), not a `kernel.JobStore`, so it can be supplied at scheduler-construction time while capturing a `*ProcessDriver` that is fully populated only later (by first-Start time). This mirrors the codebase's existing forward-reference wiring (SignalBus). The driver auto-supplies this thunk for its owned default scheduler.

- [ ] **Step 1: Failing durable e2e test** (SQLite persistent file, fake clock) — schedule a timer via a driver on a SQLite store (commit persists the descriptor), **tear the driver + scheduler down**, construct a FRESH scheduler (same SQLite store, same fake clock) wired via `WithJobStore(func() kernel.JobStore { return runtime.NewJobStore(driver2) })`, `Start` it (no explicit `RehydrateTimers` call), advance the fake clock past `NextRun`, and assert the timer fires and advances the persisted instance. Model the setup on `runtime/rehydrate_durable_test.go` but drive rehydration through the scheduler's own start, not `RehydrateTimers`.

Run: → FAIL (`undefined: scheduling.WithJobStore`; and without rehydration the fire never happens).

- [ ] **Step 2: Add `WithJobStore` + config field**

In `scheduling/scheduler.go`, add to `config`:
```go
	jobStoreProvider func() kernel.JobStore
	rehydrated       bool // set once, under mu, after the first successful LoadScheduled+register
```
and the option:
```go
// WithJobStore supplies a provider of the kernel.JobStore the scheduler uses to
// self-rehydrate armed timers on its first start. The provider is a thunk so it
// can capture a ProcessDriver that is fully constructed only after the scheduler
// (breaking the driver↔jobstore↔scheduler construction cycle, mirroring the
// SignalBus forward-reference). A nil provider is ignored. On first Start the
// scheduler calls provider().LoadScheduled(ctx) and registers each ScheduledJob;
// this replaces the need for an explicit ProcessDriver.RehydrateTimers call.
func WithJobStore(provider func() kernel.JobStore) Option {
	return func(c *config) {
		if provider != nil {
			c.jobStoreProvider = provider
		}
	}
}
```

- [ ] **Step 3: Rehydrate once on first start**

Add a `rehydrate` method and call it from both `Start` and `Schedule` after `ensureStarted` succeeds. Do the `LoadScheduled` + registration **outside** `s.mu` (DB I/O must not block concurrent Start/Schedule/Close), guarded by a `sync.Once` so it runs exactly once. Registration uses the started `impl.Schedule(ctx, spec.TimerID, spec.Trigger, job.Fire)`:

```go
// rehydrateOnce (add field: rehydrateOnce sync.Once; rehydrateErr error)

func (s *Scheduler) rehydrate(ctx context.Context, impl *gocronsched.GocronScheduler) error {
	if s.cfg.jobStoreProvider == nil {
		return nil
	}
	s.rehydrateOnce.Do(func() {
		js := s.cfg.jobStoreProvider()
		if js == nil {
			return
		}
		jobs, err := js.LoadScheduled(ctx)
		// Register everything we DID resolve even if err != nil (partial: some defs unresolved).
		for _, job := range jobs {
			if _, serr := impl.Schedule(ctx, job.Spec.TimerID, job.Spec.Trigger, job.Fire); serr != nil {
				// log WARN + skip an unschedulable rehydrated timer; never fail the batch
			}
		}
		s.rehydrateErr = err // surfaced by explicit Start; auto-start path logs it
	})
	return s.rehydrateErr
}
```

Wire it:
- `Start(ctx)`: after `ensureStarted(ctx)` returns `impl`, `return s.rehydrate(ctx, impl)` (so an explicit Start surfaces a rehydration error).
- `Schedule(...)`: after `ensureStarted(context.Background())` returns `impl`, call `if err := s.rehydrate(context.Background(), impl); err != nil { log WARN }` (a Schedule must not fail because rehydration had unresolved defs), then proceed to `impl.Schedule(...)`.

Design note for the reviewer: `rehydrateOnce` guarantees single execution across the Start-and-auto-start paths; the DB call is outside `s.mu`; a rehydrated timer that later collides with a fresh arm of the same `timerID` is safely replaced by gocron's name-dedup.

- [ ] **Step 4: Auto-wire the owned default scheduler**

In `runtime/processdriver.go`, where the driver creates its owned default scheduler (~L144–160, the `ownedScheduler` path), pass `scheduling.WithJobStore(func() kernel.JobStore { return NewJobStore(driver) })` into `scheduling.NewScheduler(...)` **when** `driver.timerStore != nil && driver.defsReg != nil`. The thunk captures the `driver` pointer (already allocated); it is resolved at first `driver.Start(ctx)` when the driver is fully built. For a consumer-injected scheduler (`WithScheduler`), the consumer opts in by constructing their scheduler with `scheduling.WithJobStore(...)` themselves (documented in Task 5); `RehydrateTimers` remains available for them.

- [ ] **Step 5: Run** — `go test -race ./scheduling/... ./runtime/... -count=1` incl. the new durable e2e (SQLite) + the existing `rehydrate_durable_test.go` (PG/MySQL/SQLite testcontainers) — all green. `RehydrateTimers` still works (unchanged).

- [ ] **Step 6: Commit**

```bash
git add scheduling/scheduler.go runtime/processdriver.go scheduling/*_test.go runtime/*_test.go
git commit -m "feat(scheduler): self-rehydrate armed timers on start via WithJobStore"
```

---

## Task 4: `examples/scenarios/timer_durability` — restart-survival demonstration

**Files:** `examples/scenarios/timer_durability/main.go` (create). This is the user's explicitly-requested example: start a process with a fake clock, advance to before a timed event fires, stop the instance (tear down driver + scheduler), start a fresh driver+scheduler from the **same persistent database**, advance the clock to the fire time, and observe the timed event fire.

**Requirements:**
- **Persistent database:** use SQLite via `persistence.OpenSQLite` with a real file path (a temp-dir file, e.g. `filepath.Join(os.TempDir(), "wrkflw_timer_durability.db")`) so the DB survives across the two driver instances within the one process run — a genuine persistent store, pure-Go, no Docker. Migrate it (`persistence.NewSQLiteMigrator(...).Up`) before use. Clean up the file at the end.
- **Shared fake clock:** one `clock.Clock` fake (the in-repo `clock` façade over clockwork) shared by BOTH driver instances and BOTH schedulers via `WithClock`, so a single `Advance` drives engine timestamps and timer firing across the "restart".
- **Definition:** a minimal process that parks on a timed event whose firing is observable — a `UserTask` with `activity.WithDeadline(schedule.AfterDuration(1*time.Hour), "escalate", "notify-escalation")` routing to an escalation end, OR an `IntermediateCatchEvent` with a timer trigger of `schedule.AfterDuration(1*time.Hour)`. Register a catalog action (e.g. `notify-escalation`) that prints so the fire is visible.
- **Flow (print each milestone):**
  1. Build fake clock at T0; open+migrate the SQLite file; build driver1 (`WithInstanceStore` SQLite, `WithTimerStore`, `WithDefinitions`, default scheduler with the shared clock — which now self-rehydrates). Register the definition.
  2. `driver1.Start(ctx)`; `driver1.Drive(...)` a new instance → it arms the 1h timer and parks. Print the armed next-run.
  3. Advance the fake clock by 30m (before fire). Print "advanced to T+30m, timer NOT yet fired" (assert the instance is still parked / escalation action not run).
  4. `driver1.Shutdown(ctx)` — simulate process stop.
  5. Build driver2 on the SAME SQLite file + SAME fake clock + default self-rehydrating scheduler; register the same definition; `driver2.Start(ctx)` → the scheduler self-rehydrates the persisted timer (no `RehydrateTimers` call).
  6. Advance the fake clock past the original 1h instant. Print "timer fired" when the escalation action runs; load the instance from the store and print its terminal status/path.
  7. Print success + clean up the DB file.
- **Do NOT** wire any `processtest`/test helper (examples show internal mechanics). Use only public root-package API.

- [ ] **Step 1: Write `main.go`** per the flow above, using the public `persistence`, `runtime`, `definition/*`, `action`, `clock`, and `scheduling` APIs. Model the persistence + driver wiring on `examples/mysql_wiring/main.go` (but SQLite) and the fake-clock timer advance on `runtime/rehydrate_durable_test.go`.

- [ ] **Step 2: Build + run it**

Run: `go build ./examples/... && go run ./examples/scenarios/timer_durability`
Expected: prints the milestone log ending in the timer firing AFTER the simulated restart, then "success". Non-zero exit on any assertion failure.

- [ ] **Step 3: Commit**

```bash
git add examples/scenarios/timer_durability/
git commit -m "docs(examples): timer durability across restart (persistent SQLite + fake clock)"
```

---

## Task 5: ADR-0102 + annotate ADR-0027 + README

**Files:** create `docs/adr/0102-scheduler-subsystem.md`; modify `docs/adr/0027-timer-rehydration.md`; modify `runtime/README.md`.

- [ ] **Step 1: Write ADR-0102 (Nygard, canonical two-bullet Status/Date like ADR-0001)** covering the whole scheduler subsystem now delivered: typed `schedule.TriggerSpec` (gocron-neutral, full parity); native gocron behind `kernel.Scheduler`; goroutine-free `NewScheduler` + `Start(ctx)`; durable descriptor persistence (`next_run`/`trigger_kind`/`trigger_payload`); and **this plan's addition** — `kernel.JobStore.LoadScheduled` + scheduler self-rehydration on start (`WithJobStore` provider thunk). **Consequences MUST state (corrected wording):** *"ADR-0027's atomic timer-write mechanism (`AppliedStep.TimerArms/TimerCancels`, fused into the state-commit transaction) is RETAINED unchanged; ADR-0102 only relocates rehydration OWNERSHIP from the driver's explicit `RehydrateTimers` to scheduler self-rehydration via `JobStore.LoadScheduled`. Atomicity is preserved by leaving the fused-write path untouched. `RehydrateTimers` is retained as an explicit fallback for consumer-injected schedulers."*

- [ ] **Step 2: Annotate `docs/adr/0027-timer-rehydration.md`** — add under its Status/Context:
  `> **Extended by ADR-0102 (2026-07-08):** the atomic timer-write mechanism (AppliedStep.TimerArms/TimerCancels on the commit tx) is RETAINED. ADR-0102 adds scheduler self-rehydration (kernel.JobStore.LoadScheduled) so an explicit RehydrateTimers call is no longer required for the driver's owned scheduler; RehydrateTimers remains for injected schedulers.`
  (Note: the superseded plan's proposed "timer arms now written by JobStore.Save…replacing AppliedStep" wording is factually wrong and must NOT be used.)

- [ ] **Step 3: Update `runtime/README.md`** — in the `RehydrateTimers` / restart-recovery section, add that the driver's owned default scheduler now **self-rehydrates on `Start`** (via `WithJobStore`, wired automatically), so an explicit `RehydrateTimers` call is only needed for consumer-injected schedulers; point to `examples/scenarios/timer_durability`.

- [ ] **Step 4: Commit**

```bash
git add docs/adr/0102-scheduler-subsystem.md docs/adr/0027-timer-rehydration.md runtime/README.md
git commit -m "docs(adr): ADR-0102 scheduler subsystem + self-rehydration; annotate ADR-0027"
```

---

## Verification Checklist

- [ ] `go build ./...` clean; `go test -race ./... -count=1` 0 fail / 0 races (PG+MySQL+SQLite testcontainers + Docker).
- [ ] `golangci-lint run ./...` 0 issues; touched packages ≥85% coverage.
- [ ] `AppliedStep.TimerArms/TimerCancels`, `applyTimerOps`, and `Store.Commit` timer path are UNCHANGED (`git diff` confirms atomicity mechanism untouched).
- [ ] `RehydrateTimers` still passes its existing tests (`runtime/rehydrate_test.go`, `runtime/rehydrate_durable_test.go`).
- [ ] The durable e2e proves a timer survives a driver+scheduler teardown and fires after a fresh scheduler self-rehydrates — with NO explicit `RehydrateTimers` call.
- [ ] `examples/scenarios/timer_durability` builds and runs to "success".
- [ ] ADR-0102 present (Nygard, corrected supersession wording); ADR-0027 annotated; README updated.
- [ ] `/code-review` run on the branch; Critical/Important findings fixed.
