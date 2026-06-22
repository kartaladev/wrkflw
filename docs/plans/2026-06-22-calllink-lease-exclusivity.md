# Call-link notifier lease exclusivity — Implementation Plan

> **For agentic workers:** executed via superpowers:subagent-driven-development. Strict TDD: a visible RED (`go test` failing/not-compiling) precedes every implementation. Each task ends green + committed.

**Goal:** Add opt-in, lease-based multi-replica exclusivity to the call-link notifier so a completed child is delivered by ~one replica instead of N. Store-level lease (`claimed_at`/`claimed_by`); the `runtime.CallLinkStore` port and `CallNotifier` are unchanged. `ttl=0` (default) preserves exact current behaviour.

**Architecture:** `WithCallLinkLease(owner, ttl)` + `WithCallLinkClock(clk)` on both stores (mem + Postgres). `ClaimPending` claims atomically when `ttl>0` (Postgres `UPDATE…FOR UPDATE SKIP LOCKED…RETURNING`; mem under mutex), else runs the existing plain SELECT. Façade re-exports the options.

**Tech Stack:** Go 1.25, pgx, goose migrations, clockwork fake clock, testcontainers (`database.RunTestDatabase`), testify.

## Global Constraints

- Module `github.com/zakyalvan/krtlwrkflw`; no `pkg/` prefix.
- **Strict TDD**, RED before GREEN in its own test run.
- **Engine/model production diff ZERO** (only `internal/persistence/postgres`, `runtime`, `persistence`).
- `workflow-` error prefix (ADR-0026); assert `errors.Is`. Black-box tests; table-test assert-closure form; `t.Context()`; clock via `clock.Clock` (never import clockwork from non-test runtime code — fake clock is test-only).
- Backward-compatible: `ttl<=0` ⇒ existing plain-SELECT `ClaimPending`, byte-for-byte behaviour; existing tests stay green.
- Postgres tests run with `-p 1`; use `database.RunTestDatabase(t)` + `pg.Migrate`.
- Gate: `go test -race -p 1 ./...` green; ≥85% on `runtime`, `internal/persistence/postgres`, `persistence`; `golangci-lint run ./...` clean.
- Spec: docs/specs/2026-06-22-calllink-lease-exclusivity-design.md. ADR-0031.

## File Structure

- `runtime/mem_calllink.go` (**modify**) — lease options + `ClaimPending` lease branch.
- `runtime/mem_calllink_lease_test.go` (**create**) — mem lease tests.
- `internal/persistence/postgres/migrations/0006_call_link_lease.sql` (**create**).
- `internal/persistence/postgres/call_links.go` (**modify**) — lease options + `ClaimPending` lease-claim.
- `internal/persistence/postgres/call_links_lease_test.go` (**create**) — testcontainers lease tests.
- `persistence/persistence.go` (**modify**) — re-export lease options on `NewCallLinkStore`/`NewCallNotifier`.
- `persistence/*_test.go` (**modify/create**) — façade option wiring assertion if useful.

---

### Task 1: MemCallLinkStore lease (runtime, no DB)

**Files:** modify `runtime/mem_calllink.go`; create `runtime/mem_calllink_lease_test.go`.

**Context:** `MemCallLinkStore` (struct `mu sync.Mutex; links map[string]*memLink`) implements `runtime.CallLinkStore.ClaimPending(ctx, limit) ([]PendingNotify, error)` — today it returns terminal (`l.terminal && !l.notified`) links up to `limit`. Read the current constructor (likely `NewMemCallLinkStore()`) and `memLink` shape first.

**Interfaces produced (used by later tasks for naming parity):**
- `runtime.MemCallLinkOption` functional-option type.
- `runtime.WithMemCallLinkLease(owner string, ttl time.Duration) MemCallLinkOption`.
- `runtime.WithMemCallLinkClock(clk clock.Clock) MemCallLinkOption`.
- `NewMemCallLinkStore(opts ...MemCallLinkOption) *MemCallLinkStore` (keep zero-arg call sites working — variadic).

**Steps (TDD):**
1. Write `runtime/mem_calllink_lease_test.go` (black-box `runtime_test`, fake clock via `clockwork.NewFakeClockAt`, adapter to `clock.Clock`): 
   - With `WithMemCallLinkLease("A", 30s)` + fake clock: seed a terminal link; first `ClaimPending` returns it (and records the lease); an immediate second `ClaimPending` returns nothing (lease live).
   - Advance the fake clock past 30s → a second `ClaimPending` reclaims it.
   - A `MarkNotified` link is never returned.
   - Default (no lease option / `ttl=0`): two consecutive `ClaimPending` both return the link (current behaviour).
   Add a `memLink.claimedAt time.Time` field as needed. Run → RED (option undefined).
2. Implement: add `owner string`, `leaseTTL time.Duration`, `clk clock.Clock` (default `clock.System()`) to the store; the options; `NewMemCallLinkStore(opts...)`. In `ClaimPending`, when `leaseTTL>0`: select terminal, un-notified links whose `claimedAt.IsZero() || !claimedAt.After(now.Add(-leaseTTL))`, set `claimedAt=now`, `claimedBy=owner`, return them (respect `limit`). When `leaseTTL<=0`: existing path unchanged. Run → GREEN.
3. Commit `feat(runtime): MemCallLinkStore opt-in claim lease`.

---

### Task 2: Postgres CallLinkStore lease (migration + claim SQL)

**Files:** create `internal/persistence/postgres/migrations/0006_call_link_lease.sql`; modify `internal/persistence/postgres/call_links.go`; create `internal/persistence/postgres/call_links_lease_test.go`.

**Context:** `CallLinkStore{pool}` with `NewCallLinkStore(pool)`. `ClaimPending(ctx, limit)` runs the plain SELECT over `wrkflw_call_links` (`status IN ('completed','failed') AND notified_at IS NULL ORDER BY child_instance_id [LIMIT $1]`) and scans into `runtime.PendingNotify`. `MarkNotified` sets `status='notified', notified_at=now`. Migrations live in `migrations/` and run via `Migrate`. Mirror the relay's `WithRelayClock` for clock injection and `TestRelaySkipLockedNoDoublePublish` for the concurrent-claim test.

**Interfaces produced:**
- `postgres.CallLinkOption`; `postgres.WithCallLinkLease(owner string, ttl time.Duration)`; `postgres.WithCallLinkClock(clk clock.Clock)`; `NewCallLinkStore(pool, opts ...CallLinkOption)`.

**Steps (TDD):**
1. Migration `0006_call_link_lease.sql` (goose up/down, matching the existing migration style):
   ```sql
   -- +goose Up
   ALTER TABLE wrkflw_call_links ADD COLUMN claimed_at TIMESTAMPTZ;
   ALTER TABLE wrkflw_call_links ADD COLUMN claimed_by TEXT;
   -- +goose Down
   ALTER TABLE wrkflw_call_links DROP COLUMN claimed_by;
   ALTER TABLE wrkflw_call_links DROP COLUMN claimed_at;
   ```
   (Match the exact goose annotation format used by 0005/0004 — read one first.)
2. Write `call_links_lease_test.go` (testcontainers; `database.RunTestDatabase(t)`, `pg.Migrate`, injected `clockwork` fake clock via the store's clock option):
   - Seed a terminal (`completed`, `notified_at` NULL) link (reuse the existing test seeding helper or insert directly).
   - With `WithCallLinkLease("A", 30s)` + fake clock: first `ClaimPending` returns the link and stamps `claimed_at`/`claimed_by='A'` (assert via a direct SELECT).
   - An immediate `ClaimPending` from a store with `WithCallLinkLease("B", 30s)` (same pool, same fake clock) returns nothing (lease live).
   - Advance the fake clock past 30s → store "B" reclaims it.
   - A `notified` link is never returned.
   - `ttl=0` (`NewCallLinkStore(pool)`): two consecutive claims both return the link (current behaviour) — guards backward-compat.
   Run → RED (option undefined).
3. Implement options + lease-claim. When `ttl>0`, `ClaimPending` runs the `UPDATE … FROM (SELECT … WHERE notified_at IS NULL AND (claimed_at IS NULL OR claimed_at <= $cutoff) ORDER BY child_instance_id FOR UPDATE SKIP LOCKED [LIMIT $n]) … RETURNING …` from spec §2.1 (cutoff `= clk.Now().Add(-ttl)`, claimed_at `= clk.Now()`), scanning the same columns into `runtime.PendingNotify`. When `ttl<=0`, the existing plain SELECT is unchanged. Default clock `clock.System()`. Run → GREEN. Lint.
4. Commit `feat(persistence/postgres): call-link claim lease (migration 0006)`.

---

### Task 3: Façade exposure + ADR/HANDOVER + gate

**Files:** modify `persistence/persistence.go` (+ a small façade test); ADR already written (0031) — verify; update `docs/plans/HANDOVER.md` + cross-session memory.

**Context:** `persistence.NewCallLinkStore(pool) runtime.CallLinkStore` and `persistence.NewCallNotifier(pool, deliver, reg, clk, opts...) *runtime.CallNotifier` (builds `postgres.NewCallLinkStore(pool)` internally). Re-export the postgres lease options so a consumer enables exclusivity through the façade.

**Steps (TDD):**
1. Add `persistence.WithCallLinkLease(owner, ttl)` and `persistence.WithCallLinkClock(clk)` (thin wrappers returning a façade option type that maps to the postgres options). Thread them into both `NewCallLinkStore` and `NewCallNotifier` (apply to the internal store). Write a façade test: `NewCallLinkStore(pool, persistence.WithCallLinkLease("A", 30s))` returns a store whose leased `ClaimPending` reserves a seeded row (testcontainers) — RED first (option undefined), then GREEN.
2. Verify ADR-0031 is accurate vs the implementation; update `docs/plans/HANDOVER.md` (mark the call-link half of "multi-replica exclusivity" done, note the timer half deferred with rationale) and the cross-session memory.
3. Commit `feat(persistence): expose call-link lease options on façade` (+ a separate docs commit).

---

## Verification Checklist (after all tasks)

- [ ] `go test -race -p 1 ./...` green.
- [ ] ≥85% on `runtime`, `internal/persistence/postgres`, `persistence`.
- [ ] `golangci-lint run ./...` clean.
- [ ] Engine/model production diff ZERO (`git diff --stat main -- engine/ model/`).
- [ ] Backward-compat: existing call-notifier/call-link tests unchanged and green; `ttl=0` path identical.
- [ ] Migration `0006` up+down applied by `Migrate` in a fresh test DB.
- [ ] Opus whole-branch review; address blockers; merge + push; HANDOVER + memory updated.

## Spec coverage self-check
- §2.1 Postgres lease-claim → Task 2. §2.2 Mem lease → Task 1. §2.3 façade → Task 3. §3 correctness (at-least-once, reclaim) → Tasks 1–2 tests. §4 testing → per-task. ✓
