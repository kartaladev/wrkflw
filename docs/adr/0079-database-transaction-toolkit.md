# 0079. Neutral database transaction toolkit (internal/database + internal/database/transaction)

Status: **Accepted — 2026-07-02.**
Plan: `docs/plans/2026-07-02-database-transaction-toolkit.md`.
Relates to: ADR-0073 (MySQL persistence backend), ADR-0067 (transactional outbox).

## Context

The persistence layer straddles two database drivers: `pgx/v5` for Postgres and
`database/sql` (via `go-sql-driver/mysql`) for MySQL. Before this work, every repository
and service that touched the database either captured a concrete driver type in its
signature or repeated the same boilerplate for executing queries in or out of a
transaction. Three problems followed:

1. **Driver coupling.** Code that accepted `*pgxpool.Pool` directly could not be exercised
   against a `*sql.DB` test double, and vice versa. Extending to a third driver (e.g.
   SQLite for unit tests) required invasive changes across many call sites.

2. **Ambient transaction friction.** Callers that needed to share a transaction across
   multiple repositories had to thread a `pgx.Tx` or `*sql.Tx` value manually. Because
   the two driver transaction types share no common interface, the threading value had to
   be typed as `any`, obscuring intent and making misuse undetectable at compile time.

3. **Extraction cleanliness.** Parts of the codebase (`internal/database`) were candidates
   for eventual extraction as a standalone toolkit. Any dependency on other `wrkflw`
   packages (e.g., `internal/persistence`, `runtime`) would block that extraction.

A neutral seam between driver specifics and application logic was needed.

## Decision

We introduce two internal packages — `internal/database` and
`internal/database/transaction` — each importing **only the Go standard library and the two
database drivers** (`pgx/v5`, `go-sql-driver/mysql`, `database/sql`). Neither package
imports any other `wrkflw` package, preserving extraction cleanliness. A separate
`internal/dbtest` package carries test helpers that do require other `wrkflw` packages,
keeping them out of the toolkit import graph.

### 1. Neutral Querier interface and result types

```go
type Querier interface {
    Exec(ctx context.Context, sql string, args ...any) (Result, error)
    Query(ctx context.Context, sql string, args ...any) (Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) Row
}
```

`Result`, `Rows`, and `Row` are thin interfaces over the corresponding `pgx` and
`database/sql` concrete types. All application-layer repository code is written against
`Querier`; neither `*pgxpool.Pool` nor `*sql.DB` appears in repository signatures.

### 2. From(conn any) type-switch dispatcher

We provide a `From(conn any) Querier` function that accepts any of the following and
returns a `Querier` wrapping it:

- `*pgxpool.Pool` — Postgres connection pool (pgx)
- `pgx.Tx` — Postgres transaction (pgx)
- `*sql.DB` — generic SQL connection pool
- `*sql.Tx` — generic SQL transaction
- `*sql.Conn` — single generic SQL connection

**Generics were considered and rejected.** Both `pgx.Tx` and `*sql.Tx` carry a `Commit`
method, but their full method sets are incompatible: `pgx.Tx` exposes batch execution,
`CopyFrom`, and copy-protocol methods absent from `database/sql`, while `*sql.Tx`
exposes `Stmt` and `StmtContext`. No single generic constraint captures both without
reducing to `any`. A type-switch dispatcher is the pragmatic seam: it keeps the call
sites clean while isolating driver-specific wrapping to one function.

### 3. Tx interface and BeginTx

`database.Tx` extends `Querier` with `Commit` and `Rollback`. `database.BeginTx(ctx,
conn, opts)` begins a transaction against a `Querier`, returning a `database.Tx`. This
provides a uniform transaction-begin API across both drivers.

### 4. Ambient context-propagated transaction (internal/database/transaction)

The `transaction` package implements a flat-join ambient transaction pattern:

- **`Begin(ctx, querier, fn)`** — opens a new transaction, stores it in `ctx`, runs
  `fn`, and commits or rolls back based on `fn`'s return value. The caller is the
  *owner*.
- **`JoinOrBegin(ctx, querier, fn)`** — if a transaction is already stored in `ctx`,
  joins it (inner participant); otherwise behaves as `Begin` (new owner).
- **`MarkRollback(ctx)`** — marks the ambient transaction rollback-only. Inner
  participants call this instead of rolling back directly.
- **`IsRollbackMarked(ctx) bool`** — lets the owner check whether any inner
  participant requested rollback before deciding to commit.

**Flat-join semantics.** An inner `JoinOrBegin` commit is a no-op; only the outermost
owner commits. An inner call to `MarkRollback` sets a flag that the owner observes
before committing, ensuring the whole unit rolls back without exposing a savepoint API.
Savepoints are explicitly deferred (see Consequences).

### 5. Batcher

`database.Batcher` / `database.NewBatch` provide a batch-execution API:

- Against `*pgxpool.Pool` or `pgx.Tx`, the batcher uses pgx's native pipeline protocol,
  sending all statements in a single round-trip.
- Against `database/sql` connections, the batcher executes statements sequentially in a
  loop. **This is documented emulation** — the API is identical; the round-trip savings
  are not present. Callers that care about throughput should prefer Postgres.

LISTEN/NOTIFY is a Postgres-specific protocol with no `database/sql` equivalent and
remains outside the neutral surface. Code that needs LISTEN/NOTIFY imports pgx directly.

### 6. Extraction constraint

`go list -deps ./internal/database/... | grep zakyalvan/krtlwrkflw` must return only:

```
github.com/zakyalvan/krtlwrkflw/internal/database
github.com/zakyalvan/krtlwrkflw/internal/database/transaction
```

This is enforced in CI. Any `wrkflw` import added to these packages is a failing check.

## Consequences

- **Cleaner repository signatures.** All repositories accept `database.Querier`; the
  driver is injected at the outermost wire point. Adding a third driver (e.g. SQLite for
  lightweight unit tests) requires only a new `From` branch, not changes to any
  repository.
- **Ambient transaction removes manual threading.** Callers use `JoinOrBegin` and
  `MarkRollback`; the driver handle is never threaded by hand through intermediate
  layers.
- **Extraction-ready.** `internal/database` and `internal/database/transaction` import
  only the standard library and the two database drivers, with no dependency on the rest
  of `wrkflw`. The `internal/dbtest` helper package carries test utilities that do
  require other `wrkflw` packages and is kept separate specifically to preserve this
  guarantee.
- **Phase-2 store refactor deferred.** Migrating the existing Postgres and MySQL
  repository implementations onto `database.Querier` is a separate work item (its own
  spec). The toolkit is available immediately; adoption by existing stores follows in a
  subsequent phase.
- **`Rebind` (placeholder translation) deferred.** Postgres uses `$1`/`$2` positional
  placeholders; MySQL uses `?`. A `Rebind` utility to translate between them was
  considered and deferred; for now, callers author two query variants or use a helper
  outside this package.
- **Savepoints deferred.** The flat-join model covers the common use case (service +
  repository sharing one transaction). Nested independent savepoints (partial rollback
  within a transaction) are deferred to a future ADR if a concrete use case arises.
- **`database/sql` batch emulation is documented, not transparent.** Teams that
  benchmark batch inserts must use Postgres (pgx) to get native pipeline savings. The
  documented emulation prevents silent performance cliffs on MySQL.
