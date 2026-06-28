# 0072. Multi-replica TIMER exclusivity

Status: **Accepted — 2026-06-28 (Option A implemented).** Supersedes the 2026-06-27 Proposed status.
Proposal doc: `docs/specs/2026-06-27-multi-replica-timer-exclusivity-proposal.md`.
Relates to: ADR-0027 (timer rehydration), ADR-0031/0059/0061 (call-link lease / elector / heartbeat).
Maintainer decision (2026-06-28): **Option A** (re-arm on leadership acquisition); cross-dialect
portability (MySQL) confirmed as a goal, which reinforced A over B (B imposes a MySQL 8.0+ floor and
loses `LISTEN/NOTIFY`). Option B remains the recorded future target if load distribution is later
required.

## Context

In a multi-replica deployment, each replica arms timers in its own gocron instance. Double-firing is
**already safe** via the engine CAS (`Store.Commit` optimistic concurrency + `armTimer`'s
`ErrConcurrentUpdate` retry) — exactly one replica's fire commits. The open issue is the *arming*
ownership across replicas: timers armed at runtime live only in the arming replica's gocron, so on
leader failover (with the optional `WithTimerElector`) the new leader lacks those jobs until a restart
triggers `RehydrateTimers`. This is an optimization/robustness gap, not a safety bug.

Resolving it is a scheduling-architecture decision with real trade-offs (single-leader + failover
re-arm vs a distributed claim-based scheduler), so it is recorded as a **proposal** for explicit
maintainer approval rather than implemented autonomously.

## Decision

**Option A implemented.** Keep gocron + the leader elector for fire-time exclusivity, and close the
failover gap by re-arming all persisted timers when a replica acquires leadership (not only at
startup). Concretely:

- The internal elector (`internal/scheduling/gocron/elector.go`) gains a
  `WithOnLeadershipAcquired(func(context.Context))` option. The callback fires each time `IsLeader`
  transitions to leader (including re-acquisition after a heartbeat step-down). It runs in a
  `wg`-tracked goroutine on the elector's background context, so it never blocks gocron's `IsLeader`
  hot path, is cancelled and waited-for by `Close` (goleak-clean), and overlapping invocations from
  rapid step-down/re-acquire are coalesced.
- The public façade (`scheduling/scheduler.go`) re-exports it as `WithOnLeadershipAcquired`, threaded
  through `WithTimerElector`. Consumers wire it to `runtime.Runner.RehydrateTimers` (capturing the
  runner in the closure, since the runner is built after the scheduler).
- On leadership change the new leader rehydrates the full armed set from `wrkflw_timers` before firing;
  double-fire stays impossible via the engine CAS. Failover window ≤ heartbeat interval + rehydrate.

No `engine/`, `model/`, or `runtime/` code changed — the work is confined to the scheduler adapter and
its façade. `RehydrateTimers` already existed (ADR-0027) and is reused unchanged.

Options B and C (documented in the proposal) were **not** taken:
- **B** (DB-polled `FOR UPDATE SKIP LOCKED` claim-based scheduler; no leader, load-distributed) remains
  the recorded future target if cross-replica timer load distribution becomes a requirement.
- **C** (status quo + documented constraint) is the fallback A improves on.

## Consequences

- The failover gap is closed: a new leader re-arms runtime-armed timers from `wrkflw_timers` on
  acquisition rather than only on process restart. Double-fire remains impossible due to the CAS
  (ADR-0027).
- Single-replica firing is retained (no load distribution) — acceptable per the maintainer decision;
  Option B is the path if that changes.
- Cross-dialect portability is preserved: the hook adds no new Postgres-specific primitive, and the
  elector's only DB-ism stays `pg_try_advisory_lock`, which ports cheaply to MySQL `GET_LOCK`. This was
  a deciding factor for A over B (B would impose a MySQL 8.0+ floor via `SKIP LOCKED` and lose its
  `LISTEN/NOTIFY` wake) — relevant because full MySQL persistence support is now a planned program.
- A residual two-leader window of ≤ one heartbeat interval persists by design (ADR-0061); the CAS is
  the exactly-once backstop within it.
- No `engine/`, `model/`, or scheduler code changes were made for this item.
