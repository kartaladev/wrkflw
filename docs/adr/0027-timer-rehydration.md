# 27. Timer rehydration on restart via a runtime-owned armed-timers side table

- Status: Accepted
- Date: 2026-06-22

## Context

Timers are armed in-process: when the engine emits a `ScheduleTimer{TimerID, FireAt, Kind}`
command, the runtime's `perform` calls `Scheduler.Schedule(timerID, fireAt, callback)`,
registering an in-memory gocron (or `MemScheduler`) job. When the process restarts those
in-memory jobs are lost — a parked instance waiting on a timer (intermediate catch event,
SLA deadline, in-wait reminder, boundary timer, event-gateway timer, retry backoff) never
resumes. This is an operational gap noted in the scheduling sub-project deferred follow-ups.

**The load-bearing fact (verified against the codebase):** `FireAt` is not persisted anywhere
in `InstanceState`. `engine.timerRecord` carries `TimerID/Kind/Token/TaskToken/NodeID/ScopeID`
but no fire time; `Token` has no fire time; and several `ScheduleTimer` emission sites (boundary,
event-gateway) do not even write a `timerRecord` — those arms live in `Boundaries`/`ArmedEvents`.
The fire time exists only transiently in the `ScheduleTimer` command and the in-memory scheduler
job. Rehydration therefore fundamentally requires persisting `FireAt` in a new location; it is not
merely "add a list query over existing state."

This problem has the same crash-safety/atomicity shape as true async call activity (ADR-0024,
ADR-0025): a durable side-record written atomically in the commit transaction, consumed by an
idempotent resumer. The headline lesson of that work — the engine did not change — applies here
too.

## Decision

Add a durable, opt-in timer-rehydration mechanism in the runtime and persistence layers, leaving
the engine and model untouched (zero production diff in `engine/` and `model/`).

**1. `runtime.TimerStore` read port and `ArmedTimer` value.**
`ArmedTimer` is a value type carrying `InstanceID`, `DefID`, `DefVersion`, `TimerID`, `FireAt`,
and `Kind`. Storing `DefID`/`DefVersion` on the record means `RehydrateTimers` resolves the
process definition via the `DefinitionRegistry` (key `"DefID:DefVersion"`) without loading
instance state per timer. `TimerStore` is a read port with a single method,
`ListArmed(ctx) ([]ArmedTimer, error)`, ordered by `(FireAt, InstanceID, TimerID)` for
deterministic re-arm order.

**2. Writes ride the commit transaction — atomic, engine untouched.**
`runtime.AppliedStep` gains two additive fields following the ADR-0025 precedent:

```go
TimerArms    []ArmedTimer // timers armed by this step (ScheduleTimer commands)
TimerCancels []string     // timer IDs disarmed by this step
```

The runtime derives these per applied step with a pure, kind-agnostic helper `timerOpsFor`:
`ScheduleTimer` commands become arms (a re-schedule of the same `TimerID` is an upsert);
`CancelTimer` commands and a `TimerFired` trigger (a fired timer is consumed) become cancels.
The `Store` applies `TimerArms` (upsert via `upsertTimer`) and `TimerCancels` (delete via
`deleteTimer`) in the same `pgx.Tx` as the snapshot CAS, journal, and outbox writes. Atomicity
guarantees:

- The timer arm and the token park commit together — a crash leaves either both or neither.
- A fired timer's removal commits with the resuming state transition — a crash never re-fires a
  consumed timer (its row is gone from the armed set).

This is opt-in: `TimerArms`/`TimerCancels` stay empty and the `Store` does nothing extra unless a
`TimerStore` is wired, preserving existing behavior verbatim.

**3. One-shot rehydration — `Runner.RehydrateTimers(ctx)`.**
The fire-callback construction previously inlined in `perform(ScheduleTimer)` is extracted into
`Runner.armTimer(def, instanceID, timerID, fireAt)`, used by both `perform(ScheduleTimer)` (a
behavior-preserving refactor) and rehydration. `RehydrateTimers` lists armed timers, resolves
each definition via the registry, and calls `armTimer` per timer. A `FireAt` already in the past
fires immediately; the resulting `TimerFired` for an already-consumed timer is a clean engine
no-op, so re-arming is idempotent and safe. Timers whose definition the registry cannot resolve
are skipped and counted in the returned error rather than failing the whole batch.

**4. Persistence: `wrkflw_timers` table and Postgres `TimerStore`.**
Migration `0005_timers.sql` adds:

```sql
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

`internal/persistence/postgres.TimerStore` implements `runtime.TimerStore.ListArmed` against
this table. `persistence.NewTimerStore(pool) runtime.TimerStore` is the public façade (mirrors
`persistence.NewCallLinkStore` from ADR-0024).

**5. Wiring and opt-in.**
`WithTimerStore(ts TimerStore) Option` on `Runner` (mirrors `WithCallLinks`). Absent this
option, `TimerArms`/`TimerCancels` are never populated and `RehydrateTimers` is a no-op call
that returns a descriptive error if invoked. `MemStore` acquires `NewMemStoreWithTimers(mts
*MemTimerStore)` that shares the `MemTimerStore` instance as both write target and read source;
`NewMemStore` is preserved.

## Consequences

**Easier:**

- Timers survive a process restart. Every timer kind (intermediate, SLA, in-wait/reminder,
  boundary, event-gateway, retry) is covered uniformly by `timerOpsFor`, which keys off
  `ScheduleTimer`/`CancelTimer` commands and `TimerFired` triggers without kind-specific logic.
- Engine purity is preserved: `engine/` and `model/` are untouched — the engine's determinism,
  sealed command/trigger sets, and `Step` function are byte-identical.
- The pattern is consistent with ADR-0025: `AppliedStep` is the single atomic-write boundary,
  extended with two nullable slices. Existing callers (nil slices) are unaffected.
- The feature is fully opt-in; consumers who do not call `WithTimerStore` see no behavioral
  change.
- Past-`FireAt` rehydration fires immediately, which is both the correct semantic and a safe
  default (the engine treats a re-fired consumed timer as an idempotent no-op).

**Harder / trade-offs:**

- One new table (`wrkflw_timers`) and one new migration (`0005`) are added to the schema.
  Operators must run the migration before deploying a build that uses `WithTimerStore`.
- `AppliedStep` gains two more optional fields, continuing the mild widening established in
  ADR-0025. The alternative — a shared-tx seam between `Store` and a separate timer-write port
  — was rejected for the same reasons as in ADR-0025: more coupling, no crash-safety benefit.
- **Multi-replica rehydration exclusivity is deferred.** Two replicas each calling
  `RehydrateTimers` will both re-arm every timer, producing redundant-but-correct double fires
  (the second `TimerFired` is an idempotent engine no-op via `ErrTokenNotFound`/
  `ErrInvalidTransition`). A `FOR UPDATE SKIP LOCKED` ownership claim — consistent with the
  call-activity deferral in ADR-0024 — is the documented follow-up for strict exclusivity and
  reduced redundant scheduler load.
- **Orphaned row pruning is deferred.** In-tx delete on fire/cancel should keep the table clean
  in the normal path; a defense-in-depth pruner for rows whose instance reached a terminal state
  without a clean cancel is a documented follow-up.
- **Rehydration observability is deferred.** Counts of re-armed and skipped timers are logged
  but not emitted as metrics or spans; alignment with the observability track is a follow-up.
- `RehydrateTimers` requires all three of `WithScheduler`, `WithTimerStore`, and
  `WithDefinitions`; calling it without any of them returns a descriptive error rather than a
  silent no-op. This is the right fail-fast posture for a startup-critical path.

**Cross-references:** ADR-0024 (async call activity — crash-safety precedent), ADR-0025
(atomic `AppliedStep` side-effects — the contract this extends), ADR-0009 (scheduling),
ADR-0008 (façade / internal boundary).
