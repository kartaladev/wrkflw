# Store Unification + Dialect Abstraction + SQLite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse `internal/persistence/{postgres,mysql}` into one neutral store over `database.Querier` + a `dialect.Dialect` strategy, migrated onto the ambient `transaction` toolkit, and add a SQLite backend — with a 3-dialect conformance suite as the parity net.

**Architecture:** Two axes — *access mechanism* (pgx / `database/sql`, already behind `database.Querier`) and *SQL dialect* (`Postgres`/`MySQL`/`SQLite`, a new `dialect` package). One set of store implementations in `internal/persistence/store` runs against `database.Querier` + a `Dialect`; divergences that aren't SQL text (LISTEN/NOTIFY receive, advisory locks) are optional capability interfaces. The old dialect packages are deleted; the `persistence` facade keeps stable constructors and gains `OpenSQLite`.

**Tech Stack:** Go 1.25, `jackc/pgx/v5`, `go-sql-driver/mysql` via `database/sql`, `modernc.org/sqlite` (pure-Go) via `database/sql`, `pressly/goose` migrations, `testcontainers-go` (PG+MySQL), `internal/database` + `internal/database/transaction` toolkit, `internal/dbtest` helpers.

**Spec:** `docs/specs/2026-07-02-store-unification-dialect-sqlite-design.md`.

## Global Constraints

- **Go 1.25**; hard requirement.
- Error sentinels/messages use the `workflow-<package>:` prefix (e.g. `workflow-dialect:`, `workflow-store:`).
- **TDD strict**: every new symbol gets a failing test first, verified red via `go test`, before implementation. No batching test+impl in one edit.
- Black-box tests (`package X_test`). The project `table-test` skill governs any test with 2+ same-SUT cases (assert-closure form, `t.Context()`, `ctx` modifier); `use-testcontainers` and `use-mockgen` apply.
- Real databases only: PG + MySQL via `dbtest.RunTestDatabase`/`RunTestMySQL` (testcontainers), SQLite via `dbtest.RunTestSQLite` (in-process). Never mocked. Docker required for PG/MySQL.
- Per-package coverage ≥ 85%; `go test -race ./...` clean; `golangci-lint run ./...` clean before done.
- **`internal/database` + `internal/database/transaction` remain extraction-clean** — this plan does NOT modify them; the CI `extraction` job (`scripts/check-extraction.sh`) must stay green. The new `store`/`dialect` packages MAY import `runtime`/`model` (they are consumers).
- **Behavior preservation is the contract for ports.** The neutral store must produce byte-identical observable behavior to today's `postgres`/`mysql` stores. The conformance suite (Task 7+) folds in the existing per-dialect assertions and is the gate — a port is done only when the suite is green on all applicable dialects.
- Writes keep `time.Now().UTC()`; reads keep UTC normalization (ADR-0080): scanned `time.Time` from real columns is `.UTC()`-normalized.
- `runtime.Store`, `runtime.JournalReader`, `runtime.InstanceLister`, `runtime.TimerStore`, `runtime.CallLinkStore`, `runtime.ChainLinkStore`, and facade `Store`/`Relay`/`DefinitionStore` interfaces are UNCHANGED.

---

## File Structure

**New — `internal/persistence/dialect/` (SQL-dialect strategy):**
- `dialect.go` — `Dialect` interface, `Notifier`/`Locker` capability interfaces, `ErrUnsupported` sentinel.
- `postgres.go` — `Postgres` dialect (pgx-family errors).
- `mysql.go` — `MySQL` dialect (`go-sql-driver` errors).
- `sqlite.go` — `SQLite` dialect (`modernc.org/sqlite` errors).
- `*_test.go` — black-box unit tests (Rebind, fragments, error classification via constructed driver errors).

**New — `internal/persistence/store/` (neutral store):**
- `store.go` — `Store` (Create/Load/Commit/Entries) + `querier(ctx)` helper + write helpers.
- `lister.go`, `timerstore.go`, `definitions.go`, `dedup.go`, `chainlink.go`, `pruner.go`, `call_links.go`, `relay.go`, `relay_backoff.go`, `ownership.go`.
- `history_cap.go`, `trigger_codec.go` — pure-Go helpers, moved once.
- `errors.go` — store-level sentinels.
- `conformance_test.go` — the parametrized 3-dialect harness + shared fixtures.
- `*_test.go` — per-store conformance tests (black-box `store_test`).

**New — SQLite enablement:**
- `internal/dbtest/sqlite.go` — `RunTestSQLite(t) *sql.DB`.
- `migrations/sqlite/*.sql` (or the repo's existing migrations layout mirrored for sqlite) — SQLite schema.

**Modify:**
- `persistence/persistence.go` — `OpenPostgres` builds neutral store + Postgres dialect + pgx capabilities.
- `persistence/mysql.go` — `OpenMySQL` builds neutral store + MySQL dialect.
- `persistence/sqlite.go` (NEW) — `OpenSQLite` + DSN helper.
- `persistence/{dedup,pruner,health,relay_health}.go` — retarget to the neutral store types.
- `go.mod`/`go.sum` — add `modernc.org/sqlite`.

**Delete (Task 22):** `internal/persistence/postgres/`, `internal/persistence/mysql/` (all files).

**New — ADRs (Task 23):** `docs/adr/0081-store-unification-dialect.md`, `docs/adr/0082-sqlite-backend.md` (numbers confirmed in Task 23).

---

## Task 1: `dialect` interface + capabilities + `ErrUnsupported`

**Files:**
- Create: `internal/persistence/dialect/dialect.go`
- Test: `internal/persistence/dialect/dialect_test.go`

**Interfaces:**
- Produces: `dialect.Dialect` (methods below), `dialect.Notifier`, `dialect.Locker`, `dialect.ErrUnsupported`.

- [ ] **Step 1: Write the failing test**

```go
// internal/persistence/dialect/dialect_test.go
package dialect_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

func TestErrUnsupportedIsSentinel(t *testing.T) {
	if dialect.ErrUnsupported == nil {
		t.Fatal("ErrUnsupported must be a non-nil sentinel")
	}
	wrapped := errors.Join(errors.New("ctx"), dialect.ErrUnsupported)
	if !errors.Is(wrapped, dialect.ErrUnsupported) {
		t.Fatal("ErrUnsupported must be matchable via errors.Is")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/dialect/`
Expected: FAIL — `undefined: dialect.ErrUnsupported`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/persistence/dialect/dialect.go
package dialect

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by a capability a dialect/access combination does
// not provide (e.g. SQLite locking).
var ErrUnsupported = errors.New("workflow-dialect: capability not supported")

// Dialect abstracts the SQL-text and driver-error differences between backends.
// It is orthogonal to the access mechanism (pgx vs database/sql).
type Dialect interface {
	Name() string
	// Rebind converts a query written with ? placeholders into this dialect's
	// placeholder style ($1.. for Postgres; ? unchanged for MySQL/SQLite).
	Rebind(query string) string
	// UpsertTimer/UpsertDefinition/InsertIgnoreDedup return the dialect's
	// conflict clause appended to the shared base INSERT for these three sites.
	UpsertTimer() string
	UpsertDefinition() string
	InsertIgnoreDedup() string
	// JournalTriggerColumn is the journal payload column name: "trigger"
	// (Postgres/SQLite) or "trigger_" (MySQL reserved word).
	JournalTriggerColumn() string
	// OutboxStatsQuery returns the dialect's pending/dead/age aggregate query.
	OutboxStatsQuery() string
	// NotifyStatement returns "NOTIFY <channel>" for Postgres, "" otherwise.
	NotifyStatement(channel string) string
	// SupportsReturning selects the leased-claim strategy: UPDATE..RETURNING
	// (true) vs SELECT..FOR UPDATE SKIP LOCKED + UPDATE (false).
	SupportsReturning() bool
	// IsUniqueViolation / IsRetryableConflict classify this dialect's expected
	// driver errors.
	IsUniqueViolation(err error) bool
	IsRetryableConflict(err error) bool
}

// Notifier is the receive side of Postgres LISTEN/NOTIFY; only the (pgx,
// Postgres) combination provides it. Listen returns a wake channel, a cancel
// func, and an error.
type Notifier interface {
	Listen(ctx context.Context, channel string) (<-chan struct{}, func(), error)
}

// Locker is a distributed advisory lock (Postgres advisory / MySQL GET_LOCK).
// SQLite provides none: its implementation returns ErrUnsupported.
type Locker interface {
	TryLock(ctx context.Context, key string) (bool, error)
	Unlock(ctx context.Context, key string) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/persistence/dialect/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/dialect/dialect.go internal/persistence/dialect/dialect_test.go
git commit -m "feat(dialect): Dialect + Notifier/Locker capability interfaces + ErrUnsupported"
```

---

## Task 2: `Postgres` dialect

**Files:**
- Create: `internal/persistence/dialect/postgres.go`
- Test: `internal/persistence/dialect/postgres_test.go`

**Interfaces:**
- Consumes: `dialect.Dialect` (Task 1).
- Produces: `dialect.Postgres` (a value/struct implementing `Dialect`), constructor `dialect.NewPostgres()` returning `Dialect`.

- [ ] **Step 1: Write the failing test**

```go
// internal/persistence/dialect/postgres_test.go
package dialect_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

func TestPostgresRebindNumbers(t *testing.T) {
	d := dialect.NewPostgres()
	got := d.Rebind(`INSERT INTO t (a,b) VALUES (?,?) ON CONFLICT DO NOTHING`)
	want := `INSERT INTO t (a,b) VALUES ($1,$2) ON CONFLICT DO NOTHING`
	if got != want {
		t.Fatalf("Rebind = %q, want %q", got, want)
	}
}

func TestPostgresClassifiesErrors(t *testing.T) {
	d := dialect.NewPostgres()
	if !d.IsUniqueViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("23505 must be unique violation")
	}
	if !d.IsRetryableConflict(&pgconn.PgError{Code: "40001"}) {
		t.Fatal("40001 must be retryable (serialization failure)")
	}
	if d.SupportsReturning() != true {
		t.Fatal("Postgres supports RETURNING")
	}
	if d.NotifyStatement("wrkflw_outbox") != "NOTIFY wrkflw_outbox" {
		t.Fatal("Postgres NotifyStatement must emit NOTIFY")
	}
	if d.JournalTriggerColumn() != "trigger" {
		t.Fatal(`Postgres journal column is "trigger"`)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/dialect/ -run TestPostgres`
Expected: FAIL — `undefined: dialect.NewPostgres`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/persistence/dialect/postgres.go
package dialect

import (
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type postgres struct{}

// NewPostgres returns the Postgres SQL dialect.
func NewPostgres() Dialect { return postgres{} }

func (postgres) Name() string { return "postgres" }

func (postgres) Rebind(query string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

func (postgres) UpsertTimer() string {
	return ` ON CONFLICT (instance_id, timer_id) DO UPDATE SET fire_at = EXCLUDED.fire_at`
}
func (postgres) UpsertDefinition() string {
	return ` ON CONFLICT (id, version) DO UPDATE SET payload = EXCLUDED.payload`
}
func (postgres) InsertIgnoreDedup() string { return ` ON CONFLICT DO NOTHING` }
func (postgres) JournalTriggerColumn() string { return "trigger" }
func (postgres) NotifyStatement(channel string) string { return "NOTIFY " + channel }
func (postgres) SupportsReturning() bool { return true }

func (postgres) OutboxStatsQuery() string {
	// Ported verbatim from internal/persistence/postgres/relay.go OutboxStats.
	return `SELECT
	  count(*) FILTER (WHERE dead_lettered_at IS NULL),
	  count(*) FILTER (WHERE dead_lettered_at IS NOT NULL),
	  COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE dead_lettered_at IS NULL))), 0)
	FROM wrkflw_outbox`
}

func (postgres) IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
func (postgres) IsRetryableConflict(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "40001"
}
```

> **Porting note:** the exact upsert column lists and `OutboxStatsQuery` MUST match today's `internal/persistence/postgres/{store.go,definitions.go,relay.go}` SQL. Open those files and copy the real conflict targets / column names; the fragments above are the shape, not a licence to invent columns. The conformance suite (Task 8+) will catch any mismatch.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/persistence/dialect/ -run TestPostgres`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/dialect/postgres.go internal/persistence/dialect/postgres_test.go
git commit -m "feat(dialect): Postgres dialect (rebind \$n, upserts, NOTIFY, pgconn error mapping)"
```

---

## Task 3: `MySQL` dialect

**Files:**
- Create: `internal/persistence/dialect/mysql.go`
- Test: `internal/persistence/dialect/mysql_test.go`

**Interfaces:**
- Produces: `dialect.NewMySQL() Dialect`.

- [ ] **Step 1: Write the failing test**

```go
// internal/persistence/dialect/mysql_test.go
package dialect_test

import (
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

func TestMySQLRebindLeavesQuestionMarks(t *testing.T) {
	d := dialect.NewMySQL()
	q := `INSERT INTO t (a,b) VALUES (?,?)`
	if d.Rebind(q) != q {
		t.Fatalf("MySQL Rebind must be identity, got %q", d.Rebind(q))
	}
}

func TestMySQLClassifiesErrors(t *testing.T) {
	d := dialect.NewMySQL()
	if !d.IsUniqueViolation(&mysqldriver.MySQLError{Number: 1062}) {
		t.Fatal("1062 must be unique violation")
	}
	if !d.IsRetryableConflict(&mysqldriver.MySQLError{Number: 1213}) {
		t.Fatal("1213 must be retryable (deadlock)")
	}
	if !d.IsRetryableConflict(&mysqldriver.MySQLError{Number: 1205}) {
		t.Fatal("1205 must be retryable (lock wait timeout)")
	}
	if d.SupportsReturning() {
		t.Fatal("MySQL does not support RETURNING")
	}
	if d.NotifyStatement("wrkflw_outbox") != "" {
		t.Fatal("MySQL has no NOTIFY")
	}
	if d.JournalTriggerColumn() != "trigger_" {
		t.Fatal(`MySQL journal column is "trigger_"`)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/dialect/ -run TestMySQL`
Expected: FAIL — `undefined: dialect.NewMySQL`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/persistence/dialect/mysql.go
package dialect

import (
	"errors"

	mysqldriver "github.com/go-sql-driver/mysql"
)

type mysql struct{}

// NewMySQL returns the MySQL SQL dialect.
func NewMySQL() Dialect { return mysql{} }

func (mysql) Name() string                 { return "mysql" }
func (mysql) Rebind(query string) string   { return query } // ? is native
func (mysql) UpsertTimer() string          { return ` ON DUPLICATE KEY UPDATE fire_at = VALUES(fire_at)` }
func (mysql) UpsertDefinition() string     { return ` ON DUPLICATE KEY UPDATE payload = VALUES(payload)` }
func (mysql) InsertIgnoreDedup() string    { return "" } // uses INSERT IGNORE prefix instead — see note
func (mysql) JournalTriggerColumn() string { return "trigger_" }
func (mysql) NotifyStatement(string) string { return "" }
func (mysql) SupportsReturning() bool      { return false }

func (mysql) OutboxStatsQuery() string {
	// Ported verbatim from internal/persistence/mysql/relay.go OutboxStats.
	return `SELECT
	  SUM(CASE WHEN dead_lettered_at IS NULL THEN 1 ELSE 0 END),
	  SUM(CASE WHEN dead_lettered_at IS NOT NULL THEN 1 ELSE 0 END),
	  COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN dead_lettered_at IS NULL THEN created_at END), UTC_TIMESTAMP(6)), 0)
	FROM wrkflw_outbox`
}

func (mysql) IsUniqueViolation(err error) bool {
	var me *mysqldriver.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
func (mysql) IsRetryableConflict(err error) bool {
	var me *mysqldriver.MySQLError
	return errors.As(err, &me) && (me.Number == 1213 || me.Number == 1205)
}
```

> **Porting note — dedup insert-ignore divergence:** MySQL uses an `INSERT IGNORE` *prefix*, Postgres/SQLite use a *suffix* clause. The Deduper (Task 12) must ask the dialect for BOTH a prefix and a suffix rather than only a suffix. Adjust the `Dialect` interface if needed: add `InsertIgnorePrefix() string` (returns `"INSERT IGNORE"` for MySQL, `"INSERT"` for PG/SQLite) and keep `InsertIgnoreDedup()` as the suffix (`" ON CONFLICT DO NOTHING"` / `" ON CONFLICT DO NOTHING"` for SQLite / `""` for MySQL). Update Task 1's interface + Task 2/4 impls + their tests in this task's commit if you take this route. Copy the real dedup SQL from `internal/persistence/{postgres,mysql}/dedup.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/persistence/dialect/ -run TestMySQL`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/dialect/mysql.go internal/persistence/dialect/mysql_test.go
git commit -m "feat(dialect): MySQL dialect (identity rebind, ON DUPLICATE KEY, mysql error mapping)"
```

---

## Task 4: `SQLite` dialect

**Files:**
- Create: `internal/persistence/dialect/sqlite.go`
- Test: `internal/persistence/dialect/sqlite_test.go`

**Interfaces:**
- Produces: `dialect.NewSQLite() Dialect`.

- [ ] **Step 1: Write the failing test**

```go
// internal/persistence/dialect/sqlite_test.go
package dialect_test

import (
	"testing"

	sqlitedriver "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

func TestSQLiteRebindLeavesQuestionMarks(t *testing.T) {
	d := dialect.NewSQLite()
	q := `INSERT INTO t (a,b) VALUES (?,?)`
	if d.Rebind(q) != q {
		t.Fatalf("SQLite Rebind must be identity, got %q", d.Rebind(q))
	}
}

func TestSQLiteClassifiesErrors(t *testing.T) {
	d := dialect.NewSQLite()
	if !d.SupportsReturning() {
		t.Fatal("SQLite (>=3.35) supports RETURNING")
	}
	if d.JournalTriggerColumn() != "trigger" {
		t.Fatal(`SQLite journal column is "trigger"`)
	}
	uniq := &sqlitedriver.Error{}
	_ = sqlitelib.SQLITE_CONSTRAINT_UNIQUE // referenced to pin the code constant used in impl
	_ = uniq
}
```

> **Porting note:** `modernc.org/sqlite` exposes result codes in `modernc.org/sqlite/lib` (e.g. `sqlitelib.SQLITE_CONSTRAINT_UNIQUE`, `sqlitelib.SQLITE_BUSY`) and errors as `*sqlite.Error` with a `.Code()` method. Confirm the exact type/accessor when implementing (this dep is added in Task 5; if Task 4 precedes the dep, move Task 5 before Task 4). Build the `IsUniqueViolation`/`IsRetryableConflict` against `*sqlite.Error.Code()`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/dialect/ -run TestSQLite`
Expected: FAIL — `undefined: dialect.NewSQLite` (and/or missing dep — see note; do Task 5 first if so).

- [ ] **Step 3: Write minimal implementation**

```go
// internal/persistence/dialect/sqlite.go
package dialect

import (
	"errors"

	sqlite "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

type sqliteDialect struct{}

// NewSQLite returns the SQLite SQL dialect (modernc.org/sqlite).
func NewSQLite() Dialect { return sqliteDialect{} }

func (sqliteDialect) Name() string                 { return "sqlite" }
func (sqliteDialect) Rebind(query string) string   { return query }
func (sqliteDialect) UpsertTimer() string          { return ` ON CONFLICT (instance_id, timer_id) DO UPDATE SET fire_at = excluded.fire_at` }
func (sqliteDialect) UpsertDefinition() string     { return ` ON CONFLICT (id, version) DO UPDATE SET payload = excluded.payload` }
func (sqliteDialect) InsertIgnoreDedup() string    { return ` ON CONFLICT DO NOTHING` }
func (sqliteDialect) JournalTriggerColumn() string { return "trigger" }
func (sqliteDialect) NotifyStatement(string) string { return "" }
func (sqliteDialect) SupportsReturning() bool      { return true }

func (sqliteDialect) OutboxStatsQuery() string {
	return `SELECT
	  COALESCE(SUM(CASE WHEN dead_lettered_at IS NULL THEN 1 ELSE 0 END),0),
	  COALESCE(SUM(CASE WHEN dead_lettered_at IS NOT NULL THEN 1 ELSE 0 END),0),
	  COALESCE(CAST((julianday('now') - julianday(MIN(CASE WHEN dead_lettered_at IS NULL THEN created_at END))) * 86400 AS INTEGER), 0)
	FROM wrkflw_outbox`
}

func (sqliteDialect) IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlitelib.SQLITE_CONSTRAINT_UNIQUE
}
func (sqliteDialect) IsRetryableConflict(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && (se.Code() == sqlitelib.SQLITE_BUSY || se.Code() == sqlitelib.SQLITE_LOCKED)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/persistence/dialect/ -run TestSQLite`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/dialect/sqlite.go internal/persistence/dialect/sqlite_test.go
git commit -m "feat(dialect): SQLite dialect (modernc error mapping, RETURNING, ON CONFLICT)"
```

---

## Task 5: Add `modernc.org/sqlite` + `dbtest.RunTestSQLite`

> Do this task BEFORE Task 4 if the dialect impl needs the dep to compile.

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/dbtest/sqlite.go`
- Test: `internal/dbtest/sqlite_test.go`

**Interfaces:**
- Produces: `dbtest.RunTestSQLite(t *testing.T) *sql.DB` — a migrated, per-test SQLite database (WAL, busy_timeout), torn down via `t.Cleanup`.

- [ ] **Step 1: Add the dependency**

Run: `go get modernc.org/sqlite@latest && go mod tidy`
Verify: `go.mod` lists `modernc.org/sqlite`.

- [ ] **Step 2: Write the failing test**

```go
// internal/dbtest/sqlite_test.go
package dbtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestRunTestSQLite_PingsAndMigrates(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	require.NoError(t, db.PingContext(t.Context()))
	// A core table from the migration set must exist.
	var name string
	err := db.QueryRowContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='wrkflw_instances'`).Scan(&name)
	require.NoError(t, err, "migrations must have created wrkflw_instances")
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/dbtest/ -run TestRunTestSQLite`
Expected: FAIL — `undefined: dbtest.RunTestSQLite` (then, after Step 4 stub, migration table missing until Task 6).

- [ ] **Step 4: Write minimal implementation**

```go
// internal/dbtest/sqlite.go
package dbtest

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	"github.com/stretchr/testify/require"
	// migrate import wired in Task 6
)

// RunTestSQLite opens a fresh file-backed SQLite database (WAL, busy_timeout),
// applies migrations, and returns a *sql.DB torn down via t.Cleanup. In-process;
// no Docker. For lightweight/single-node tests only.
func RunTestSQLite(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err, "open sqlite")
	db.SetMaxOpenConns(1) // single-writer
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(t.Context()), "ping sqlite")
	// migrations applied here once Task 6 provides the migrate entry point:
	// require.NoError(t, persistencesqlite.Migrate(t.Context(), db))
	return db
}
```

> **Porting note:** the migration call is commented until Task 6 provides `Migrate` for SQLite; wire it in Task 6 and this test goes green then. If you prefer, split: this task lands the helper + dep (test asserts ping only), Task 6 adds migrations and the table assertion. Keep the RED observable either way.

- [ ] **Step 4b: Run test** — ping passes; table assertion fails until Task 6 (expected; note in report).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/dbtest/sqlite.go internal/dbtest/sqlite_test.go
git commit -m "feat(dbtest): add modernc.org/sqlite + RunTestSQLite helper"
```

---

## Task 6: SQLite migrations + `Migrate` entry point

**Files:**
- Create: SQLite migration SQL files mirroring the existing PG/MySQL schema (locate the current migrations dir first — check `internal/persistence/{postgres,mysql}/migrate.go` for the goose FS embed path and mirror it for sqlite).
- Modify: `internal/dbtest/sqlite.go` (wire `Migrate` + uncomment the table assertion path).
- Test: reuse `internal/dbtest/sqlite_test.go` (now the table assertion must pass).

**Interfaces:**
- Consumes: goose (existing migration tooling).
- Produces: a SQLite migration runner (e.g. `store.MigrateSQLite(ctx, *sql.DB)` or a `dialect`/facade-level `Migrate`) using `goose.DialectSQLite3`.

- [ ] **Step 1: Inspect existing migrations** — READ `internal/persistence/postgres/migrate.go` + the embedded `.sql` files to learn the exact schema (tables: `wrkflw_instances`, `wrkflw_journal`, `wrkflw_outbox`, `wrkflw_timers`, `wrkflw_definitions`, `wrkflw_call_links`, `wrkflw_chain_links`, `wrkflw_processed_message`, and any others). Note column types + constraints.

- [ ] **Step 2: Write the failing test** — the Task-5 test's table assertion (uncomment it / enable the `Migrate` call).

Run: `go test ./internal/dbtest/ -run TestRunTestSQLite`
Expected: FAIL — `wrkflw_instances` missing (no SQLite migrations yet).

- [ ] **Step 3: Author SQLite migrations + Migrate** — translate the PG schema to SQLite: `TIMESTAMPTZ`/`DATETIME(6)` → `TEXT`/`DATETIME` storing RFC3339 UTC; `BYTEA`/`BLOB` → `BLOB`; `BIGSERIAL` → `INTEGER PRIMARY KEY`/explicit; JSON → `TEXT`. Preserve unique constraints (for `IsUniqueViolation`) and the `(instance_id, timer_id)`/`(id, version)` conflict targets used by the dialect upserts. Wire a `Migrate` using `goose.SetBaseFS` + `goose.DialectSQLite3`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/dbtest/ -run TestRunTestSQLite`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dbtest/sqlite.go internal/persistence/**/migrate*.go <sqlite migration files>
git commit -m "feat(persistence): SQLite migrations + Migrate (goose sqlite3)"
```

---

## Task 7: Conformance harness + neutral `store` skeleton

**Files:**
- Create: `internal/persistence/store/store.go` (Store struct + `querier(ctx)` + constructor skeleton), `internal/persistence/store/conformance_test.go`
- Test: `internal/persistence/store/conformance_test.go`

**Interfaces:**
- Consumes: `database.From`, `database.Querier`, `database.BeginTx`, `transaction.Begin/JoinOrBegin/MarkRollback`, `dialect.Dialect`, `dbtest.RunTestDatabase/RunTestMySQL/RunTestSQLite`.
- Produces: `store.New(conn any, d dialect.Dialect, opts ...Option) *Store`; internal `func (s *Store) querier(ctx) database.Querier`; test harness `forEachDialect(t, func(t, backend))` where `backend` carries `{conn any, dialect dialect.Dialect, name string}` and a helper to obtain a fresh migrated conn per dialect.

- [ ] **Step 1: Write the failing test** (the harness itself, exercised by a trivial round-trip through `querier`)

```go
// internal/persistence/store/conformance_test.go
package store_test

import (
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
)

type backend struct {
	name    string
	conn    any
	dialect dialect.Dialect
}

// forEachDialect runs fn against Postgres (pgx), MySQL, and SQLite, each with a
// fresh migrated database. Capability-gated tests inspect backend.name.
func forEachDialect(t *testing.T, fn func(t *testing.T, b backend)) {
	t.Helper()
	t.Run("postgres", func(t *testing.T) {
		var pool *pgxpool.Pool = dbtest.RunTestDatabase(t)
		// NOTE: postgres store tests need the schema; RunTestDatabase gives a bare DB.
		// Apply postgres migrations here (persistence.MigratePostgres / store.MigratePostgres).
		fn(t, backend{"postgres", pool, dialect.NewPostgres()})
	})
	t.Run("mysql", func(t *testing.T) {
		var db *sql.DB = dbtest.RunTestMySQL(t) // already migrated
		fn(t, backend{"mysql", db, dialect.NewMySQL()})
	})
	t.Run("sqlite", func(t *testing.T) {
		var db *sql.DB = dbtest.RunTestSQLite(t) // migrated
		fn(t, backend{"sqlite", db, dialect.NewSQLite()})
	})
}

func TestStoreQuerierRoundTrip(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s := store.New(b.conn, b.dialect)
		q := s.QuerierForTest(t.Context()) // test-only accessor exposing querier(ctx)
		var one int
		require.NoError(t, q.QueryRow(t.Context(), b.dialect.Rebind(`SELECT 1`)).Scan(&one))
		require.Equal(t, 1, one)
	})
}
```

> **Porting note:** `RunTestDatabase` returns a bare Postgres DB (no migrations); MySQL/SQLite helpers migrate. Either (a) add a `MigratePostgres` call in the harness, or (b) extend `RunTestDatabase` to optionally migrate. Pick (a) to avoid changing dbtest semantics; call the existing postgres migrate. Expose `querier(ctx)` to the black-box test via a `QuerierForTest` method guarded by `//go:build` or a plainly-named exported test helper in an `export_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/store/ -run TestStoreQuerierRoundTrip`
Expected: FAIL — `undefined: store.New`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/persistence/store/store.go
package store

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// Store is the neutral, dialect-parametrized persistence store.
type Store struct {
	conn    any
	dialect dialect.Dialect
	notify  dialect.Notifier // optional
}

// Option configures a Store.
type Option func(*Store)

// WithNotifier injects the LISTEN receive-side capability (pgx + Postgres only).
func WithNotifier(n dialect.Notifier) Option { return func(s *Store) { s.notify = n } }

// New builds a Store over conn (*pgxpool.Pool or *sql.DB) with the given dialect.
func New(conn any, d dialect.Dialect, opts ...Option) *Store {
	s := &Store{conn: conn, dialect: d}
	for _, o := range opts {
		o(s)
	}
	return s
}

// querier returns the ambient transaction's Querier if ctx carries one, else a
// pool-backed Querier over conn.
func (s *Store) querier(ctx context.Context) database.Querier {
	if q, ok := transaction.Ambient(ctx); ok { // see note on transaction.Ambient
		return q
	}
	q, _ := database.From(s.conn)
	return q
}
```

> **Porting note — `transaction.Ambient`:** the `transaction` package currently exposes `JoinOrBegin` but the read-path wants "return the ambient Querier if present, else nil/false" WITHOUT beginning a new tx. If no such accessor exists, add a small `transaction.Ambient(ctx) (Querier, bool)` to the transaction package — this is a NEW toolkit symbol and needs its own TDD test in the `transaction` package (add a sub-step here), and it must NOT break the extraction constraint (it only uses stdlib + database). Alternatively route reads through `database.From(s.conn)` unconditionally and only writes through `JoinOrBegin`; if reads never need to see an uncommitted ambient write, that is simpler — decide based on whether any read-after-write-in-same-tx exists in the current stores (check `relay.go`/`store.go`). Document the choice.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/persistence/store/ -run TestStoreQuerierRoundTrip`
Expected: PASS on all three subtests (Docker up).

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/store.go internal/persistence/store/conformance_test.go internal/persistence/store/export_test.go
git commit -m "feat(store): neutral Store skeleton + querier(ctx) + 3-dialect conformance harness"
```

---

## Tasks 8–14: Port the structurally-parallel stores (one task each)

Each of these ports one store from `internal/persistence/{postgres,mysql}` into the neutral `store` package over `database.Querier` + `dialect` + `transaction`, with a conformance test that runs on all three dialects. **The porting recipe is identical for each; the specifics (methods, SQL) come from the existing source files.**

**Porting recipe (apply per task):**
1. READ both `internal/persistence/postgres/<file>.go` and `internal/persistence/mysql/<file>.go`. They are ~85-90% identical.
2. Create `internal/persistence/store/<file>.go` with the SAME method set and SAME public signatures (satisfying the same `runtime` interface), but:
   - Replace the concrete `*pgxpool.Pool`/`*sql.DB` field with the shared `Store` (or a small typed struct holding `conn any` + `dialect` + capabilities).
   - Every SQL string is written ONCE with `?` placeholders and run through `s.dialect.Rebind(...)`.
   - Upsert/ignore/notify/trigger-column/outbox-stats differences come from the `dialect` methods, not inline `if`.
   - Reads: `q := s.querier(ctx)`. Multi-write methods: `q, err := transaction.JoinOrBegin(ctx, s.conn)` then `q.Commit(ctx)`.
   - Error classification via `s.dialect.IsUniqueViolation` / `IsRetryableConflict`.
   - Scanned `time.Time` from real columns: `.UTC()`-normalized (ADR-0080).
3. Write a conformance test using `forEachDialect` asserting the store's observable behavior. FOLD IN the assertions from the existing `postgres_test`/`mysql_test` for this store so nothing regresses.
4. RED (undefined symbol), GREEN (all three dialects), commit.

Because the recipe references real source, there are no placeholders — the implementer reads the exact SQL from the named files. Each task's test must run green on postgres+mysql+sqlite before commit.

### Task 8: `Store` core (Create / Load / Commit / Entries) — the hot path

**Files:** Create `internal/persistence/store/store.go` (extend Task-7 skeleton), `internal/persistence/store/store_conformance_test.go`. Source: `postgres/store.go`, `mysql/store.go`.

**Interfaces:** Produces `Store` satisfying `runtime.Store` + `runtime.JournalReader` (`Create`, `Load`, `Commit`, `Entries`). Consumes `dialect`, `transaction`, `database.Querier`, `history_cap`/`trigger_codec` helpers (Task 15 — if not yet ported, port the two pure-Go helpers as part of this task's first step since Store depends on them).

- [ ] **Step 1: Write the failing conformance test**

```go
// internal/persistence/store/store_conformance_test.go
package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestStoreCreateLoadCommit(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s := store.New(b.conn, b.dialect)
		var _ runtime.Store = s // compile-time interface check

		inst := newTestInstance(t) // helper builds a valid runtime instance/snapshot
		require.NoError(t, s.Create(t.Context(), inst))

		got, err := s.Load(t.Context(), inst.ID)
		require.NoError(t, err)
		require.Equal(t, inst.ID, got.ID)

		// Commit a token move + journal entry; assert version CAS + journal + outbox row.
		require.NoError(t, s.Commit(t.Context(), /* expectedVersion, snapshot, journal, events per runtime.Store API */))
		// assert wrkflw_journal + wrkflw_outbox rows via a raw query through b.dialect.Rebind
	})
}
```

> Fill `newTestInstance` + the exact `Commit` args from the real `runtime.Store` signature and the existing `postgres/store_test.go`. Fold in that file's CAS-conflict and outbox assertions.

- [ ] **Step 2: RED** — `go test ./internal/persistence/store/ -run TestStoreCreateLoadCommit` → FAIL (methods undefined).
- [ ] **Step 3: Port** `Create/Load/Commit/Entries` + the write helpers (`writeJournal`, `writeOutbox`, timer/call-link helpers) per the recipe. NOTIFY via `dialect.NotifyStatement` inside the commit tx. Conflict handling via `dialect.IsRetryableConflict`/`IsUniqueViolation`.
- [ ] **Step 4: GREEN** — all three dialects pass; `go test -race ./internal/persistence/store/ -run TestStoreCreateLoadCommit`.
- [ ] **Step 5: Commit** — `feat(store): port Store Create/Load/Commit/Entries onto Querier+dialect+transaction`.

### Task 9: `Lister` — Source: `{postgres,mysql}/lister.go`. Produces `Lister` satisfying `runtime.InstanceLister`. Conformance test folds in `lister_test.go` assertions (paging, filters, cursor). Recipe as above.

### Task 10: `TimerStore` — Source: `{postgres,mysql}/timerstore.go`. Produces `TimerStore` satisfying `runtime.TimerStore` (`ListArmed`, `Stats`). fire_at `.UTC()` normalized (already done in Task 13 of Phase 1 — preserve). Recipe as above.

### Task 11: `DefinitionStore` — Source: `{postgres,mysql}/definitions.go`. Upsert via `dialect.UpsertDefinition`. Recipe as above.

### Task 12: `Deduper` — Source: `{postgres,mysql}/dedup.go`. `Seen(ctx, ...)` joins the ambient tx (drops the concrete-tx param). Insert-ignore via the dialect prefix/suffix (Task 3 note). `Prune`. Recipe as above.

### Task 13: `ChainLinkStore` — Source: `{postgres,mysql}/chainlink.go`. created_at `.UTC()` normalized. Recipe as above.

### Task 14: `Pruner` — Source: `{postgres,mysql}/pruner.go`. Retention deletes (outbox/call_links/chain_links/processed_message, + PruneTimers). Recipe as above.

Each of Tasks 9–14: RED (undefined) → port per recipe → GREEN on all three dialects (`-race`) → commit `feat(store): port <X> onto Querier+dialect`.

---

## Task 15: Move pure-Go helpers once

**Files:** Create `internal/persistence/store/history_cap.go`, `internal/persistence/store/trigger_codec.go`, `internal/persistence/store/relay_backoff.go`. Source: identical files in both dialect packages.

- [ ] **Step 1:** Confirm the three files are byte-identical across `postgres/` and `mysql/` (they are, per the exploration).
- [ ] **Step 2: RED** — write/keep the existing unit tests for these (copy from either package's `*_test.go`) in `store_test`; run → FAIL (undefined in `store`).
- [ ] **Step 3:** Copy the files verbatim into `store` (adjust package clause). If Task 8 already needed `capHistory`/trigger codec, this task formalizes the tests.
- [ ] **Step 4: GREEN** — `go test ./internal/persistence/store/ -run 'TestCapHistory|TestTrigger|TestRelayBackoff'`.
- [ ] **Step 5: Commit** — `refactor(store): move pure-Go helpers (history cap, trigger codec, relay backoff) into store`.

---

## Task 16: `CallLinkStore` incl. leased-claim divergence

**Files:** Create `internal/persistence/store/call_links.go`, conformance test. Source: `{postgres,mysql}/call_links.go`.

**Interfaces:** Produces `CallLinkStore` satisfying `runtime.CallLinkStore` (insert, ListRunningChildren, MarkNotified, ParentOf/ChildrenOf/LookupChild, and the leased claim).

- [ ] **Step 1: Write the failing conformance test** — cover plain CRUD + the leased-claim (claim-one, skip-locked semantics). Fold in `call_links_*_test.go` assertions.
- [ ] **Step 2: RED**.
- [ ] **Step 3: Port.** The leased-claim branches on `s.dialect.SupportsReturning()`:
  - `true` (PG/SQLite): `UPDATE ... SET ... WHERE id = (SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING ...` (SQLite: no SKIP LOCKED — use the plain single-writer claim; guard by dialect name if needed).
  - `false` (MySQL): `SELECT ... FOR UPDATE SKIP LOCKED` then a separate `UPDATE`, inside `transaction.JoinOrBegin`.
  Copy the exact SQL from both source files.
- [ ] **Step 4: GREEN** on all three (`-race`). SQLite claim is serialized — assert correctness, not concurrency.
- [ ] **Step 5: Commit** — `feat(store): port CallLinkStore incl. leased-claim (SupportsReturning branch)`.

---

## Task 17: `Relay` (drain / dead-letter / redrive / stats) + notify-send + poll

**Files:** Create `internal/persistence/store/relay.go`. Source: `{postgres,mysql}/relay.go`.

**Interfaces:** Produces `Relay` satisfying the facade `Relay` interface (`Run`, `DrainOnce`, `ListDeadLettered`, `Redrive`, `OutboxStats`). Consumes `runtime.Publisher`, `dialect.OutboxStatsQuery`, `RelayBackoff` (Task 15).

- [ ] **Step 1: Write the failing conformance test** — drain publishes + advances; dead-letter after max retries; redrive; `OutboxStats` counts. Fold in `relay_test.go`. Redrive by id-list: PG `= ANY($n)` vs MySQL/SQLite `IN (...)` — provide via a dialect helper or build the `IN` list generically with `?` and Rebind (prefer the generic `IN` form so all three share it; note PG accepts `IN` too).
- [ ] **Step 2: RED**.
- [ ] **Step 3: Port** DrainOnce/ListDeadLettered/Redrive/OutboxStats. Claim via `FOR UPDATE SKIP LOCKED` (PG/MySQL) — SQLite has no SKIP LOCKED, so the SQLite drain path uses a plain `SELECT ... LIMIT n` under its single-writer model (guard by `dialect.Name()`/a `SupportsSkipLocked()` flag — add that flag to `Dialect` if cleaner, with tests in the dialect package). NOTIFY send handled in Task 8's Commit; the relay's poll loop is the default.
- [ ] **Step 4: GREEN** on all three (`-race`, `goleak`).
- [ ] **Step 5: Commit** — `feat(store): port Relay (drain/deadletter/redrive/stats, poll + notify-send)`.

---

## Task 18: LISTEN receive-side `Notifier` capability (pgx)

**Files:** Create `internal/persistence/store/notifier_pgx.go` (or in the facade), conformance/integration test.

**Interfaces:** Produces a `pgxNotifier` implementing `dialect.Notifier` over a `*pgxpool.Pool` (acquires a dedicated conn, `LISTEN`, `WaitForNotification`); wired into `Relay` so `Run` wakes on NOTIFY when a `Notifier` is present, else polls.

- [ ] **Step 1: Write the failing test** (Postgres only — capability is pgx-specific): arm the relay with the notifier, commit an outbox row (fires NOTIFY), assert the drain wakes without waiting the full poll interval.
- [ ] **Step 2: RED**.
- [ ] **Step 3: Implement** `pgxNotifier.Listen` (dedicated `pool.Acquire`, `LISTEN wrkflw_outbox`, goroutine feeding a `<-chan struct{}`, cancel func releases the conn). Relay `Run`: `select` on the wake channel + poll ticker.
- [ ] **Step 4: GREEN** — Postgres test passes; MySQL/SQLite relays keep polling (no notifier). `-race`, `goleak` (the listen goroutine must exit on cancel).
- [ ] **Step 5: Commit** — `feat(store): pgx LISTEN notifier capability + relay wake integration`.

---

## Task 19: `Ownership` (`Locker`) + SQLite `ErrUnsupported`

**Files:** Create `internal/persistence/store/ownership.go`. Source: `{postgres,mysql}/ownership.go`.

**Interfaces:** Produces `AdvisoryLockOwnership` satisfying the current ownership interface, backed by a `dialect.Locker`; a `postgresLocker` (advisory lock), a `mysqlLocker` (`GET_LOCK`), and a `sqliteLocker` returning `dialect.ErrUnsupported`.

- [ ] **Step 1: Write the failing conformance test** — PG/MySQL: acquire → second acquire fails → unlock → re-acquire succeeds. SQLite: `TryLock` returns `ErrUnsupported`. Fold in `ownership_test.go`.
- [ ] **Step 2: RED**.
- [ ] **Step 3: Implement** the three lockers (copy PG advisory + MySQL GET_LOCK SQL from source; SQLite returns `(false, dialect.ErrUnsupported)`), and the ownership type delegating to the injected `Locker`.
- [ ] **Step 4: GREEN** on all three (`-race`).
- [ ] **Step 5: Commit** — `feat(store): port Ownership over Locker capability; SQLite ErrUnsupported`.

---

## Task 20: Facade rewire `OpenPostgres` / `OpenMySQL`

**Files:** Modify `persistence/persistence.go`, `persistence/mysql.go`, `persistence/dedup.go`, `persistence/pruner.go`, `persistence/health.go`, `persistence/relay_health.go`.

**Interfaces:** `OpenPostgres(ctx, pool, ...Option)` / `OpenMySQL(ctx, db, ...MySQLOption)` keep signatures; internally build `store.New(conn, dialect.NewPostgres()/NewMySQL(), ...)` + inject the pgx `Notifier` + the right `Locker`. `NewRelay`/`NewDefinitionStore`/etc. forward to the neutral `store` constructors. Option types map to `store.Option`.

- [ ] **Step 1: Write/adjust the failing test** — the existing `persistence/*_test.go` (facade) must pass against the rewired constructors. Add a focused test asserting `OpenPostgres` returns a working `Store` (create+load) and that `WithListenNotify` wires the pgx notifier.
- [ ] **Step 2: RED** — after switching the facade to `store.New`, the facade tests fail to compile / behave until wiring is complete.
- [ ] **Step 3: Rewire** each `Open*` + `New*` forwarder to the neutral store; reconcile options (`Option = store.Option` alias or a small adapter). ProbeUTC stays.
- [ ] **Step 4: GREEN** — `go test ./persistence/ -run TestOpen` + facade suite green (Docker).
- [ ] **Step 5: Commit** — `refactor(persistence): rewire OpenPostgres/OpenMySQL onto the neutral store`.

---

## Task 21: `OpenSQLite` facade

**Files:** Create `persistence/sqlite.go`, `persistence/sqlite_test.go`.

**Interfaces:** Produces `persistence.OpenSQLite(ctx, db *sql.DB, ...Option) (Store, error)` — `database.From(db)` + `ProbeUTC(ctx, q, database.SQLite?)` ... note: `database.ProbeUTC` currently has `Postgres`/`MySQL` dialects only. Add a `database.SQLite` dialect const + probe SQL to the toolkit — **this touches `internal/database`, which is extraction-constrained; it only adds a stdlib-based probe, so the extraction check stays green** (verify with `scripts/check-extraction.sh`). Add its unit test in the `internal/database` package (TDD).

- [ ] **Step 1: Write the failing test** — `OpenSQLite(ctx, dbtest.RunTestSQLite(t))` returns a working Store (create+load round-trip); ownership-dependent call returns `ErrUnsupported`.
- [ ] **Step 2: RED**.
- [ ] **Step 3: Implement** `OpenSQLite` (build `store.New(db, dialect.NewSQLite())`, no notifier, sqlite locker); add `database.SQLite` + `ProbeUTC` SQLite branch (`SELECT datetime('2000-01-01 00:00:00')` instant-equality) with its own toolkit test.
- [ ] **Step 4: GREEN** — `go test ./persistence/ -run TestOpenSQLite` + `go test ./internal/database/ -run TestProbeUTC` + `scripts/check-extraction.sh`.
- [ ] **Step 5: Commit** — `feat(persistence): OpenSQLite + database.SQLite probe dialect`.

---

## Task 22: Delete old packages + update all call sites

**Files:** Delete `internal/persistence/postgres/`, `internal/persistence/mysql/` (entire dirs). Modify every non-test/test importer + `examples/*`.

- [ ] **Step 1: Find importers** — `grep -rl "internal/persistence/postgres\|internal/persistence/mysql" --include=*.go .`. Expect: the facade (now rewired), scheduling electors, examples, and the dialect-package tests (none should remain).
- [ ] **Step 2: Delete + rewire** — remove the dirs; repoint any remaining importer to the neutral `store` / facade. The scheduling `mysql_elector`/`elector` tests that used `dbtest.RunTestMySQL` are unaffected (helpers live in dbtest).
- [ ] **Step 3: Compile gate** — `go build ./... && go test -run '^$' ./...` (all packages compile).
- [ ] **Step 4: Full verify** — `go test ./...` (Docker) green; `TZ=Asia/Jakarta go test ./internal/persistence/store/...`; `go test -race -coverprofile=cover.out ./internal/persistence/... ./persistence/...` ≥85%; `golangci-lint run ./...`; `scripts/check-extraction.sh`.
- [ ] **Step 5: Commit** — `refactor(persistence): delete postgres/mysql packages; unify on neutral store`.

---

## Task 23: ADRs

**Files:** Create `docs/adr/00NN-store-unification-dialect.md`, `docs/adr/00NN-sqlite-backend.md` (numbers via `ls docs/adr/ | sort | tail -3`; expect 0081, 0082).

- [ ] **Step 1:** Determine next ADR numbers.
- [ ] **Step 2:** ADR — store unification + dialect abstraction. Nygard template. Decision: two-axis model (access × dialect); neutral `store` over `database.Querier` + `dialect.Dialect`; capability interfaces (`Notifier`, `Locker`); the rename (no per-database store package); deletion of `postgres`/`mysql` packages; conformance suite. Consequences: single store to maintain; adding a driver = a dialect + optional capabilities; SQLite is single-node/test-oriented.
- [ ] **Step 3:** ADR — SQLite backend. Decision: `modernc.org/sqlite` (pure-Go), `database/sql` access, WAL + busy_timeout, ownership `ErrUnsupported`, single-writer. Consequences: lightweight/test use; not for multi-node/high-concurrency.
- [ ] **Step 4:** Verify format/numbering.
- [ ] **Step 5: Commit** — `docs(adr): store unification + dialect abstraction + SQLite backend`.

---

## Final Verification

- [ ] `go build ./...`
- [ ] `go test ./...` (Docker running) — all green, no regressions.
- [ ] `TZ=Asia/Jakarta go test ./internal/persistence/store/... ./persistence/...` — time fidelity under non-UTC host.
- [ ] `go test -race -coverprofile=cover.out ./internal/persistence/... ./persistence/... ./internal/persistence/dialect/... && go tool cover -func=cover.out | tail -1` — ≥ 85% per package.
- [ ] `golangci-lint run ./...` — clean.
- [ ] `scripts/check-extraction.sh` — internal/database still imports only the two toolkit packages (the `database.SQLite` probe addition must not have pulled in wrkflw deps).
- [ ] `internal/persistence/postgres` and `internal/persistence/mysql` no longer exist; `grep -r "internal/persistence/\(postgres\|mysql\)" --include=*.go .` returns nothing.
- [ ] Conformance suite runs and passes on all three dialects (postgres, mysql, sqlite).
