# 31. Lease-based multi-replica exclusivity for the call-link notifier

- Status: Accepted
- Date: 2026-06-22

## Context

The async call-activity notifier (`runtime.CallNotifier`, ADR-0024/0025) runs a poll loop on every
replica, claiming terminal-but-unnotified call links and resuming the parked parent. `ClaimPending`
is a plain SELECT (`status IN ('completed','failed') AND notified_at IS NULL`) with no claim, so with
N replicas a single completed child is claimed and delivered by up to N replicas before any marks it
notified. This is **correct but redundant** (the engine `Deliver` is idempotent via optimistic CAS /
`ErrTokenNotFound`): N× parent-def lookups, engine deliveries, and DB load per completed child.

`FOR UPDATE SKIP LOCKED` (the outbox relay's exclusivity primitive) does not apply: the relay
publishes and commits inside one tx, but the notifier's `deliver` runs *outside* the claim, so a
tx-holding lock is released before delivery (`call_links.go:32` documents this). The correct
primitive for work whose processing outlives the claiming tx is a **lease**.

The HANDOVER lists "multi-replica `FOR UPDATE SKIP LOCKED` exclusivity (timers + call-links)" and
"lease-column ownership" as deferred follow-ups. This ADR covers the **call-link** half.

## Decision

Add **opt-in, lease-based** exclusivity to the call-link store, configured at construction so the
`runtime.CallLinkStore` port and `CallNotifier` are unchanged:

- Migration `0006` adds `claimed_at TIMESTAMPTZ` and `claimed_by TEXT` to `wrkflw_call_links`.
- Both stores gain `WithCallLinkLease(owner string, ttl time.Duration)` and
  `WithCallLinkClock(clk)`. When `ttl > 0`, `ClaimPending` claims atomically: Postgres uses
  `UPDATE … FROM (SELECT … WHERE notified_at IS NULL AND (claimed_at IS NULL OR claimed_at <= now-ttl)
  ORDER BY child_instance_id FOR UPDATE SKIP LOCKED [LIMIT n]) … RETURNING …`; the mem store does the
  equivalent under its mutex. The lease reserves a row for `ttl`, so other replicas skip it across
  the claim→deliver→`MarkNotified` window; an expired or crashed claim returns to the claimable set.
- When `ttl <= 0` (default), `ClaimPending` runs the existing plain SELECT verbatim — exact current
  behaviour, fully backward-compatible.
- The `persistence` façade re-exports the lease options on `NewCallLinkStore` and `NewCallNotifier`.

Multi-replica **timer** exclusivity is deliberately **not** included (see Consequences).

## Consequences

**Positive**

- With the lease enabled, a completed child is delivered by ~one replica instead of N — eliminating
  the redundant parent-def lookups, engine deliveries, and DB contention at scale.
- No port-interface or `CallNotifier` change; the feature is pure opt-in wiring.
- At-least-once preserved: a row reaches `notified` only after a successful/already-resumed
  delivery; a crashed claim's lease expires and another replica reclaims it.

**Negative / trade-offs**

- A transient delivery failure now retries after `ttl` (the lease) instead of on the next poll. Pick
  `ttl` above the poll interval and below the acceptable retry delay. Documented on the option.
- A new migration and two nullable columns; `claimed_by` is diagnostics-only.
- Opt-in means a consumer running multiple replicas must remember to enable the lease to get the
  benefit; off by default to preserve exact single-replica behaviour.

**Deferred — multi-replica timer exclusivity (out of scope, deliberate)**

Correct exclusive timers cannot reuse this lease as-is: a timer must stay *armed* on a scheduler
until it fires (potentially far in the future), so the owning replica must renew its lease and a dead
owner's timers must be re-armed by another replica — i.e. a claim-renew-**failover** loop replacing
the current per-replica gocron arming (a distributed scheduler). Because timer double-fire is already
correct via the engine's optimistic CAS (`RehydrateTimers` double-arm is redundant, not wrong), the
marginal value is low relative to that cost. The current all-replicas-arm-all behaviour stays
(resilient, redundant); the distributed-scheduler version is a future track if the redundancy becomes
a measured problem.
