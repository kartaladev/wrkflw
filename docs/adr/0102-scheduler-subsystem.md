# 102. Scheduler subsystem: typed TriggerSpec, native gocron, durable descriptors, and self-rehydration

- Status: Accepted
- Date: 2026-07-08

## Context

The scheduling sub-system grew organically across multiple sessions and accumulated several
structural problems:

1. **Vendor lock-in in the public API.** The `scheduling` package exposed gocron-specific types
   directly, binding any consumer who used it to the gocron data model. A swap or upgrade of the
   underlying scheduler library would have been a breaking public-API change.

2. **Goroutine-spawning constructor.** `scheduling.NewScheduler` started background goroutines
   in the constructor, violating the project's fail-fast, lifecycle-explicit construction
   conventions (ADR-0083/0084). There was no `Start`/`Stop` lifecycle, making orderly shutdown
   impossible in an embedded library context.

3. **No durable timer descriptors.** When the engine armed a timer its `FireAt`, `trigger_kind`,
   and `trigger_payload` were held only in the in-memory gocron job. A process restart lost
   every armed timer. ADR-0027 established the `wrkflw_timers` table and the
   `AppliedStep.TimerArms/TimerCancels` atomic write path to persist this state; what was still
   missing was a rehydration path that did not require the driver's caller to coordinate it.

4. **Rehydration ownership gap.** ADR-0027 introduced `ProcessDriver.RehydrateTimers(ctx)` as
   an explicit startup call. This was correct for injected schedulers, but for the driver's own
   default scheduler it required the caller to know about the internal dependency — creating an
   awkward startup sequence and an easy-to-forget step.

5. **Construction cycle.** Connecting the driver's scheduler to the driver's own `JobStore`
   (which is a field on the driver) created a circular reference when wiring during construction:
   the driver needs the scheduler, the scheduler needs the job store, and the job store is the
   driver's field.

The tech stack (`gocron` pinned to v2.21.2, `jonboulle/clockwork`) is locked; the goal is to
neutralize the public surface, add lifecycle, and make the full timer-durability story
self-contained.

## Decision

We will structure the scheduler subsystem around a gocron-neutral trigger type, a durable timer
descriptor, and scheduler-owned self-rehydration, as detailed in the numbered points below.

**1. Typed, gocron-neutral `schedule.TriggerSpec`.**
We introduce a `schedule.TriggerSpec` value type (and its wire format, `TriggerWire`) in the
public `scheduling` package. `TriggerSpec` carries `Kind` (one-of `KindOnce`, `KindCron`,
`KindInterval`) plus the corresponding payload. It provides full semantic parity with the
gocron job types previously used directly. All public APIs that previously accepted gocron
types now accept `TriggerSpec`; gocron is an implementation detail behind `kernel.Scheduler`.

**2. Native gocron behind `kernel.Scheduler`.**
The `kernel.Scheduler` port remains the engine-facing abstraction. The `scheduling` package
provides a `NewScheduler(opts...) (*Scheduler, error)` constructor that builds a gocron
`Scheduler` internally and exposes it through the port. The constructor is goroutine-free:
no background goroutine is started in `New`. Callers invoke `Start(ctx)` to begin scheduling
and `Shutdown()` to stop it, giving the embedding application full lifecycle control.
`ProcessDriver.Start(ctx)` and `ProcessDriver.Shutdown()` delegate to the owned scheduler.

**3. Durable descriptor persistence.**
The `wrkflw_timers` table (already created by ADR-0027's `0005_timers.sql` migration) gains
three additional columns: `next_run TIMESTAMPTZ` (the next scheduled fire time as the scheduler
sees it, for dead-replica detection), `trigger_kind SMALLINT` (the `TriggerSpec.Kind`), and
`trigger_payload JSONB/TEXT` (the marshalled `TriggerSpec` payload). These are written in the
same commit transaction as `TimerArms` (via the `AppliedStep` atomic path — see below), so the
timer descriptor is always consistent with the timer arm state.

**4. `kernel.JobStore.LoadScheduled` and scheduler self-rehydration.**
We add a `LoadScheduled(ctx) ([]ScheduledJob, error)` method to `kernel.JobStore`. This allows
the scheduler to query all armed timers (with their `TriggerSpec`) at startup and re-register
them as gocron jobs without external coordination.

The `scheduling.WithJobStore(func() kernel.JobStore)` option accepts a **provider thunk** (a
`func()` that returns the store) rather than the store value directly. This breaks the
construction cycle: the thunk captures the driver by closure, and is evaluated lazily after
the driver is fully constructed. The driver auto-wires this thunk for its owned default
scheduler so the scheduler can self-rehydrate.

On `Start(ctx)`, the default scheduler calls `LoadScheduled`, iterates the returned jobs, and
registers each as a gocron job before the scheduler begins its run loop. A `FireAt` already in
the past fires immediately; a re-fire of an already-consumed timer is an idempotent engine
no-op (ADR-0027, §3). Self-rehydration requires no call from the embedding application.

**5. `RehydrateTimers` retained for consumer-injected schedulers.**
`ProcessDriver.RehydrateTimers(ctx)` is retained as an explicit, consumer-callable rehydration
path. A consumer who injects their own scheduler via `WithScheduler` does not benefit from the
auto-wired thunk; they must either wire `WithJobStore` on their scheduler themselves or call
`RehydrateTimers` once at startup. Requiring `WithScheduler`, `WithTimerStore`, and
`WithDefinitions` for `RehydrateTimers` is unchanged.

## Consequences

**Positive:**

- The `scheduling` package's public surface is now gocron-neutral. Upgrading or replacing
  gocron is an `internal/` concern and a non-breaking change for consumers.
- Lifecycle is explicit and library-embedding-safe: no goroutines start at construction time;
  `Start`/`Shutdown` give the host application full control.
- Timer descriptors (`trigger_kind`/`trigger_payload`/`next_run`) are persisted atomically with
  the timer arm, so a restarted scheduler can reconstruct its job queue without consulting
  per-instance state.
- The driver's owned default scheduler self-rehydrates on `Start`; the embedding application
  needs no explicit `RehydrateTimers` call in the common case.
- The provider-thunk pattern (`func() kernel.JobStore`) breaks the circular construction
  dependency cleanly without exposing the driver internals or adding an initialization phase.

**ADR-0027's atomic timer-write mechanism (`AppliedStep.TimerArms/TimerCancels`, fused into
the state-commit transaction) is RETAINED unchanged; ADR-0102 only relocates rehydration
OWNERSHIP from the driver's explicit `RehydrateTimers` to scheduler self-rehydration via
`JobStore.LoadScheduled`. Atomicity is preserved by leaving the fused-write path untouched.
`RehydrateTimers` is retained as an explicit fallback for consumer-injected schedulers.**

**Harder / trade-offs:**

- The `wrkflw_timers` table requires a schema migration to add the three new descriptor
  columns (`next_run`, `trigger_kind`, `trigger_payload`). Operators running an existing
  deployment with ADR-0027's schema must apply this migration before upgrading.
- The provider-thunk pattern (`func() kernel.JobStore`) is a subtle indirection that is
  necessary to break the cycle but unusual in Go APIs; it is documented in the option's
  godoc. Consumer-injected schedulers that want self-rehydration must also wire the thunk,
  which is marginally more complex than a direct store reference.
- `RehydrateTimers` continues to exist alongside self-rehydration. Two paths for the same
  semantic operation increase the surface a new contributor must understand; the README and
  this ADR document when each is appropriate.
- Multi-replica rehydration exclusivity remains deferred (see ADR-0027): two replicas both
  calling self-rehydration or `RehydrateTimers` produces redundant-but-correct double fires.

**Cross-references:** ADR-0027 (timer-rehydration atomic write path and `wrkflw_timers` schema
— extended by this decision), ADR-0009 (scheduling), ADR-0025 (`AppliedStep` atomic side-effects),
ADR-0083/0084 (constructor conventions and lifecycle), ADR-0087 (runtime decomposition).
