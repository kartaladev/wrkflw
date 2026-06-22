# Design: ops-hardening trio (dedup pruning, MarkNotified clock, advisory-lock close guard)

**Date:** 2026-06-23
**Status:** Approved (autonomous run)
**Track:** Consolidated-backlog (Production-hardening + Test/doc). Non-engine.
**ADR:** 0033.

## 1. Scope

Three small, independent, **non-engine** hardening items from the backlog, bundled into one track
(engine/model ZERO diff; changes only in `internal/persistence/postgres` + `persistence` façade):

1. **`wrkflw_processed_message` pruning** — the idempotent-consumer dedup table (ADR-0018) grows
   unbounded; operators have no supported way to trim it. Add a pruning method.
2. **`MarkNotified` clock injection** — `internal/persistence/postgres/call_links.go` `MarkNotified`
   stamps `notified_at` with `time.Now().UTC()` instead of the injected `c.clk` (added in ADR-0031),
   so a fake clock can't drive it deterministically. Use `c.clk`.
3. **`AdvisoryLockOwnership` use-after-close guard** — after `Close` returns the pinned session conn
   to the pool, `Acquire`/`Release` call `Exec`/`QueryRow` on a released `*pgxpool.Conn` (documented
   as "undefined behaviour"). Add an explicit guard returning a sentinel error.

## 2. Design

### 2.1 Dedup pruning

Add to the internal `Deduper` and the `persistence.Deduper` interface:

```go
// Prune deletes processed-message records older than `before`, returning the
// number of rows removed. Run it periodically (e.g. a cron) with `before` well
// past the relay's MaxDeliveryAttempts × max backoff so no in-flight redelivery
// is still deduping against a pruned id.
Prune(ctx context.Context, before time.Time) (deleted int64, err error)
```

Internal impl: `DELETE FROM wrkflw_processed_message WHERE processed_at < $1`, returning
`tag.RowsAffected()`. The cutoff is an absolute `time.Time` passed by the caller (who owns the clock
+ retention policy) — keeps `Deduper` clock-free and the method trivially testable. `workflow-postgres:`
error prefix. The façade interface gains `Prune`; the compile-time assertion already guards it.

### 2.2 MarkNotified clock

`call_links.go:304`: replace `time.Now().UTC()` with `c.clk.Now().UTC()`. `c.clk` defaults to
`clock.System()` (unchanged for existing callers) and is overridable via `WithCallLinkClock`. No
signature change. (Pre-existing nit noted in ADR-0031.)

### 2.3 Advisory-lock close guard

`AdvisoryLockOwnership` gains a `closed bool` (guarded by the existing `mu`). `Close` sets it.
`Acquire` and `Release` check it first under the lock and return a new sentinel
`ErrOwnershipClosed` (`"workflow-postgres: ownership: closed"`) instead of touching the released
conn. `Close` becomes idempotent (a second `Close` is a no-op returning nil). This converts
"undefined behaviour" into a clear, testable error.

## 3. Testing strategy

- **Prune** (testcontainers): seed rows with `processed_at` at two ages (insert with explicit
  timestamps), `Prune(before=mid)` deletes only the older rows and returns the right count; a second
  prune over an empty window returns 0. Façade-level test through `persistence.NewDeduper`.
- **MarkNotified clock** (testcontainers): with `WithCallLinkClock(fakeClock)`, after `MarkNotified`
  a direct `SELECT notified_at` equals the fake clock's time (not wall-clock).
- **Advisory-lock guard** (testcontainers): after `Close`, `Acquire` and `Release` return
  `ErrOwnershipClosed`; a second `Close` returns nil. (Acquire/Release/Close happy paths already
  tested in ownership_test.go.)

**Gate:** `go test -race -p 1 ./...` green; ≥85% on `internal/persistence/postgres` + `persistence`;
`golangci-lint` clean; engine/model diff ZERO; migrations unchanged (no new migration — Prune is a
DML method, the table already exists).

## 4. ADR

| ADR | Decision |
|---|---|
| **0033** | Ops-hardening trio: (1) `Deduper.Prune(ctx, before)` DML method (caller-supplied absolute cutoff; no new migration; façade-exposed) for `wrkflw_processed_message` retention; (2) `MarkNotified` uses the injected `c.clk` not wall-clock; (3) `AdvisoryLockOwnership` gains a `closed` guard + `ErrOwnershipClosed` so post-`Close` `Acquire`/`Release` fail cleanly and `Close` is idempotent. Non-engine; engine/model untouched. |
