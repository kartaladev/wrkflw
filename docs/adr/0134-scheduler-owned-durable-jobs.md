# 134. Scheduler-owned durable jobs: self-contained `scheduler` library with an Activation model

- Status: Accepted
- Date: 2026-07-22
- Design doc: `docs/specs/2026-07-14-scheduling-owned-durable-jobs-design.md` (v2,
  post-audit consolidated design — the authoritative expansion of every decision
  summarized here, D1–D16).

## Context

The scheduling subsystem is intended to be spinnable as a standalone library, and the
"Plan-3 JobStore" that `runtime/timerops.go` anticipates — scheduler-owned write
ownership of durable timers — was deferred when ADR-0102 landed. Four problems block
both goals today:

1. **Inverted dependency.** `scheduling` imports `runtime/kernel` (`kernel.Scheduler`,
   `kernel.JobStore`, `kernel.ScheduledJob`, `kernel.JobSpec`,
   `kernel.ErrUnresolvedTimerDefinitions`). A library cannot depend on the application
   layer that consumes it.
2. **Split trees.** The gocron engine lives at repo-root `internal/scheduling/gocron`,
   unreachable from a spun-out module, and transitively imports
   `internal/observability`.
3. **Vocabulary coupling.** `scheduling` takes `definition/schedule.TriggerSpec` — a
   wrkflw authoring type carrying expr-lang forms a generic scheduler must not know.
4. **Fused durability (ADR-0027).** Timer arms/cancels travel on
   `AppliedStep.TimerArms/TimerCancels` and are written to `wrkflw_timers` inside
   `Store.Commit`'s transaction. The scheduler only reads (`LoadScheduled`,
   ADR-0102) at rehydration. Moving write-ownership to the scheduler must not lose
   the atomic-with-state guarantee — and the runtime (`ProcessDriver`) is
   vendor-free and holds no DB connection, so it cannot own a transaction itself.
   The existing seam: `Store.Commit` already joins an ambient ctx-transaction via
   `transaction.JoinOrBegin`.

An adversarial audit of the first design draft (four parallel source-verified
reviews) surfaced the decisive constraint: **gocron executes jobs asynchronously on
separate goroutines**, so any design that registers a job in-memory *inside* the
still-open ambient transaction lets an immediate/past-due one-shot **fire before the
commit** — on the instance-Create path the fire's `Load` sees no committed row,
gets `ErrInstanceNotFound`, and the fire is dropped permanently. Symmetrically,
disarming in-memory inside a transaction that later rolls back silently loses a
fire until restart. The originally-chosen single-call `Schedule` (persist + arm in
one call) is therefore unsafe for wrkflw's join-an-outer-tx integration, while
remaining perfectly safe for consumers whose stores self-commit.

## Decision

Rename and unify the subsystem into a self-contained **`scheduler`** package tree and
move durable-job write-ownership into it, with atomicity preserved through the
ambient ctx-transaction and an **Activation model** that structurally eliminates the
fire-before-commit hazard. Headline decisions (full detail and rationale in the
design doc, D1–D16):

**1. Self-containment (D12).** Rename `scheduling` → `scheduler`; relocate
`internal/scheduling/gocron/**` → `scheduler/internal/gocron/**` (engine + pg/my
electors + locker adapter); sever `internal/observability` behind a minimal
in-tree shim (porting the meter-scoped instrument helpers); delete
`internal/scheduling/`. `scheduler` ends with **zero `kartaladev/wrkflw/*`
imports**, enforced by an AST-based guard test.

**2. Own trigger vocabulary with pure next-run (D10).** `scheduler.Trigger` — value
type with constructors `At/After/Every/EveryRandom/Cron/Daily/Weekly/Monthly` and a
scheduler-owned `ClockTime` — plus **`Next(after) (time.Time, bool)`, a pure
computation** used to persist `next_run` in-transaction before any in-memory arming
(fixes cron/calendar shapes persisting zero today). The runtime converts
`schedule.TriggerSpec → scheduler.Trigger` at the Job boundary; the converter is
total over all `schedule.Kind` values, with `Unset/Expr/EveryExpr` as explicit
errors.

**3. The Activation model (D3a) — the load-bearing decision.**
`Job.Activation() ∈ {ActivationAuto, ActivationManual}`. `Schedule(ctx, j)` ALWAYS
persists via the routed `JobStore.Save` (joining an ambient ctx-tx when present) and
arms in-memory **only for `Auto`**. `Manual` jobs are persist-only — the scheduler
retains no in-memory record of them until `Activate(ctx, sj)`, which is an
**upsert by job id** (re-activation replaces, never duplicates). **wrkflw timer
jobs are `Manual` and persisted via the DIRECT-SAVE path**: the runtime calls its
own `jobStore.Save` inside `RunInTx` (same tx as `Store.Commit`/`Create`) —
`scheduler.Schedule` is NOT on the commit path at all — and calls `Activate` only
after the commit succeeds. No in-memory registration can exist before its durable
state is committed, so nothing can fire early and a rollback has nothing to undo;
because the scheduler is untouched in-tx, a state commit also survives the
shutdown-drain window (ADR-0133 closes the owned scheduler before draining
in-flight Drive loops — a skipped post-commit `Activate` is logged and rehydrates
on next boot), and durability is **scheduler-independent** (consumer-injected
`WithScheduler` deployments persist identically to the owned default). Cancels
mirror it: durable delete in-tx, in-memory `Deactivate` post-commit — a
rolled-back cancel never silently disarms. Library consumers with self-committing
stores keep single-call ergonomics via `Auto`.

**4. Scheduler surface (D15).** `Schedule / Activate / Deactivate / Cancel /
Scheduled / List`. `Scheduled(ctx, id)` returns `(ScheduledJob, error)` with
sentinel `ErrJobNotFound`; `Cancel` (turnkey delete+disarm) is idempotent on
unknown ids; `NextRun(id)` is dropped (read `Scheduled().NextRun()`, which is live
gocron state for armed jobs).

**5. `JobStore` port (D1/D2/D6/D16).** Defined in `scheduler`, **implemented by the
consumer**, registered per `JobKind` via the thunk-form option
`WithJobStore(kind, provide)` (a plain value would recreate the
driver ↔ jobstore ↔ scheduler construction cycle). Shape: `Save / Delete / Load` —
no `Update` (run-count tracking deferred). A kind with **no registered store is a
supported non-durable mode** (in-memory only, no WARN) — used by the ADR-0121
event-based start timers. Registration serves the scheduler's rehydration and the
turnkey `Schedule`/`Cancel`; wrkflw's commit-path durability does NOT depend on it
(direct-Save, decision 3).

**6. Typed descriptor via consumer implementation (D8 relaxed).** A generic
`ScheduledJob` cannot rebuild the typed `wrkflw_timers` row, so the runtime
**implements `scheduler.Job`/`ScheduledJob` itself** with a concrete type carrying
`kernel.JobSpec` (which gains `Kind engine.TimerKind`); the runtime's
`JobStore.Save` type-asserts it back — no stringly-typed map extraction, no
reverse trigger converter. Constructors (`NewJob`/`NewJobWithID`/…) remain for the
common consumer case.

**7. Atomicity seams (D4).** `TxRunner{ RunInTx }` — an optional capability on
`InstanceStore` (type-asserted like `Notifier`/`Locker`); SQL stores run
`transaction.Begin` → `fn(txCtx)` → commit/rollback, **detecting the
rollback-only mark at commit time** (a joined participant's rollback must surface
as an error, never as success); both the instance-Create and step-Commit paths are
wrapped. Atomicity is **ctx-carried** — `JoinOrBegin` joins the ambient handle
from ctx regardless of its conn argument — so the requirement on the store-side
**`TimerWriter`** capability (which the timer write SQL moves behind) is
same-database wiring, not same-connection. `persistence.CachingInstanceStore`
**forwards** `TxRunner` to the inner store and evicts (never puts) the touched
instance on rollback. The fused `AppliedStep.TimerArms/TimerCancels` path and
`perform`'s `ScheduleTimer`/`CancelTimer` cases are retired **parity-first** (new
path proven green before the old one is deleted).

**8. Executable contract (D5).** `Job.Action() JobFunc` +
`Job.Data() DataProvider{Get, Static}`, wrapped by the engine into a **zero-param
gocron task** so gocron's schedule-time parameter reflection is never engaged;
provider failures surface at fire time as task errors. Named `JobFunc` to avoid
clashing with wrkflw's `action.Action`.

**9. Production hardening in-scope (D14).** ① Observability through gocron-native
hooks (`MonitorStatus` → OTel counters/histogram; `AfterJobRunsWithError/
WithPanic`, `AfterLockError` → slog), ③ overrun protection
(`WithSingletonMode(LimitModeReschedule)` default with per-job opt-out), ⑧ `List`
enumeration. Deferred: per-fire timeout, retry/backoff, durable-snapshot +
run-count, elector metrics, phantom-skip gate.

**10. gocron pin bump** to v2.22.0 — decided separately in **ADR-0135** (hard-pin
change requires its own record).

## Consequences

**Easier:**

- `scheduler` becomes module-portable: all dependency arrows point inward; the
  spin-out is a `go.mod` split away, locked by the AST guard test.
- The fire-before-commit and rolled-back-cancel hazards are **structurally
  impossible** for wrkflw (nothing is in gocron until after commit), not mitigated
  by compensation or hooks; post-commit `Activate` failures self-heal via
  rehydration from durable truth.
- `next_run` is finally correct for cron/Daily/Weekly/Monthly triggers, computed
  purely and testable without gocron.
- One durability owner: arms and cancels live behind the scheduler's port instead
  of being fused into `Store.Commit`, while keeping ADR-0027's atomicity guarantee
  through the ambient transaction.
- The library API stays honest for non-wrkflw consumers: `Auto` keeps single-call
  simplicity; nothing in the generic surface exists solely to serve wrkflw.

**Harder / trade-offs:**

- **Breaking**: `kernel.Scheduler`, `kernel.JobStore`, `kernel.ScheduledJob` move
  and reshape; `kernel.ErrUnresolvedTimerDefinitions` and
  `kernel.ErrUnsupportedTrigger` relocate to `scheduler` with new messages (D7 —
  old `errors.Is` targets break); `persistence.NewSchedulerLocker`'s return type
  changes import path with the rename; `processtest.MemScheduler` and
  `runtimetest.RecordingScheduler` are rewrites; every scheduler call-site
  updates in one movement.
- The runtime carries slightly more choreography (persist in-tx, activate
  post-commit) instead of one call — the price of correctness under an ambient
  outer tx; encapsulated in the Drive commit flow.
- A benign residual race remains: a fire between commit and post-commit
  `Deactivate` is the existing stale-fire no-op in `handleTimerFired`.
- `wrkflw_timers` schema unchanged, but the write path relocation must be proven
  parity-first before the fused path is deleted, temporarily carrying both paths
  in-tree during the migration phases.
- Mem stores cannot provide true rollback semantics for `RunInTx`; rollback-parity
  tests are scoped to the SQL stores and Mem is documented as sequencing-only (an
  fn error after a Mem `Commit` leaves the instance permanently advanced).
- **Distributed (elector) mode**: arm-locality is unchanged — the elector gates
  fires, not arms; leadership-acquired recovery still rides `RehydrateTimers`,
  now safe under repetition because `Activate` is an upsert by id.
- A benign arm/cancel post-commit interleave exists (concurrent steps flipping
  the same timer id): a phantom in-memory arm self-limits to one spurious no-op
  fire, then disappears; documented + test-locked rather than serialized.
- A fired recurring timer whose recurrence lookup transiently errors takes the
  safe-default consume path AND is now deactivated post-commit — strictly
  better than the prior fused-path behaviour (native job kept firing against a
  deleted row); the timer re-arms from its durable row at next rehydration.
- `CachingInstanceStore.RunInTx` caches state as writes happen inside the
  transaction; a concurrent same-process `Load` can observe uncommitted (in-tx)
  state until the outer commit — bounded by the engine's optimistic-CAS model
  (a phantom read loses its CAS write) and by evict-on-rollback/panic; a
  buffer-then-apply-after-commit cache design would remove the window entirely
  and is noted as a possible follow-up.

**Follow-ups created by this ADR (not in scope):** module spin-out checklist
(the `scheduler/` `_test` files importing `internal/dbtest` relocate at split
time); deferred ⑥ (run-count/last-run) additionally unblocks pruner-safe
retention for recurring timer rows (interim: recurring kinds excluded from
`PruneTimers`); `processtest` harness API ripple to consumer docs (ADR-0092).

**Supersedes / cross-references:** supersedes the **write-ownership half of
ADR-0027** (its `wrkflw_timers` table, atomicity guarantee, read-side
`TimerStore.ListArmed`, and the rehydration semantics — idempotent re-arm,
past-due immediate fire, skip-unresolved — remain); reshapes ADR-0102's
`kernel.JobStore` self-rehydration into `scheduler.JobStore.Load` + `Activate`
(`ProcessDriver.RehydrateTimers` retained per ADR-0102 §5); builds on ADR-0059
(elector), ADR-0003 (clock), ADR-0121 (event-based start timers → non-durable
kind); ADR-0135 (gocron v2.22.0 bump).
