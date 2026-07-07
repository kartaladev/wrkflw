# Plan 3/3 — `JobStore` durability: descriptor persistence, ambient-tx consistency, self-rehydration, ADR-0102

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make scheduled timers durable and self-recovering under the JobStore-owned model: persist the trigger **descriptor** (so recurring jobs rehydrate correctly), have the scheduler call a workflow-provided **`JobStore`** for the full lifecycle, keep timer+state writes **atomic** (ambient transaction, preserving ADR-0027's guarantee), and have the scheduler **self-rehydrate on start** (no explicit `RehydrateTimers`). Write ADR-0102.

**Architecture:** `wrkflw_timers` gains `next_run`, `trigger_kind`, `trigger_payload`. A `kernel.JobStore` (implemented on the runtime/persistence side, closing over the def registry + the driver's deliver path) rebuilds *executable* jobs from persisted specs — solving handler rebinding via wrkflw's uniform "deliver `TimerFired`" handler. The scheduler calls `JobStore.Save/Update/Delete` **within the ambient state-commit transaction**; gocron registration is a reconciled projection. On construction the scheduler runs `LoadScheduled` and re-registers everything.

**Tech Stack:** Go 1.25; Postgres 17 / MySQL 8 / SQLite (neutral store + `internal/persistence/dialect`, goose migrations); gocron behind the port.

## Global Constraints

- `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean; ≥85% coverage on touched packages.
- **Strict TDD**; `use-testcontainers` for DB tests (`dbtest.RunTestSQLite`/`RunTestDatabase`); `use-mockgen` for any mocked interface; black-box tests.
- Never compare `dialect.Name` to `"sqlite"` — use `dialect.TimestampsAsText()` (ADR-0080). Timestamps via `timeArg`/`parseTimeText`.
- Depends on **Plan 1** (`schedule.TriggerSpec`, `model.TriggerWire`) and **Plan 2** (`Scheduler` port, `ArmedTimer.Trigger/NextRun`, gocron native jobs). This is **Plan 3 of 3** for ADR-0102.

---

## File Structure

- **Create** migrations: `internal/persistence/store/migrations/postgres/0010_timers_trigger.sql`, `.../mysql/0004_timers_trigger.sql`, `.../sqlite/000X_timers_trigger.sql` (numbers = next free per dialect).
- **Modify** `internal/persistence/store/timerstore.go` — `ListArmed` SELECT + `scanArmedTimer` for `next_run`/`trigger_kind`/`trigger_payload`.
- **Modify** `internal/persistence/store/store_core.go` — `upsertTimer` INSERT columns.
- **Modify** `internal/persistence/dialect/{postgres,mysql,sqlite}.go` — `UpsertTimer()` conflict columns.
- **Modify** `runtime/kernel/timerstore.go` — `ArmedTimer` (already has `Trigger`/`NextRun` from Plan 2); add `JobStore`, `JobSpec`, `ScheduledJob`.
- **Create** `runtime/jobstore.go` — workflow `JobStore` implementation (rebuilds `Fire`).
- **Modify** `internal/scheduling/gocron/scheduler.go` + `scheduling/scheduler.go` — accept a `JobStore`, self-rehydrate on start, call `Save/Update/Delete`.
- **Modify** `runtime/timerops.go` — retire explicit `RehydrateTimers` (or make it a thin delegate); ambient-tx threading.
- **Create** `docs/adr/0102-scheduler-subsystem.md`; **modify** `docs/adr/0027-timer-rehydration.md` (status note).

---

## Task 1: Schema — add `next_run` / `trigger_kind` / `trigger_payload`

**Files:** the three migration files; `internal/persistence/dialect/{postgres,mysql,sqlite}.go`.

- [ ] **Step 1: Failing test** — a cross-dialect schema-parity/round-trip test (extend the existing timers conformance test) asserting a cron `ArmedTimer` upserts and `ListArmed` returns its `Trigger`:

```go
// internal/persistence/store/timerstore_test.go (extend)
func TestTimerStoreTriggerDescriptor(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	// migrate, construct Store + TimerStore …
	spec := schedule.Cron(`0 9 * * *`)
	// upsert an ArmedTimer{Trigger: spec, NextRun: <t>} via the Store commit path
	got, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 1)
	if c, ok := got[0].Trigger.CronExpr(); !ok || c != `0 9 * * *` {
		t.Fatalf("trigger = %+v", got[0].Trigger)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — FAIL (columns/scan absent).

- [ ] **Step 3: Implement migrations** (verbatim shape per dialect):

Postgres `0010_timers_trigger.sql`:
```sql
-- +goose Up
ALTER TABLE wrkflw_timers RENAME COLUMN fire_at TO next_run;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_kind    SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_payload JSONB;
ALTER INDEX wrkflw_timers_fire_at_idx RENAME TO wrkflw_timers_next_run_idx;
-- +goose Down
ALTER INDEX wrkflw_timers_next_run_idx RENAME TO wrkflw_timers_fire_at_idx;
ALTER TABLE wrkflw_timers DROP COLUMN trigger_payload;
ALTER TABLE wrkflw_timers DROP COLUMN trigger_kind;
ALTER TABLE wrkflw_timers RENAME COLUMN next_run TO fire_at;
```
MySQL `0004_timers_trigger.sql`:
```sql
-- +goose Up
ALTER TABLE wrkflw_timers CHANGE fire_at next_run DATETIME(6) NOT NULL;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_kind    SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_payload JSON;
-- +goose Down
ALTER TABLE wrkflw_timers DROP COLUMN trigger_payload;
ALTER TABLE wrkflw_timers DROP COLUMN trigger_kind;
ALTER TABLE wrkflw_timers CHANGE next_run fire_at DATETIME(6) NOT NULL;
```
SQLite `000X_timers_trigger.sql` (SQLite lacks RENAME-with-type; recreate or use `ALTER TABLE … RENAME COLUMN` + `ADD COLUMN`):
```sql
-- +goose Up
ALTER TABLE wrkflw_timers RENAME COLUMN fire_at TO next_run;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_kind    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_payload TEXT;
-- +goose Down
ALTER TABLE wrkflw_timers DROP COLUMN trigger_payload;
ALTER TABLE wrkflw_timers DROP COLUMN trigger_kind;
ALTER TABLE wrkflw_timers RENAME COLUMN next_run TO fire_at;
```
Update each dialect's `UpsertTimer()` to add `next_run = excluded.next_run` (already present as fire_at→next_run), `trigger_kind`, `trigger_payload` to the conflict update column list.

- [ ] **Step 4: Run** — `go test ./internal/persistence/... -run TestTimerStoreTriggerDescriptor` after Task 2's scan/upsert land; the migration alone should apply cleanly (`go test ./internal/persistence/store/... -run Migrat`).

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/migrations/ internal/persistence/dialect/
git commit -m "feat(persistence): timers next_run + trigger_kind + trigger_payload (3-dialect)"
```

---

## Task 2: Store read/write of the descriptor

**Files:** `internal/persistence/store/timerstore.go`, `internal/persistence/store/store_core.go`; `runtime/kernel/timerstore.go` (ArmedTimer already carries `Trigger`/`NextRun`).

- [ ] **Step 1: Failing test** — the Task 1 round-trip test now exercises scan/upsert (run it).

- [ ] **Step 2: Run to verify it fails** — FAIL (scan/upsert don't read/write the new columns).

- [ ] **Step 3: Implement** —
  - `store_core.go` `upsertTimer`: INSERT `(instance_id, timer_id, next_run, kind, def_id, def_version, trigger_kind, trigger_payload)`; bind `timeArg(dialect, tm.NextRun)`, `int16(triggerKindOf(tm.Trigger))`, and `json.Marshal(model.PutTrigger(tm.Trigger))` (store the `TriggerWire` JSON; NULL when zero).
  - `timerstore.go` `ListArmed` SELECT adds `next_run, trigger_kind, trigger_payload`; `scanArmedTimer` decodes `next_run` (via the text/native codec) and `trigger_payload` JSON → `model.TriggerWire` → `model.ReadTrigger(w, "", false)` into `ArmedTimer.Trigger`. (`trigger_kind` is a query/index convenience; the payload is authoritative.)

- [ ] **Step 4: Run** — `go test ./internal/persistence/...` → PASS (SQLite; add PG/MySQL via testcontainers conformance).

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/ runtime/kernel/timerstore.go
git commit -m "feat(store): read/write timer trigger descriptor + next_run"
```

---

## Task 3: `kernel.JobStore` + workflow implementation (handler rebuild)

**Files:** `runtime/kernel/timerstore.go` (port + types), `runtime/jobstore.go` (impl), `runtime/jobstore_test.go`.

**Interfaces — Produces:**
```go
// runtime/kernel
type JobSpec struct {
	TimerID, InstanceID, DefID string
	DefVersion int
	Trigger schedule.TriggerSpec
	NextRun time.Time
}
type ScheduledJob struct { Spec JobSpec; Fire func() }
type JobStore interface {
	LoadScheduled(ctx context.Context) ([]ScheduledJob, error)
	Save(ctx context.Context, spec JobSpec) error
	Update(ctx context.Context, timerID string, nextRun time.Time) error
	Delete(ctx context.Context, timerID string) error
}
```

The runtime `JobStore` wraps the durable `TimerStore` (read → rebuild `Fire`) and `Store.upsertTimer`/`deleteTimer` (write, ambient-tx aware). `Fire` is rebuilt uniformly: look up the def via the registry (`DefID`/`DefVersion`), then the closure delivers `engine.NewTimerFired(now, timerID)` to `InstanceID` via the driver's deliver path (the retrying closure already in `armTimer`).

- [ ] **Step 1: Failing test** — an in-mem `JobStore` `LoadScheduled` returns a `ScheduledJob` whose `Fire()` delivers a `TimerFired` that advances a parked instance (assert via the in-mem driver).

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** the port + a `runtime.jobStore` struct: `LoadScheduled` calls `TimerStore.ListArmed`, and for each `ArmedTimer` builds a `ScheduledJob{Spec, Fire: r.timerFireFunc(def, a.InstanceID, a.TimerID)}` (extract the closure body of today's `armTimer` into `timerFireFunc`). `Save/Update/Delete` call the ambient-tx-aware store writes (Task 4 threads the tx). Also add an in-mem `JobStore` sibling to `MemTimerStore` for tests.

- [ ] **Step 4: Run** — `go test ./runtime/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/kernel/timerstore.go runtime/jobstore.go runtime/jobstore_test.go
git commit -m "feat(runtime): JobStore port + workflow impl (rebuilds executable Fire)"
```

---

## Task 4: Scheduler self-rehydration + ambient-tx consistency

**Files:** `internal/scheduling/gocron/scheduler.go`, `scheduling/scheduler.go` (accept `JobStore`, self-rehydrate, call `Save/Update/Delete`); `runtime/timerops.go` / commit path (thread ambient tx); retire explicit `RehydrateTimers`.

**Consistency mechanism (the crux):**
- Timer lifecycle changes coincide with a **state commit**: arming while applying a step; one-shot `Delete` and recurring `Update(nextRun)` when `TimerFired` is applied. The runtime's commit runs within a DB transaction exposed through `ctx` (ambient tx, as the store already supports for state/outbox writes). When the scheduler calls `JobStore.Save/Update/Delete`, it passes that `ctx` so the store write joins the **same tx** → timer row + state commit atomic. gocron in-memory registration happens after; if the tx rolls back, the orphaned gocron job fires → `TimerFired` → engine no-op (idempotent).
- Concretely: `Schedule(ctx, …)` in the adapter does `jobStore.Save(ctx, spec)` (ambient tx) **then** registers on gocron. `AfterJobRuns` for a one-shot calls `jobStore.Delete(deliverCtx, id)`; for recurring, `jobStore.Update(deliverCtx, id, job.NextRun())` — both run inside the `TimerFired` deliver's commit tx (thread the tx into the fire callback's follow-on commit).

- [ ] **Step 1: Failing test** — durable e2e (SQLite/testcontainers): schedule a cron job (commit), drop the scheduler, construct a fresh scheduler with the same `JobStore`; it `LoadScheduled`-rehydrates and the cron fires at the next `NextRun` under the fake clock. Plus an atomicity test: a forced state-commit rollback leaves **no** timer row (both-or-neither).

- [ ] **Step 2: Run to verify it fails.**

- [ ] **Step 3: Implement** —
  - gocron adapter + façade: add `WithJobStore(kernel.JobStore)`; on construction (after `Start()`), call `jobStore.LoadScheduled(ctx)` and re-register each `ScheduledJob` (its `Fire` already built). On `Schedule`, call `Save`; in `AfterJobRuns`, `Delete` (one-shot) / `Update` (recurring). All with the ambient ctx.
  - Runtime: thread the commit tx into `ctx` for the `Schedule` call at arm time and for the `TimerFired` follow-on. Remove `AppliedStep.TimerArms/TimerCancels` fused writes (superseded by JobStore writes) OR keep them as the JobStore's own write mechanism — the JobStore `Save`/`Delete` ARE the `upsertTimer`/`deleteTimer` on the ambient tx.
  - Retire the public `RehydrateTimers` entry point (scheduler self-rehydrates); if kept for compatibility, make it a no-op-with-warning or a thin `LoadScheduled` trigger.

- [ ] **Step 4: Run** — `go test ./...` incl. the durable rehydrate + atomicity tests (testcontainers). Migrate wiring examples: drop explicit `RehydrateTimers`; pass the `JobStore` to the scheduler.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduling/gocron/ scheduling/ runtime/ examples/
git commit -m "feat(scheduler): JobStore-owned lifecycle, self-rehydration, ambient-tx consistency"
```

---

## Task 5: ADR-0102 (+ supersede ADR-0027 timer-write)

**Files:** Create `docs/adr/0102-scheduler-subsystem.md`; modify `docs/adr/0027-timer-rehydration.md`.

- [ ] **Step 1: Write ADR-0102 (Nygard)** covering: typed `schedule.TriggerSpec` (full gocron parity, gocron-neutral), gocron-native scheduling with `NextRun()` as the authoritative fire time, `JobStore`-owned durable lifecycle + self-rehydration, ambient-tx consistency, and the neutralized `scheduling` package (reusing the persistence advisory lock). **Explicitly:** `## Consequences` states *"Supersedes the timer-write mechanism of ADR-0027: timer arms are now written by scheduler-orchestrated `JobStore.Save/Update/Delete` on the ambient state-commit transaction, replacing `AppliedStep.TimerArms/TimerCancels`. The atomicity guarantee of ADR-0027 is preserved; only the mechanism changes."* Also note it extends (not replaces) the expr-lang-for-durations decision.

- [ ] **Step 2: Annotate ADR-0027** — add to its Status/Context header:
  `> **Partially superseded by ADR-0102 (2026-07-07):** the transactional timer-write mechanism (AppliedStep.TimerArms/TimerCancels) is replaced by scheduler-owned JobStore writes on the ambient tx; the atomicity guarantee is retained. Rehydration is now scheduler-driven (JobStore.LoadScheduled).`

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0102-scheduler-subsystem.md docs/adr/0027-timer-rehydration.md
git commit -m "docs(adr): 0102 scheduler subsystem; supersede ADR-0027 timer-write"
```

---

## Self-Review (spec coverage for Plan 3)

- Descriptor persistence (`next_run`/`trigger_kind`/`trigger_payload`, 3-dialect) → Tasks 1–2. `JobStore` port + workflow impl (handler rebuild) → Task 3. Scheduler self-rehydration + ambient-tx consistency (ADR-0027 guarantee preserved) → Task 4. ADR-0102 + ADR-0027 supersession documented → Task 5.
- **Consistency task (Task 4) is the integration crux** — the ambient-tx threading into `Schedule`/`AfterJobRuns` and the reconciliation of gocron-as-projection are the highest-risk items; verify with the forced-rollback atomicity test and the durable rehydrate test before claiming done.
- After Plan 3, the whole ADR-0102 spec is delivered; proceed to the boundary-enhancements plan (ADR-0103/0104), which builds on the finalized `TriggerSpec` timer API.
