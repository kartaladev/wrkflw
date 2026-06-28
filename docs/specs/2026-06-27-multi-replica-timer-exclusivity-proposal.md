# Proposal (NOT YET APPROVED) — Multi-replica TIMER exclusivity

Date: 2026-06-27
Status: **APPROVED 2026-06-28 → Option A implemented** (see ADR-0072, now Accepted). Originally
deliberately NOT implemented in the 2026-06-27 backlog program (flagged as a major architectural change
per the user's "skip items needing major approvals"); the maintainer subsequently chose Option A and
confirmed MySQL portability as a goal, which reinforced A over B.
Relates to: ADR-0027 (timer rehydration), ADR-0031/0059/0061 (call-link lease, elector, heartbeat),
the engine CAS (`runtime.Store.Commit` optimistic concurrency).

## Why this is approval-gated, not auto-done

Double-firing of a timer is **already correct** today: `armTimer` (`runtime/runner.go:1057`) retries
on `runtime.ErrConcurrentUpdate`, and `Store.Commit` (`runtime/ports.go:73`) is a CAS on the expected
token — so when N replicas fire the same timer, exactly one wins the commit and the rest no-op. The
remaining issue is **efficiency/correctness-of-arming in multi-replica**, not safety. Fixing it
changes how the scheduler arms/owns timers across replicas — a non-trivial scheduling-architecture
decision with real distributed-systems trade-offs. That warrants explicit sign-off.

## Current state (mapped)

- `scheduling.NewScheduler` wraps gocron (`internal/scheduling/gocron`). Timers are armed per-replica
  via `armTimer` → `Scheduler.Schedule(timerID, fireAt, fire)`; the fire callback `Deliver`s a
  `TimerFired` trigger with CAS retry.
- `RehydrateTimers` (`runtime/runner.go:1091`) re-arms persisted timers (`TimerStore.ListArmed`) **at
  startup only**.
- An optional Postgres leader elector exists (`WithTimerElector`, ADR-0059) using
  `pg_try_advisory_lock` + heartbeat (ADR-0061). gocron's `WithDistributedElector` makes **only the
  leader replica fire** armed jobs.
- The `wrkflw_timers` table is the durable source of armed timers; the engine CAS makes firing idempotent.

### The actual gap

With the elector enabled, only the leader *fires*, so exclusivity at fire-time is achievable. BUT
timers armed **at runtime** (a `Schedule` call while handling a request) are added only to **that
replica's** in-memory gocron job set. Other replicas — including whoever becomes leader next — do not
have those jobs armed until a process restart triggers `RehydrateTimers`. So on **leader failover**,
runtime-armed timers can be silently un-armed on the new leader until restart. That is the
"claim-renew-failover loop" the backlog refers to.

## Options

### Option A (recommended, lower effort): elector + re-arm on leadership acquire
Keep gocron + the existing elector for fire-time exclusivity, and close the failover gap by
**re-arming all persisted timers when a replica acquires leadership** (not only at startup): hook the
elector's leadership-acquired transition to invoke `RehydrateTimers`. On the leader, gocron fires;
on leadership change, the new leader rehydrates the full armed set from `wrkflw_timers` before firing.
- Pros: reuses all existing machinery (elector, heartbeat, RehydrateTimers, CAS); small, contained.
- Cons: all timers run on one replica (no load distribution); a window of ≤ heartbeat-interval +
  rehydrate-time after failover; requires the elector to expose a leadership-acquired callback (it
  currently only gates firing internally).
- Work: add a leadership-transition hook to `PostgresElector`; wire it to `RehydrateTimers`; tests for
  failover re-arm (testcontainers, two scheduler instances, force leader step-down).

### Option B (more robust, higher effort): distributed claim-based timer scheduler
Replace per-replica gocron arming with a DB-polled due-timer **claimer**: a loop polls `wrkflw_timers`
for due rows, claims one via `FOR UPDATE SKIP LOCKED` (+ a short lease/`claimed_at`), fires it, and on
success deletes/advances the row (atomic with the state commit). Any replica may claim any due timer.
- Pros: no leader needed; natural load distribution across replicas; failover is automatic (another
  replica claims); exclusivity is enforced by the row lock, not by leadership; mirrors the call-link
  `ClaimPending` lease (ADR-0031) — a proven pattern in this codebase.
- Cons: a new scheduler implementation behind the `runtime.Scheduler` port; poll latency vs gocron's
  precise timers (mitigated by a short poll interval + `LISTEN/NOTIFY` wake like the relay); larger diff
  and more tests; the `clockwork.Clock` fake-time story must be preserved for tests.
- Work: a new `internal/scheduling/dbclaim` scheduler implementing the port; `wrkflw_timers` gains
  `claimed_at`/`claimed_by`/lease columns (migration); claim/fire/release loop; NOTIFY wake; failover
  and exclusivity tests; an option to select gocron vs db-claim.

### Option C (status quo + document)
Do nothing structural; rely on the CAS (already safe) and document that multi-replica runtime-armed
timers should use Option A's elector with a single active scheduler replica, or run the scheduler on
one replica only. Cheapest; leaves the failover gap as a documented operational constraint.

## Recommendation

**Option A** for the next increment (closes the real failover gap with minimal, well-understood
change reusing the elector + RehydrateTimers), with **Option B** recorded as the eventual target if
load distribution across replicas becomes a requirement. Option C only if multi-replica timer load is
not a near-term concern.

## Cross-dialect portability (e.g. future MySQL support)

Both options keep the engine core dialect-neutral — the DB-specific coordination primitive lives in
`internal/` behind a port (`gocron.Elector` for A, `runtime.Scheduler` for B), so the engine never
imports it. "Add MySQL support" means writing one more adapter, not touching `engine/`/`model/`/
`runtime/`. They differ in how cheaply that adapter ports:

- **Option A — low friction.** The only DB-specific piece is the elector. `PostgresElector`
  (`internal/scheduling/gocron/elector.go`, `var _ gocron.Elector`) runs
  `SELECT pg_try_advisory_lock(hashtextextended($1, 0))` inside its single `IsLeader(ctx) error`
  method. MySQL has a direct 1:1 analog — `SELECT GET_LOCK(?, 0)` / `SELECT RELEASE_LOCK(?)` — that is
  arguably a cleaner fit: it takes a string key directly (no `hashtextextended` hashing) and is
  session-scoped, so a dropped connection releases leadership, matching the failover behaviour the
  heartbeat (ADR-0061) already relies on. Everything else in A (`RehydrateTimers`, the `Store.Commit`
  CAS, the leadership-acquired hook) is dialect-neutral. **Porting A = one new `Elector` impl; no
  minimum MySQL version, no migration.**

- **Option B — medium friction, with two caveats.**
  1. `FOR UPDATE SKIP LOCKED` works on **MySQL 8.0+** (and MariaDB 10.6+) but **hard-requires** it —
     MySQL 5.7 has no `SKIP LOCKED`, so the claim approach is off the table there.
  2. `LISTEN/NOTIFY` (B's wake optimization to cut poll latency) **does not exist in MySQL**. On MySQL
     you fall back to pure interval polling — functionally correct, just higher timer latency.
  Plus a second dialect of the `wrkflw_timers` lease migration and a dialect switch (or parallel
  adapter) inside the `dbclaim` scheduler.

| | DB-specific primitive | MySQL equivalent | Porting friction |
|---|---|---|---|
| **A** | `pg_try_advisory_lock` (elector) | `GET_LOCK()` — direct, session-scoped | **Low** — one `Elector` impl, no migration, no version floor |
| **B** | `SKIP LOCKED` + `LISTEN/NOTIFY` | `SKIP LOCKED` (8.0+ only); **no NOTIFY** | **Medium** — MySQL 8.0+ floor, lose NOTIFY wake → polling, second migration |

If cross-dialect portability is a near-term goal, this is an additional point in favour of **Option A**
(or, if B is chosen, of designing the `dbclaim` adapter dialect-aware from day one rather than
hard-coding Postgres-only `LISTEN/NOTIFY`).

## Decision needed from the maintainer

1. Is multi-replica timer **load distribution** required (→ Option B), or is single-leader firing with
   correct failover sufficient (→ Option A)?
2. Acceptable failover window? (Option A: ≤ heartbeat interval + rehydrate; Option B: ≤ poll interval.)
3. Willing to add `wrkflw_timers` lease columns + a migration (Option B), or keep the table read-only
   for arming (Option A)?
4. Is **cross-dialect portability** (e.g. future MySQL support) a near-term goal? If so it favours
   Option A (cheap `GET_LOCK` elector port, no version floor); Option B is still possible but imposes a
   MySQL 8.0+ floor and loses its `LISTEN/NOTIFY` wake.

No code will be written for this until one of the options is approved.
