# Database Querier + Ambient Transaction Toolkit & Time-Correctness (Phase 1) — Design

- Status: Approved (brainstorming), implementation pending
- Date: 2026-07-02
- Scope: **Phase 1 only.** Refactoring the existing `internal/persistence/{postgres,mysql}`
  stores onto the new toolkit is a **separate follow-up spec** (Phase 2).

## Purpose

Three related gaps in the persistence layer:

1. **No driver-agnostic query/transaction abstraction.** Store code imports `pgx`
   or `database/sql` directly, and each dialect hand-rolls its own `Begin`/`Commit`/
   `Rollback` and its own `DBTX` seam. There is no uniform way to (a) run SQL without
   naming the driver, (b) treat "transactional vs non-transactional" transparently,
   or (c) compose an ambient transaction that downstream code joins.
2. **Time can drift.** Postgres columns are `TIMESTAMPTZ` (absolute instant, safe);
   MySQL columns are `DATETIME(6)` (no timezone). Correctness depends on the consumer's
   DSN carrying `parseTime=true&loc=UTC` and on the session `time_zone` — neither is
   enforced, and there is no UTC normalization on the read path. Two MySQL columns even
   default to DB-side `CURRENT_TIMESTAMP(6)`, which is session-`time_zone`-sensitive.
3. **No real-database example.** Every `examples/` program uses in-memory stores; nothing
   demonstrates a real Postgres/MySQL round-trip.

## Goals

- A **driver-agnostic query abstraction** (`internal/database`) so implementation code
  never imports `pgx`, `database/sql`, or `sqlx`, and cannot tell whether its handle is a
  pool or an in-flight transaction.
- A **generic ambient-transaction toolkit** (`internal/database/transaction`) offering both
  an **imperative** owner handle (`Begin` → `Commit`/`Rollback`) and a **declarative**
  ambient join (`JoinOrBegin`, `MarkRollback`) propagated through `context.Context`,
  supporting multiple database types with no change to calling code on migration.
- **Time correctness** with no timezone drift across either backend.
- **Real-DB verification**: testcontainers integration tests (both dialects) plus a
  self-contained runnable example.
- Both new packages depend only on **stdlib + drivers**, with **zero wrkflw imports**, so
  the toolkit can be extracted into its own library later.

## Non-Goals (Phase 1)

- Refactoring the existing `postgres`/`mysql` stores onto the toolkit (→ Phase 2 spec).
- A single dialect-agnostic SQL string per query (placeholder/clause differences remain);
  a `database.Rebind` helper is explicitly deferred.
- Savepoint-based **nested** transactions; v1 is **flat join** (see Transaction Semantics).
- Abstracting Postgres `LISTEN/NOTIFY` — it has no MySQL counterpart and stays
  Postgres-binding-specific (see Driver-Specific Features).

## Architecture

```
internal/database/                 Querier, Result, Rows, Row, Batcher/Batch/BatchResults,
                                   From(conn any) (Querier, error)          ← ALL driver knowledge here
internal/database/transaction/     Control, Querier(=database.Querier+Control),
                                   Begin, JoinOrBegin, MarkRollback, IsRollbackMarked
        ↑ both import only stdlib + pgx + database/sql — NO engine/model/runtime → extractable as one toolkit
```

- `transaction` depends on `database`. `database` owns every driver `import`.
- "Extractable" means **no wrkflw coupling**; depending on the drivers it abstracts is
  expected of a database toolkit. A later split with build tags to make each driver
  optional is possible but out of scope.

### Package `database`

```go
// Querier runs SQL without revealing the driver OR whether the underlying handle
// is a pool (non-transactional) or an in-flight transaction.
type Querier interface {
    Exec(ctx context.Context, query string, args ...any) (Result, error)
    Query(ctx context.Context, query string, args ...any) (Rows, error)
    QueryRow(ctx context.Context, query string, args ...any) Row
}

type Result interface { RowsAffected() (int64, error) }
type Rows   interface { Next() bool; Scan(dest ...any) error; Err() error; Close() error }
type Row    interface { Scan(dest ...any) error }

// From adapts a raw driver handle to a Querier. It type-switches over the supported
// handle types and returns an error for anything else.
//   accepted: *pgxpool.Pool, pgx.Tx, *sql.DB, *sql.Tx, *sql.Conn
func From(conn any) (Querier, error)
```

- `From` accepting **both** pools/dbs **and** transactions is what makes "transactional or
  not" invisible: a repository takes a `database.Querier` and cannot tell the difference.
- Scanning goes through neutral `Rows`/`Row`; both drivers expose `Next/Scan/Err/Close`
  and `Scan`. `pgx.Rows.Close()` is void → the adapter returns `nil`. Types in use
  (`[]byte` for JSON, `time.Time`, primitives) scan identically on both drivers.

#### `Batcher` (secondary, optional)

```go
type Batch        interface { Queue(query string, args ...any) }
type Batcher      interface { SendBatch(ctx context.Context, b Batch) BatchResults }
type BatchResults interface { Exec() (Result, error); Query() (Rows, error); Close() error }
```

- A `Querier` from `From` **also** implements `Batcher` when the driver supports it; callers
  type-assert: `if b, ok := q.(database.Batcher); ok { ... }`.
- **pgx** implements it natively (true pipelining via `SendBatch`).
- **`database/sql`** has no pipelining; its adapter **emulates** by executing the queued
  statements sequentially — identical observable semantics, **no round-trip savings**. This
  caveat is documented on the type so it never reads as a silent performance win.

### Package `transaction`

```go
type Control interface {
    Commit(ctx context.Context) error    // honors the rollback-only mark → rolls back instead
    Rollback(ctx context.Context) error
}

// Querier is the transactional handle: a database.Querier you can also commit/rollback.
type Querier interface {
    database.Querier
    Control
}

// Begin starts a transaction on conn, stashes it into the returned context (for ambient
// join), and returns the transactional Querier (the imperative owner handle).
func Begin(ctx context.Context, conn any) (Querier, context.Context, error)

// JoinOrBegin joins the ambient transaction in ctx if present; otherwise begins a fresh
// one. When it JOINS, the returned Querier's Commit/Rollback are no-ops (the owner controls
// the real commit). When it BEGINS, they are real.
func JoinOrBegin(ctx context.Context, conn any) (Querier, error)

// MarkRollback flags the ambient transaction rollback-only. The owner's Commit then rolls
// back. No-op if ctx carries no transaction.
func MarkRollback(ctx context.Context)
func IsRollbackMarked(ctx context.Context) bool
```

Internally, `Begin`/`JoinOrBegin` obtain the driver transaction via the `database` package's
driver dispatch (the same type-switch that backs `From`, extended with a begin capability),
wrap it as a `database.Querier`, and attach commit/rollback + the rollback-only flag. `conn`
is `any` to honor a single ergonomic entry point (`transaction.Begin(pool)` /
`transaction.Begin(db)`); Go generics cannot dispatch `.Begin`/`.BeginTx` over an
incompatible `*pgxpool.Pool | *sql.DB` union, so the dispatch is a type-switch localized to
the driver-aware `database` package.

#### Transaction semantics (flat join, v1)

- **Owner (imperative):** `tx, ctx, err := transaction.Begin(ctx, pool)`; end with
  `tx.Commit(ctx)` / `tx.Rollback(ctx)`.
- **Downstream (declarative/ambient):** `tx, err := transaction.JoinOrBegin(ctx, pool)`.
  Joins the ambient tx if `ctx` has one (inner `Commit`/`Rollback` = no-op) else begins fresh
  (real). Callers always write the same `defer tx.Rollback(ctx)` / `return tx.Commit(ctx)`;
  it only "bites" when the call owns the tx.
- **Propagation rule:** only `Begin` stashes the transaction into the returned `context.Context`.
  A fresh tx begun by `JoinOrBegin` (no ambient present) is a **leaf** owned by the caller and
  is **not** re-propagated to deeper `JoinOrBegin` calls (those would begin their own). To
  compose nested joins, start the outermost scope with `Begin` and thread its returned `ctx`
  downward. This keeps `JoinOrBegin`'s two-value signature while making the ambient origin
  unambiguous.
- **Veto:** inner code calls `transaction.MarkRollback(ctx)`; the owner's `Commit` observes
  the flag and rolls back — no stack unwinding required.
- **Nesting:** flat only. True nested savepoints (`SAVEPOINT` / `ROLLBACK TO SAVEPOINT`) are
  deferred; the engine's stores don't need them.

Canonical usage (driver-free, tx-transparent store code):

```go
func (s *store) upsert(ctx context.Context, q database.Querier, r rec) error {
    _, err := q.Exec(ctx, upsertSQL, r.id, r.data)   // no pgx, no sql, unaware it's a tx
    return err
}

// owner
tx, ctx, err := transaction.Begin(ctx, pool)
if err != nil { return err }
defer tx.Rollback(ctx)
if err := s.upsert(ctx, tx, r); err != nil { return err }   // tx IS a database.Querier
if bad { transaction.MarkRollback(ctx) }
return tx.Commit(ctx)                                        // rolls back if marked
```

### Driver-specific features

- `NOTIFY wrkflw_outbox` **emit** is a plain `Exec("NOTIFY …")` → fits `Querier.Exec`.
- `LISTEN` + `WaitForNotification` needs a hijacked dedicated pgx connection and has **no
  `database/sql`/MySQL counterpart** → it stays Postgres-binding-specific (as it is today,
  in `internal/persistence/postgres/relay.go` and `internal/authz/casbin/pg_watcher.go`).
  Nothing to abstract.

## Time-Correctness Hardening

Independent of the toolkit; a surgical change to the **existing** stores' connection setup
and read paths (not the Phase-2 refactor). Three layers:

**A. Connection-level pinning (primary).**
- **MySQL:** require DSN `parseTime=true&loc=UTC` (read-back interpretation) **and** pin the
  session `time_zone = '+00:00'` via a connection-init hook applied to *every* pooled
  connection (covers the `DEFAULT CURRENT_TIMESTAMP(6)` columns
  `wrkflw_outbox.next_attempt_at` and `wrkflw_processed_message.processed_at`, plus any
  `NOW()`).
- **Postgres:** pin `TimeZone=UTC` (pool `RuntimeParams` / `SET TIME ZONE 'UTC'`) so pgx
  returns `timestamptz` as UTC-located values. The instant is already safe.

**B. Fail-fast probe at `Open` (guardrail).** `OpenMySQL`/`OpenPostgres` run a one-shot probe
— `SELECT` a known literal datetime into a `time.Time` and assert its zone offset is 0. A
misconfigured handle (`loc=Local`) fails **at startup** with a clear, actionable error
instead of drifting silently in production.

**C. Normalize-on-read (belt-and-suspenders).** Every `time.Time` scanned in the store
mapping layer passes through `.UTC()` before reaching engine/runtime. Implemented as a
`database.UTC(t time.Time) time.Time` helper (and/or a `database.UTCTime` scan target that
normalizes in `Scan`); existing store read sites call it explicitly.

Writes already use `time.Now().UTC()` everywhere and are unchanged.

## Error Handling

- `From`/`Begin`/`JoinOrBegin` return a descriptive error for an unsupported `conn` type
  (`workflow-database: unsupported connection type %T`).
- `Begin`/`JoinOrBegin` wrap driver begin failures with the package prefix.
- `Control.Commit` returns the driver commit error, or the rollback error when the tx was
  marked rollback-only.
- No-op `Commit`/`Rollback` (joined case) return `nil`.
- Sentinels follow the project's `workflow-<pkg>:` prefix convention (e.g.
  `workflow-database:`, `workflow-transaction:`).

## Testing Strategy

testcontainers against **both** real engines (reuse `internal/database/testutils.go`
`RunTestDatabase` for PG and `internal/database/testutils_mysql.go` for MySQL):

- **Toolkit round-trip:** `Begin` → `Exec` via `database.Querier` → nested `JoinOrBegin`
  (asserts inner commit is a no-op and outer commit persists) → `MarkRollback` rolls back the
  whole unit of work → verify persisted/absent rows.
- **`From` transparency:** the same repository function runs identically given a pool-backed
  and a tx-backed `Querier`.
- **`Batcher`:** pgx pipelines; `database/sql` emulates — both produce identical results;
  assert the documented semantics.
- **Time fidelity:** write a known instant, read back, assert `.Equal()` **and**
  `Location()` offset 0; the whole suite runs under **`TZ=Asia/Jakarta`** (non-UTC host) to
  catch `time.Local` leakage; a golden case asserts a timer `fire_at` rehydrates
  bit-identical; a negative test asserts the fail-fast probe rejects a `loc=Local` MySQL DSN.
- Black-box tests (`*_test` packages); per-package coverage ≥ 85%; `-race` clean; goleak
  where goroutines/containers are involved.

## Examples

- `examples/real_db_transaction/main.go` — a runnable program that **spins up Postgres and
  MySQL via testcontainers on `go run`** (no manual DB setup, no compose file), migrates
  both, and runs the **same** consumer code path on each dialect: `transaction.Begin` →
  repository writes through `database.Querier` → a `MarkRollback` demonstration → `Commit`,
  printing the round-tripped timestamps to visibly show no drift. Requires a running Docker
  daemon (documented in the file header).

## ADRs

Record as part of implementation (next free number to confirm at plan time; index currently
points at **0078**):

- **ADR — Database querier + ambient transaction toolkit.** The `database.Querier` /
  `transaction` abstraction, the generic-vs-type-switch decision, flat-join semantics, and
  the extraction constraint (stdlib + drivers, no wrkflw imports).
- **ADR — UTC time discipline for SQL backends.** Connection-level pinning + fail-fast probe
  + normalize-on-read, and the MySQL `DATETIME(6)` vs Postgres `TIMESTAMPTZ` rationale.

## Deferred / Follow-ups

- **Phase 2:** migrate existing `postgres`/`mysql` stores onto `database.Querier` +
  `transaction` (separate spec → plan → build).
- `database.Rebind(dialect, sql)` for single-SQL stores.
- Savepoint-based nested transactions.
- Optional build-tag split so each driver dependency is opt-in (aids extraction).
