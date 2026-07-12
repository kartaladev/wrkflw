# Database Toolkit + Transactions + Time-Correctness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a driver-agnostic SQL query + ambient-transaction toolkit (`internal/database` + `internal/database/transaction`) and enforce UTC time-correctness across the Postgres and MySQL backends, verified against real databases.

**Architecture:** A low-level `database` package hides pgx/`database/sql` behind a `Querier` (Exec/Query/QueryRow) with neutral `Result`/`Rows`/`Row`, a `From(conn any)` dispatcher, an optional `Batcher`, and a raw `Tx`+`BeginTx`. A `transaction` package builds an ambient, context-propagated transaction on top: `Begin` (imperative owner) + `JoinOrBegin`/`MarkRollback` (declarative), with flat-join semantics (inner commit is a no-op, rollback-only mark honored by the owner). Time-correctness pins UTC at the connection level, fails fast at `Open`, and normalizes on read.

**Tech Stack:** Go 1.25, `jackc/pgx/v5` (+ `pgxpool`), `go-sql-driver/mysql` via `database/sql`, `testcontainers-go`, existing `internal/database/testutils.go` (PG) + `testutils_mysql.go` (MySQL) helpers.

## Global Constraints

- **Go 1.25**; hard requirement.
- Both new packages import **only stdlib + `pgx` + `database/sql`** — **zero** imports of `engine`, `model`, `runtime`, or any other wrkflw package (extraction constraint).
- Error sentinels/messages use the `workflow-<package>:` prefix (e.g. `workflow-database:`, `workflow-transaction:`).
- **TDD strict**: every new symbol gets a failing test first, verified red via `go test`, before implementation. No batching test+impl in one edit.
- Black-box tests where practical (`package X_test`). Table tests follow the project `table-test` skill (`assert` closure form, `ctx` modifier, `t.Context()`).
- Tests needing a real DB use testcontainers via the existing helpers — never mocks, never a shared dev DB (`use-testcontainers` skill). Requires a running Docker daemon.
- Per-package coverage ≥ 85%; `go test -race ./...` clean; `golangci-lint run ./...` clean before done.
- **Phase 1 only.** Do NOT refactor the existing `internal/persistence/{postgres,mysql}` stores onto the toolkit — that is a deferred follow-up spec. Time-correctness changes to existing stores are surgical (connection setup, `Open` probe, read-site `.UTC()`) and explicitly in-scope.
- Writes already use `time.Now().UTC()`; leave write timestamps unchanged.

---

## File Structure

**New — `internal/database/` (neutral querier + driver adapters):**
- `errors.go` — `ErrUnsupportedConn`.
- `querier.go` — `Querier`, `Result`, `Rows`, `Row` interfaces.
- `tx.go` — `Tx` interface, `From(conn any)`, `BeginTx(ctx, conn any)` dispatch (type-switch).
- `batch.go` — `Batch`, `Batcher`, `BatchResults` interfaces + `NewBatch()`.
- `adapter_pgx.go` — pgx adapters (querier/result/rows/row/tx/batch) over the pgx `DBTX` seam.
- `adapter_sql.go` — `database/sql` adapters (querier/result/rows/row/tx) + batch emulation.
- `timeutil.go` — `UTC(t)` helper, `Dialect`, `ProbeUTC(ctx, q, dialect)`.

**New — `internal/database/transaction/`:**
- `transaction.go` — `Control`, `Querier`, ambient ctx handle, `MarkRollback`, `IsRollbackMarked`.
- `begin.go` — `Begin`, `JoinOrBegin`, `ownerQuerier`, `joinedQuerier`.

**Modify — time-correctness (existing stores, surgical):**
- `persistence/mysql.go` — add `MySQLDSN(base string) (string, error)`; call `ProbeUTC` in `OpenMySQL`.
- `persistence/persistence.go` — call `ProbeUTC` in `OpenPostgres`.
- `internal/persistence/postgres/store.go` + `internal/persistence/postgres/timerstore.go` — `.UTC()` on scanned times.
- `internal/persistence/mysql/store.go` + `internal/persistence/mysql/timerstore.go` — `.UTC()` on scanned times.

**New — example + ADRs:**
- `examples/real_db_transaction/main.go` — testcontainers-driven runnable demo (both dialects).
- `docs/adr/00NN-database-transaction-toolkit.md`, `docs/adr/00NN-utc-time-discipline.md` (numbers confirmed in Task 16).

---

## Task 1: `database` neutral interfaces + `From` skeleton

**Files:**
- Create: `internal/database/errors.go`, `internal/database/querier.go`, `internal/database/tx.go`
- Test: `internal/database/from_test.go`

**Interfaces:**
- Produces: `database.Querier` (`Exec(ctx,string,...any)(Result,error)`, `Query(...)(Rows,error)`, `QueryRow(...)Row`), `database.Result` (`RowsAffected()(int64,error)`), `database.Rows` (`Next()bool`,`Scan(...any)error`,`Err()error`,`Close()error`), `database.Row` (`Scan(...any)error`), `database.Tx` (embeds `Querier` + `Commit(ctx)error`,`Rollback(ctx)error`), `database.From(conn any)(Querier,error)`, `database.BeginTx(ctx,conn any)(Tx,error)`, `database.ErrUnsupportedConn`.

- [ ] **Step 1: Write the failing test**

```go
// internal/database/from_test.go
package database_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
)

func TestFromRejectsUnsupportedConn(t *testing.T) {
	_, err := database.From("not a conn")
	if !errors.Is(err, database.ErrUnsupportedConn) {
		t.Fatalf("want ErrUnsupportedConn, got %v", err)
	}
}

func TestBeginTxRejectsUnsupportedConn(t *testing.T) {
	_, err := database.BeginTx(t.Context(), 42)
	if !errors.Is(err, database.ErrUnsupportedConn) {
		t.Fatalf("want ErrUnsupportedConn, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/`
Expected: FAIL — `undefined: database.From`, `database.ErrUnsupportedConn`, `database.BeginTx`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/database/errors.go
package database

import "errors"

// ErrUnsupportedConn is returned by From/BeginTx for a connection handle whose
// concrete type is not one of the supported driver types.
var ErrUnsupportedConn = errors.New("workflow-database: unsupported connection type")
```

```go
// internal/database/querier.go
package database

import "context"

// Querier runs SQL without revealing the driver or whether the underlying handle
// is a pool (non-transactional) or an in-flight transaction.
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) Row
}

// Result is the neutral outcome of an Exec.
type Result interface{ RowsAffected() (int64, error) }

// Rows is the neutral iterator over a Query result set.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Row is the neutral single-row result of a QueryRow.
type Row interface{ Scan(dest ...any) error }
```

```go
// internal/database/tx.go
package database

import (
	"context"
	"fmt"
)

// Tx is a Querier that can additionally be committed or rolled back. It is the
// raw driver transaction wrapped as a Querier, with no ambient/rollback-mark
// semantics (that layer lives in the transaction package).
type Tx interface {
	Querier
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// From adapts a raw driver handle to a Querier. Supported: *pgxpool.Pool, pgx.Tx,
// *sql.DB, *sql.Tx, *sql.Conn. Any other type yields ErrUnsupportedConn.
func From(conn any) (Querier, error) {
	switch c := conn.(type) {
	default:
		_ = c
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedConn, conn)
	}
}

// BeginTx starts a transaction on conn and returns it as a Tx. Supported conns
// are the pool/db types (*pgxpool.Pool, *sql.DB). Any other type yields
// ErrUnsupportedConn.
func BeginTx(ctx context.Context, conn any) (Tx, error) {
	switch c := conn.(type) {
	default:
		_ = c
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedConn, conn)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/errors.go internal/database/querier.go internal/database/tx.go internal/database/from_test.go
git commit -m "feat(database): neutral Querier/Result/Rows/Row interfaces + From/BeginTx skeleton"
```

---

## Task 2: pgx adapter (`From(*pgxpool.Pool)` round-trip)

**Files:**
- Create: `internal/database/adapter_pgx.go`
- Modify: `internal/database/tx.go` (add `*pgxpool.Pool` + `pgx.Tx` cases to `From`)
- Test: `internal/database/adapter_pgx_test.go`

**Interfaces:**
- Consumes: `database.Querier/Result/Rows/Row` (Task 1), `internal/database/testutils.go` `RunTestDatabase(t)`.
- Produces: internal `pgxQuerier`, `pgxResult`, `pgxRows`, `pgxRow` wrappers; `From` accepts `*pgxpool.Pool` and `pgx.Tx`.

- [ ] **Step 1: Write the failing test**

```go
// internal/database/adapter_pgx_test.go
package database_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
)

func TestPgxQuerierRoundTrip(t *testing.T) {
	pool := database.RunTestDatabase(t) // testcontainers PG; returns *pgxpool.Pool
	q, err := database.From(pool)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if _, err := q.Exec(t.Context(), `CREATE TEMP TABLE t (id int, name text)`); err != nil {
		t.Fatalf("exec create: %v", err)
	}
	res, err := q.Exec(t.Context(), `INSERT INTO t VALUES ($1,$2)`, 1, "a")
	if err != nil {
		t.Fatalf("exec insert: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	var name string
	if err := q.QueryRow(t.Context(), `SELECT name FROM t WHERE id=$1`, 1).Scan(&name); err != nil {
		t.Fatalf("queryrow: %v", err)
	}
	if name != "a" {
		t.Fatalf("name = %q, want a", name)
	}
}
```

> Note: `RunTestDatabase` currently lives in `package database` (`internal/database/testutils.go`). Confirm it is exported and usable from `database_test`; if it is `internal`-only to the package, call it via a small exported test shim or from the same package. Keep the black-box preference where the helper allows.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/ -run TestPgxQuerierRoundTrip`
Expected: FAIL — `From` returns `ErrUnsupportedConn` for `*pgxpool.Pool`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/database/adapter_pgx.go
package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxDBTX is the minimal pgx querier satisfied by *pgxpool.Pool and pgx.Tx.
type pgxDBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type pgxQuerier struct{ db pgxDBTX }

func (q pgxQuerier) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	ct, err := q.db.Exec(ctx, sql, args...)
	return pgxResult{ct}, err
}
func (q pgxQuerier) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := q.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgxRows{rows}, nil
}
func (q pgxQuerier) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return pgxRow{q.db.QueryRow(ctx, sql, args...)}
}

type pgxResult struct{ ct pgconn.CommandTag }

func (r pgxResult) RowsAffected() (int64, error) { return r.ct.RowsAffected(), nil }

type pgxRows struct{ rows pgx.Rows }

func (r pgxRows) Next() bool             { return r.rows.Next() }
func (r pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r pgxRows) Err() error             { return r.rows.Err() }
func (r pgxRows) Close() error           { r.rows.Close(); return nil } // pgx Close is void

type pgxRow struct{ row pgx.Row }

func (r pgxRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

// compile-time checks
var (
	_ pgxDBTX = (*pgxpool.Pool)(nil)
	_ pgxDBTX = (pgx.Tx)(nil)
)
```

Add to `internal/database/tx.go` `From` switch (replace the `default`-only body):

```go
func From(conn any) (Querier, error) {
	switch c := conn.(type) {
	case *pgxpool.Pool:
		return pgxQuerier{c}, nil
	case pgx.Tx:
		return pgxQuerier{c}, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedConn, conn)
	}
}
```

Add the imports `"github.com/jackc/pgx/v5"` and `"github.com/jackc/pgx/v5/pgxpool"` to `tx.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/ -run TestPgxQuerierRoundTrip`
Expected: PASS (Docker daemon required).

- [ ] **Step 5: Commit**

```bash
git add internal/database/adapter_pgx.go internal/database/tx.go internal/database/adapter_pgx_test.go
git commit -m "feat(database): pgx Querier adapter + From(*pgxpool.Pool|pgx.Tx)"
```

---

## Task 3: `database/sql` adapter (`From(*sql.DB)` round-trip)

**Files:**
- Create: `internal/database/adapter_sql.go`
- Modify: `internal/database/tx.go` (add `*sql.DB`, `*sql.Tx`, `*sql.Conn` cases to `From`)
- Test: `internal/database/adapter_sql_test.go`

**Interfaces:**
- Consumes: `database.Querier/...` (Task 1), `internal/database/testutils_mysql.go` MySQL helper (confirm exact name; referred to here as `RunTestMySQL(t) *sql.DB`).
- Produces: internal `sqlQuerier`, `sqlResult`, `sqlRows`, `sqlRow` wrappers; `From` accepts `*sql.DB`, `*sql.Tx`, `*sql.Conn`.

- [ ] **Step 1: Write the failing test**

```go
// internal/database/adapter_sql_test.go
package database_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
)

func TestSQLQuerierRoundTrip(t *testing.T) {
	db := database.RunTestMySQL(t) // testcontainers MySQL; *sql.DB with parseTime=true&loc=UTC
	q, err := database.From(db)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if _, err := q.Exec(t.Context(), `CREATE TEMPORARY TABLE t (id int, name varchar(16))`); err != nil {
		t.Fatalf("exec create: %v", err)
	}
	res, err := q.Exec(t.Context(), `INSERT INTO t VALUES (?,?)`, 1, "a")
	if err != nil {
		t.Fatalf("exec insert: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	var name string
	if err := q.QueryRow(t.Context(), `SELECT name FROM t WHERE id=?`, 1).Scan(&name); err != nil {
		t.Fatalf("queryrow: %v", err)
	}
	if name != "a" {
		t.Fatalf("name = %q, want a", name)
	}
}
```

> Confirm the MySQL helper name in `internal/database/testutils_mysql.go`; if it differs from `RunTestMySQL`, use the actual exported name in this test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/ -run TestSQLQuerierRoundTrip`
Expected: FAIL — `From` returns `ErrUnsupportedConn` for `*sql.DB`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/database/adapter_sql.go
package database

import (
	"context"
	"database/sql"
)

// sqlDBTX is the minimal database/sql querier satisfied by *sql.DB, *sql.Tx, *sql.Conn.
type sqlDBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlQuerier struct{ db sqlDBTX }

func (q sqlQuerier) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	res, err := q.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlResult{res}, nil
}
func (q sqlQuerier) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{rows}, nil
}
func (q sqlQuerier) QueryRow(ctx context.Context, query string, args ...any) Row {
	return sqlRow{q.db.QueryRowContext(ctx, query, args...)}
}

type sqlResult struct{ res sql.Result }

func (r sqlResult) RowsAffected() (int64, error) { return r.res.RowsAffected() }

type sqlRows struct{ rows *sql.Rows }

func (r sqlRows) Next() bool             { return r.rows.Next() }
func (r sqlRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r sqlRows) Err() error             { return r.rows.Err() }
func (r sqlRows) Close() error           { return r.rows.Close() }

type sqlRow struct{ row *sql.Row }

func (r sqlRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

var (
	_ sqlDBTX = (*sql.DB)(nil)
	_ sqlDBTX = (*sql.Tx)(nil)
	_ sqlDBTX = (*sql.Conn)(nil)
)
```

Add to `internal/database/tx.go` `From` switch, before the default:

```go
	case *sql.DB:
		return sqlQuerier{c}, nil
	case *sql.Tx:
		return sqlQuerier{c}, nil
	case *sql.Conn:
		return sqlQuerier{c}, nil
```

Add `"database/sql"` to `tx.go` imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/ -run TestSQLQuerierRoundTrip`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/adapter_sql.go internal/database/tx.go internal/database/adapter_sql_test.go
git commit -m "feat(database): database/sql Querier adapter + From(*sql.DB|*sql.Tx|*sql.Conn)"
```

---

## Task 4: `BeginTx` for both drivers + tx-as-Querier transparency

**Files:**
- Modify: `internal/database/tx.go` (`BeginTx` cases), `internal/database/adapter_pgx.go` (pgx `Tx` wrapper), `internal/database/adapter_sql.go` (sql `Tx` wrapper)
- Test: `internal/database/tx_test.go`

**Interfaces:**
- Produces: `BeginTx(ctx, *pgxpool.Pool)` / `BeginTx(ctx, *sql.DB)` returning `database.Tx`; internal `pgxTx`/`sqlTx` wrappers embedding the querier + `Commit`/`Rollback`.

- [ ] **Step 1: Write the failing test** (both dialects via a table; runs each against its container)

```go
// internal/database/tx_test.go
package database_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
)

func TestBeginTxCommitRollback(t *testing.T) {
	// Postgres
	t.Run("postgres", func(t *testing.T) {
		pool := database.RunTestDatabase(t)
		mustExec(t, pool, `CREATE TABLE tx_t (id int)`) // helper Exec via From
		tx, err := database.BeginTx(t.Context(), pool)
		assertNoErr(t, err)
		_, err = tx.Exec(t.Context(), `INSERT INTO tx_t VALUES (1)`)
		assertNoErr(t, err)
		assertNoErr(t, tx.Rollback(t.Context()))
		assertCount(t, pool, `SELECT count(*) FROM tx_t`, 0) // rolled back
	})
}
```

Provide small local test helpers (`mustExec`, `assertNoErr`, `assertCount`) in the test file using `database.From`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/ -run TestBeginTxCommitRollback`
Expected: FAIL — `BeginTx` returns `ErrUnsupportedConn` for `*pgxpool.Pool`.

- [ ] **Step 3: Write minimal implementation**

Add to `adapter_pgx.go`:

```go
type pgxTx struct {
	pgxQuerier
	tx pgx.Tx
}

func (t pgxTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgxTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
```

Add to `adapter_sql.go`:

```go
type sqlTx struct {
	sqlQuerier
	tx *sql.Tx
}

func (t sqlTx) Commit(ctx context.Context) error   { return t.tx.Commit() }
func (t sqlTx) Rollback(ctx context.Context) error { return t.tx.Rollback() }
```

Fill `BeginTx` in `tx.go`:

```go
func BeginTx(ctx context.Context, conn any) (Tx, error) {
	switch c := conn.(type) {
	case *pgxpool.Pool:
		tx, err := c.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("workflow-database: begin pgx tx: %w", err)
		}
		return pgxTx{pgxQuerier{tx}, tx}, nil
	case *sql.DB:
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("workflow-database: begin sql tx: %w", err)
		}
		return sqlTx{sqlQuerier{tx}, tx}, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedConn, conn)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/ -run TestBeginTxCommitRollback`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/tx.go internal/database/adapter_pgx.go internal/database/adapter_sql.go internal/database/tx_test.go
git commit -m "feat(database): BeginTx returning driver-agnostic Tx (pgx + database/sql)"
```

---

## Task 5: `Batcher` interfaces + pgx native implementation

**Files:**
- Create: `internal/database/batch.go`
- Modify: `internal/database/adapter_pgx.go` (implement `Batcher` on `pgxQuerier`)
- Test: `internal/database/batch_pgx_test.go`

**Interfaces:**
- Produces: `database.Batch` (`Queue(query string, args ...any)`), `database.Batcher` (`SendBatch(ctx, Batch) BatchResults`), `database.BatchResults` (`Exec()(Result,error)`,`Query()(Rows,error)`,`Close()error`), `database.NewBatch() Batch`.

- [ ] **Step 1: Write the failing test**

```go
// internal/database/batch_pgx_test.go
package database_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
)

func TestPgxBatcher(t *testing.T) {
	pool := database.RunTestDatabase(t)
	q, _ := database.From(pool)
	_, _ = q.Exec(t.Context(), `CREATE TABLE b_t (id int)`)
	b, ok := q.(database.Batcher)
	if !ok {
		t.Fatal("pgx querier should implement Batcher")
	}
	batch := database.NewBatch()
	batch.Queue(`INSERT INTO b_t VALUES ($1)`, 1)
	batch.Queue(`INSERT INTO b_t VALUES ($1)`, 2)
	br := b.SendBatch(t.Context(), batch)
	defer br.Close()
	for range 2 {
		if _, err := br.Exec(); err != nil {
			t.Fatalf("batch exec: %v", err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/ -run TestPgxBatcher`
Expected: FAIL — `undefined: database.NewBatch`, `database.Batcher`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/database/batch.go
package database

import "context"

// Batch accumulates queued statements to run together.
type Batch interface{ Queue(query string, args ...any) }

// Batcher sends a Batch. A Querier obtained from From implements Batcher when the
// underlying driver supports batching. pgx pipelines natively; the database/sql
// adapter EMULATES by executing the queued statements sequentially — identical
// observable results, NO round-trip savings.
type Batcher interface {
	SendBatch(ctx context.Context, b Batch) BatchResults
}

// BatchResults iterates the results of a sent Batch, in queue order.
type BatchResults interface {
	Exec() (Result, error)
	Query() (Rows, error)
	Close() error
}

type queued struct {
	query string
	args  []any
}

type batch struct{ items []queued }

// NewBatch returns an empty Batch.
func NewBatch() Batch { return &batch{} }

func (b *batch) Queue(query string, args ...any) {
	b.items = append(b.items, queued{query, args})
}
```

Add to `adapter_pgx.go`:

```go
func (q pgxQuerier) SendBatch(ctx context.Context, b Batch) BatchResults {
	pb := &pgx.Batch{}
	for _, it := range b.(*batch).items {
		pb.Queue(it.query, it.args...)
	}
	return pgxBatchResults{q.db.(interface {
		SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
	}).SendBatch(ctx, pb)}
}

type pgxBatchResults struct{ br pgx.BatchResults }

func (r pgxBatchResults) Exec() (Result, error) {
	ct, err := r.br.Exec()
	return pgxResult{ct}, err
}
func (r pgxBatchResults) Query() (Rows, error) {
	rows, err := r.br.Query()
	if err != nil {
		return nil, err
	}
	return pgxRows{rows}, nil
}
func (r pgxBatchResults) Close() error { return r.br.Close() }
```

> The `q.db.(interface{ SendBatch... })` assertion works because `*pgxpool.Pool` and `pgx.Tx` both provide `SendBatch`. Extend the `pgxDBTX` interface to include `SendBatch(context.Context, *pgx.Batch) pgx.BatchResults` instead of the inline assertion for clarity.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/ -run TestPgxBatcher`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/batch.go internal/database/adapter_pgx.go internal/database/batch_pgx_test.go
git commit -m "feat(database): Batcher interface + pgx native SendBatch"
```

---

## Task 6: `Batcher` emulation for `database/sql`

**Files:**
- Modify: `internal/database/adapter_sql.go` (implement `Batcher` on `sqlQuerier` by sequential execution)
- Test: `internal/database/batch_sql_test.go`

**Interfaces:**
- Consumes: `database.Batch/Batcher/BatchResults` (Task 5).
- Produces: `sqlQuerier.SendBatch` returning a `sqlBatchResults` that steps through queued statements sequentially.

- [ ] **Step 1: Write the failing test**

```go
// internal/database/batch_sql_test.go
package database_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
)

func TestSQLBatcherEmulates(t *testing.T) {
	db := database.RunTestMySQL(t)
	q, _ := database.From(db)
	_, _ = q.Exec(t.Context(), `CREATE TABLE b_t (id int)`)
	b := q.(database.Batcher)
	batch := database.NewBatch()
	batch.Queue(`INSERT INTO b_t VALUES (?)`, 1)
	batch.Queue(`INSERT INTO b_t VALUES (?)`, 2)
	br := b.SendBatch(t.Context(), batch)
	defer br.Close()
	for range 2 {
		if _, err := br.Exec(); err != nil {
			t.Fatalf("batch exec: %v", err)
		}
	}
	var n int
	_ = q.QueryRow(t.Context(), `SELECT count(*) FROM b_t`).Scan(&n)
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/ -run TestSQLBatcherEmulates`
Expected: FAIL — `sqlQuerier` does not implement `Batcher`.

- [ ] **Step 3: Write minimal implementation**

```go
// add to internal/database/adapter_sql.go
func (q sqlQuerier) SendBatch(ctx context.Context, b Batch) BatchResults {
	return &sqlBatchResults{ctx: ctx, q: q, items: b.(*batch).items}
}

type sqlBatchResults struct {
	ctx   context.Context
	q     sqlQuerier
	items []queued
	i     int
}

func (r *sqlBatchResults) Exec() (Result, error) {
	it := r.items[r.i]
	r.i++
	return r.q.Exec(r.ctx, it.query, it.args...)
}
func (r *sqlBatchResults) Query() (Rows, error) {
	it := r.items[r.i]
	r.i++
	return r.q.Query(r.ctx, it.query, it.args...)
}
func (r *sqlBatchResults) Close() error { return nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/ -run TestSQLBatcherEmulates`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/adapter_sql.go internal/database/batch_sql_test.go
git commit -m "feat(database): database/sql Batcher emulation (sequential, documented)"
```

---

## Task 7: `UTC` helper + `ProbeUTC` fail-fast

**Files:**
- Create: `internal/database/timeutil.go`
- Test: `internal/database/timeutil_test.go`

**Interfaces:**
- Produces: `database.UTC(t time.Time) time.Time`; `database.Dialect` (`Postgres`, `MySQL`); `database.ProbeUTC(ctx, q Querier, d Dialect) error` (returns non-nil when the connection reads a datetime with a non-UTC zone offset).

- [ ] **Step 1: Write the failing test**

```go
// internal/database/timeutil_test.go
package database_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/internal/database"
)

func TestUTCNormalizes(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	in := time.Date(2020, 1, 2, 10, 0, 0, 0, loc)
	got := database.UTC(in)
	if _, off := got.Zone(); off != 0 {
		t.Fatalf("zone offset = %d, want 0", off)
	}
	if !got.Equal(in) {
		t.Fatalf("instant changed: %v != %v", got, in)
	}
}

func TestProbeUTCPassesOnPostgres(t *testing.T) {
	pool := database.RunTestDatabase(t)
	q, _ := database.From(pool)
	if err := database.ProbeUTC(t.Context(), q, database.Postgres); err != nil {
		t.Fatalf("probe: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/ -run 'TestUTC|TestProbeUTC'`
Expected: FAIL — `undefined: database.UTC`, `database.ProbeUTC`, `database.Postgres`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/database/timeutil.go
package database

import (
	"context"
	"fmt"
	"time"
)

// UTC returns t in UTC without changing the instant. Use it on every time.Time
// scanned from the database so callers always receive UTC-located values.
func UTC(t time.Time) time.Time { return t.UTC() }

// Dialect selects the probe SQL for ProbeUTC.
type Dialect int

const (
	Postgres Dialect = iota
	MySQL
)

// ProbeUTC verifies the connection interprets stored datetimes as UTC. It reads a
// known literal timestamp and fails if the scanned INSTANT drifted from the known
// UTC value — which happens for a MySQL DSN missing loc=UTC (the DATETIME string is
// parsed in the host zone, shifting the instant). Postgres TIMESTAMPTZ always
// preserves the instant, so this passes regardless of the returned Location; the
// read-side Location is handled by UTC() normalization. Instant-equality is used
// (not a zone-offset check) precisely because pgx may return TIMESTAMPTZ in
// time.Local without the instant being wrong. Call once at Open for fail-fast.
//
// Note: this probes the read-back interpretation (MySQL loc). The MySQL session
// time_zone (which governs DEFAULT CURRENT_TIMESTAMP(6) columns) is enforced
// separately by persistence.MySQLDSN.
func ProbeUTC(ctx context.Context, q Querier, d Dialect) error {
	known := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	var sql string
	switch d {
	case Postgres:
		sql = `SELECT TIMESTAMPTZ '2000-01-01 00:00:00+00'`
	case MySQL:
		sql = `SELECT TIMESTAMP('2000-01-01 00:00:00')`
	default:
		return fmt.Errorf("workflow-database: probe: unknown dialect %d", d)
	}
	var got time.Time
	if err := q.QueryRow(ctx, sql).Scan(&got); err != nil {
		return fmt.Errorf("workflow-database: probe query: %w", err)
	}
	if !got.Equal(known) {
		return fmt.Errorf("workflow-database: connection is not UTC (read %s, want %s); "+
			"for MySQL set DSN parseTime=true&loc=UTC (see persistence.MySQLDSN)",
			got.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano))
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/ -run 'TestUTC|TestProbeUTC'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/timeutil.go internal/database/timeutil_test.go
git commit -m "feat(database): UTC read helper + ProbeUTC fail-fast connection check"
```

---

## Task 8: `transaction` — Control, Querier, MarkRollback

**Files:**
- Create: `internal/database/transaction/transaction.go`
- Test: `internal/database/transaction/transaction_test.go`

**Interfaces:**
- Consumes: `database.Querier`, `database.Tx` (Tasks 1–4).
- Produces: `transaction.Control` (`Commit(ctx)error`,`Rollback(ctx)error`), `transaction.Querier` (embeds `database.Querier` + `Control`), `transaction.MarkRollback(ctx)`, `transaction.IsRollbackMarked(ctx) bool`, internal `handle{tx database.Tx; rollbackOnly bool}` stored in ctx.

- [ ] **Step 1: Write the failing test**

```go
// internal/database/transaction/transaction_test.go
package transaction_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database/transaction"
)

func TestMarkRollbackNoAmbientIsNoop(t *testing.T) {
	// No transaction in ctx: MarkRollback is a no-op and IsRollbackMarked is false.
	ctx := t.Context()
	transaction.MarkRollback(ctx)
	if transaction.IsRollbackMarked(ctx) {
		t.Fatal("want false with no ambient tx")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/transaction/`
Expected: FAIL — `undefined: transaction.MarkRollback`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/database/transaction/transaction.go
package transaction

import (
	"context"

	"github.com/kartaladev/wrkflw/internal/database"
)

// Control commits or rolls back a transaction. Commit honors a rollback-only mark.
type Control interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Querier is the transactional handle: a database.Querier you can also commit/roll back.
type Querier interface {
	database.Querier
	Control
}

type ctxKey struct{}

type handle struct {
	tx           database.Tx
	rollbackOnly bool
}

func fromCtx(ctx context.Context) *handle {
	h, _ := ctx.Value(ctxKey{}).(*handle)
	return h
}

// MarkRollback flags the ambient transaction in ctx rollback-only. No-op if none.
func MarkRollback(ctx context.Context) {
	if h := fromCtx(ctx); h != nil {
		h.rollbackOnly = true
	}
}

// IsRollbackMarked reports whether the ambient transaction is rollback-only.
func IsRollbackMarked(ctx context.Context) bool {
	h := fromCtx(ctx)
	return h != nil && h.rollbackOnly
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/transaction/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/transaction/transaction.go internal/database/transaction/transaction_test.go
git commit -m "feat(transaction): Control, Querier, MarkRollback/IsRollbackMarked + ambient ctx handle"
```

---

## Task 9: `transaction.Begin` (owner) + rollback-only honoring

**Files:**
- Create: `internal/database/transaction/begin.go`
- Test: `internal/database/transaction/begin_test.go`

**Interfaces:**
- Consumes: `database.BeginTx`, `handle` (Task 8).
- Produces: `transaction.Begin(ctx, conn any) (Querier, context.Context, error)`, internal `ownerQuerier` (delegates Exec/Query/QueryRow to `handle.tx`; `Commit` rolls back if `rollbackOnly`).

- [ ] **Step 1: Write the failing test** (Postgres)

```go
// internal/database/transaction/begin_test.go
package transaction_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
)

func TestBeginCommitPersists(t *testing.T) {
	pool := database.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tb (id int)`)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tb VALUES (1)`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tb`).Scan(&n)
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestBeginMarkRollbackRollsBack(t *testing.T) {
	pool := database.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tr (id int)`)

	tx, ctx, _ := transaction.Begin(t.Context(), pool)
	_, _ = tx.Exec(ctx, `INSERT INTO tr VALUES (1)`)
	transaction.MarkRollback(ctx)
	if err := tx.Commit(ctx); err != nil { // honors mark -> rolls back
		t.Fatalf("commit: %v", err)
	}
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tr`).Scan(&n)
	if n != 0 {
		t.Fatalf("count = %d, want 0 (rolled back)", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/transaction/ -run TestBegin`
Expected: FAIL — `undefined: transaction.Begin`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/database/transaction/begin.go
package transaction

import (
	"context"

	"github.com/kartaladev/wrkflw/internal/database"
)

// Begin starts a transaction on conn, stashes it into the returned context (so
// downstream JoinOrBegin calls join it), and returns the owner Querier.
func Begin(ctx context.Context, conn any) (Querier, context.Context, error) {
	tx, err := database.BeginTx(ctx, conn)
	if err != nil {
		return nil, ctx, err
	}
	h := &handle{tx: tx}
	ctx = context.WithValue(ctx, ctxKey{}, h)
	return &ownerQuerier{h: h}, ctx, nil
}

type ownerQuerier struct{ h *handle }

func (o *ownerQuerier) Exec(ctx context.Context, q string, a ...any) (database.Result, error) {
	return o.h.tx.Exec(ctx, q, a...)
}
func (o *ownerQuerier) Query(ctx context.Context, q string, a ...any) (database.Rows, error) {
	return o.h.tx.Query(ctx, q, a...)
}
func (o *ownerQuerier) QueryRow(ctx context.Context, q string, a ...any) database.Row {
	return o.h.tx.QueryRow(ctx, q, a...)
}
func (o *ownerQuerier) Commit(ctx context.Context) error {
	if o.h.rollbackOnly {
		return o.h.tx.Rollback(ctx)
	}
	return o.h.tx.Commit(ctx)
}
func (o *ownerQuerier) Rollback(ctx context.Context) error { return o.h.tx.Rollback(ctx) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/transaction/ -run TestBegin`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/transaction/begin.go internal/database/transaction/begin_test.go
git commit -m "feat(transaction): Begin owner querier honoring rollback-only mark"
```

---

## Task 10: `transaction.JoinOrBegin` (flat join, no-op inner commit)

**Files:**
- Modify: `internal/database/transaction/begin.go` (add `JoinOrBegin` + `joinedQuerier`)
- Test: `internal/database/transaction/join_test.go`

**Interfaces:**
- Produces: `transaction.JoinOrBegin(ctx, conn any) (Querier, error)`; internal `joinedQuerier` (delegates Exec/Query/QueryRow to the ambient `handle.tx`; `Commit` is a no-op; `Rollback` sets `rollbackOnly`).

- [ ] **Step 1: Write the failing test**

```go
// internal/database/transaction/join_test.go
package transaction_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
)

func TestJoinInnerCommitIsNoopOuterControls(t *testing.T) {
	pool := database.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tj (id int)`)

	outer, ctx, _ := transaction.Begin(t.Context(), pool)

	inner, err := transaction.JoinOrBegin(ctx, pool) // joins ambient
	if err != nil {
		t.Fatalf("joinorbegin: %v", err)
	}
	_, _ = inner.Exec(ctx, `INSERT INTO tj VALUES (1)`)
	_ = inner.Commit(ctx) // no-op; must NOT commit the real tx

	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n)
	if n != 0 {
		t.Fatalf("row visible before outer commit: %d", n)
	}
	_ = outer.Commit(ctx) // real commit
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n)
	if n != 1 {
		t.Fatalf("count after outer commit = %d, want 1", n)
	}
}

func TestJoinInnerRollbackMarksWholeUnit(t *testing.T) {
	pool := database.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tjr (id int)`)

	outer, ctx, _ := transaction.Begin(t.Context(), pool)
	inner, _ := transaction.JoinOrBegin(ctx, pool)
	_, _ = inner.Exec(ctx, `INSERT INTO tjr VALUES (1)`)
	_ = inner.Rollback(ctx) // marks rollback-only; does not touch the real tx yet

	_ = outer.Commit(ctx) // honors mark -> rolls back
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tjr`).Scan(&n)
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/transaction/ -run TestJoin`
Expected: FAIL — `undefined: transaction.JoinOrBegin`.

- [ ] **Step 3: Write minimal implementation**

```go
// add to internal/database/transaction/begin.go

// JoinOrBegin joins the ambient transaction in ctx if present; otherwise begins a
// fresh one (a leaf owned by the caller — not re-propagated for deeper joins; start
// the outermost scope with Begin to compose nested joins). When joined, the returned
// Querier's Commit is a no-op and Rollback marks the whole unit rollback-only.
func JoinOrBegin(ctx context.Context, conn any) (Querier, error) {
	if h := fromCtx(ctx); h != nil {
		return &joinedQuerier{h: h}, nil
	}
	q, _, err := Begin(ctx, conn)
	return q, err
}

type joinedQuerier struct{ h *handle }

func (j *joinedQuerier) Exec(ctx context.Context, q string, a ...any) (database.Result, error) {
	return j.h.tx.Exec(ctx, q, a...)
}
func (j *joinedQuerier) Query(ctx context.Context, q string, a ...any) (database.Rows, error) {
	return j.h.tx.Query(ctx, q, a...)
}
func (j *joinedQuerier) QueryRow(ctx context.Context, q string, a ...any) database.Row {
	return j.h.tx.QueryRow(ctx, q, a...)
}
func (j *joinedQuerier) Commit(ctx context.Context) error { return nil } // owner controls
func (j *joinedQuerier) Rollback(ctx context.Context) error {
	j.h.rollbackOnly = true
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/transaction/ -run TestJoin`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/database/transaction/begin.go internal/database/transaction/join_test.go
git commit -m "feat(transaction): JoinOrBegin flat-join (no-op inner commit, rollback marks unit)"
```

---

## Task 11: MySQL DSN helper (`persistence.MySQLDSN`)

**Files:**
- Modify: `persistence/mysql.go` (add `MySQLDSN`)
- Test: `persistence/mysql_dsn_test.go`

**Interfaces:**
- Produces: `persistence.MySQLDSN(base string) (string, error)` — returns a DSN guaranteed to carry `parseTime=true`, `loc=UTC`, and the `time_zone='+00:00'` system-variable param (go-sql-driver applies unknown params as `SET <var>` per connection). Idempotent if already present.

- [ ] **Step 1: Write the failing test**

```go
// persistence/mysql_dsn_test.go
package persistence_test

import (
	"strings"
	"testing"

	"github.com/kartaladev/wrkflw/persistence"
)

func TestMySQLDSNForcesUTC(t *testing.T) {
	got, err := persistence.MySQLDSN("user:pass@tcp(127.0.0.1:3306)/wrkflw")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, want := range []string{"parseTime=true", "loc=UTC", "time_zone=%27%2B00%3A00%27"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dsn %q missing %q", got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/ -run TestMySQLDSNForcesUTC`
Expected: FAIL — `undefined: persistence.MySQLDSN`.

- [ ] **Step 3: Write minimal implementation**

```go
// add to persistence/mysql.go
import "github.com/go-sql-driver/mysql" // if not already imported

// MySQLDSN returns base with the parameters required for correct DATETIME(6)
// time handling: parseTime=true, loc=UTC, and time_zone='+00:00' (applied as a
// session SET on every connection by go-sql-driver). Existing values are overridden.
func MySQLDSN(base string) (string, error) {
	cfg, err := mysql.ParseDSN(base)
	if err != nil {
		return "", fmt.Errorf("workflow-persistence-mysql: parse dsn: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	return cfg.FormatDSN(), nil
}
```

Add imports `"fmt"`, `"time"` to `persistence/mysql.go` if missing.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/ -run TestMySQLDSNForcesUTC`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add persistence/mysql.go persistence/mysql_dsn_test.go
git commit -m "feat(persistence): MySQLDSN helper forcing parseTime/loc=UTC/time_zone"
```

---

## Task 12: Wire `ProbeUTC` into `OpenMySQL` and `OpenPostgres`

**Files:**
- Modify: `persistence/mysql.go` (`OpenMySQL` runs `database.ProbeUTC(..., database.MySQL)`), `persistence/persistence.go` (`OpenPostgres` runs `database.ProbeUTC(..., database.Postgres)`)
- Test: `persistence/probe_test.go`

**Interfaces:**
- Consumes: `database.From`, `database.ProbeUTC` (Task 7).
- Produces: `OpenMySQL`/`OpenPostgres` return an error when the connection is not UTC (fail-fast).

- [ ] **Step 1: Write the failing test** (negative: a MySQL DB opened with `loc=Local` is rejected)

```go
// persistence/probe_test.go
package persistence_test

import (
	"database/sql"
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/persistence"
)

func TestOpenMySQLRejectsNonUTC(t *testing.T) {
	// RunTestMySQLDSN returns the container DSN; build a deliberately-wrong handle.
	dsn := database.RunTestMySQLDSN(t) // add helper alongside RunTestMySQL returning the raw DSN
	bad, err := sql.Open("mysql", forceLocalLoc(dsn)) // loc=Local, parseTime=true
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer bad.Close()
	if _, err := persistence.OpenMySQL(t.Context(), bad); err == nil {
		t.Fatal("want fail-fast error for non-UTC MySQL connection")
	}
}
```

Provide `forceLocalLoc(dsn)` in the test (parse via `mysql.ParseDSN`, set `Loc = time.Local`, `ParseTime = true`, re-format). If a `RunTestMySQLDSN` helper does not exist, add it beside `RunTestMySQL` in `testutils_mysql.go` (returns the DSN string it already builds).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/ -run TestOpenMySQLRejectsNonUTC`
Expected: FAIL — `OpenMySQL` currently does not probe; returns no error.

- [ ] **Step 3: Write minimal implementation**

In `OpenMySQL` (after obtaining `db *sql.DB`, before returning the Store):

```go
	q, err := database.From(db)
	if err != nil {
		return nil, err
	}
	if err := database.ProbeUTC(ctx, q, database.MySQL); err != nil {
		return nil, err
	}
```

In `OpenPostgres` (after obtaining `pool`):

```go
	q, err := database.From(pool)
	if err != nil {
		return nil, err
	}
	if err := database.ProbeUTC(ctx, q, database.Postgres); err != nil {
		return nil, err
	}
```

Add `"github.com/kartaladev/wrkflw/internal/database"` imports. Note the `ctx` parameter is currently named `_` in both `Open*` signatures — rename to `ctx` and use it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/ -run TestOpenMySQLRejectsNonUTC`
Expected: PASS. Also run `go test ./persistence/ -run TestOpen` to confirm the correctly-configured helpers still Open successfully.

- [ ] **Step 5: Commit**

```bash
git add persistence/mysql.go persistence/persistence.go persistence/probe_test.go internal/database/testutils_mysql.go
git commit -m "feat(persistence): fail-fast ProbeUTC in OpenMySQL/OpenPostgres"
```

---

## Task 13: Normalize-on-read `.UTC()` at critical store read sites

**Files:**
- Modify: `internal/persistence/postgres/store.go`, `internal/persistence/postgres/timerstore.go`, `internal/persistence/mysql/store.go`, `internal/persistence/mysql/timerstore.go`
- Test: `internal/persistence/postgres/time_roundtrip_test.go`, `internal/persistence/mysql/time_roundtrip_test.go`

**Interfaces:**
- Consumes: `database.UTC` (Task 7).
- Produces: every `time.Time` returned from `Load`/timer-list read paths is UTC-located.

- [ ] **Step 1: Write the failing test** (Postgres timer rehydrate fidelity, run under non-UTC host)

```go
// internal/persistence/postgres/time_roundtrip_test.go
package postgres_test

import (
	"testing"
	"time"
	// ... existing package imports for constructing a Store + TimerStore
)

func TestTimerFireAtRehydratesUTC(t *testing.T) {
	// Arrange: a known fire_at instant.
	want := time.Date(2031, 3, 4, 5, 6, 7, 890000000, time.UTC)
	// ... arm a timer with want via the TimerStore, then ListArmed and read it back ...
	got := listArmedFireAt(t) // helper returning the single armed timer's FireAt
	if _, off := got.Zone(); off != 0 {
		t.Fatalf("FireAt zone offset = %d, want 0 (UTC)", off)
	}
	if !got.Equal(want) {
		t.Fatalf("FireAt = %v, want %v", got, want)
	}
}
```

Run this test file with `TZ=Asia/Jakarta` in Step 2/4 to catch `time.Local` leakage.

- [ ] **Step 2: Run test to verify it fails**

Run: `TZ=Asia/Jakarta go test ./internal/persistence/postgres/ -run TestTimerFireAtRehydratesUTC`
Expected: FAIL on the `offset != 0` assertion **if** pgx returns `TIMESTAMPTZ` in the host `Asia/Jakarta` zone before normalization.

> **Honest red caveat:** whether this is red depends on the driver's default `Location`. If pgx already returns UTC (or MySQL with `loc=UTC` already does), the assertion passes without the fix — in that case the `.UTC()` normalization is a **documented defensive guarantee** and this task reduces to landing the regression test. Do not fabricate a red; record which dialect actually drifted. The durable property (scanned timestamps are UTC-located) holds either way after Step 3. The MySQL mirror test is expected to pass as-is under `loc=UTC` and serves as a regression guard, not a red.

- [ ] **Step 3: Write minimal implementation**

At each site where a `time.Time` is scanned in the four files, wrap with `database.UTC(...)`. Example (timerstore `ListArmed` scan):

```go
	var fireAt time.Time
	if err := rows.Scan(&timerID, &instanceID, &fireAt /* ... */); err != nil {
		return nil, err
	}
	fireAt = database.UTC(fireAt)
	armed = append(armed, runtime.ArmedTimer{TimerID: timerID, InstanceID: instanceID, FireAt: fireAt /* ... */})
```

Apply the same to `Store.Load` scanned timestamps (`CreatedAt`, `StartedAt`, `EndedAt`, `UpdatedAt`, token/timer/incident times mapped from the snapshot) in both dialects. Add the `internal/database` import to each file.

> If the snapshot is stored as JSON (not discrete timestamp columns), the times inside it are already serialized/deserialized as RFC3339 with zone and need no change — verify which timestamps are real columns vs JSON before editing. The **timer `fire_at`** column is a real `TIMESTAMPTZ`/`DATETIME(6)` and is the primary target.

- [ ] **Step 4: Run test to verify it passes**

Run: `TZ=Asia/Jakarta go test ./internal/persistence/postgres/ ./internal/persistence/mysql/ -run TestTimer`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/postgres/store.go internal/persistence/postgres/timerstore.go internal/persistence/mysql/store.go internal/persistence/mysql/timerstore.go internal/persistence/postgres/time_roundtrip_test.go internal/persistence/mysql/time_roundtrip_test.go
git commit -m "fix(persistence): normalize scanned timestamps to UTC (timer rehydrate fidelity)"
```

---

## Task 14: Testcontainers integration test — toolkit + `From` transparency (both dialects)

**Files:**
- Create: `internal/database/integration_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–10.
- Produces: an integration test asserting the same repository function runs identically given a pool-backed and a tx-backed `Querier`, on both PG and MySQL, plus a `MarkRollback` end-to-end.

- [ ] **Step 1: Write the failing test**

```go
// internal/database/integration_test.go
package database_test

import (
	"context"
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
)

// insertOne uses only database.Querier — no driver, no idea if q is a tx.
func insertOne(ctx context.Context, q database.Querier, ph func(int) string, id int) error {
	_, err := q.Exec(ctx, `INSERT INTO shared VALUES (`+ph(1)+`)`, id)
	return err
}

func TestQuerierTransparentPoolVsTx_Postgres(t *testing.T) {
	pool := database.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE shared (id int)`)
	pg := func(int) string { return "$1" }

	// via pool (non-tx)
	if err := insertOne(t.Context(), base, pg, 1); err != nil {
		t.Fatalf("pool insert: %v", err)
	}
	// via tx, then rollback via mark: identical call, different persistence outcome
	tx, ctx, _ := transaction.Begin(t.Context(), pool)
	if err := insertOne(ctx, tx, pg, 2); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	transaction.MarkRollback(ctx)
	_ = tx.Commit(ctx)

	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM shared`).Scan(&n)
	if n != 1 { // only the pool insert survives
		t.Fatalf("count = %d, want 1", n)
	}
}
```

Add the MySQL mirror (`ph := func(int) string { return "?" }`, `database.RunTestMySQL`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/database/ -run TestQuerierTransparent`
Expected: FAIL first as compile/red until Tasks 1–10 are complete; then PASS. (If Tasks 1–10 are done, this is a new assertion — write it, watch it pass; if it passes immediately, that is acceptable for an integration assertion over already-built units.)

- [ ] **Step 3: Write minimal implementation**

No new production code expected; if the test surfaces a gap (e.g. a missing `From` case), fix it minimally in the relevant adapter file.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/database/ -run TestQuerierTransparent`
Expected: PASS (both dialects).

- [ ] **Step 5: Commit**

```bash
git add internal/database/integration_test.go
git commit -m "test(database): Querier transparency pool-vs-tx + MarkRollback e2e (PG + MySQL)"
```

---

## Task 15: Runnable testcontainers example

**Files:**
- Create: `examples/real_db_transaction/main.go`

**Interfaces:**
- Consumes: `database`, `transaction`, the testcontainers helpers (or inline container setup).
- Produces: a `main` that spins up PG + MySQL, runs the same `transaction.Begin` → `database.Querier` write → `MarkRollback` demo → `Commit` on each, printing round-tripped timestamps.

- [ ] **Step 1: Write the failing test** (a build/vet guard — examples are `main` packages)

```bash
# no unit test; the deliverable is a compiling, runnable main.
go vet ./examples/real_db_transaction/
```

Expected initial: FAIL — package does not exist.

- [ ] **Step 2: Run to verify it fails**

Run: `go vet ./examples/real_db_transaction/`
Expected: FAIL (no such package/dir).

- [ ] **Step 3: Write minimal implementation**

Create `examples/real_db_transaction/main.go` with a documented header (requires Docker), a `run(ctx, conn any, placeholder func(int) string, dialect database.Dialect)` function that: `database.ProbeUTC`, creates a demo table, runs `transaction.Begin` → insert via `database.Querier` → print the row's round-tripped timestamp (UTC) → demonstrate `MarkRollback` on a second tx → `Commit`. Spin up both containers via testcontainers, call `run` for each, and log a clear "no drift" summary. Keep it under ~150 lines; reuse patterns from `examples/mysql_wiring/main.go` for container/DSN setup.

- [ ] **Step 4: Run to verify it passes**

Run: `go vet ./examples/real_db_transaction/ && go build ./examples/real_db_transaction/`
Expected: PASS (compiles). Optionally `go run ./examples/real_db_transaction/` with Docker running to see the demo.

- [ ] **Step 5: Commit**

```bash
git add examples/real_db_transaction/main.go
git commit -m "docs(examples): real-DB transaction demo (testcontainers PG + MySQL, same code path)"
```

---

## Task 16: ADRs

**Files:**
- Create: `docs/adr/00NN-database-transaction-toolkit.md`, `docs/adr/00NN-utc-time-discipline.md`

- [ ] **Step 1: Determine the next free ADR numbers**

Run: `ls docs/adr/ | sort | tail -3`
Use the next two sequential numbers (memory indicates the index points at **0078**; confirm against the directory).

- [ ] **Step 2: Write ADR — database transaction toolkit**

Nygard template (Status/Date, Context, Decision, Consequences). Decision covers: `database.Querier` + neutral result types; `From(conn any)` type-switch (why not generics — incompatible driver tx method sets); `Tx`/`BeginTx`; `transaction` ambient-ctx propagation with flat-join + rollback-only mark; the extraction constraint (stdlib + drivers, zero wrkflw imports); `Batcher` (pgx native / `database/sql` emulated); LISTEN/NOTIFY stays Postgres-binding-specific. Consequences: Phase-2 store refactor deferred; `Rebind` and savepoints deferred.

- [ ] **Step 3: Write ADR — UTC time discipline**

Nygard template. Decision: connection-level pinning (MySQL DSN `parseTime=true&loc=UTC` + session `time_zone='+00:00'`; Postgres `TimeZone=UTC`), fail-fast `ProbeUTC` at `Open`, normalize-on-read `.UTC()`. Context: `TIMESTAMPTZ` vs `DATETIME(6)`, the `DEFAULT CURRENT_TIMESTAMP(6)` columns. Consequences: consumers should build DSNs via `MySQLDSN`; misconfiguration now fails at startup.

- [ ] **Step 4: Verify links/format**

Run: `ls docs/adr/ | tail -4` and confirm both files exist with correct numbering and the Nygard sections.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/
git commit -m "docs(adr): database transaction toolkit + UTC time discipline"
```

---

## Final Verification

- [ ] `go build ./...`
- [ ] `go test ./internal/database/... ./internal/database/transaction/... ./persistence/...` (Docker running)
- [ ] `TZ=Asia/Jakarta go test ./internal/persistence/postgres/... ./internal/persistence/mysql/...` (time fidelity under non-UTC host)
- [ ] `go test -race -coverprofile=cover.out ./internal/database/... ./internal/database/transaction/... && go tool cover -func=cover.out | tail -1` — ≥ 85%
- [ ] `go test ./...` from repo root — no regressions
- [ ] `golangci-lint run ./...` — clean
- [ ] Confirm neither `internal/database` nor `internal/database/transaction` imports any wrkflw package other than each other (extraction constraint): `go list -deps ./internal/database/... | grep kartaladev/wrkflw` shows only `internal/database` paths.
