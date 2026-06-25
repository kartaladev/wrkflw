# 59. Single-leader timer firing via a gocron distributed elector

- Status: Accepted
- Date: 2026-06-25

## Context

ADR-0050 added a Postgres-backed `gocron.Locker` so that, across N replicas, only
the replica that wins a per-timer advisory lock runs a given timer's fire — a
**load-balanced** mode that keeps timer load spread across replicas while giving
per-timer mutual exclusion. ADR-0050 explicitly deferred two follow-ups:

1. an **`Elector`** (single-leader) variant for consumers who prefer one replica to
   own all timers; and
2. claiming during `RehydrateTimers` itself (`FOR UPDATE SKIP LOCKED`) so replicas
   don't all re-arm — an arming-cost optimization.

This ADR delivers (1). gocron v2 ships two pluggable distributed primitives: the
`Locker` (per-job exclusion, load-balanced — already shipped) and the `Elector`
(`IsLeader(ctx) error`: leader-runs-everything). Some operators prefer the
single-leader shape: simpler reasoning (one replica fires all timers, the others
are pure hot-standby), no per-fire lock churn, and a single point to observe for
"is timer firing happening". The `Elector` is the natural gocron-native way to
provide it without reinventing a distributed scheduler.

## Decision

Add a Postgres-backed `gocron.Elector`, opt-in, confined to the same seams as the
Locker (ADR-0050). It is the **single-leader alternative** to the Locker, and the
two are **mutually-exclusive operating modes**.

- **`internal/scheduling/gocron.PostgresElector`** implements `gocron.Elector` using
  a single well-known **session-level advisory lock** (`pg_try_advisory_lock(
  hashtextextended(key,0))`) held on a **dedicated pooled connection for the
  elector's lifetime** — the same idiom as `AdvisoryLockOwnership` (ADR-0020).
  `NewPostgresElector(ctx, pool, opts...)` acquires the dedicated `*pgxpool.Conn`.
  - **`IsLeader`**: if leadership is already held (in-memory mutex-guarded bool) it
    returns `nil` with no round-trip (gocron calls it on every job run, so the hot
    path must be cheap). Otherwise it attempts `pg_try_advisory_lock(<leader-key>)`
    on the dedicated conn; on success it becomes leader and returns `nil`; on
    refusal it returns the sentinel `ErrNotLeader`, so gocron skips every job on
    this instance.
  - The **leader key is a fixed constant** so all replicas contend for the same
    lock; `WithElectorKey` overrides it so multiple independent engines can coexist
    in one database.
  - **Natural failover, no lease loop**: when the leader process dies its connection
    drops and Postgres auto-releases the advisory lock; a follower then wins it on
    its next `IsLeader` attempt. There is no heartbeat or lease-renewal goroutine.
    Caveat (split-brain window): `IsLeader` is sticky — once leadership is held it
    returns `nil` from an in-memory flag with no DB round-trip. If the leader's
    dedicated connection is severed *server-side* (lock auto-released) while the
    process keeps running, that process still believes it leads while a follower can
    also acquire the lock — a transient two-leader window. The exactly-once backstop
    (ADR-0027 version-CAS + in-tx timer-row deletion) downgrades this to redundant
    fires, not double-execution — the same guarantee the Locker relies on. A
    lease/heartbeat that re-checks the lock would close the window at the cost of the
    distributed-scheduler machinery deliberately avoided here.
    **Narrowed by [ADR-0061](0061-elector-heartbeat.md):** a bounded background
    heartbeat now `Ping`s the dedicated connection each interval and steps the leader
    down on silent loss, narrowing this window to **≤ one heartbeat interval** (5s
    default, tunable) without the full distributed-scheduler machinery. The ADR-0027
    version-CAS remains the exactly-once backstop.
  - **`Close()`** releases the lock and returns the conn; idempotent (mirrors
    `AdvisoryLockOwnership.Close`).
  - Compile-time assertion `var _ gocron.Elector = (*PostgresElector)(nil)`.
- **`WithElector(gocron.Elector)`** on the internal scheduler passes
  `gocron.WithDistributedElector` at construction (mirrors `WithLocker`).
- **`scheduling.WithTimerElector(pool, opts...)`** is the consumer-facing façade
  option; it constructs the elector internally so gocron stays invisible to the
  public API (mirrors `WithDistributedTimerLock`). The `Scheduler` holds the elector
  and its existing `Close()` releases it (and its dedicated connection) alongside
  the gocron scheduler.
- **Mutual exclusion is enforced, not silently resolved.** Setting both a Locker and
  an Elector errors at construction: `gocron.ErrLockerElectorConflict` internally and
  `scheduling.ErrTimerLockElectorConflict` at the façade. (gocron itself does not
  reject the combination — it would store both — so we reject it ourselves to keep
  the two modes unambiguous.)

## Consequences

- With `WithTimerElector` set, every replica still *arms* its timers, but only the
  elected leader's `IsLeader` returns `nil`, so only the leader runs fire callbacks.
  Followers skip all fires until they win leadership. This eliminates the
  steady-state N× redundant `Deliver` storm (and its CAS-conflict logs) — the same
  benefit as the Locker, achieved by concentrating firing on one replica instead of
  spreading it.
- **Locker vs. Elector — pick one.** Locker = load-balanced per-timer exclusion
  (timer work spread across replicas, one pooled connection held per *fire*).
  Elector = single-leader (all timers fire on one replica, one pooled connection
  held for the elector's *lifetime*, others idle). They are mutually exclusive;
  combining them is a construction error.
- **Failover window.** Between the leader dying and a follower's next `IsLeader`
  attempt winning the lock, no timer fires. Followers re-attempt on each scheduled
  job tick, so the gap is bounded by gocron's check cadence; pending fires that were
  missed are recovered by `RehydrateTimers` (ADR-0027) and the engine's idempotent
  CAS backstop, not lost.
- **Exactly-once is still the engine's job.** As with the Locker, the
  Elector reduces redundancy; the engine version-CAS plus in-tx timer-row deletion
  (ADR-0027) remain the true exactly-once guarantee.
- **Cost:** one pooled connection held for the elector's whole lifetime (the leader's
  is the one holding the advisory lock; followers hold an idle conn that repeatedly
  fails `pg_try_advisory_lock`). Size the pool for `+1` connection per replica when
  this mode is enabled. Without the option, behaviour is unchanged.
- Opt-in and additive: single-replica and MemStore consumers are unaffected.
  Engine/model diff is **ZERO**; the change lives entirely in the scheduling adapter
  and façade.
- **The OTHER ADR-0050 deferred item — claim-on-rehydrate
  (`FOR UPDATE SKIP LOCKED`) to stop all replicas re-arming — is INTENTIONALLY NOT
  built here.** Failover-safe *arming*-partitioning (versus the *firing*-time
  exclusion the Locker/Elector provide) requires a full distributed scheduler that
  reassigns a dead replica's claimed timers — exactly the build ADR-0050 declined in
  favour of gocron-native primitives. The Locker and Elector already make
  multi-replica timers *correct* (no duplicate fires, idempotent backstop); the
  remaining per-replica *arming* cost (every replica re-arms on rehydrate) is
  acceptable overhead, not a correctness gap, and does not justify the distributed-
  scheduler complexity. It remains deliberately declined.
