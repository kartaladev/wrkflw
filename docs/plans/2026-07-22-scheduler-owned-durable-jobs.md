# Scheduler-Owned Durable Jobs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every implementer MUST first load `cc-skills-golang:golang-how-to` plus this repo's `table-test`, `use-mockgen`, `use-testcontainers` skills (they override the generic golang skills).

**Goal:** Make the scheduling subsystem a self-contained `scheduler` library (zero wrkflw imports) that owns durable-job persistence through a consumer-implemented `JobStore`, replacing the fused ADR-0027 write path while preserving atomic-with-state commits via the ambient ctx-transaction and the Manual-activation model (ADR-0134, ADR-0135).

**Architecture:** Rename/unify `scheduling/` + `internal/scheduling/` → `scheduler/` (compile-preserving first). Introduce `scheduler.Trigger` (own vocabulary, pure `Next`), `Job`/`ScheduledJob` with `Activation() ∈ {Auto, Manual}`, and a `JobStore{Save,Delete,Load}` port routed by `JobKind`. wrkflw arms timers as Manual jobs: persist inside `RunInTx` (same tx as `Store.Commit`/`Create` via `JoinOrBegin`), `Activate` post-commit — nothing is in gocron before its durable state commits.

**Tech Stack:** Go 1.25, gocron v2.22.0 (bumped from v2.21.2 — ADR-0135), clockwork behind `clock.Clock`, `robfig/cron/v3` (promoted from indirect) for pure cron next-run, testcontainers via `internal/dbtest`.

## Global Constraints

- **Authoritative docs:** spec `docs/specs/2026-07-14-scheduling-owned-durable-jobs-design.md` (v2, D1–D16), ADR-0134, ADR-0135. On conflict, spec wins over this plan.
- **TDD strict** per CLAUDE.md "TDD Operational Discipline": every new symbol needs an observable RED (`go test` failing in a Bash call) before its implementation. Compile error `undefined: X` is a valid red.
- **Hot-path-first coverage** (CLAUDE.md Golang rule #8): the hot paths of this change are (a) Drive commit → in-tx persist → post-commit Activate, (b) timer fire → `applyTrigger` CAS loop, (c) rehydration `Load`+`Activate`, (d) `TimerWriter` upsert/delete in-tx. Cover ALL of them (incl. failure branches) before anything cosmetic. ≥85% line coverage is a floor.
- **Table tests** use the project `table-test` skill form: `assert func(t *testing.T, got X, err error)` closures, `ctx` modifier field where context-sensitive, `t.Context()` not `context.Background()`. Black-box `_test` packages except where unexported access is required (use `export_test.go`).
- **Mocks** via `use-mockgen` (`//go:generate mockgen --typed`, mocks live in the producer package). **Containers** via `internal/dbtest` helpers only (`dbtest.RunTestDatabase`, `dbtest.RunTestMySQL`, `dbtest.RunTestSQLite`).
- **Error sentinels** use prefix `workflow-scheduler: ...` in package `scheduler`, `workflow-runtime: ...` in runtime, `workflow-store: ...` in the store.
- **Never import** gocron/clockwork/watermill/casbin from engine/runtime code — only through `scheduler`, `clock.Clock`, etc. `scheduler/` production files import NOTHING under `github.com/kartaladev/wrkflw/` (Task 14 guard).
- **Commit choreography (feature-bundle policy, CLAUDE.md Git Discipline):**
  - Task 1 → its own commit: `refactor(scheduler): unify scheduling trees into scheduler/ (compile-preserving)`
  - Task 2 → its own commit: `chore(deps): bump gocron to v2.22.0 (ADR-0135)` — bundles `docs/adr/0135-gocron-v2-22-0-bump.md`.
  - Task 3 creates the **feature bundle commit** `feat(scheduler): scheduler-owned durable jobs (ADR-0134)` including ALL currently-uncommitted design docs (spec, ADR-0134, edits to ADR-0027/0102, this plan). **Every task after 3 folds into it with `git add -A && git commit --amend --no-edit`.** Never push the branch before the Delivery Gate.
  - Delivery Gate (end of Task 14): full Verification + `/code-review` + `/security-review`, all findings fixed (amend), then `git checkout main && git merge --no-ff feat/scheduling-owned-jobs && git push`.
- Steps that say "Run: `go test ./scheduler/...`" mean from the repo root. Postgres/MySQL tests need Docker running.
- **Branch:** all work happens on the existing branch `feat/scheduling-owned-jobs` (name predates the D12 rename; keep it).
- **Sentinel renames are part of the reshape:** the existing `workflow-scheduling:` prefixed sentinels (`ErrTimerLockElectorConflict`, `ErrSchedulerClosed` in the façade; `ErrLockerElectorConflict` in the engine) are renamed to the `workflow-scheduler:` prefix in Task 7's movement (BREAKING message change — CHANGELOG).

### Current-code anchor map (verified 2026-07-22; line numbers drift after Task 1)

| Symbol | Location |
|---|---|
| `kernel.Scheduler` (old port: `Schedule(ctx, timerID, TriggerSpec, fire func())`, `Cancel`, `NextRun`) | `runtime/kernel/scheduler.go:23` |
| `kernel.ErrUnsupportedTrigger` | `runtime/kernel/scheduler.go:15` |
| `kernel.JobSpec` / `kernel.ScheduledJob` / `kernel.JobStore` (LoadScheduled) | `runtime/kernel/timerstore.go:93,107,116` |
| `kernel.ArmedTimer` / `TimerStore` / `MemTimerStore` | `runtime/kernel/timerstore.go:23,36,44` |
| `kernel.ErrUnresolvedTimerDefinitions` | `runtime/kernel/errors_construct.go:20` |
| `kernel.AppliedStep.TimerArms/TimerCancels` | `runtime/kernel/ports.go:61-68` |
| MemInstanceStore timer application | `runtime/kernel/memstore.go:107-111,150-154` |
| `runtime.NewJobStore` / `jobStore.LoadScheduled` | `runtime/jobstore.go:34,36` |
| `timerOpsFor` / `nextRunFor` / `armTimer` / `timerFireFunc` / `rehydrateTrigger` | `runtime/timerops.go:29,84,130,160,372` |
| `armStartTimer` / `startTimerFireFunc` / `RehydrateStartTimers` | `runtime/timerops.go:304,271,347` |
| Drive commit flow (arms→AppliedStep→Create/Commit→perform loop) | `runtime/processdriver.go:619-659` |
| `perform` `ScheduleTimer`/`CancelTimer` cases (+ TimerRetry metric) | `runtime/processdriver_action.go:306-326` |
| Driver default-scheduler construction / `ownedScheduler` | `runtime/processdriver.go:119,184-201,231-234` |
| `WithScheduler(sched kernel.Scheduler)` | `runtime/processdriver_options.go:81` |
| `scheduling.Scheduler` façade (concrete) + `WithJobStore(func() kernel.JobStore)` | `scheduling/scheduler.go:55,189` |
| gocron engine `GocronScheduler.Schedule/Cancel/NextRun/Close/CloseWithContext` | `internal/scheduling/gocron/scheduler.go:221,282,299,320,329` |
| gocron trigger mapping `jobDefinition(t, now)` | `internal/scheduling/gocron/trigger.go:15` |
| engine's `internal/observability` usage (`Option`, `Telemetry`, `New`, `With*`) | `internal/scheduling/gocron/scheduler.go:21,45-49,158,208` |
| `Store.Create` / `Store.Commit` (JoinOrBegin at 66/206; `applyTimerOps` at 119/285) | `internal/persistence/store/store_core.go:63,191` |
| `upsertTimer` (8 cols) / `deleteTimer` / `applyTimerOps` / `triggerPayloadArg` | `internal/persistence/store/store_core.go:419,455,466,441` |
| `transaction.Begin(ctx, conn) (Querier, context.Context, error)` / `JoinOrBegin` | `internal/database/transaction/begin.go:11,44` |
| `schedule.TriggerSpec` 10 kinds + accessors | `definition/schedule/trigger.go` |
| `processtest.MemScheduler` (Tick-driven, NextFireAt/Pending) | `processtest/memscheduler.go` |
| `runtimetest.RecordingScheduler` | `runtime/internal/runtimetest/doubles.go:22` |
| e2e test to relocate | `scheduling/processdriver_e2e_test.go` |
| elector tests importing `internal/dbtest` | `scheduling/elector_test.go`, `scheduling/mysql_elector_test.go` |
| Example to update | `examples/scenarios/timer_durability/main.go` |

### Plan-level naming decisions (locked here; spec-compatible)

1. **Port vs concrete:** `scheduler.Scheduler` is the **interface** (the runtime port; what `MemScheduler` implements): `Schedule/Activate/Deactivate/Cancel/Scheduled/List`. The concrete gocron-backed façade (today `scheduling.Scheduler`) is renamed **`scheduler.NativeScheduler`**; constructor stays `scheduler.NewScheduler(opts...) (*NativeScheduler, error)`. Lifecycle (`Start/Close/CloseWithContext`) stays concrete-only; the driver keeps its `ownedScheduler *scheduler.NativeScheduler` split.
2. **Calendar ctor fidelity:** to keep the `TriggerSpec → Trigger` converter total and lossless, scheduler calendar ctors mirror `definition/schedule`'s richness: `Daily(interval uint, at ...ClockTime)`, `Weekly(interval uint, days []time.Weekday, at ...ClockTime)`, `Monthly(interval uint, days []int, at ...ClockTime)` (spec sketched single-value forms; fidelity wins).
3. **`Trigger.Next(after)` semantics (amended post-review):** computes the next DUE fire instant for a trigger armed at `after`. One-shots `At(t)`/`After(d)` report `ok=true` even when the target instant is at or before `after` — an already-due one-shot fires immediately on arm; the scheduler never drops past-due one-shots (`At(past)`→`(past, true)`, `After(d≤0)`→`(after+d, true)`). `At` of the zero time is the one exception, reporting `ok=false` (misused/never-armed Trigger, matching the zero-Trigger rule). Recurring shapes are unchanged — strictly after `after`: `Every(d)`/`EveryRandom(min,max)`→`after+d`/`after+min` for a positive interval, `ok=false` for a non-positive `d`/`min` (would never advance; `EveryRandom` bounds otherwise unvalidated here — `min>max` is a schedule-time concern); `Cron`→`robfig/cron/v3` standard-parser `.Next(after)`; `Daily/Weekly/Monthly`→next matching atTime/weekday/day-of-month occurrence, defaulting to midnight when `atTimes` is omitted (interval affects subsequent fires, which are the live scheduler's business, matching gocron first-fire behaviour). Zero `Trigger` → `ok=false`.
4. **Self-containment guard scope:** production (non-`_test`) files under `scheduler/` = zero wrkflw imports, AST-enforced. `_test` files MAY import `internal/dbtest` (elector container tests) and `definition/schedule` (the bump-regression characterization test until its Task-7 port); this is the documented spin-out caveat (tests relocate at module-split time).
5. **wrkflw JobKind constants:** `runtime` defines `const timerJobKind scheduler.JobKind = "wrkflw.timer"` (durable, store-registered for rehydration) and `const startTimerJobKind scheduler.JobKind = "wrkflw.start-timer"` (non-durable, no store — ADR-0121 path).
6. **Job ids are the ENGINE timer ids, unchanged (audit v2 simplification).** Engine timer ids are already globally unique across instances (`nextTimerID()` = `"<instanceID>-tm<seq>"`, `engine/step_state.go:132-135`), so NO composite id scheme is introduced: the scheduler keys by the engine timer id exactly as today, `processtest.Harness.classify`/`Pending` keep working on `tok.AwaitCommand` unchanged, and start-timer ids (`start-timer:%s:%d:%s`) pass through untouched. The runtime `JobStore` port's `Delete(ctx, id)` deletes by `timer_id` alone (globally unique; SQL `DELETE FROM wrkflw_timers WHERE timer_id = ?`); Drive's cancel path uses a runtime-internal `deleteTimer(ctx, instanceID, timerID)` by-parts helper for PK-exact deletes.
7. **Direct-Save model (audit v2 revision):** the Drive commit path persists arms/cancels via the runtime's OWN `jobStore` inside the tx — `scheduler.Schedule` is NEVER called on the commit path (it would fail `ErrSchedulerClosed` during the ADR-0133 shutdown drain and roll back healthy commits; and injected schedulers never get the store registered). The scheduler is used post-commit only (`Activate`/`Deactivate`, both log-and-continue). `Activate` and the engine's `ScheduleJob` are **upserts by job id** (remove-then-add, carrying forward today's replace semantics) — load-bearing for repeated rehydration and elector failover.

---

### Task 1: Mechanical unification + rename (compile-preserving)

**Files:**
- Rename tree: `scheduling/` → `scheduler/` (package `scheduling` → `scheduler` in every file)
- Move tree: `internal/scheduling/gocron/**` → `scheduler/internal/gocron/**` (**package name `gocron` stays** — `gocronsched` is only the import ALIAS used by the façade; do NOT rename the package)
- Create: `scheduler/internal/obs/obs.go` + `scheduler/internal/obs/obs_test.go` (observability shim)
- Move: `scheduling/processdriver_e2e_test.go` → `runtime/processdriver_scheduler_e2e_test.go` (package `runtime_test`)
- Modify: every importer of `github.com/kartaladev/wrkflw/scheduling` and `.../internal/scheduling/gocron` (runtime, examples, service, transport if any — find with grep)
- Delete: `internal/scheduling/` (whole tree, after move)

**Interfaces:**
- Consumes: existing `kernel.Scheduler` signature — UNCHANGED in this task.
- Produces: import path `github.com/kartaladev/wrkflw/scheduler` exposing the exact same API `scheduling` had (`NewScheduler`, `Scheduler` concrete, `Option`, `WithClock/WithLogger/WithTracerProvider/WithMeterProvider/WithTimeSkew/WithLocker/WithElector/WithJobStore`, `Locker`, `Elector`, `ErrSchedulerClosed`, `ErrTimerLockElectorConflict`, `backend/{postgres,mysql}`). Behaviour byte-identical; ALL existing tests pass unmodified except import paths.

- [ ] **Step 1: Baseline green.** Run: `go build ./... && go test ./scheduling/... ./internal/scheduling/... ./runtime/...` → PASS. Record output.
- [ ] **Step 2: git-mv the trees.**

```bash
git mv scheduling scheduler
mkdir -p scheduler/internal
git mv internal/scheduling/gocron scheduler/internal/gocron
rmdir internal/scheduling
```

- [ ] **Step 3: Rename package + rewrite imports (mechanical).**

```bash
# package clause in the renamed root tree
grep -rl '^package scheduling' scheduler --include='*.go' | xargs sed -i '' 's/^package scheduling$/package scheduler/; s/^package scheduling_test$/package scheduler_test/'
# all import paths, repo-wide
grep -rl 'kartaladev/wrkflw/internal/scheduling/gocron' --include='*.go' . | xargs sed -i '' 's#kartaladev/wrkflw/internal/scheduling/gocron#kartaladev/wrkflw/scheduler/internal/gocron#g'
grep -rl '"github.com/kartaladev/wrkflw/scheduling' --include='*.go' . | xargs sed -i '' 's#github.com/kartaladev/wrkflw/scheduling#github.com/kartaladev/wrkflw/scheduler#g'
# qualified identifiers scheduling.X → scheduler.X (check each hit — do NOT blind-replace strings/comments mentioning "scheduling" the concept)
grep -rn 'scheduling\.' --include='*.go' .
```

Then fix remaining `scheduling.` qualifiers by hand — known sites: `runtime/processdriver.go:119,184-201` (`scheduling.Scheduler`, `.Option`, `.NewScheduler`, `.WithJobStore`, `.WithClock`...), **`persistence/scheduler_locker.go:39,52,68,82,112` (PUBLIC API — `NewSchedulerLocker` returns `scheduling.Locker`; its import path changes → CHANGELOG entry)**, `persistence/scheduler_locker_test.go`, `runtime/jobstore_rehydrate_durable_test.go`, `runtime/jobstore_unresolved_test.go`, `runtime/processdriver_shutdown_test.go`, doc refs in `runtime/kernel/errors_construct.go:15-16`, and examples. Update doc comments that name the package.

**Intentional behaviour note:** the telemetry scope string `observability.New("github.com/kartaladev/wrkflw/scheduling", …)` at (post-move) `scheduler/internal/gocron/scheduler.go:208` IS renamed by the sed to `…/wrkflw/scheduler` — an intentional instrumentation-scope rename shipping with the package rename (check `scheduler_logger_test.go` for assertions on the old string and update).

- [ ] **Step 4: Sever `internal/observability` from the engine.** Create `scheduler/internal/obs/obs.go` — port ONLY what `scheduler/internal/gocron/scheduler.go` uses (`Option`, `WithLogger`, `WithTracerProvider`, `WithMeterProvider`, `Telemetry` struct with `Logger *slog.Logger`, `Tracer trace.Tracer`, `Meter metric.Meter`, ctor `New(component string, opts ...Option) Telemetry`, plus the meter-scoped instrument helpers the Telemetry exposes — copy the used subset verbatim from `internal/observability`, including `Int64Counter`/`Float64Histogram` helpers needed by Task 13). Point `scheduler/internal/gocron/scheduler.go:21` at `github.com/kartaladev/wrkflw/scheduler/internal/obs`. Copy the corresponding focused tests.
- [ ] **Step 5: Relocate the e2e test.** `git mv scheduler/processdriver_e2e_test.go runtime/processdriver_scheduler_e2e_test.go`; change its package clause to `runtime_test`; fix imports.
- [ ] **Step 6: Green verification.** Run: `go build ./... && go test ./...`
Expected: PASS everywhere (this task changes zero behaviour). If a test fails, the rename leaked semantics — fix before proceeding.
- [ ] **Step 7: Lint.** Run: `golangci-lint run ./...` → clean.
- [ ] **Step 8: Commit (own commit — NOT the bundle).**

```bash
git add -A
git commit -m "refactor(scheduler): unify scheduling trees into scheduler/ (compile-preserving)

Rename scheduling→scheduler (D12, ADR-0134), relocate
internal/scheduling/gocron→scheduler/internal/gocron, sever
internal/observability behind scheduler/internal/obs, move the driver
e2e test to runtime/. Zero behaviour change; old kernel.Scheduler
surface intact."
```

---

### Task 2: gocron pin bump v2.21.2 → v2.22.0 (ADR-0135)

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `CLAUDE.md` (Tech Stack row: `pinned to v2.21.2` → `pinned to v2.22.0`)
- Test: `scheduler/internal/gocron/bump_regression_test.go` (new)

**Interfaces:** none new — regression-locks existing behaviour under the new pin.

- [ ] **Step 1: RED — write the regression lock first.** Create `scheduler/internal/gocron/bump_regression_test.go` (**package `gocron_test`** — matching the tree's existing external tests) asserting the two behaviours ADR-0134 depends on, driven through the EXISTING engine API with a fake clock:

```go
package gocron_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/kartaladev/wrkflw/definition/schedule"
	gocronsched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// TestBumpRegression_OneShotFiresExactlyOnce locks the WithLimitedRuns(1)
// semantics of a one-shot timer across the v2.22.0 bump: exactly one fire,
// and the job is gone afterwards (NextRun reports false).
func TestBumpRegression_OneShotFiresExactlyOnce(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s, err := gocronsched.NewGocronScheduler(gocronsched.WithClock(clk))
	if err != nil {
		t.Fatalf("NewGocronScheduler: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	fired := make(chan struct{}, 2)
	if _, err := s.Schedule(t.Context(), "t1", schedule.AfterDuration(time.Minute), func() { fired <- struct{}{} }); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	clk.Advance(time.Minute + time.Second)
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("one-shot did not fire")
	}
	clk.Advance(time.Hour)
	select {
	case <-fired:
		t.Fatal("one-shot fired twice")
	case <-time.After(200 * time.Millisecond):
	}
	if _, ok := s.NextRun("t1"); ok {
		t.Fatal("consumed one-shot still pending")
	}
}
```

(Adapt channel/wait details to the timing style used in `scheduler/internal/gocron/scheduler_test.go` — mirror its established fake-clock synchronization idiom. **The final `NextRun("t1")==false` assertion is BINDING on that idiom**: consumed-job cleanup runs via an async `AfterJobRuns` listener, so poll with an eventually-style wait, never a bare immediate check.)
- [ ] **Step 2: Verify it passes on v2.21.2 first** (it locks CURRENT behaviour): `go test -run TestBumpRegression ./scheduler/internal/gocron/` → PASS. (This is a characterization test, not a red-cycle symbol — the "red" equivalent is proving it guards on the OLD pin.)
- [ ] **Step 3: Bump.** Run: `go get github.com/go-co-op/gocron/v2@v2.22.0 && go mod tidy`
- [ ] **Step 3b: ADR-0135 verification gate.** The ADR's fix inventory was researched offline (plausible-unverified). Now that the module is fetched, verify each claim against the source: read `$(go env GOMODCACHE)/github.com/go-co-op/gocron/v2@v2.22.0/` — confirm the skip-budget behaviour (`limitRunsTo` handling on skipped runs), `ErrSchedulerBusy`, typed `LimitMode`, and that `MonitorStatus`/EventListener signatures are unchanged. Record the outcome in ADR-0135 (flip the verification-gate note to "verified at bump time" or amend the claims). **Do not proceed to the singleton default (Task 6) with an unverified skip-budget claim.**
- [ ] **Step 4: Full verification under the new pin.** Run: `go test ./scheduler/... ./runtime/... ./processtest/...`
Expected: PASS (incl. the new regression test and the existing timeskew tests; singleton-overrun and Monitor/EventListener verification are deferred to Tasks 6/13 where those behaviours first ship).
- [ ] **Step 5: Update the CLAUDE.md pin row** (`**pinned to v2.21.2**` → `**pinned to v2.22.0**`, note "ADR-0135").
- [ ] **Step 6: Lint + commit (own commit).**

```bash
golangci-lint run ./...
git add -A
git commit -m "chore(deps): bump gocron to v2.22.0 (ADR-0135)

One-shot/limited-runs + singleton regression-locked before the bump.
Bundles ADR-0135."
```

---

### Task 3: `scheduler.Trigger` — own vocabulary + pure `Next` (red-first)

**Files:**
- Create: `scheduler/trigger.go`
- Test: `scheduler/trigger_test.go` (package `scheduler_test`)
- Modify: `go.mod` (`robfig/cron/v3` becomes a direct dependency)

**Interfaces:**
- Produces (used by Tasks 4–7, 10):

```go
type ClockTime struct{ Hour, Minute, Second uint }
type Trigger struct{ /* unexported */ }
func At(t time.Time) Trigger
func After(d time.Duration) Trigger
func Every(d time.Duration) Trigger
func EveryRandom(min, max time.Duration) Trigger
func Cron(expr string) Trigger
func Daily(interval uint, at ...ClockTime) Trigger
func Weekly(interval uint, days []time.Weekday, at ...ClockTime) Trigger
func Monthly(interval uint, days []int, at ...ClockTime) Trigger
func (t Trigger) IsZero() bool
func (t Trigger) Recurring() bool
func (t Trigger) Next(after time.Time) (time.Time, bool)
// package-internal accessors for the gocron engine (mirror schedule.TriggerSpec's):
// AbsTime() / Duration() / Random() / CronExpr() / Calendar() — exported, same shapes as definition/schedule
```

- [ ] **Step 1: RED — trigger_test.go.** Table test (assert-closure form) covering every constructor's `Next`: `At` future/past, `After`, `Every`, `EveryRandom` (earliest bound `after+min`), `Cron("0 9 * * 1-5")` next weekday 09:00, `Daily(1, ClockTime{9,0,0})`, `Weekly(1, []time.Weekday{time.Monday}, ClockTime{9,0,0})`, `Monthly(1, []int{15}, ClockTime{9,0,0})`, zero Trigger → `ok=false`, `Recurring()` per kind, `IsZero`. Use fixed `after := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)` anchors and exact expected instants.

```go
func TestTrigger_Next(t *testing.T) {
	after := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC) // a Wednesday
	tests := []struct {
		name   string
		trig   scheduler.Trigger
		assert func(t *testing.T, next time.Time, ok bool)
	}{
		{
			name: "at future returns the instant",
			trig: scheduler.At(after.Add(time.Hour)),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if !ok || !next.Equal(after.Add(time.Hour)) {
					t.Fatalf("next=%v ok=%v", next, ok)
				}
			},
		},
		{
			name: "at past reports no future fire",
			trig: scheduler.At(after.Add(-time.Hour)),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name: "cron weekday morning",
			trig: scheduler.Cron("0 9 * * 1-5"),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		// ... one case per remaining constructor + zero Trigger ...
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, ok := tt.trig.Next(after)
			tt.assert(t, next, ok)
		})
	}
}
```

- [ ] **Step 2: Verify RED.** Run: `go test ./scheduler/` → FAIL `undefined: scheduler.Trigger` (etc.).
- [ ] **Step 3: GREEN — implement `scheduler/trigger.go`.** Struct mirrors `definition/schedule.TriggerSpec` fields (kind, dur, at, cron, min/max, interval, atTimes, weekdays, days) with an unexported `kind` enum (`triggerUnset`, `triggerAt`, `triggerAfter`, `triggerEvery`, `triggerEveryRandom`, `triggerCron`, `triggerDaily`, `triggerWeekly`, `triggerMonthly`). `Next` per the locked semantics (naming decision 3); cron via `cron.ParseStandard(expr)` then `sched.Next(after)`; calendar shapes via a small pure helper that scans forward from `after` to the next matching (weekday/day-of-month, atTime) instant in UTC. Godoc every exported symbol (this is public library API — write a testable `Example` too, `scheduler/example_trigger_test.go` can fold into Step 1's file or Task 14).
- [ ] **Step 4: Verify GREEN.** Run: `go test ./scheduler/` → PASS. Run `go mod tidy` (robfig promoted to direct) and re-run.
- [ ] **Step 5: Create the feature-bundle commit** (first feature task):

```bash
git add -A
git commit -m "feat(scheduler): scheduler-owned durable jobs (ADR-0134)

Design bundle: spec v2 + ADR-0134 + ADR-0027/0102 banners + plan.
Implementation folded in task-by-task (amended)."
```

---

### Task 4: Job vocabulary — `JobKind`, `ActivationType`, `DataProvider`, `JobFunc`, `Job`, `ScheduledJob`, constructors, sentinels (red-first)

**Files:**
- Create: `scheduler/job.go`, `scheduler/dataprovider.go`, `scheduler/errors.go`
- Test: `scheduler/job_test.go`, `scheduler/dataprovider_test.go`
(The kernel-side sentinel deletions happen in Task 7's switchover; this task only ADDS the scheduler-side sentinels in `scheduler/errors.go`.)

**Interfaces:**
- Produces (exact shapes — Tasks 5–11 depend on these):

```go
type JobKind string
type ActivationType int
const (
	ActivationAuto ActivationType = iota
	ActivationManual
)
type DataProvider interface {
	Get(ctx context.Context) (map[string]any, error)
	Static() bool
}
type JobFunc = func(ctx context.Context, data DataProvider) error
type Job interface {
	ID() string
	Kind() JobKind
	Activation() ActivationType
	Trigger() Trigger
	Action() JobFunc
	Data() DataProvider
}
type ScheduledJob interface {
	Job
	NextRun() time.Time
}
type JobOption func(*jobConfig)
func WithManualActivation() JobOption
// WithoutOverrunProtection opts a recurring job out of the default singleton
// mode (production item ③): its fires may overlap. No effect on one-shots.
func WithoutOverrunProtection() JobOption
// (job carries unexported singleton() bool — true by default for recurring
// triggers; the façade passes it to the engine's ScheduleJob singleton flag.)
func NewJob(kind JobKind, trig Trigger, fn JobFunc, data DataProvider, opts ...JobOption) (Job, error)
func NewJobWithID(id string, kind JobKind, trig Trigger, fn JobFunc, data DataProvider, opts ...JobOption) (Job, error)
func NewScheduledJob(j Job, nextRun time.Time) (ScheduledJob, error)
func NewStaticDataProvider(m map[string]any) DataProvider
func NewEmptyDataProvider() DataProvider
var ErrJobNotFound = errors.New("workflow-scheduler: job not found")
var ErrUnresolvedTimerDefinitions = errors.New("workflow-scheduler: some scheduled jobs reference unresolved definitions")
var ErrUnsupportedTrigger = errors.New("workflow-scheduler: unsupported trigger")
```

- [ ] **Step 1: RED — job_test.go.** Cases: `NewJob` auto-generates a non-empty UUID-string id and defaults `ActivationAuto`; `WithManualActivation` flips it; `NewJob` errors on empty kind / zero trigger / nil fn / nil provider (each its own case, `errors.Is`-checkable or message-prefixed `workflow-scheduler:`); `NewJobWithID("")` errors; `NewScheduledJob(nil, ...)` errors; `NewScheduledJob` round-trips `NextRun`.
- [ ] **Step 2: Verify RED.** `go test ./scheduler/` → FAIL (undefined symbols).
- [ ] **Step 3: GREEN — implement** unexported `job`/`scheduledJob` structs behind the constructors. UUID via `github.com/google/uuid` (already a direct dependency).
- [ ] **Step 4: Verify GREEN.** `go test ./scheduler/` → PASS.
- [ ] **Step 5: RED then GREEN — dataprovider_test.go.** `NewStaticDataProvider` returns a COPY (mutating the returned map does not affect subsequent Gets), `Static()==true`; `NewEmptyDataProvider().Get` → empty non-nil map. Run red (undefined), implement, run green.
- [ ] **Step 6: Amend the bundle.** `git add -A && git commit --amend --no-edit`

---

### Task 5: `JobStore` port + kind-routed registration option (red-first, PURELY ADDITIVE)

**Files:**
- Create: `scheduler/jobstore.go`
- Test: `scheduler/jobstore_test.go`
- Modify: `scheduler/scheduler.go` — ADD `config.jobStores map[JobKind]func() JobStore` and the kind-keyed option under the TEMPORARY name `WithKindJobStore` (Go cannot overload; the name flips to `WithJobStore` in Task 7). **Do NOT touch the existing `WithJobStore(provider func() kernel.JobStore)` option, the `config.jobStoreProvider` field, or the `rehydrate()` path** — live consumers (`runtime/processdriver.go:194`, `runtime/jobstore_rehydrate_durable_test.go:69-71`, `runtime/jobstore_unresolved_test.go:163-165`, `examples/scenarios/timer_durability/main.go:142-146,207-211`, and `rehydrate()` itself at façade `:348`) keep compiling until Task 7's movement deletes/renames everything at once.

**Interfaces:**
- Produces:

```go
type JobStore interface {
	Load(ctx context.Context) ([]ScheduledJob, error)
	Save(ctx context.Context, j ScheduledJob) error
	Delete(ctx context.Context, id string) error
}
func WithKindJobStore(kind JobKind, provide func() JobStore) Option // renamed → WithJobStore in Task 7
```

- Consumes: Task 4 types. The tree compiles and ALL tests pass at this task's boundary (`go build ./...` is part of the gate).

- [ ] **Step 1: RED.** Test that `WithKindJobStore("wrkflw.timer", thunk)` records the thunk keyed by kind (expose the map via `scheduler/export_test.go`), that a nil thunk is ignored, and that registering the same kind twice keeps the LAST registration.
- [ ] **Step 2: Verify RED** (`go test ./scheduler/` → compile fail). **Step 3: GREEN** (port + option, additive only). **Step 4: verify PASS — whole tree:** `go build ./... && go test ./scheduler/... ./runtime/...`.
- [ ] **Step 5: Amend the bundle.**

---

### Task 6: gocron engine — schedule `Job`s natively (trigger rewrite, zero-param task, singleton default) (red-first)

**Files:**
- Create: `scheduler/internal/gocron/job_schedule.go` (new Job-based entry), rewrite `scheduler/internal/gocron/trigger.go` → mapping from `scheduler.Trigger` (NOT `schedule.TriggerSpec`)
- Test: `scheduler/internal/gocron/job_schedule_test.go`, extend `trigger_test.go`
- Modify: `scheduler/internal/gocron/scheduler.go` — add `ScheduleJob`, `RemoveJob` alongside the old `Schedule` (old stays until Task 7)

**Interfaces:**
- Produces (consumed by Task 7's façade):

```go
// package gocron (the engine package's real name; aliased gocronsched by importers)
// ScheduleJob registers the job (any activation — the caller decides WHEN to
// call this; the engine always arms). UPSERT BY ID: an existing registration
// under the same id is removed first (carries forward today's remove-then-add
// at scheduler.go:225-228) — load-bearing for repeated rehydration.
// Returns the live first-run time.
func (s *GocronScheduler) ScheduleJob(ctx context.Context, id string, trig TriggerDef, task func(context.Context) error, singleton bool) (time.Time, error)
// jobDefinition maps a TriggerDef → gocron.JobDefinition (+oneShot flag).
func jobDefinition(t TriggerDef, now time.Time) (gocron.JobDefinition, bool, error)
```

**Import direction note:** `scheduler/internal/gocron` must NOT import the parent `scheduler` package (cycle — the parent imports the engine); that is why `ScheduleJob` takes decomposed values. `TriggerDef` is the engine-local mirror struct (exported, in `scheduler/internal/gocron/trigger.go`, same fields the old mapping consumed — mirroring today's `schedule.TriggerSpec` accessors AbsTime/Duration/Random/CronExpr/Calendar). The façade (parent) converts `scheduler.Trigger` → `gocronsched.TriggerDef` and wraps `j.Action()`/`j.Data()` into the zero-param `func(ctx) error` closure (D5/C1).

- [ ] **Step 1: RED — job_schedule_test.go.** Fake clock; `ScheduleJob` with a one-shot `TriggerDef` fires the closure once with a live ctx; **upsert-by-id**: a second `ScheduleJob` under the same id replaces the first (only the second closure ever fires — the double-Activate/rehydrate-repeat guarantee); `singleton=true` recurring job with a slow task does not overlap (counter + gate channel: two due fires, one running → second rescheduled, not parallel); an invalid `TriggerDef` (zero) returns an error wrapping the engine's unsupported-trigger error.
- [ ] **Step 2: Verify RED.** `go test ./scheduler/internal/gocron/` → compile fail.
- [ ] **Step 3: GREEN.** Implement `TriggerDef` + rewrite `jobDefinition` over it (keep the old `schedule.TriggerSpec` overload as `jobDefinitionSpec` temporarily — the old `Schedule` still uses it until Task 7). `ScheduleJob` = remove-existing-by-id first (today's `:225-228` semantics), then `gocron.NewTask(task)` zero-param + `WithName(id)` + `WithSingletonMode(gocron.LimitModeReschedule)` when singleton + the existing timeskew/one-shot handling. `RemoveJob(id)` = today's `Cancel` internals.
- [ ] **Step 4: Verify GREEN**, then run the WHOLE engine package: `go test ./scheduler/internal/gocron/` → PASS (old tests untouched).
- [ ] **Step 5: Amend the bundle.**

---

### Task 7: Scheduler surface switchover — ONE movement (façade + port + runtime + doubles)

This is the M7 "interface reshape in one movement": intermediate steps will not compile; the task ends green with the OLD `kernel.Scheduler` surface deleted everywhere. Read the whole task before starting.

**Files:**
- Modify: `scheduler/scheduler.go` — concrete type renamed `NativeScheduler`; new methods `Schedule(ctx, Job) (ScheduledJob, error)`, `Activate(ctx, ScheduledJob) error`, `Deactivate(ctx, id string) error`, `Cancel(ctx, id string) error`, `Scheduled(ctx, id string) (ScheduledJob, error)`, `List(ctx) iter.Seq[ScheduledJob]`; DELETE old `Schedule(ctx, timerID, trig, fire)`, `Cancel(ctx, id)` void, `NextRun(id)`; rehydration = `Load` on every registered store then `Activate` each.
- Create: `scheduler/port.go` — `type Scheduler interface { Schedule…; Activate…; Deactivate…; Cancel…; Scheduled…; List… }` + `var _ Scheduler = (*NativeScheduler)(nil)`.
- Delete from `runtime/kernel`: `scheduler.go` (old port + `ErrUnsupportedTrigger`), `JobSpec`→ stays (Task 9 extends it), `ScheduledJob`/`JobStore` in `timerstore.go:107-121`, `ErrUnresolvedTimerDefinitions` in `errors_construct.go`.
- Modify: `runtime/processdriver.go` (field `sched scheduler.Scheduler`, `ownedScheduler *scheduler.NativeScheduler`, default construction, `isNilScheduler`), `runtime/processdriver_options.go:81` (`WithScheduler(sched scheduler.Scheduler)`), `runtime/timerops.go` (`armTimer`/`armStartTimer` build Jobs; `timerFireFunc` unchanged; `RehydrateTimers` via Load+Activate), `runtime/timerjob.go` NEW (`timerJob`/`scheduledTimerJob`/`newScheduledTimerJob` — the concrete types below), `runtime/jobstore.go` (re-shape to the new port: `Load` produces Manual `timerJob`s; **explicit no-op `Save`/`Delete` stubs** — real writes land in Task 10), `runtime/processdriver_action.go` ScheduleTimer/CancelTimer cases (keep for now — interim: build a Manual `timerJob` + post-commit `Activate` only, NO `Schedule` call; Task 11 deletes them), `runtime/rehydrate_durable_test.go:107` (`NextRun` → `Scheduled(ctx,id)`), `processtest/memscheduler_test.go` (old `kernel.ErrUnsupportedTrigger` refs), `scheduler/internal/gocron/bump_regression_test.go` (port to `ScheduleJob`/`TriggerDef`; delete `jobDefinitionSpec`).
- Rewrite: `processtest/memscheduler.go` (implements `scheduler.Scheduler`; keeps `Tick`, `NextFireAt`, `Pending` — all keying by engine timer id, unchanged for the Harness; gains `RegisterJobStore(kind, store)`; Manual jobs held UNARMED until `Activate` — mirroring the no-pen library semantics: `Scheduled`/`List` show armed only), `runtime/internal/runtimetest/doubles.go` `RecordingScheduler` (records `Job`s: `Scheduled bool`, `FireAt time.Time` from `j.Trigger().Next(clk.Now())`).
- Modify: `runtime/timerops.go` start-timer path → `startTimerJobKind` non-durable `ActivationAuto` jobs (`runtime/event_start.go` is pure def-scanning — likely no-op, verify).
- Modify: `examples/scenarios/timer_durability/main.go` and the other example mains that break (full list in Task 14).

**Interfaces:**
- Consumes: Tasks 3–6 (Trigger, Job vocabulary, JobStore port, engine `ScheduleJob`/`TriggerDef`).
- Produces: `scheduler.Scheduler` port (final shape, above); runtime `timerJob` concrete: 

```go
// runtime (unexported)
type timerJob struct {
	spec kernel.JobSpec      // typed descriptor (Task 9 adds Kind field)
	trig scheduler.Trigger
	fn   scheduler.JobFunc
	data scheduler.DataProvider
}
func (j *timerJob) ID() string                              { return j.spec.TimerID }
func (j *timerJob) Kind() scheduler.JobKind                 { return timerJobKind }
func (j *timerJob) Activation() scheduler.ActivationType    { return scheduler.ActivationManual }
func (j *timerJob) Trigger() scheduler.Trigger              { return j.trig }
func (j *timerJob) Action() scheduler.JobFunc               { return j.fn }
func (j *timerJob) Data() scheduler.DataProvider            { return j.data }
func (j *timerJob) descriptor() kernel.JobSpec              { return j.spec }
// scheduledTimerJob wraps timerJob + nextRun for the ScheduledJob shape;
// built via newScheduledTimerJob(j *timerJob) *scheduledTimerJob, which
// computes nextRun = j.trig.Next(now) (Task 11 calls it post-commit).
```

plus the `TriggerSpec → scheduler.Trigger` converter:

```go
// convertTrigger maps a resolved schedule.TriggerSpec to the scheduler's own
// vocabulary. Total over all 10 schedule.Kind values: Unset/Expr/EveryExpr are
// programming errors (engine resolves them before arming) returned as
// fmt.Errorf("workflow-runtime: convert trigger: %w: kind %v", scheduler.ErrUnsupportedTrigger, k).
func convertTrigger(t schedule.TriggerSpec) (scheduler.Trigger, error)
```

- [ ] **Step 1: RED — converter test first** (`runtime/timerops_convert_test.go`): table over ALL 10 kinds — 7 executable kinds assert value-equivalence via `Trigger.Next` on a fixed anchor (e.g. converted `schedule.At(x)` and `scheduler.At(x)` produce equal `Next`); `KindUnset/KindExpr/KindEveryExpr` assert `errors.Is(err, scheduler.ErrUnsupportedTrigger)`. Run: `go test ./runtime/ -run TestConvertTrigger` → FAIL (undefined).
- [ ] **Step 2: RED — port-shape tests.** `scheduler/scheduler_surface_test.go`: with a registered fake JobStore (records calls), `Schedule` of an Auto job persists (Save called) AND arms (fires on clock advance); `Schedule` of a **Manual job persists but does NOT arm and leaves NO scheduler record** (no fire on advance; `Scheduled(ctx,id)` → `ErrJobNotFound`; `List` does not include it — the returned ScheduledJob value carries `NextRun == trig.Next(now)` for the CALLER only); `Activate` arms it (fires) and is an **upsert by id** (double-`Activate` never duplicates — one fire per due instant); `Deactivate` disarms without `Delete`; `Cancel` calls `Delete` + disarms, unknown id → nil; `Scheduled` unknown id → `errors.Is(err, scheduler.ErrJobNotFound)`; `List` yields all armed; a job of an UNREGISTERED kind schedules in-memory only, **no WARN logged** (use a `slog` recorder handler); rehydration: store `Load` returns two ScheduledJobs → both armed after `Start`, and a SECOND `Start`/rehydrate does not duplicate them; `Load` error wrapping `ErrUnresolvedTimerDefinitions` → non-fatal WARN; rehydration I/O runs with a background-derived ctx (never a caller tx ctx). These are the package's hot paths — write them ALL now.
- [ ] **Step 3: Verify RED.** `go test ./scheduler/` → compile fail.
- [ ] **Step 3b: RED — doubles + runtime job types.** Two focused red cycles BEFORE the movement (CLAUDE.md forbids batching new symbols without observable reds): (i) extend `processtest/memscheduler_test.go` against the NEW surface — Manual job held unarmed until `Activate`, `RegisterJobStore(kind, store)` routing, `Tick` fires only activated jobs, `NextFireAt` enumerates armed jobs (via the same bookkeeping `List` uses), `Pending` keys by engine timer id; run → compile fail (new methods undefined). (ii) add `runtime/timerjob_test.go` — `timerJob` satisfies `scheduler.Job` with `ActivationManual` + `timerJobKind`, `newScheduledTimerJob` computes `NextRun = trig.Next(now)`, `descriptor()` round-trips the `kernel.JobSpec`; run → compile fail. (`RecordingScheduler` is a pure re-shape riding existing suites — no separate red needed.)
- [ ] **Step 4: GREEN — the movement.** In order: (a) façade rename `Scheduler`→`NativeScheduler` + the six new methods over the engine (`ScheduleJob` for arm; `Scheduled`/`List` read the engine's armed state — there is NO manual pen: Manual jobs leave no record until `Activate`); rename `WithKindJobStore`→`WithJobStore` (deleting the old thunk option + `config.jobStoreProvider`); rehydrate = `Load` on every registered store + `Activate` each, pinned to `Start` (lazy trigger, if kept, uses `context.Background()`); rename the `workflow-scheduling:` sentinels to `workflow-scheduler:`; (b) delete old kernel port symbols (`kernel.Scheduler`, `kernel.JobStore`, `kernel.ScheduledJob`, `kernel.ErrUnsupportedTrigger`, `kernel.ErrUnresolvedTimerDefinitions`); (c) runtime re-point (fields, options; **interim arm semantics — direct to Activate**: `armTimer` builds a Manual `timerJob` and calls `sched.Activate(ctx, newScheduledTimerJob(j))` post-commit — no `Schedule` call, durably identical to today because the fused AppliedStep path still owns the rows; runtime `jobStore` reshaped to the new port with **explicit no-op `Save`/`Delete` stubs** (real writes land in Task 10) + `Load` producing Manual `timerJob`s); (d) start-timer path → `startTimerJobKind` Auto jobs via `Schedule`; (e) doubles rewrites (MemScheduler per Step 3b(i); `RecordingScheduler` records the Job's `Trigger().Next(clk.Now())`); (f) fixes: `runtime/rehydrate_durable_test.go:107` (`NextRun` → `Scheduled(ctx,id)`), `processtest/memscheduler_test.go` old `kernel.ErrUnsupportedTrigger` refs, port `scheduler/internal/gocron/bump_regression_test.go` to `ScheduleJob`/`TriggerDef` + delete `jobDefinitionSpec`, e2e/example call-site updates (11 example mains — see Task 14 list). (`runtime/event_start.go` is pure def-scanning — likely no-op; verify.)
- [ ] **Step 5: Verify GREEN — full repo.** Run: `go build ./... && go test ./...`
Expected: PASS. This is the task's exit gate; nothing may stay red.
- [ ] **Step 6: Lint.** `golangci-lint run ./...` → clean.
- [ ] **Step 7: Amend the bundle.**

---

### Task 8: `TxRunner.RunInTx` on the instance stores (red-first)

**Files:**
- Create: `runtime/kernel/txrunner.go` (port), `internal/persistence/store/txrunner.go` (SQL impl)
- Modify: `runtime/kernel/memstore.go` (Mem impl — sequencing-only), `internal/database/transaction/begin.go` (expose the rollback-only state, e.g. `func RollbackOnly(ctx context.Context) bool` reading the ambient handle — needed because a joined participant's `Rollback` marks rollback-only and the owner `Commit` then rolls back returning nil), **`persistence/caching_instance_store.go` (forward the capability — audit v2 BLOCKER)**
- Test: `runtime/kernel/txrunner_test.go`, `internal/persistence/store/txrunner_test.go` (SQL rollback parity via `dbtest.RunTestDatabase` + `RunTestSQLite`), `persistence/caching_instance_store_test.go` additions

**Interfaces:**
- Produces:

```go
// package kernel
// TxRunner is an optional capability an InstanceStore may implement (like
// Notifier/Locker): RunInTx runs fn inside one store transaction; every
// JoinOrBegin-aware write invoked with txCtx joins it. fn's error rolls the
// whole unit back. Mem stores provide sequencing only (no rollback) — see
// ADR-0134; rollback-parity guarantees are SQL-only.
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(txCtx context.Context) error) error
}
```

- SQL impl (consumed by Task 11):

```go
// package store
func (s *Store) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	q, txCtx, err := transaction.Begin(ctx, s.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: run in tx: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()
	if err := fn(txCtx); err != nil {
		return err
	}
	// Rollback-only detection (audit v2-A6): a joined participant's Rollback marks
	// the unit rollback-only; the owner Commit would then roll back and return
	// nil. Surface that as an error — success must mean COMMITTED.
	if transaction.RollbackOnly(txCtx) {
		return ErrTxRolledBack // "workflow-store: run in tx: rolled back by participant"
	}
	if err := q.Commit(ctx); err != nil {
		return s.mapConflict(fmt.Errorf("workflow-store: run in tx: commit: %w", err))
	}
	committed = true
	return nil
}
```

- CachingInstanceStore forwarding (audit v2 BLOCKER — without it, the type-assert fails on the ADR-0099 recommended production wrapper and atomicity silently degrades):

```go
// package persistence
// RunInTx forwards the TxRunner capability to the inner store when it has one.
// On any error/rollback the touched instances are EVICTED (never put): a
// put-after-commit inside a tx that later rolls back would poison the cache.
func (c *CachingInstanceStore) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	tx, ok := c.inner.(kernel.TxRunner)
	if !ok {
		return fn(ctx) // inner store without the capability: degraded, same as direct use
	}
	err := tx.RunInTx(ctx, fn)
	if err != nil {
		c.evictTouched(ctx) // instances written through the wrapper during fn
	}
	return err
}
```

(Adapt eviction bookkeeping to the wrapper's actual internals — read `persistence/caching_instance_store.go` first; the invariant to enforce: after a rolled-back `RunInTx`, a `Load` through the wrapper returns the PRE-tx state, never a cached in-tx value.)

- [ ] **Step 1: RED — SQL parity test.** `internal/persistence/store/txrunner_test.go`: on a real Postgres (`dbtest.RunTestDatabase`), `RunInTx(fn)` where fn calls `store.Create(txCtx, step)` then returns an injected error → assert NO `wrkflw_instances` row exists after (rollback), and the success path commits the row. Add the **rollback-only case**: fn performs a joined write then calls the joined querier's `Rollback` and returns nil → `RunInTx` returns `ErrTxRolledBack` (never nil). Same shape for SQLite (`dbtest.RunTestSQLite`). Run → FAIL `undefined: RunInTx`.
- [ ] **Step 2: Verify RED.** **Step 3: GREEN** (code above + `transaction.RollbackOnly` + Mem: `func (s *MemInstanceStore) RunInTx(ctx, fn) error { return fn(ctx) }` with the sequencing-only doc). **Step 4: verify PASS** (`go test ./internal/persistence/store/ ./runtime/kernel/ ./internal/database/...`).
- [ ] **Step 5: RED then GREEN — rollback-through-wrapper.** `persistence/caching_instance_store_test.go`: wrap a SQL store; `RunInTx` commits a step through the WRAPPER then fn returns an error → rollback; assert a subsequent `Load` through the wrapper returns the pre-tx state (cache evicted, not poisoned) and the capability assert `_, ok := any(wrapper).(kernel.TxRunner)` holds. Run red (undefined `RunInTx` on wrapper), implement, run green.
- [ ] **Step 6: Amend the bundle.**

---

### Task 9: Store-side `TimerWriter` capability + `JobSpec.Kind` (red-first)

**Files:**
- Modify: `runtime/kernel/timerstore.go` — add `Kind engine.TimerKind` to `JobSpec` (:93); define the capability:

```go
// TimerWriter is the write-side capability a TimerStore MAY implement. It is
// type-asserted off the store supplied via WithTimerStore. Writes join an
// ambient ctx-transaction (JoinOrBegin) so the runtime JobStore can persist
// atomically with the state commit (ADR-0134; supersedes the fused
// AppliedStep write path of ADR-0027).
type TimerWriter interface {
	UpsertJob(ctx context.Context, spec JobSpec) error
	DeleteJob(ctx context.Context, instanceID, timerID string) error
}
```

- Modify: `internal/persistence/store/timerstore.go` — implement `TimerWriter` on the SQL TimerStore, reusing `upsertTimer`/`deleteTimer` SQL, building the row from `JobSpec` (`next_run`=Spec.NextRun, `kind`=int16(Spec.Kind), trigger payload via `triggerPayloadArg(Spec.Trigger)`). NOTE (audit v2 correction): `store.NewTimerStore(conn, dialect)` takes its own caller-supplied conn — nothing shares it automatically; correctness does NOT depend on same-conn because `JoinOrBegin` joins the ambient handle from ctx regardless of the conn argument. The wiring requirement is same-DATABASE.
- Modify: `runtime/kernel/timerstore.go` `MemTimerStore` — implement `TimerWriter` (Arm/Cancel mapped from JobSpec).
- Test: `internal/persistence/store/timerwriter_test.go`, `runtime/kernel/timerstore_test.go` additions.

**Interfaces:** Produces `kernel.TimerWriter` + `kernel.JobSpec.Kind` (consumed by Task 10/11). Consumes Task 8's ambient tx.

- [ ] **Step 1: RED — descriptor round-trip (all 8 columns).** On Postgres + SQLite: `UpsertJob` a JobSpec with every field set (incl. `Kind: engine.TimerDeadline` — pick a real non-zero `engine.TimerKind`), then `ListArmed` → assert instance_id, timer_id, next_run, kind, def_id, def_version and the Trigger round-trips (`Recurring`, `Kind()`, payload equality). Second case: **same-tx atomicity** — inside `store.RunInTx`, `Commit` a step AND `UpsertJob`, inject failure after → NEITHER persisted. Third: **join-by-ctx (audit v2 rewrite of the old "same-conn negative")** — construct a TimerStore over a DIFFERENT pool than the Store, run the same in-tx sequence handing it `txCtx`, inject failure → assert the foreign-conn writer's row ALSO rolled back (it JOINED the ambient handle — `JoinOrBegin` is ctx-carried and ignores its conn argument). Comment the test: it documents that atomicity composition is by ctx, and the deployment invariant is same-database wiring, not same-connection.
- [ ] **Step 2: Verify RED.** **Step 3: GREEN.** **Step 4: verify PASS** incl. `go test ./runtime/...` (JobSpec gained a field — zero-value compatible).
- [ ] **Step 5: Amend the bundle.**

---

### Task 10: Runtime `JobStore` — `Save` (type-assert) / `Delete` / `Load` (red-first)

**Files:**
- Rewrite: `runtime/jobstore.go` — `NewJobStore(driver) scheduler.JobStore` (replacing Task 7's no-op stubs with real writes); `Save` type-asserts `scheduledTimerJob`/`timerJob` → `descriptor()` → `driver.timerWriter.UpsertJob(ctx, spec)`; `Load` = today's `LoadScheduled` rebuild logic (it lives HERE in `runtime/jobstore.go:36`, not timerops.go), producing Manual `scheduledTimerJob`s with `nextRun` from the armed row. **Job ids are the ENGINE timer ids — NO composite scheme** (Global Constraint 6: `nextTimerID()` = `"<instanceID>-tm<seq>"` is already globally unique; the start-timer kind's `start-timer:…` ids pass through untouched and are never parsed). The port's `Delete(ctx, id)` deletes by `timer_id` alone (`DELETE FROM wrkflw_timers WHERE timer_id = ?` — add `DeleteJobByTimerID` to the Task 9 writer or reuse `DeleteJob` with a scan; simplest: `TimerWriter.DeleteJob(ctx, instanceID, timerID)` stays PK-exact and the port `Delete` is implemented via a small `deleteByTimerID` SQL on the writer). Drive's cancel path (Task 11) uses the runtime-internal `jobStore.deleteTimer(ctx, instanceID, timerID)` by-parts helper for PK-exact deletes — no id parsing anywhere.
- Modify: `runtime/processdriver.go` — resolve `driver.timerWriter kernel.TimerWriter` by type-asserting the `WithTimerStore` value at construction; nil when absent (Mem default wires `MemTimerStore` which now implements it); hold `driver.jobStore` as a field (Task 11 consumes it).
- Test: extend `runtime/jobstore_test.go`, `runtime/jobstore_unresolved_test.go`, `runtime/jobstore_rehydrate_durable_test.go` (port to new shapes).

**Interfaces:**
- Produces: `runtime.NewJobStore(driver *ProcessDriver) scheduler.JobStore`; internal `deleteTimer(ctx, instanceID, timerID)`; `driver.timerWriter`; `driver.jobStore`.
- Consumes: Task 7 `timerJob`/`scheduledTimerJob`, Task 9 `TimerWriter`+`JobSpec.Kind`, Task 5 port.

- [ ] **Step 1: RED.** New cases: `Save` with a foreign `scheduler.Job` implementation → typed error (`workflow-runtime: job store: unexpected job implementation`); `Save` of a `timerJob` → `MemTimerStore` row present with ALL descriptor fields (incl. Kind); `Delete` (by timer id) and internal `deleteTimer` (by parts) both remove it; `Load` on a store with two armed timers returns two Manual `ScheduledJob`s whose `ID()` equals the ENGINE timer id and whose fire closure delivers `TimerFired` (drive via processtest fakes as the existing `jobstore_test.go` does); unresolved definition → partial + `errors.Is(err, scheduler.ErrUnresolvedTimerDefinitions)`.
- [ ] **Step 2: Verify RED.** **Step 3: GREEN.** **Step 4: verify PASS** (`go test ./runtime/...`).
- [ ] **Step 5: Amend the bundle.**

---

### Task 11: Drive-flow rewire — in-tx persist, post-commit activate; delete `perform` timer cases (red-first, parity)

**Files:**
- Modify: `runtime/processdriver.go:619-659` — replace the AppliedStep-fused block:

```go
// BEFORE (retiring): timerArms/timerCancels → kernel.AppliedStep{... TimerArms, TimerCancels}
// AFTER:
var armJobs []*timerJob        // built from ScheduleTimer commands (convertTrigger + nextRun via trig.Next(now) + timerFireFunc)
var cancelIDs []cancelKey      // {instanceID, timerID} from CancelTimer + consumed TimerFired (non-recurring)
armJobs, cancelIDs = timerJobsFor(...) // evolved timerOpsFor
appliedStep := kernel.AppliedStep{State: st, Trigger: t, Events: events, CallOutcome: outcome}

// DIRECT-SAVE (Global Constraint 7): the scheduler is NEVER called inside
// commitFn — durability rides the runtime's own jobStore, so the commit
// survives the ADR-0133 shutdown-drain window and works identically with a
// consumer-injected scheduler.
var armed []*scheduledTimerJob
commitFn := func(txCtx context.Context) error {
	var err error
	if create {
		appliedStep.NewCallLink = firstCallLink
		token, err = driver.store.Create(txCtx, appliedStep)
	} else {
		token, err = driver.store.Commit(txCtx, token, appliedStep)
	}
	if err != nil {
		return err
	}
	for _, j := range armJobs { // JobStore.Save joins the SAME tx (JoinOrBegin via TimerWriter)
		sj := newScheduledTimerJob(j) // nextRun computed HERE, in-tx — the persisted value
		if serr := driver.jobStore.Save(txCtx, sj); serr != nil {
			return serr
		}
		armed = append(armed, sj)
	}
	for _, ck := range cancelIDs { // PK-exact by-parts delete; NO in-mem disarm yet
		if derr := driver.jobStore.deleteTimer(txCtx, ck.instanceID, ck.timerID); derr != nil {
			return derr
		}
	}
	return nil
}
if tx, ok := driver.store.(kernel.TxRunner); ok {
	err = tx.RunInTx(ctx, commitFn)
} else {
	err = commitFn(ctx) // store without the capability: writes each self-commit (documented degraded atomicity, same as pre-ADR-0027 Mem behaviour)
}
if err != nil { return st, fmt.Errorf("workflow-runtime: commit: %w", err) }
// Post-commit: flip in-memory state to durable truth. Activate receives the
// SAME ScheduledJob persisted in-tx (no re-anchoring of relative triggers).
for _, sj := range armed {
	if aerr := driver.sched.Activate(ctx, sj); aerr != nil {
		/* WARN log — scheduler may be closed during drain; rehydration self-heals */
	}
}
for _, ck := range cancelIDs {
	_ = driver.sched.Deactivate(ctx, ck.timerID) // engine timer id — no composite
}
```

(`create=false` reset and `firstCallLink=nil` bookkeeping stay outside `commitFn` exactly as today. `driver.jobStore` is the Task-10 runtime JobStore field. `armed` must be reset per queue iteration.)
- Modify: `runtime/timerops.go` — `timerOpsFor` → `timerJobsFor` (same derivation, builds `*timerJob`s: `TimerRetry` metric moves HERE: `if cmd.Kind == engine.TimerRetry { driver.obs.actionRetries.Add(ctx, 1) }`); DELETE `nextRunFor` (subsumed by `Trigger.Next` — the arm's `spec.NextRun = trig.Next(now)`); `armTimer` deleted (Drive owns it now); `RehydrateTimers` stays (Load+Activate).
- Modify: `runtime/timerops_internal_test.go:154` — port/delete the `nextRunFor` cases (fold into the `Trigger.Next` and `timerJobsFor` suites).
- Modify: `runtime/processdriver_action.go` — **DELETE the `ScheduleTimer` and `CancelTimer` cases** (:306-326) entirely.
- Test: `runtime/timer_txflow_test.go` (new) + existing timer e2e suites must stay green.

**Interfaces:** Consumes everything prior. Produces the final Drive semantics.

- [ ] **Step 1: RED — the hot-path tests** in `runtime/timer_txflow_test.go` (use `dbtest.RunTestSQLite` for speed, Postgres for at least one):
  1. **Same-tx atomicity:** step that arms a timer + injected `Save` failure (fault-wrap the TimerWriter) → instance state NOT advanced AND no timer row (rollback), no in-memory arm (`Scheduled` → ErrJobNotFound, `List` empty).
  2. **Create-path rollback:** StartInstance whose first step arms an immediate one-shot + injected commit failure → no instance row, no timer row, NO fire ever (advance clock, assert nothing fires — the BLOCKER-1 regression test).
  3. **Activation ordering:** a `schedule.AfterDuration(0)` (past-due) timer armed by a step — assert the fire's `Load` succeeds (instance visible), i.e. fire happens only after commit.
  4. **Rolled-back cancel:** step that cancels an armed recurring timer + injected failure after the delete → timer STILL fires on next advance (in-memory arm untouched).
  5. **Double-arm regression:** count `Save` calls per armed timer == 1 (perform-path deleted).
  6. **Arm/cancel interleave (audit v2-A4):** step A commits arm-T; concurrent step B commits cancel-T; force the post-commit order `Deactivate(T)` before `Activate(T)` (orchestrate with gates around the post-commit flips) → the phantom in-memory arm fires AT MOST once as a stale no-op, then disappears (its `TimerFired` consumption cancels it); instance state unaffected.
- [ ] **Step 2: Verify RED** (`go test ./runtime/ -run TestTimerTxFlow`).
- [ ] **Step 3: GREEN — implement the rewire** (code sketch above). Keep `syncWaiters` and the perform loop for non-timer commands unchanged.
- [ ] **Step 4: Verify GREEN + full parity.** Run: `go test ./runtime/... ./processtest/... ./examples/...` → PASS, plus the relocated `runtime/processdriver_scheduler_e2e_test.go`.
- [ ] **Step 5: Amend the bundle.**

---

### Task 12: Retire the fused path (parity-green precondition)

**Files:**
- Modify: `runtime/kernel/ports.go:61-68` — DELETE `TimerArms`/`TimerCancels` from `AppliedStep`.
- Modify: `runtime/kernel/memstore.go:107-111,150-154` — delete their application.
- Modify: `internal/persistence/store/store_core.go` — delete `applyTimerOps` calls (:119, :285) and the `applyTimerOps` func (:466); KEEP `upsertTimer`/`deleteTimer` bodies only if the Task-9 writer reuses them — otherwise fold into the writer and delete here.
- Rewrite seeding in the **six known `TimerArms`-constructing test files** (not just "fix compile errors" — the conformance suites seed timers via `Commit(AppliedStep{TimerArms})` and must re-seed through `TimerWriter.UpsertJob`, which is real test-design work): `internal/persistence/store/timerstore_conformance_test.go`, `internal/persistence/store/store_conformance_test.go`, `internal/persistence/store/store_faults_test.go`, `persistence/facade_mysql_test.go`, `persistence/facade_sqlite_test.go`, plus any `runtime/kernel` memstore test constructing the fields.
- Modify: `internal/persistence/store/pruner.go:189` — **exclude recurring trigger kinds from `PruneTimers`** (audit v2: `next_run` is written once and never updated under D16, so a routine prune would delete a STILL-ARMED recurring timer's durable row; one WHERE-clause addition on `trigger_kind`), + note the caveat in `docs/production-checklist.md` (§ timer pruning).

- [ ] **Step 1: Confirm parity is green FIRST** (the Task 11 suite + whole repo): `go test ./...` → PASS. Do not start deleting before this passes.
- [ ] **Step 2: RED — pruner exclusion.** Extend the pruner test: an expired-`next_run` row with a RECURRING trigger_kind survives `PruneTimers`; a one-shot expired row is pruned. Run → FAIL (current SQL prunes both).
- [ ] **Step 3: Delete the fields + applications; re-seed the six test files through `TimerWriter.UpsertJob`; implement the pruner WHERE clause.** Compile errors point at every leftover producer/consumer.
- [ ] **Step 4: Verify.** `go test ./... && golangci-lint run ./...` → PASS/clean.
- [ ] **Step 5: Amend the bundle.**

---

### Task 13: Production observability — gocron-native Monitor + EventListeners (red-first)

**Files:**
- Create: `scheduler/internal/gocron/monitor.go` + `monitor_test.go`
- Modify: `scheduler/internal/gocron/scheduler.go` — wire `gocron.WithMonitorStatus(m)` + `gocron.WithEventListeners(gocron.AfterJobRunsWithError(...), gocron.AfterJobRunsWithPanic(...), gocron.AfterLockError(...))` at engine construction.

**Interfaces:** internal only. Metrics (via the Task-1 obs shim meter): counter `wrkflw_scheduler_job_runs_total{status}` (`success|fail|skip|singleton_rescheduled`), histogram `wrkflw_scheduler_job_duration_seconds{status}`. Listeners log via the engine's `slog` with `job_id`/`job_name`/`error` (+ stack on panic).

- [ ] **Step 1: RED — monitor_test.go.** Using an in-memory OTel `sdkmetric` reader: schedule a job whose task returns an error → after fire, `job_runs_total{status="fail"}` == 1 and one histogram point; a panicking task → recovered (scheduler alive; schedule+fire another job fine) and the panic listener logged (slog recorder).
- [ ] **Step 2: Verify RED. Step 3: GREEN. Step 4: verify PASS** (`go test ./scheduler/...`).
- [ ] **Step 5: Amend the bundle.**

---

### Task 14: Self-containment AST guard, examples, docs, delivery

**Files:**
- Create: `scheduler/selfcontainment_guard_test.go` (replaces/absorbs `scheduler/neutrality_test.go` — read it first; keep any still-relevant assertions)
- Modify: the **eleven** example mains touching the old surface (compile-fixed in Task 7; polish here): `timer_boundary`, `inwait_reminder`, `usertask_deadline`, `event_based_gateway`, `catch_event_reminder`, `retry_recovery`, `boundary_action`, `timer_durability`, `production_wiring`, `mysql_wiring` (semantic: leadership-acquired `RehydrateTimers` — verify against the upsert-by-id Activate contract), `sqlite_wiring`.
- Docs sweep (audit v2 file list): `CHANGELOG.md` (BREAKING: kernel.Scheduler/JobStore moved+reshaped; sentinel homes+messages; `persistence.NewSchedulerLocker` return-type path; scheduling→scheduler rename; gocron v2.22.0), `README.md:1379` (shutdown prose naming `scheduling.Scheduler`), `INTERACTIONS.md:135` (path reference), root `doc.go:91` + `processtest/doc.go:17` (MemScheduler contract description), `service/options.go:120` + `service/service.go:230` godoc, `persistence/{persistence.go:415,sqlite.go:129,mysql.go:103}` RehydrateTimers godoc (retained; now Load+Activate), `docs/production-checklist.md` (pruner caveat — Task 12). Godoc pass over `scheduler` package; testable `Example` functions for `NewScheduler`+`NewJob`+`Trigger` (`scheduler/example_test.go`).

- [ ] **Step 1: RED — guard test.** Using `golang.org/x/tools/go/packages` (already an indirect dep via mockgen tooling — verify; else use `go/parser`+`go/build` walking `scheduler/` dirs): load all non-`_test` packages under `github.com/kartaladev/wrkflw/scheduler/...`; assert NO import path has prefix `github.com/kartaladev/wrkflw/` EXCEPT the `scheduler/...` self-prefix. Verify it's a real guard: temporarily add a `_ "github.com/kartaladev/wrkflw/clock"` import to `scheduler/trigger.go`, run → FAIL; revert, run → PASS. (This temporary-violation run IS the red verification.)
- [ ] **Step 2: Examples + godoc + CHANGELOG.** `go build ./examples/... && go vet ./...`.
- [ ] **Step 3: Full Verification (CLAUDE.md):**

```bash
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1   # ≥85%, hot paths confirmed covered by name
go test ./...
golangci-lint run ./...
```

- [ ] **Step 4: Hot-path coverage audit (rule #8).** `go tool cover -func=cover.out | grep -E 'scheduler|timerops|jobstore|txrunner|store_core'` — every Drive/fire/rehydrate/writer function ≥ its package floor; NO hot-path function at 0%.
- [ ] **Step 5: Amend the bundle one final time**, then **Delivery Gate:** run `/code-review` (high) on the branch; fix ALL findings (amend); run `/security-review`; fix ALL findings (amend); re-run Step 3.
- [ ] **Step 6: Deliver.**

```bash
git checkout main && git fetch origin && git merge origin/main --no-edit  # sync guard
git merge --no-ff feat/scheduling-owned-jobs
git push origin main
```

---

## Verification checklist (whole plan)

- [ ] `scheduler/` production files import zero `kartaladev/wrkflw/*` (guard test green)
- [ ] gocron pinned v2.22.0 in go.mod; CLAUDE.md row updated; ADR-0135 in the chore commit
- [ ] Old `kernel.Scheduler`/`kernel.JobStore`/`kernel.ScheduledJob`/moved sentinels GONE from kernel; `ArmedTimer`/`TimerStore`/`JobSpec(+Kind)` remain
- [ ] `AppliedStep.TimerArms/TimerCancels` deleted; `applyTimerOps` gone from `Store.Create`/`Commit`; `perform` has no timer cases
- [ ] Manual-activation ordering proven: fire-before-commit regression test (Create path) + rolled-back-cancel test + arm/cancel-interleave test green on SQL; rollback leaves zero scheduler state (`Scheduled`→`ErrJobNotFound`, `List` empty)
- [ ] Drive commitFn NEVER calls `scheduler.Schedule` (direct-Save); commit survives the shutdown-drain window; durability identical with an injected scheduler
- [ ] `Activate`/engine `ScheduleJob` are upserts by id (double-Activate/rehydrate-repeat tests green)
- [ ] `persistence.CachingInstanceStore` forwards `TxRunner`; rollback-through-wrapper test green (cache evicted, never poisoned); `RunInTx` detects rollback-only (sentinel, never nil-on-rollback)
- [ ] Descriptor round-trip covers all 8 `wrkflw_timers` columns on Postgres + SQLite; join-by-ctx test documents ctx-carried atomicity
- [ ] Job ids are the unchanged engine timer ids (no composite scheme; Harness `Pending`/`classify` untouched)
- [ ] `Trigger.Next` persists non-zero `next_run` for cron/Daily/Weekly/Monthly arms; `PruneTimers` excludes recurring trigger kinds
- [ ] ADR-0135 claims re-verified against fetched v2.22.0 source at Task 2 Step 3b (gate flipped in the ADR)
- [ ] Start timers (ADR-0121) run under the non-durable kind, Auto activation, no WARN
- [ ] `processtest.MemScheduler` + `runtimetest.RecordingScheduler` rewritten; `NextFireAt`/`Pending`/`Tick` still serve the harness
- [ ] Observability: run counters + duration histogram + panic/error/lock listeners live
- [ ] ≥85% coverage floor AND hot-path audit pass; `go test ./...` + lint clean
- [ ] One `refactor` commit + one `chore` commit + ONE `feat` bundle commit (docs included); merged `--no-ff`; pushed
