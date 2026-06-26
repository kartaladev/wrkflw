# 0072. Multi-replica TIMER exclusivity

Status: **Proposed — 2026-06-27 (NOT accepted; awaiting maintainer decision).**
Proposal doc: `docs/specs/2026-06-27-multi-replica-timer-exclusivity-proposal.md`.
Relates to: ADR-0027 (timer rehydration), ADR-0031/0059/0061 (call-link lease / elector / heartbeat).

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

**Deferred.** No implementation. Three options are documented in the proposal doc:
- **A (recommended):** keep gocron + elector for fire-time exclusivity; re-arm all persisted timers on
  leadership acquisition (hook the elector's leadership transition to `RehydrateTimers`). Small, reuses
  existing machinery; single-replica firing; failover window ≤ heartbeat + rehydrate.
- **B:** replace per-replica gocron arming with a DB-polled `FOR UPDATE SKIP LOCKED` claim-based timer
  scheduler (mirrors the call-link `ClaimPending` lease); no leader, load-distributed, automatic
  failover; larger change + a `wrkflw_timers` lease migration.
- **C:** status quo + documented operational constraint (run the scheduler on one replica / rely on CAS).

## Consequences

- Until approved, the documented operational guidance stands: enable `WithTimerElector` and treat the
  leader as the single active timer-firing replica; runtime-armed timers are correct on that leader and
  re-armed on restart via `RehydrateTimers`. Double-fire remains impossible due to the CAS.
- This ADR will be revised to **Accepted** with the chosen option (and a normal spec → plan → SDD
  track) once the maintainer answers the three questions in the proposal doc (load distribution
  required? acceptable failover window? willing to add timer lease columns?).
- No `engine/`, `model/`, or scheduler code changes were made for this item.
