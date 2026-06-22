# Timer rehydration on restart — design

**Date:** 2026-06-22
**Status:** Proposed (awaiting user approval)
**Track:** Scheduling / production-hardening (consolidated-backlog top pick)
**ADR:** 0027 (Timer rehydration via a runtime-owned armed-timers side table)

## Context

Timers are armed in-process: when the engine emits a `ScheduleTimer{TimerID, FireAt, Kind}`
command, the runtime's `perform` calls `Scheduler.Schedule(timerID, fireAt, fire)`, registering an
in-memory gocron (or `MemScheduler`) job. When the process restarts, **those in-memory jobs are
lost** — a parked instance waiting on a timer (intermediate catch, SLA breach, in-wait reminder,
boundary timer, event-gateway timer, retry backoff) never resumes. This is a real operational gap
(noted in the Scheduling sub-project deferred follow-ups).

**The load-bearing fact (verified):** `FireAt` is **not persisted anywhere** in `InstanceState`.
`engine.timerRecord` carries `TimerID/Kind/Token/TaskToken/NodeID/ScopeID` but **no fire time**;
`Token` has no fire time; and several `ScheduleTimer` emission sites (boundary, event-gateway) don't
even write a `timerRecord` (those arms live in `Boundaries`/`ArmedEvents`). The fire time exists only
transiently in the `ScheduleTimer` command and the in-memory scheduler job. Therefore rehydration
**fundamentally requires persisting `FireAt`** in a new location — it is not merely "add a list
query over existing state".

### Established precedent

This problem has the same crash-safety/atomicity shape as **true async call activity** (ADR-0024,
ADR-0025): a durable side-record, written atomically in the child's commit transaction, drained by
an idempotent resumer. The headline lesson there — *the engine did not change* — applies here too.
Timer rehydration reuses that pattern: a durable armed-timers record, written in the commit tx,
re-armed on startup.

## Goals

1. On process restart, re-arm every pending timer so parked instances resume.
2. Persist `FireAt` durably and atomically with instance state (no orphaned/lost timers across a crash).
3. Keep the engine/model **pure and untouched** (zero production diff in `engine/`+`model/`).
4. Library-first: the consumer controls when rehydration runs (one-shot at startup).
5. Cover **all** timer kinds uniformly (intermediate, SLA, in-wait/reminder, boundary,
   event-gateway, retry).

## Non-goals

- **Multi-replica exclusivity.** Two replicas each calling `RehydrateTimers` will both re-arm,
  producing redundant-but-correct double fires (the second `TimerFired` is an idempotent no-op). A
  `FOR UPDATE SKIP LOCKED` / ownership claim is a documented follow-up, consistent with the
  async-call-activity deferral.
- **Background reconciliation poller.** Rehydration is a startup concern; a continuous poller (the
  `CallNotifier` shape) is unnecessary because timers are armed in-process during normal operation.
- **Engine changes.** No new `FireAt` field on `timerRecord`; the engine is untouched.

## Design

### 1. `runtime.TimerStore` read port + `ArmedTimer` value

```go
// ArmedTimer is one timer currently armed (scheduled, not yet fired or cancelled).
type ArmedTimer struct {
    InstanceID string
    DefID      string // resolves the def via the registry on rehydration — no per-instance state Load
    DefVersion int
    TimerID    string
    FireAt     time.Time
    Kind       engine.TimerKind
}

// TimerStore is the read-side port for enumerating armed timers at startup.
type TimerStore interface {
    ListArmed(ctx context.Context) ([]ArmedTimer, error)
}
```

`DefID/DefVersion` are stored on the record so rehydration resolves each timer's definition through
the `DefinitionRegistry` (key `"DefID:DefVersion"`) without loading instance state per timer.

### 2. Writes ride the commit transaction (atomic, engine untouched)

`runtime.AppliedStep` gains two additive fields:

```go
type AppliedStep struct {
    // ... existing: State, Trigger, Events, NewCallLink, CallOutcome ...
    TimerArms    []ArmedTimer // timers armed by this step
    TimerCancels []string     // timer IDs disarmed by this step
}
```

The runtime derives these per applied step with a **pure helper** over the step's commands +
trigger (mirroring `outboxEventsFor`):

- **Arm** ← each `ScheduleTimer` command in the step result. A re-schedule of the same `TimerID`
  (e.g. a retry timer) is an **upsert** (overwrites `FireAt`).
- **Cancel** ← each `CancelTimer` command, **and** the fired timer ID when the applied trigger is
  `TimerFired` (a fired timer is consumed and must leave the armed set).

The `Store` applies `TimerArms` (upsert) and `TimerCancels` (delete) **in the same `pgx.Tx`** as the
snapshot CAS + journal + outbox writes. Atomicity guarantees:

- The timer arm and the token park (both in the *same* `AppliedStep` for the triggering step) commit
  together — a crash leaves either both or neither.
- A fired timer's removal commits with the resuming state transition — a crash never re-fires a
  consumed timer (it's gone from the armed set).

This is **opt-in**: nil/absent timer wiring ⇒ `TimerArms`/`TimerCancels` stay empty and the `Store`
does nothing extra (existing behavior preserved, timers remain in-memory-only and lost on restart).

### 3. One-shot rehydration — `Runner.RehydrateTimers(ctx)`

The fire-callback construction currently inlined in `perform(ScheduleTimer)` (the
`Scheduler.Schedule` call wrapping the retry-on-CAS `Deliver` loop) is extracted into:

```go
func (r *Runner) armTimer(def *model.ProcessDefinition, instanceID, timerID string, fireAt time.Time)
```

used by both `perform(ScheduleTimer)` (behavior-preserving refactor) and rehydration.

```go
// RehydrateTimers re-arms every persisted armed timer on the scheduler. Call once
// at startup after constructing the Runner. Requires WithScheduler + WithTimerStore
// + WithDefinitions; returns a descriptive error otherwise.
func (r *Runner) RehydrateTimers(ctx context.Context) error
```

It lists armed timers, resolves each def via the registry, and calls `r.armTimer(...)`. A `FireAt`
already in the past fires immediately (gocron's `OneTimeJobStartImmediately`, `MemScheduler` fires on
the next `Tick`). The resulting `TimerFired` for an already-consumed timer is a clean engine no-op,
so re-arming is idempotent and safe.

### 4. Wiring & opt-in

- `WithTimerStore(ts TimerStore) Option` on the `Runner` (mirrors `WithCallLinks`).
- `MemStore`: `NewMemStoreWithTimers(mts *MemTimerStore)` shares the `MemTimerStore` instance as both
  the in-tx write target and the read source (mirrors `NewMemStoreWithCallLinks`). `NewMemStore`
  preserved.
- `MemTimerStore`: mutex-guarded in-memory map keyed by `(InstanceID, TimerID)`; implements
  `TimerStore`; receives arm/cancel from `MemStore.Create`/`Commit`.
- Postgres: table `wrkflw_timers` + migration `0005`; `Store` writes arms/cancels in-tx;
  `postgres.NewTimerStore(pool)` implements `ListArmed`; `persistence.NewTimerStore(pool)
  runtime.TimerStore` façade (mirrors `persistence.NewCallLinkStore`).

```sql
-- migration 0005_timers.sql
CREATE TABLE wrkflw_timers (
    instance_id TEXT        NOT NULL,
    timer_id    TEXT        NOT NULL,
    fire_at     TIMESTAMPTZ NOT NULL,
    kind        SMALLINT    NOT NULL,
    def_id      TEXT        NOT NULL,
    def_version INT         NOT NULL,
    PRIMARY KEY (instance_id, timer_id)
);
CREATE INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers (fire_at);
```

`ListArmed`: `SELECT instance_id, def_id, def_version, timer_id, fire_at, kind FROM wrkflw_timers`
(ordered by `fire_at, instance_id, timer_id` for deterministic re-arm order).

### 5. Data flow

```
arm:    Deliver(trg) → engine.Step → ScheduleTimer cmd → perform arms scheduler
                     → deliverLoop derives AppliedStep.TimerArms → Store.Commit upserts wrkflw_timers (same tx as state)
cancel: CancelTimer cmd / TimerFired trigger → AppliedStep.TimerCancels → Store deletes row (same tx)
restart: RehydrateTimers → TimerStore.ListArmed → registry.Lookup(def) → r.armTimer(...) per timer
fire:    scheduler → TimerFired → Deliver → engine resumes; row deleted in the commit tx
```

## Testing strategy

- **`MemTimerStore`** unit tests: arm/upsert/cancel/ListArmed (table-driven, `assert`-closure).
- **`AppliedStep` derivation** unit test: a step with `ScheduleTimer` → `TimerArms`; a `CancelTimer`
  and a `TimerFired` → `TimerCancels`; re-schedule same ID → single upsert.
- **`MemStore` atomicity** unit test: Create/Commit record arms and delete cancels only when a
  `MemTimerStore` is wired.
- **Rehydration Mem e2e** ("highest-value"): arm an intermediate-timer instance, **discard the
  original Runner + Scheduler**, build a fresh Runner + fresh `MemScheduler` + the same
  `MemTimerStore` + registry, call `RehydrateTimers`, advance the fake clock past `FireAt`, `Tick`,
  assert the instance resumes to `StatusCompleted`.
- **Postgres crash-safety e2e** (testcontainers, `database.RunTestDatabase`, run `-p 1`): arm via one
  `Store`, **discard it**, build a fresh `Store` + `postgres.TimerStore` + `Runner`, `RehydrateTimers`,
  advance clock + fire, assert resume to `StatusCompleted`; a second `ListArmed` returns the timer
  gone after it fired.
- **Misconfiguration**: `RehydrateTimers` without scheduler/timer-store/registry returns a
  descriptive error.
- All black-box (`package <pkg>_test`), table-driven with the `assert`-closure form, `t.Context()`,
  fake clock via clockwork.

## Verification gate

- `go test -race -p 1 ./...` green (Postgres testcontainers; `-p 1`).
- Touched packages ≥ 85% line coverage.
- `golangci-lint run ./...` clean.
- **Engine/model purity: zero production diff** in `engine/` and `model/` over the branch
  (`git diff <base> -- engine model` empty) — the load-bearing invariant.
- No forbidden vendor imports introduced (`gocron`/`clockwork`/`watermill`/`casbin` stay confined).

## Risks & mitigations

- **Timer-op derivation misses a kind** → the pure helper keys off `ScheduleTimer`/`CancelTimer`
  commands + `TimerFired` triggers uniformly, so it is kind-agnostic; an e2e per representative kind
  (intermediate + at least one of boundary/SLA) guards it.
- **Double-arm across replicas** → correct via idempotent re-fire; documented as a follow-up
  (SKIP LOCKED / ownership claim).
- **Stale def in registry** → `RehydrateTimers` skips (logs) a timer whose def the registry can't
  resolve rather than failing the whole batch; surfaced as an error count in the return/log.
- **FireAt in the past on rehydration** → intentional immediate fire; the engine no-ops a
  consumed-timer `TimerFired`.

## Deferred follow-ups
1. Multi-replica rehydration exclusivity (`FOR UPDATE SKIP LOCKED` / ownership claim).
2. Pruning of any orphaned `wrkflw_timers` rows for instances that reached a terminal state without a
   clean cancel (defense-in-depth; the in-tx delete on fire/cancel should keep it clean).
3. Optional observability on rehydration (count re-armed, span) — align with the observability track.
