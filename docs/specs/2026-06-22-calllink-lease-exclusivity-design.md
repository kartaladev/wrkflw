# Design: lease-based multi-replica exclusivity for the call-link notifier

**Date:** 2026-06-22
**Status:** Approved (autonomous run)
**Track:** Consolidated-backlog top pick (Production-hardening). Follow-up to ADR-0024/0025.
**ADR:** 0031.

## 1. Problem & scope

The async call-activity notifier (`runtime.CallNotifier`, ADR-0024) runs a poll loop on every
replica. On each tick it `ClaimPending`s terminal-but-unnotified call links
(`status IN ('completed','failed') AND notified_at IS NULL`), resolves each parent definition, and
delivers `SubInstanceCompleted/Failed` to resume the parked parent — then `MarkNotified`.

Today `ClaimPending` is a **plain SELECT with no claim**. With N replicas polling, a single
completed child is claimed by up to N replicas before any of them `MarkNotified`s, so up to N
replicas each resolve the parent and call `deliver` (a real engine `Deliver` with optimistic-CAS).
Only one wins; the rest get `ErrTokenNotFound` (treated as success) — **correct but redundant**:
N× the parent-definition lookups, engine deliveries, and DB load per completed child.

`FOR UPDATE SKIP LOCKED` (as the outbox relay uses) does **not** help here: the relay processes and
commits inside one tx, but the notifier's `deliver` happens *outside* the claim — a tx-holding lock
is released before delivery (the code says so at `call_links.go:32`). The correct primitive is a
**lease**: a claim that reserves the row for a TTL so other replicas skip it, surviving across the
claim→deliver→mark window, and reclaimable if the claiming replica dies mid-delivery.

**In scope:** an **opt-in**, lease-based claim for the call-link notifier — Postgres + in-memory
stores — configured at store construction (no port-interface change). Backward-compatible: lease
off (TTL=0) preserves today's exact behaviour.

**Out of scope (deferred, documented in ADR §Consequences):** multi-replica **timer** exclusivity.
Correct exclusive timers require replacing per-replica gocron arming with a claim-renew-failover
loop (a distributed scheduler) — large, and double-fire is *already* correct via the engine CAS, so
its marginal value is low. The current all-replicas-arm-all behaviour stays (resilient, redundant).

**Engine/model untouched** (zero diff). Changes live in `internal/persistence/postgres`, `runtime`
(MemCallLinkStore), and the `persistence` façade.

## 2. Design — store-level lease (no port change)

The lease is a property of the `CallLinkStore` implementation, configured at construction; the
`runtime.CallLinkStore` port (`ClaimPending`/`MarkNotified`/`LookupChild`) and `CallNotifier` are
**unchanged**. This keeps the seam stable and makes the feature purely opt-in wiring.

```go
// runtime (MemCallLinkStore) and postgres (CallLinkStore) each gain:
//   WithCallLinkLease(owner string, ttl time.Duration)  // ttl<=0 disables (default)
//   WithCallLinkClock(clk clock.Clock)                   // default clock.System()
```

- `owner` is a free-form replica identifier (hostname/pod name) recorded for diagnostics.
- `ttl` is the lease duration. While a row's lease is live, other replicas' claims skip it.
- `clk` supplies `now` (injected for fake-clock tests, mirroring the relay's `WithRelayClock`).

### 2.1 Postgres `ClaimPending` with lease

Migration `0006_call_link_lease.sql` (additive):
```sql
ALTER TABLE wrkflw_call_links ADD COLUMN claimed_at TIMESTAMPTZ;
ALTER TABLE wrkflw_call_links ADD COLUMN claimed_by TEXT;
```

When `ttl > 0`, `ClaimPending` claims atomically (cutoff `= now - ttl`):
```sql
UPDATE wrkflw_call_links AS c
   SET claimed_at = $1, claimed_by = $2
  FROM (
    SELECT child_instance_id
      FROM wrkflw_call_links
     WHERE status IN ('completed','failed')
       AND notified_at IS NULL
       AND (claimed_at IS NULL OR claimed_at <= $3)   -- $3 = now - ttl
     ORDER BY child_instance_id
     FOR UPDATE SKIP LOCKED
     [LIMIT $4]
  ) AS picked
 WHERE c.child_instance_id = picked.child_instance_id
 RETURNING c.child_instance_id, c.parent_instance_id, c.parent_command_id,
           c.parent_def_id, c.parent_def_version, c.depth, c.status, c.output, c.error;
```
- `FOR UPDATE SKIP LOCKED` on the inner select stops two *concurrent* claims racing the same rows;
  the `claimed_at` lease stops a *later* claim (after the first tx commits) for `ttl`.
- A successful delivery → `MarkNotified` sets `notified_at` (permanent; filtered out forever).
- A transient delivery failure leaves the row claimed; it becomes reclaimable after `ttl`
  (retry latency becomes `ttl` instead of one poll — the documented trade-off; pick `ttl`
  comfortably above the poll interval and below acceptable retry delay).
- `LIMIT` is included only when `limit > 0` (mirrors the existing dynamic query).

When `ttl <= 0`, `ClaimPending` runs the **existing plain SELECT** verbatim (no behaviour change).

### 2.2 In-memory `MemCallLinkStore` with lease

`MemCallLinkStore` gains the same options. Under its existing mutex, when `ttl > 0` `ClaimPending`
selects terminal, un-notified links whose `claimedAt` is zero or `<= now-ttl`, stamps
`claimedAt=now`/`claimedBy=owner`, and returns them; a second immediate claim skips live-leased rows.
`ttl <= 0` preserves current behaviour. (The mem store is single-process; the lease is exercised in
tests to prove the claim/skip/expiry logic without Postgres.)

### 2.3 Façade + notifier wiring

- `persistence.NewCallLinkStore(pool, opts...)` gains `persistence.WithCallLinkLease(owner, ttl)` and
  `persistence.WithCallLinkClock(clk)` (re-exporting the postgres options).
- `persistence.NewCallNotifier(pool, deliver, reg, clk, opts...)` builds the internal store; it gains
  the same lease options so a consumer can enable exclusivity in one call. (The `runtime.NewCallNotifier(store, …)`
  path already works for consumers who build a leased store themselves.)
- `runtime.CallNotifier` and the `runtime.CallLinkStore` port are unchanged.

## 3. Correctness

- **At-least-once preserved:** a row reaches `notified` only after a successful (or
  already-resumed) delivery. A replica that claims then dies before `MarkNotified` leaves
  `notified_at` NULL; the lease expires after `ttl` and another replica reclaims and delivers
  (engine `Deliver` idempotent via CAS / `ErrTokenNotFound`).
- **No lost wakeups:** `claimed_at` never gates the *terminal* filter (`notified_at IS NULL`), only
  reclaim timing; an expired or crashed claim always returns to the claimable set.
- **Single-replica unaffected:** with one replica, leasing only changes transient-retry latency to
  `ttl`; default `ttl=0` keeps exact current behaviour.

## 4. Testing strategy

- **runtime (`runtime_test`)** — MemCallLinkStore lease: a leased claim hides the row from an
  immediate second claim by another owner; after advancing a fake clock past `ttl` the row is
  reclaimable; a `MarkNotified` row is never returned; `ttl=0` behaves as before (two claims both
  see the row). Black-box, table-driven, `t.Context()`, fake clock.
- **internal/persistence/postgres** (testcontainers via `database.RunTestDatabase`) — seed a
  terminal link; first leased `ClaimPending` returns it and stamps `claimed_at/by`; an immediate
  second leased claim (different owner) returns nothing (lease live); after advancing the injected
  clock past `ttl`, the second claim reclaims it; a `notified` row is never claimed; migration
  up is exercised by `Migrate`. Mirror the relay's `TestRelaySkipLockedNoDoublePublish` style for
  the concurrent-claim assertion.
- **No engine/model changes** → no new purity risk.

**Gate (touched pkgs):** `go test -race -p 1 ./...` green; ≥85% on `runtime` and
`internal/persistence/postgres` and `persistence`; `golangci-lint run ./...` clean; engine/model
import-pure; migration reversible/idempotent per the existing goose convention.

## 5. ADR

| ADR | Decision |
|---|---|
| **0031** | Opt-in **lease-based** multi-replica exclusivity for the call-link notifier: a store-level `claimed_at/claimed_by` lease (configured via `WithCallLinkLease(owner, ttl)` + `WithCallLinkClock`), claimed atomically with `UPDATE…FOR UPDATE SKIP LOCKED…RETURNING` (Postgres) / mutex (mem). `ttl=0` = off (exact current behaviour). The `runtime.CallLinkStore` port and `CallNotifier` are unchanged. Multi-replica **timer** exclusivity is explicitly deferred (needs a claim-renew-failover distributed scheduler; double-fire already correct via engine CAS). |
