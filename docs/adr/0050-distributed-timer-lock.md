# 50. Multi-replica timer exclusivity via a gocron distributed locker

- Status: Accepted
- Date: 2026-06-25

## Context

When a consumer runs N replicas of their app against one Postgres, every replica
runs its own gocron scheduler and every replica calls `RehydrateTimers` on
startup (ADR-0027). Nothing claimed a timer, so **each armed timer was armed on
every replica and fired N times.** The engine's version-CAS and the in-tx
timer-row deletion already made the *effect* idempotent (the second
`Deliver(TimerFired)` finds the token consumed and no-ops), so this was "correct
but redundant" — but at N replicas it means N× the SLA/reminder/retry fires, N×
the `Deliver` load, and a stream of alarming "permanently dropped after CAS
conflicts" error logs. The production-readiness review flagged it the CRITICAL
blocker to multi-replica deployment.

gocron v2 ships two pluggable distributed primitives: an `Elector`
(leader-runs-everything) and a `Locker` (per-job mutual exclusion, load-balanced).
The user chose the `Locker` — it keeps timer load spread across replicas while
giving per-timer exclusion — over a hand-rolled distributed scheduler, which would
reinvent what gocron already provides.

## Decision

Add a Postgres-backed `gocron.Locker`, opt-in, confined to the existing seams:

- **`internal/scheduling/gocron.PostgresLocker`** implements `gocron.Locker` using
  session-level advisory locks (`pg_try_advisory_lock(hashtextextended(key,0))`) —
  the same primitive as `AdvisoryLockOwnership` (ADR-0020). `Lock` acquires a
  pooled connection and the lock; `Unlock` releases both. A refused lock returns
  `ErrLockNotObtained`, which gocron treats as "do not run this job here".
- **`WithLocker(gocron.Locker)`** on the internal scheduler passes
  `gocron.WithDistributedLocker` at construction (so `NewGocronScheduler` now
  applies options *before* building the gocron scheduler).
- **Each job is named with its `timerID`** (`gocron.WithName`) — gocron uses the
  job name as the lock key, so exclusion is **per-timer**, not one global lock.
- **`scheduling.WithDistributedTimerLock(*pgxpool.Pool)`** is the consumer-facing
  façade option; it constructs the locker internally so gocron stays invisible to
  the public API. gocron remains confined to `internal/scheduling/gocron`.

## Consequences

- With the option set, many replicas still *arm* a timer, but at fire time only the
  replica that wins the advisory lock runs the callback. The steady-state N×
  redundant `Deliver` storm and its CAS-conflict logs are eliminated.
- **The locker dedups *concurrent* fires, not *sequential* ones.** The lock is held
  only for the fire's duration (gocron's contract); if replica B's executor reaches
  the job after A has finished and released, B can acquire and run too. The engine
  version-CAS + in-tx timer-row deletion (ADR-0027) remain the true exactly-once
  backstop — this change reduces redundancy, it does not replace the CAS. This is
  the documented, accepted semantics, consistent with gocron's own caveats about
  cross-scheduler run-time synchronization.
- **Cost:** each fire on a lock-enabled scheduler holds one pooled connection for
  the (short) fire duration. A burst of simultaneous fires draws that many
  connections; size the pool accordingly. Without the option, behaviour is exactly
  as before (no goroutine/connection overhead, the per-job name is harmless).
- Opt-in and additive: single-replica and MemStore consumers are unaffected.
  Engine/model diff is **ZERO**; the change lives entirely in the scheduling adapter
  and façade.
- **Deferred:** an `Elector` (single-leader) variant for consumers who prefer one
  replica to own all timers; and claiming during `RehydrateTimers` itself
  (`FOR UPDATE SKIP LOCKED`) so replicas don't all re-arm — an arming-cost
  optimization on top of this fire-time exclusion.
