# Ops-hardening trio — Implementation Plan

> Executed via superpowers:subagent-driven-development. Strict TDD: visible RED before GREEN. Each task ends green + committed.

**Goal:** Three non-engine persistence-hardening items: `Deduper.Prune`, `MarkNotified` clock injection, `AdvisoryLockOwnership` close guard. Engine/model ZERO diff.

## Global Constraints
- Module `github.com/kartaladev/wrkflw`; no `pkg/` prefix.
- Strict TDD; RED before GREEN.
- Engine/model production diff ZERO. Changes only in `internal/persistence/postgres` + `persistence`.
- `workflow-` error prefix; assert `errors.Is`; testcontainers via `database.RunTestDatabase`; `-p 1`.
- Backward-compat: no signature changes to existing methods except additive interface growth (`Prune`).
- Gate: `go test -race -p 1 ./...` green; ≥85% on internal/persistence/postgres + persistence; lint clean.
- Spec: docs/specs/2026-06-23-ops-hardening-design.md. ADR-0033.

## File Structure
- `internal/persistence/postgres/dedup.go` (**modify**) — `Prune`.
- `internal/persistence/postgres/dedup_prune_test.go` (**create**).
- `persistence/dedup.go` (**modify**) — `Prune` on the `Deduper` interface.
- `internal/persistence/postgres/call_links.go` (**modify**) — `MarkNotified` uses `c.clk`.
- `internal/persistence/postgres/ownership.go` (**modify**) — `closed` guard + `ErrOwnershipClosed`.
- `internal/persistence/postgres/call_links_marknotified_test.go` (**create**) + ownership guard test (extend `ownership_extra_test.go` or new file).

---

### Task 1: Deduper.Prune (internal + façade)

**Files:** modify `internal/persistence/postgres/dedup.go`, `persistence/dedup.go`; create `internal/persistence/postgres/dedup_prune_test.go`.

**Context:** `Deduper struct{ pool *pgxpool.Pool }`, `NewDeduper(pool)`, `Seen(ctx, tx, subscriber, messageID)`. Table `wrkflw_processed_message(subscriber, message_id, processed_at TIMESTAMPTZ DEFAULT now(), PRIMARY KEY(subscriber, message_id))`. Façade `persistence.Deduper` interface + `var _ Deduper = (*postgres.Deduper)(nil)`.

**Produces:** `Prune(ctx context.Context, before time.Time) (int64, error)` on internal `*Deduper` and the `persistence.Deduper` interface.

**Steps (TDD):**
1. Write `dedup_prune_test.go` (testcontainers; `database.RunTestDatabase`, `pg.Migrate`): INSERT two rows with explicit `processed_at` (one old e.g. `2026-01-01`, one recent e.g. `2026-06-01`) via direct `pool.Exec`. `Prune(ctx, before=2026-03-01)` returns `1` and only the recent row remains (assert via `SELECT count(*)` and which message_id survives). A second `Prune(ctx, before=2026-03-01)` returns `0`. Run RED (Prune undefined).
2. Implement `func (d *Deduper) Prune(ctx, before time.Time) (int64, error)`: `tag, err := d.pool.Exec(ctx, "DELETE FROM wrkflw_processed_message WHERE processed_at < $1", before)`; wrap err `workflow-postgres: deduper: prune: %w`; return `tag.RowsAffected(), nil`. Add `Prune(ctx context.Context, before time.Time) (int64, error)` to the `persistence.Deduper` interface (godoc per spec §2.1). Run GREEN. Lint.
3. Commit `feat(persistence): Deduper.Prune for processed-message retention`.

---

### Task 2: MarkNotified clock + AdvisoryLockOwnership close guard

**Files:** modify `internal/persistence/postgres/call_links.go`, `internal/persistence/postgres/ownership.go`; create/extend tests.

**Context:** `call_links.go:304` `MarkNotified` uses `time.Now().UTC()`; the store has `clk clock.Clock` (field `clk`, default `clock.System()`), already used at `call_links.go:120` as `c.clk.Now()`. `ownership.go` `AdvisoryLockOwnership{conn *pgxpool.Conn; mu sync.Mutex; held map[string]bool}` with `Acquire`/`Release`/`Close` (all lock `o.mu`); `Close` releases the conn; post-Close use is "undefined".

**Steps (TDD):**
1. **MarkNotified clock** — write `call_links_marknotified_test.go` (testcontainers): build the store with `WithCallLinkClock(clockwork.NewFakeClockAt(someFixedTime))`, seed a terminal link, `MarkNotified(childID)`, then `SELECT notified_at` and assert it equals the fake time (UTC), NOT wall-clock. Run RED (still wall-clock → assertion fails). Change `time.Now().UTC()` → `c.clk.Now().UTC()`. Run GREEN.
2. **Ownership guard** — write a test (new file or extend `ownership_extra_test.go`): construct `NewAdvisoryLockOwnership`, `Close()`, then assert `Acquire(ctx, "x")` returns `ErrOwnershipClosed` (errors.Is), `Release(ctx, "x")` returns `ErrOwnershipClosed`, and a second `Close()` returns nil. Run RED (no guard → panic/err on released conn). Implement: add `closed bool` to the struct; `var ErrOwnershipClosed = errors.New("workflow-postgres: ownership: closed")`; in `Acquire`/`Release` under `o.mu`, `if o.closed { return …, ErrOwnershipClosed }` (Acquire returns `false, ErrOwnershipClosed`); in `Close` under the lock, `if o.closed { return nil }` then set `o.closed = true` after releasing. Run GREEN. Lint.
3. Commit `fix(persistence/postgres): MarkNotified clock + advisory-lock close guard`.

---

### Task 3: ADR/HANDOVER/memory + gate (controller)
ADR-0033 written. Controller verifies, updates HANDOVER + memory, runs full gate, whole-branch review, merges.

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% touched pkgs; lint clean.
- [ ] Engine/model production diff ZERO.
- [ ] `persistence.Deduper` interface assertion still compiles; existing Seen/MarkNotified/ownership tests green.
- [ ] Whole-branch review; merge + push; HANDOVER + memory updated.

## Spec coverage self-check
- §2.1 Prune → Task 1. §2.2 MarkNotified clock → Task 2. §2.3 ownership guard → Task 2. §3 tests → per-task. ✓
