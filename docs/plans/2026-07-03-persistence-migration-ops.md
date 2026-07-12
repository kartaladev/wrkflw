# Persistence / Migration Ops (P1-C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give consumers a full migration lifecycle API (version/status/rollback/up-to) over all three dialects, a cross-dialect schema-parity guardrail, production-safety docs + an opt-in config-warning helper, and close the `PruneTimers` interface gap.

**Architecture:** A stateless internal `store.Migrator` wraps `goose.Provider` for a given dialect + embedded migration FS, building a fresh provider per call. A public `persistence.Migrator` interface + `MigrationStatus` DTO front it via per-dialect constructors, with the existing `Migrate*` funcs kept as thin `Up`-only wrappers. Additive facade work (parity test, `WarnUnsafeConfig`, `PruneTimers`) rounds out the track.

**Tech Stack:** Go 1.25, `github.com/pressly/goose/v3` (already vendored), `pgx/v5` (Postgres), `database/sql` (MySQL/SQLite via `modernc.org/sqlite`), testcontainers-go via `internal/dbtest`, `stretchr/testify`, stdlib `flag` + `log/slog`.

## Global Constraints

- **Go 1.25**; single module `github.com/kartaladev/wrkflw`.
- **Library-first (CLAUDE.md):** no shipped daemon/binary; the migration CLI lives in `examples/` as reference wiring only. No auto-migrate on import or on `Open*`.
- **No new runtime dependency.** goose v3 is already present; the CLI uses stdlib `flag` (no cobra).
- **Constructor convention (ADR-0083):** stateful constructors taking a required non-nilable dependency return `(T, error)` and reject nil (incl. typed-nil) with a wrapped `ErrNilDependency`.
- **Error sentinel prefix:** messages use the `workflow-<pkg>:` prefix (e.g. `workflow-store: ...`).
- **Engine/model zero-diff:** this track touches only `persistence`, `internal/persistence/store`, `examples/`, and `docs/`. Do NOT modify `engine/` or `model/`.
- **TDD strict (CLAUDE.md):** every new symbol is preceded by a failing test with an observable red run. Table tests use the project `table-test` skill closure form; DB tests use the `use-testcontainers` skill helpers (`dbtest.RunTest*`), never hand-rolled containers.
- **Verification per package:** `go test -race ./<pkg>/...` green, ≥85% line coverage; `golangci-lint run ./...` clean.
- **Paths never contain `superpowers`:** spec at `docs/specs/2026-07-03-persistence-migration-ops.md`, this plan at `docs/plans/2026-07-03-persistence-migration-ops.md`.
- **ADR numbering:** next free is **0085**. Task 2 writes ADR-0085; Task 5 writes ADR-0086. Nygard template; example `docs/adr/0001-record-architecture-decisions.md`.
- **Branch:** `feat/persistence-migration-ops` (already checked out).

---

## File Structure

- `internal/persistence/store/migrator.go` — **new.** Internal stateless `Migrator` + `StatusRow` + `newMigrator` (nil-guard) + exported `NewPostgresMigrator`/`NewMySQLMigrator`/`NewSQLiteMigrator` + method set. Absorbs the provider-building logic currently duplicated in `migrate_{postgres,mysql,sqlite}.go`.
- `internal/persistence/store/migrate_postgres.go` / `migrate_mysql.go` / `migrate_sqlite.go` — **modify.** `MigratePostgres`/`MigrateMySQL`/`MigrateSQLite` become thin wrappers delegating to the new constructors' `Up`. Keep the `//go:embed` FS vars (move them into `migrator.go` if cleaner, but keeping them where they are is fine — one owner per FS).
- `internal/persistence/store/migrator_test.go` — **new.** SQLite in-memory full-lifecycle tests + nil-guard unit tests.
- `persistence/migrator.go` — **new.** Public `Migrator` interface, `MigrationStatus` DTO, three `NewXMigrator` facade constructors, `StatusRow`→`MigrationStatus` mapping.
- `persistence/migrator_test.go` — **new.** Facade lifecycle (sqlite) + nil-reject + PG/MySQL-gated introspection.
- `examples/migrate/main.go` — **new.** stdlib-`flag` reference CLI with a testable `run(...)` seam.
- `examples/migrate/main_test.go` — **new.** Smoke test of `run` against sqlite in-memory.
- `internal/persistence/store/migration_parity_test.go` — **new.** Cross-dialect logical-schema parity guardrail.
- `persistence/unsafe_config.go` — **new.** `DeploymentProfile` + `WarnUnsafeConfig` + warn-message constants.
- `persistence/unsafe_config_test.go` — **new.** Table test over profiles via a capturing slog handler.
- `persistence/pruner.go` — **modify.** Add `PruneTimers` to the `Pruner` interface.
- `persistence/pruner_test.go` — **modify.** Add a `PruneTimers`-through-the-interface test.
- `docs/production-checklist.md` — **new.** Pool / statement-timeout / isolation + opt-in MUST-DOs.
- `docs/retention.md`, `README.md` — **modify.** Cross-link the checklist.
- `docs/adr/0085-migration-lifecycle-api.md`, `docs/adr/0086-opt-in-unsafe-config-warning.md` — **new.**

---

## Task 1: Internal `store.Migrator` (stateless, over goose Provider)

**Files:**
- Create: `internal/persistence/store/migrator.go`
- Create: `internal/persistence/store/migrator_test.go`
- Modify: `internal/persistence/store/migrate_postgres.go`, `migrate_mysql.go`, `migrate_sqlite.go`

**Interfaces:**
- Consumes: `store.ErrNilDependency` + `isNilDep` (errors.go); the three `//go:embed` FS vars (`postgresMigrationsFS`/`mysqlMigrationsFS`/`sqliteMigrationsFS`); `goose.NewProvider`, `goose.Dialect{Postgres,MySQL,SQLite3}`, `stdlib.OpenDBFromPool`.
- Produces (used by Task 2):
  - `type StatusRow struct { Version int64; Source string; Applied bool; AppliedAt time.Time }`
  - `func NewPostgresMigrator(pool *pgxpool.Pool) (*Migrator, error)`
  - `func NewMySQLMigrator(db *sql.DB) (*Migrator, error)`
  - `func NewSQLiteMigrator(db *sql.DB) (*Migrator, error)`
  - methods on `*Migrator`: `Up(ctx) error`, `UpByOne(ctx) error`, `UpTo(ctx, int64) error`, `Down(ctx) error`, `DownTo(ctx, int64) error`, `Version(ctx) (int64, error)`, `Status(ctx) ([]StatusRow, error)`, `HasPending(ctx) (bool, error)`.

- [ ] **Step 1: Write the failing nil-guard + lifecycle tests**

Create `internal/persistence/store/migrator_test.go` (package `store`, white-box — it constructs raw sqlite and exercises unexported behavior):

```go
package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawSQLite opens a fresh in-memory SQLite DB with single-writer serialisation.
// No migrations are applied — the Migrator drives them.
func rawSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewSQLiteMigrator_RejectsNilDB(t *testing.T) {
	t.Parallel()
	_, err := NewSQLiteMigrator(nil)
	require.ErrorIs(t, err, ErrNilDependency)
}

func TestNewSQLiteMigrator_RejectsTypedNilDB(t *testing.T) {
	t.Parallel()
	var db *sql.DB // typed nil
	_, err := NewSQLiteMigrator(db)
	require.ErrorIs(t, err, ErrNilDependency)
}

func TestMigrator_SQLiteLifecycle(t *testing.T) {
	t.Parallel()
	m, err := NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	ctx := t.Context()

	// Empty DB: pending work exists, version 0.
	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.True(t, pending, "fresh DB must have pending migrations")

	// Up applies everything; SQLite head is the single consolidated 0001.
	require.NoError(t, m.Up(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v, "SQLite migration head is version 1")

	pending, err = m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "no pending migrations after Up")

	// Status lists the applied source.
	rows, err := m.Status(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	for _, r := range rows {
		assert.True(t, r.Applied, "every source applied after Up: %s", r.Source)
	}

	// DownTo(0) rolls everything back — the wrkflw tables are gone.
	require.NoError(t, m.DownTo(ctx, 0))
	var n int
	err = m.provDB(t).QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wrkflw_instances'`,
	).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "wrkflw_instances dropped after DownTo(0)")
}

func TestMigrator_SQLiteUpTo(t *testing.T) {
	t.Parallel()
	m, err := NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	// SQLite head is 1, so UpTo(1) == Up; assert it lands on 1 and is a no-op re-run.
	require.NoError(t, m.UpTo(t.Context(), 1))
	v, err := m.Version(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), v)
	require.NoError(t, m.UpTo(t.Context(), 1), "re-running UpTo is idempotent")
}
```

Add a tiny white-box helper in the test file to reach the underlying `*sql.DB` for assertions (only sqlite path is exercised here):

```go
// provDB returns the *sql.DB backing a sqlite/mysql Migrator for white-box
// assertions. Postgres (pool-backed) is not used in this test file.
func (m *Migrator) provDB(t *testing.T) *sql.DB {
	t.Helper()
	db, ok := m.conn.(*sql.DB)
	require.True(t, ok, "provDB only valid for *sql.DB-backed migrators")
	return db
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/persistence/store/ -run 'TestNewSQLiteMigrator|TestMigrator_SQLite' 2>&1 | tail -20`
Expected: FAIL — `undefined: NewSQLiteMigrator`, `undefined: Migrator`, `undefined: StatusRow`.

- [ ] **Step 3: Implement `migrator.go`**

Create `internal/persistence/store/migrator.go`:

```go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migrator drives schema migrations for one backend over an embedded goose
// migration set. It is stateless with respect to the goose Provider: every
// method constructs a fresh Provider, runs the operation, and releases it. This
// mirrors the historical per-call construction (safe for parallel tests) and
// avoids leaking a Close lifecycle onto callers.
//
// Construct via NewPostgresMigrator, NewMySQLMigrator, or NewSQLiteMigrator.
type Migrator struct {
	conn         any          // *pgxpool.Pool or *sql.DB
	gooseDialect goose.Dialect
	fsys         fs.FS        // sub-FS rooted at the dialect's *.sql files
}

// StatusRow is one migration's applied state, decoupled from goose's type.
type StatusRow struct {
	Version   int64
	Source    string
	Applied   bool
	AppliedAt time.Time
}

func newMigrator(conn any, d goose.Dialect, embedded fs.FS, dir string) (*Migrator, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("workflow-store: new migrator: %w", ErrNilDependency)
	}
	sub, err := fs.Sub(embedded, dir)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: new migrator: sub fs: %w", err)
	}
	return &Migrator{conn: conn, gooseDialect: d, fsys: sub}, nil
}

// NewPostgresMigrator builds a Migrator over a pgx pool. The pool is not closed
// by the Migrator; each call wraps it in a short-lived database/sql shim.
func NewPostgresMigrator(pool *pgxpool.Pool) (*Migrator, error) {
	return newMigrator(pool, goose.DialectPostgres, postgresMigrationsFS, "migrations/postgres")
}

// NewMySQLMigrator builds a Migrator over a MySQL *sql.DB.
func NewMySQLMigrator(db *sql.DB) (*Migrator, error) {
	return newMigrator(db, goose.DialectMySQL, mysqlMigrationsFS, "migrations/mysql")
}

// NewSQLiteMigrator builds a Migrator over a SQLite *sql.DB.
func NewSQLiteMigrator(db *sql.DB) (*Migrator, error) {
	return newMigrator(db, goose.DialectSQLite3, sqliteMigrationsFS, "migrations/sqlite")
}

// provider builds a fresh goose.Provider plus a cleanup func. For a pgx pool the
// cleanup closes the database/sql shim (which does NOT close the pool); for a
// *sql.DB the cleanup is a no-op (the caller owns the DB).
func (m *Migrator) provider() (*goose.Provider, func() error, error) {
	var db *sql.DB
	cleanup := func() error { return nil }
	switch c := m.conn.(type) {
	case *pgxpool.Pool:
		db = stdlib.OpenDBFromPool(c)
		cleanup = db.Close
	case *sql.DB:
		db = c
	default:
		return nil, cleanup, fmt.Errorf("workflow-store: migrator: unsupported conn type %T", m.conn)
	}
	p, err := goose.NewProvider(m.gooseDialect, db, m.fsys)
	if err != nil {
		_ = cleanup()
		return nil, func() error { return nil }, fmt.Errorf("workflow-store: migrator: new provider: %w", err)
	}
	return p, cleanup, nil
}

func (m *Migrator) with(op string, fn func(context.Context, *goose.Provider) error) func(context.Context) error {
	return func(ctx context.Context) error {
		p, cleanup, err := m.provider()
		if err != nil {
			return err
		}
		defer func() { _ = cleanup() }()
		if err := fn(ctx, p); err != nil {
			return fmt.Errorf("workflow-store: migrator: %s: %w", op, err)
		}
		return nil
	}
}

// Up applies all pending migrations. Idempotent.
func (m *Migrator) Up(ctx context.Context) error {
	return m.with("up", func(ctx context.Context, p *goose.Provider) error {
		_, err := p.Up(ctx)
		return err
	})(ctx)
}

// UpByOne applies the next single pending migration.
func (m *Migrator) UpByOne(ctx context.Context) error {
	return m.with("up by one", func(ctx context.Context, p *goose.Provider) error {
		_, err := p.UpByOne(ctx)
		return err
	})(ctx)
}

// UpTo applies migrations up to and including version.
func (m *Migrator) UpTo(ctx context.Context, version int64) error {
	return m.with("up to", func(ctx context.Context, p *goose.Provider) error {
		_, err := p.UpTo(ctx, version)
		return err
	})(ctx)
}

// Down rolls back the most recently applied migration. DATA LOSS: runs the
// migration's DROP/ALTER Down statements.
func (m *Migrator) Down(ctx context.Context) error {
	return m.with("down", func(ctx context.Context, p *goose.Provider) error {
		_, err := p.Down(ctx)
		return err
	})(ctx)
}

// DownTo rolls back to (but not past) version; pass 0 to roll back everything.
// DATA LOSS.
func (m *Migrator) DownTo(ctx context.Context, version int64) error {
	return m.with("down to", func(ctx context.Context, p *goose.Provider) error {
		_, err := p.DownTo(ctx, version)
		return err
	})(ctx)
}

// Version returns the current applied schema version (0 if none applied).
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	p, cleanup, err := m.provider()
	if err != nil {
		return 0, err
	}
	defer func() { _ = cleanup() }()
	v, err := p.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: migrator: version: %w", err)
	}
	return v, nil
}

// HasPending reports whether any migration is unapplied.
func (m *Migrator) HasPending(ctx context.Context) (bool, error) {
	p, cleanup, err := m.provider()
	if err != nil {
		return false, err
	}
	defer func() { _ = cleanup() }()
	pending, err := p.HasPending(ctx)
	if err != nil {
		return false, fmt.Errorf("workflow-store: migrator: has pending: %w", err)
	}
	return pending, nil
}

// Status lists every known migration source and whether it is applied.
func (m *Migrator) Status(ctx context.Context) ([]StatusRow, error) {
	p, cleanup, err := m.provider()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cleanup() }()
	st, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: migrator: status: %w", err)
	}
	rows := make([]StatusRow, 0, len(st))
	for _, s := range st {
		row := StatusRow{Applied: s.State == goose.StateApplied, AppliedAt: s.AppliedAt}
		if s.Source != nil {
			row.Version = s.Source.Version
			row.Source = s.Source.Path
		}
		rows = append(rows, row)
	}
	return rows, nil
}
```

> Implementer note (goose v3.27.1 field shapes — CONFIRMED): `goose.MigrationStatus{ Source *Source; State State; AppliedAt time.Time }`. `Source` is a **pointer** (nil-check it); `State` is a value string (compare `== goose.StateApplied`); `AppliedAt` is a **value** `time.Time` (assign directly, no nil-check). `Source{ Path string; Version int64 }`. The code above already matches these.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/persistence/store/ -run 'TestNewSQLiteMigrator|TestMigrator_SQLite' -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Refactor the three `Migrate*` funcs into wrappers**

In `migrate_postgres.go`, replace the body of `MigratePostgres` with:

```go
func MigratePostgres(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := NewPostgresMigrator(pool)
	if err != nil {
		return err
	}
	return m.Up(ctx)
}
```

Do the same for `MigrateMySQL` (`NewMySQLMigrator(db).Up`) and `MigrateSQLite` (`NewSQLiteMigrator(db).Up`). Remove now-unused imports (`goose`, `fs`, `stdlib`) from those three files — the FS `//go:embed` vars stay. Keep the existing godoc.

- [ ] **Step 6: Verify the whole store package still passes**

Run: `go test ./internal/persistence/store/... 2>&1 | tail -15` (Docker up for the gated conformance tests; without Docker the sqlite subtests still run).
Expected: PASS, no regressions in the existing `Migrate*` behavior.
Run: `golangci-lint run ./internal/persistence/store/... 2>&1 | tail`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/persistence/store/migrator.go internal/persistence/store/migrator_test.go \
  internal/persistence/store/migrate_postgres.go internal/persistence/store/migrate_mysql.go internal/persistence/store/migrate_sqlite.go
git commit -m "feat(store): add stateless Migrator over goose Provider; Migrate* now wrappers"
```

---

## Task 2: Public `persistence.Migrator` facade + ADR-0085

**Files:**
- Create: `persistence/migrator.go`
- Create: `persistence/migrator_test.go`
- Create: `docs/adr/0085-migration-lifecycle-api.md`

**Interfaces:**
- Consumes: `store.NewPostgresMigrator`/`NewMySQLMigrator`/`NewSQLiteMigrator`, `store.StatusRow`, `store.Migrator` (Task 1); `dbtest.RunTestDatabase`/`RunTestMySQL`/`RunTestSQLite`; `persistence.Migrate` (for gated pg introspection setup parity).
- Produces:
  - `type Migrator interface { Up/UpByOne/UpTo/Down/DownTo/Version/Status/HasPending ... }`
  - `type MigrationStatus struct { Version int64; Source string; Applied bool; AppliedAt time.Time }`
  - `func NewPostgresMigrator(pool *pgxpool.Pool) (Migrator, error)`
  - `func NewMySQLMigrator(db *sql.DB) (Migrator, error)`
  - `func NewSQLiteMigrator(db *sql.DB) (Migrator, error)`

- [ ] **Step 1: Write the failing facade tests**

Create `persistence/migrator_test.go` (package `persistence_test`):

```go
package persistence_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/persistence"
)

func rawSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewSQLiteMigrator_NilRejected(t *testing.T) {
	t.Parallel()
	_, err := persistence.NewSQLiteMigrator(nil)
	require.ErrorIs(t, err, store.ErrNilDependency)
}

func TestMigrator_FacadeLifecycle_SQLite(t *testing.T) {
	t.Parallel()
	m, err := persistence.NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, m.Up(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v)

	st, err := m.Status(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, st)
	assert.True(t, st[0].Applied)

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending)

	// DownTo(0) is a data-loss rollback: re-opening reports pending again.
	require.NoError(t, m.DownTo(ctx, 0))
	pending, err = m.HasPending(ctx)
	require.NoError(t, err)
	assert.True(t, pending, "rollback leaves all migrations pending")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./persistence/ -run 'TestNewSQLiteMigrator_NilRejected|TestMigrator_FacadeLifecycle' 2>&1 | tail -15`
Expected: FAIL — `undefined: persistence.NewSQLiteMigrator`.

- [ ] **Step 3: Implement `persistence/migrator.go`**

```go
package persistence

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// Migrator drives schema migrations for one backend. Construct it with the
// dialect-specific NewPostgresMigrator, NewMySQLMigrator, or NewSQLiteMigrator.
// Every method builds a short-lived migration session, so a Migrator is safe to
// reuse and safe to call concurrently.
//
// Migration is always an explicit consumer action — no method is auto-invoked.
type Migrator interface {
	// Up applies all pending migrations. Idempotent.
	Up(ctx context.Context) error
	// UpByOne applies the next single pending migration.
	UpByOne(ctx context.Context) error
	// UpTo applies migrations up to and including version.
	UpTo(ctx context.Context, version int64) error
	// Down rolls back the most recently applied migration.
	//
	// DATA LOSS: this executes the migration's Down statements (DROP TABLE /
	// ALTER ...). Rows in dropped tables are permanently deleted. Back up first
	// and never call this against production without an operator in the loop.
	Down(ctx context.Context) error
	// DownTo rolls back to (but not past) version; pass 0 to roll back all
	// migrations.
	//
	// DATA LOSS: see Down. DownTo(ctx, 0) drops every wrkflw table.
	DownTo(ctx context.Context, version int64) error
	// Version returns the current applied schema version (0 if none applied).
	Version(ctx context.Context) (int64, error)
	// Status lists every known migration and whether it is applied.
	Status(ctx context.Context) ([]MigrationStatus, error)
	// HasPending reports whether any migration is unapplied.
	HasPending(ctx context.Context) (bool, error)
}

// MigrationStatus is one migration's applied state.
type MigrationStatus struct {
	Version   int64     // numeric version (filename prefix)
	Source    string    // migration source path/base name
	Applied   bool      // whether applied to the DB
	AppliedAt time.Time // zero if not applied
}

// migrator adapts *store.Migrator to the public Migrator interface, mapping the
// internal StatusRow to the public MigrationStatus DTO so goose's types never
// leak across the facade.
type migrator struct{ inner *store.Migrator }

// NewPostgresMigrator constructs a Migrator over a pgx pool. Returns a wrapped
// store.ErrNilDependency if pool is nil.
func NewPostgresMigrator(pool *pgxpool.Pool) (Migrator, error) {
	m, err := store.NewPostgresMigrator(pool)
	if err != nil {
		return nil, err
	}
	return &migrator{inner: m}, nil
}

// NewMySQLMigrator constructs a Migrator over a MySQL *sql.DB.
func NewMySQLMigrator(db *sql.DB) (Migrator, error) {
	m, err := store.NewMySQLMigrator(db)
	if err != nil {
		return nil, err
	}
	return &migrator{inner: m}, nil
}

// NewSQLiteMigrator constructs a Migrator over a SQLite *sql.DB.
func NewSQLiteMigrator(db *sql.DB) (Migrator, error) {
	m, err := store.NewSQLiteMigrator(db)
	if err != nil {
		return nil, err
	}
	return &migrator{inner: m}, nil
}

func (m *migrator) Up(ctx context.Context) error               { return m.inner.Up(ctx) }
func (m *migrator) UpByOne(ctx context.Context) error          { return m.inner.UpByOne(ctx) }
func (m *migrator) UpTo(ctx context.Context, v int64) error    { return m.inner.UpTo(ctx, v) }
func (m *migrator) Down(ctx context.Context) error             { return m.inner.Down(ctx) }
func (m *migrator) DownTo(ctx context.Context, v int64) error  { return m.inner.DownTo(ctx, v) }
func (m *migrator) Version(ctx context.Context) (int64, error) { return m.inner.Version(ctx) }
func (m *migrator) HasPending(ctx context.Context) (bool, error) {
	return m.inner.HasPending(ctx)
}

func (m *migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	rows, err := m.inner.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationStatus, len(rows))
	for i, r := range rows {
		out[i] = MigrationStatus{Version: r.Version, Source: r.Source, Applied: r.Applied, AppliedAt: r.AppliedAt}
	}
	return out, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./persistence/ -run 'TestNewSQLiteMigrator_NilRejected|TestMigrator_FacadeLifecycle' -v 2>&1 | tail -15`
Expected: PASS.

- [ ] **Step 5: Add the testcontainers-gated PG + MySQL introspection tests**

Append to `persistence/migrator_test.go`:

```go
import (
	// add to the existing import block:
	"github.com/kartaladev/wrkflw/internal/dbtest"
)

func TestMigrator_Postgres_Introspection(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t) // bare pool, no migrations
	m, err := persistence.NewPostgresMigrator(pool)
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, m.Up(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(9), v, "Postgres migration head is 9")

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending)

	st, err := m.Status(ctx)
	require.NoError(t, err)
	assert.Len(t, st, 9, "9 postgres migration sources")
}

func TestMigrator_MySQL_Introspection(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t) // already migrated
	m, err := persistence.NewMySQLMigrator(db)
	require.NoError(t, err)
	ctx := t.Context()

	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "MySQL migration head is 2")

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "RunTestMySQL already migrated to head")
}
```

Run: `go test ./persistence/ -run 'TestMigrator_Postgres_Introspection|TestMigrator_MySQL_Introspection' 2>&1 | tail -15` (Docker required).
Expected: PASS. (If a `Status` field/count assertion is off, correct the expected count to match the actual migration files, not the impl.)

- [ ] **Step 6: Write ADR-0085**

Create `docs/adr/0085-migration-lifecycle-api.md` (Nygard template): Context (Up-only facade, goose Provider already supports the rest, library-first so no shipped CLI); Decision (per-dialect `Migrator` type + `MigrationStatus` DTO; rollback exposed plainly with strong godoc data-loss warnings; back-compat `Migrate*` wrappers retained; internal stateless provider-per-call); Consequences (consumers can introspect + roll back + up-to; goose types don't leak; destructive ops are the operator's responsibility; the reference CLI lives in `examples/`).

- [ ] **Step 7: Verify + commit**

Run: `go test ./persistence/... 2>&1 | tail; golangci-lint run ./persistence/... 2>&1 | tail`
Expected: clean.

```bash
git add persistence/migrator.go persistence/migrator_test.go docs/adr/0085-migration-lifecycle-api.md
git commit -m "feat(persistence): public Migrator facade (version/status/rollback/up-to) [ADR-0085]"
```

---

## Task 3: Reference migration CLI (`examples/migrate`)

**Files:**
- Create: `examples/migrate/main.go`
- Create: `examples/migrate/main_test.go`

**Interfaces:**
- Consumes: `persistence.NewPostgresMigrator`/`NewMySQLMigrator`/`NewSQLiteMigrator`, `persistence.Migrator`, `persistence.MigrationStatus`.
- Produces: `func run(args []string, out io.Writer) int` (testable seam); `func main()` calls `os.Exit(run(os.Args[1:], os.Stdout))`.

- [ ] **Step 1: Write the failing smoke test**

Create `examples/migrate/main_test.go` (package `main`):

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_SQLiteUpThenStatusThenVersion(t *testing.T) {
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"

	var out bytes.Buffer
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "up"}, &out), "up must exit 0")

	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "version"}, &out))
	assert.Contains(t, out.String(), "1", "version should report head 1")

	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "status"}, &out))
	assert.Contains(t, strings.ToLower(out.String()), "applied")
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 2, run([]string{"-dialect=sqlite", "-dsn=file:x?mode=memory", "frobnicate"}, &out))
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./examples/migrate/ 2>&1 | tail -15`
Expected: FAIL — `undefined: run`.

- [ ] **Step 3: Implement `examples/migrate/main.go`**

```go
// Command migrate is REFERENCE WIRING ONLY (not a shipped product binary) that
// demonstrates driving wrkflw schema migrations from a consumer's own CLI using
// the persistence.Migrator facade.
//
// Usage:
//
//	migrate -dialect=postgres -dsn=postgres://... up
//	migrate -dialect=mysql    -dsn='user:pass@tcp(host:3306)/db' status
//	migrate -dialect=sqlite   -dsn='file:app.db?_pragma=journal_mode(WAL)' downto 3
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"

	"github.com/kartaladev/wrkflw/persistence"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

// run parses args, builds the matching Migrator, executes the subcommand, and
// returns a process exit code (0 ok, 1 runtime error, 2 usage error).
func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(out)
	dialect := fs.String("dialect", "", "postgres | mysql | sqlite")
	dsn := fs.String("dsn", "", "database connection string")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sub := fs.Arg(0)
	if *dialect == "" || *dsn == "" || sub == "" {
		fmt.Fprintln(out, "usage: migrate -dialect=<d> -dsn=<dsn> <up|upto <v>|down|downto <v>|status|version>")
		return 2
	}

	ctx := context.Background()
	m, closeFn, err := openMigrator(ctx, *dialect, *dsn)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	defer closeFn()

	if err := exec(ctx, m, sub, fs.Args()[1:], out); err != nil {
		fmt.Fprintln(out, "error:", err)
		if err == errUsage {
			return 2
		}
		return 1
	}
	return 0
}

var errUsage = fmt.Errorf("usage error")

func openMigrator(ctx context.Context, dialect, dsn string) (persistence.Migrator, func(), error) {
	switch dialect {
	case "postgres":
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, func() {}, err
		}
		m, err := persistence.NewPostgresMigrator(pool)
		return m, pool.Close, err
	case "mysql":
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, func() {}, err
		}
		m, err := persistence.NewMySQLMigrator(db)
		return m, func() { _ = db.Close() }, err
	case "sqlite":
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, func() {}, err
		}
		db.SetMaxOpenConns(1)
		m, err := persistence.NewSQLiteMigrator(db)
		return m, func() { _ = db.Close() }, err
	default:
		return nil, func() {}, fmt.Errorf("unknown dialect %q", dialect)
	}
}

func exec(ctx context.Context, m persistence.Migrator, sub string, rest []string, out io.Writer) error {
	switch sub {
	case "up":
		return m.Up(ctx)
	case "down":
		return m.Down(ctx)
	case "upto", "downto":
		if len(rest) < 1 {
			fmt.Fprintf(out, "%s requires a <version> argument\n", sub)
			return errUsage
		}
		v, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid version %q: %w", rest[0], err)
		}
		if sub == "upto" {
			return m.UpTo(ctx, v)
		}
		return m.DownTo(ctx, v)
	case "version":
		v, err := m.Version(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "current version:", v)
		return nil
	case "status":
		rows, err := m.Status(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			state := "pending"
			if r.Applied {
				state = "applied"
			}
			fmt.Fprintf(out, "%d\t%s\t%s\n", r.Version, state, r.Source)
		}
		return nil
	default:
		fmt.Fprintf(out, "unknown subcommand %q\n", sub)
		return errUsage
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./examples/migrate/ -v 2>&1 | tail -20`
Expected: PASS. Confirm `go build ./examples/migrate/` compiles.

- [ ] **Step 5: Commit**

```bash
git add examples/migrate/main.go examples/migrate/main_test.go
git commit -m "feat(examples): reference migration CLI over persistence.Migrator"
```

---

## Task 4: Cross-dialect schema-parity guardrail

**Files:**
- Create: `internal/persistence/store/migration_parity_test.go`

**Interfaces:**
- Consumes: `dbtest.RunTestDatabase`/`RunTestMySQL` (Docker-gated) + a raw in-memory SQLite; `persistence.Migrate` (pg) — or the internal `NewPostgresMigrator` since this is package `store` white-box.

- [ ] **Step 1: Write the failing parity test**

Create `internal/persistence/store/migration_parity_test.go` (package `store` white-box so it can call the internal migrators; import `dbtest`):

```go
package store

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
)

// colFacts is the logical (dialect-independent) shape of one column. Physical
// types are intentionally NOT compared — JSONB/JSON/TEXT and TIMESTAMPTZ/
// DATETIME(6)/TEXT legitimately differ per dialect.
type colFacts struct {
	Nullable   bool
	PrimaryKey bool
}

// logicalSchema is table -> column -> facts, restricted to wrkflw_* tables.
type logicalSchema map[string]map[string]colFacts

func TestMigrationParity_LogicalSchemaConverges(t *testing.T) {
	ctx := context.Background()

	// SQLite (always available, no Docker).
	sqliteDB, err := sql.Open("sqlite", "file:parity?mode=memory&cache=shared")
	require.NoError(t, err)
	sqliteDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqliteDB.Close() })
	sm, err := NewSQLiteMigrator(sqliteDB)
	require.NoError(t, err)
	require.NoError(t, sm.Up(ctx))
	sqliteSchema := introspectSQLite(t, sqliteDB)

	// Postgres + MySQL (Docker-gated; dbtest skips the test when unavailable).
	pool := dbtest.RunTestDatabase(t)
	pm, err := NewPostgresMigrator(pool)
	require.NoError(t, err)
	require.NoError(t, pm.Up(ctx))
	pgSchema := introspectPostgres(t, pool)

	mysqlDB := dbtest.RunTestMySQL(t) // already migrated
	mysqlSchema := introspectMySQL(t, mysqlDB)

	require.Equal(t, normalize(pgSchema), normalize(sqliteSchema), "postgres vs sqlite logical schema")
	require.Equal(t, normalize(pgSchema), normalize(mysqlSchema), "postgres vs mysql logical schema")
}

// normalize forces PK columns to NOT NULL across all dialects (SQLite's INTEGER
// PRIMARY KEY rowid is implicitly nullable in table_info), removing the one
// legitimate cross-dialect nullability quirk before comparison.
func normalize(s logicalSchema) logicalSchema {
	for _, cols := range s {
		for name, f := range cols {
			if f.PrimaryKey {
				f.Nullable = false
				cols[name] = f
			}
		}
	}
	return s
}

func introspectPostgres(t *testing.T, pool *pgxpool.Pool) logicalSchema {
	t.Helper()
	ctx := context.Background()
	sc := logicalSchema{}
	rows, err := pool.Query(ctx, `
		SELECT table_name, column_name, (is_nullable = 'YES')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	for rows.Next() {
		var tbl, col string
		var nullable bool
		require.NoError(t, rows.Scan(&tbl, &col, &nullable))
		if sc[tbl] == nil {
			sc[tbl] = map[string]colFacts{}
		}
		sc[tbl][col] = colFacts{Nullable: nullable}
	}
	rows.Close()
	pkRows, err := pool.Query(ctx, `
		SELECT tc.table_name, kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = 'public' AND tc.table_name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	for pkRows.Next() {
		var tbl, col string
		require.NoError(t, pkRows.Scan(&tbl, &col))
		f := sc[tbl][col]
		f.PrimaryKey = true
		sc[tbl][col] = f
	}
	pkRows.Close()
	return sc
}

func introspectMySQL(t *testing.T, db *sql.DB) logicalSchema {
	t.Helper()
	sc := logicalSchema{}
	rows, err := db.Query(`
		SELECT table_name, column_name, (is_nullable = 'YES'), (column_key = 'PRI')
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var tbl, col string
		var nullable, pk bool
		require.NoError(t, rows.Scan(&tbl, &col, &nullable, &pk))
		if sc[tbl] == nil {
			sc[tbl] = map[string]colFacts{}
		}
		sc[tbl][col] = colFacts{Nullable: nullable, PrimaryKey: pk}
	}
	return sc
}

func introspectSQLite(t *testing.T, db *sql.DB) logicalSchema {
	t.Helper()
	sc := logicalSchema{}
	tblRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	var tables []string
	for tblRows.Next() {
		var name string
		require.NoError(t, tblRows.Scan(&name))
		tables = append(tables, name)
	}
	tblRows.Close()
	sort.Strings(tables)
	for _, tbl := range tables {
		info, err := db.Query(`SELECT name, "notnull", pk FROM pragma_table_info(?)`, tbl)
		require.NoError(t, err)
		sc[tbl] = map[string]colFacts{}
		for info.Next() {
			var name string
			var notnull, pk int
			require.NoError(t, info.Scan(&name, &notnull, &pk))
			sc[tbl][name] = colFacts{Nullable: notnull == 0, PrimaryKey: pk > 0}
		}
		info.Close()
	}
	return sc
}
```

- [ ] **Step 2: Run to verify it fails first (compile) then passes**

Run: `go test ./internal/persistence/store/ -run TestMigrationParity 2>&1 | tail -20`
Expected on first write: PASS if the schemas genuinely converge (they should). To *prove the guardrail bites*, temporarily add a stray column to one dialect's `0001` migration, re-run, confirm a readable FAIL, then revert. Record in the commit message that the drift-detection was verified.

> If the assertion fails on the honest schemas, the divergence is a real finding — capture the exact table/column diff, STOP, and report it (do not "fix" by loosening the comparison). Legitimate physical-type differences are already excluded; a logical mismatch means the migration sets actually diverged and must be reconciled (a schema-fix commit + note in the plan).

- [ ] **Step 3: Commit**

```bash
git add internal/persistence/store/migration_parity_test.go
git commit -m "test(store): cross-dialect logical schema-parity guardrail"
```

---

## Task 5: Production-safety docs + `WarnUnsafeConfig` + ADR-0086

**Files:**
- Create: `persistence/unsafe_config.go`
- Create: `persistence/unsafe_config_test.go`
- Create: `docs/production-checklist.md`
- Create: `docs/adr/0086-opt-in-unsafe-config-warning.md`
- Modify: `docs/retention.md`, `README.md`

**Interfaces:**
- Produces: `type DeploymentProfile struct { MultiReplica, CallLinksEnabled, CallLinkLeaseWired, HistoryCapSet, PruningScheduled bool }`; `func WarnUnsafeConfig(logger *slog.Logger, p DeploymentProfile)`; exported message constants `WarnMsgCallLinkLease`, `WarnMsgHistoryCap`, `WarnMsgPruning`.

- [ ] **Step 1: Write the failing table test**

Create `persistence/unsafe_config_test.go` (package `persistence_test`), using the project `table-test` closure form:

```go
package persistence_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/persistence"
)

func TestWarnUnsafeConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		profile persistence.DeploymentProfile
		assert  func(t *testing.T, logged string)
	}{
		"fully safe profile warns nothing": {
			profile: persistence.DeploymentProfile{
				HistoryCapSet: true, PruningScheduled: true,
			},
			assert: func(t *testing.T, logged string) {
				assert.Empty(t, strings.TrimSpace(logged), "no warnings for a safe profile")
			},
		},
		"multi-replica call-links without lease warns": {
			profile: persistence.DeploymentProfile{
				MultiReplica: true, CallLinksEnabled: true, CallLinkLeaseWired: false,
				HistoryCapSet: true, PruningScheduled: true,
			},
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, logged, persistence.WarnMsgCallLinkLease)
			},
		},
		"lease wired suppresses the call-link warning": {
			profile: persistence.DeploymentProfile{
				MultiReplica: true, CallLinksEnabled: true, CallLinkLeaseWired: true,
				HistoryCapSet: true, PruningScheduled: true,
			},
			assert: func(t *testing.T, logged string) {
				assert.NotContains(t, logged, persistence.WarnMsgCallLinkLease)
			},
		},
		"missing history cap and pruning both warn": {
			profile: persistence.DeploymentProfile{},
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, logged, persistence.WarnMsgHistoryCap)
				assert.Contains(t, logged, persistence.WarnMsgPruning)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			persistence.WarnUnsafeConfig(logger, tc.profile)
			tc.assert(t, buf.String())
		})
	}
}

func TestWarnUnsafeConfig_NilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		persistence.WarnUnsafeConfig(nil, persistence.DeploymentProfile{})
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./persistence/ -run TestWarnUnsafeConfig 2>&1 | tail -15`
Expected: FAIL — `undefined: persistence.DeploymentProfile` / `WarnUnsafeConfig`.

- [ ] **Step 3: Implement `persistence/unsafe_config.go`**

```go
package persistence

import (
	"context"
	"log/slog"
)

// Exported warning messages so consumers (and tests) can match on them.
const (
	WarnMsgCallLinkLease = "multi-replica deployment has call-links enabled without a call-link lease/ownership wired; child notifications may be delivered more than once"
	WarnMsgHistoryCap    = "WithHistoryCap is not set; instance snapshot history can grow unbounded"
	WarnMsgPruning       = "no pruning/retention job configured; outbox, call-link, chain-link, dedup, and timer tables can grow unbounded"
)

// DeploymentProfile is the consumer's own assertion of how they run wrkflw. It
// is NOT introspected from the live system — the library cannot know deployment
// topology, so the consumer declares it. See docs/production-checklist.md.
type DeploymentProfile struct {
	MultiReplica       bool // more than one engine replica runs concurrently
	CallLinksEnabled   bool // call-activity / sub-process wiring is in use
	CallLinkLeaseWired bool // a call-link lease/ownership is configured
	HistoryCapSet      bool // WithHistoryCap has been applied to the store
	PruningScheduled   bool // a retention/pruning job is running (see docs/retention.md)
}

// WarnUnsafeConfig emits one slog.Warn per known-risky combination in p. It is a
// no-op for a safe profile, never returns an error, and never panics on a nil
// logger (it falls back to slog.Default()). Call it once at consumer startup to
// get a production-readiness reminder. It does not inspect the live system.
func WarnUnsafeConfig(logger *slog.Logger, p DeploymentProfile) {
	if logger == nil {
		logger = slog.Default()
	}
	ctx := context.Background()
	if p.MultiReplica && p.CallLinksEnabled && !p.CallLinkLeaseWired {
		logger.WarnContext(ctx, WarnMsgCallLinkLease)
	}
	if !p.HistoryCapSet {
		logger.WarnContext(ctx, WarnMsgHistoryCap)
	}
	if !p.PruningScheduled {
		logger.WarnContext(ctx, WarnMsgPruning)
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./persistence/ -run TestWarnUnsafeConfig -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Write `docs/production-checklist.md`**

Sections with concrete guidance (no placeholders):
- **Connection pool** — pgx `pool_max_conns` / `database/sql` `SetMaxOpenConns`+`SetMaxIdleConns`+`SetConnMaxLifetime`; size relative to relay + scheduler + request concurrency; SQLite MUST use `SetMaxOpenConns(1)`.
- **Statement timeout & isolation** — set a server/session `statement_timeout` (PG) / `max_execution_time` (MySQL); the store relies on `READ COMMITTED` + optimistic CAS, so do not raise isolation (unnecessary) or lower it (unsafe).
- **Production MUST-DOs (opt-in-but-unsafe-if-forgotten)** — (1) multi-replica exactly-once child notification requires the call-link lease/ownership wiring; (2) `WithHistoryCap` to bound snapshot growth; (3) a consumer-owned pruning cron (link `docs/retention.md`). Each states the concrete failure mode if skipped. Mention `persistence.WarnUnsafeConfig` as the code-level reminder.

- [ ] **Step 6: Cross-link + ADR**

Add a one-line pointer to `docs/production-checklist.md` from the top of `docs/retention.md` and from the README's operational/persistence section. Create `docs/adr/0086-opt-in-unsafe-config-warning.md` (Nygard): Context (opt-in unsafe items, library can't know topology); Decision (docs + opt-in `WarnUnsafeConfig(logger, DeploymentProfile)`, consumer-invoked); Consequences (no false-positive noise; consumer must opt in; rejected: automatic constructor-time warnings — explain why).

- [ ] **Step 7: Verify + commit**

Run: `go test ./persistence/... 2>&1 | tail; golangci-lint run ./persistence/... 2>&1 | tail`
Expected: clean.

```bash
git add persistence/unsafe_config.go persistence/unsafe_config_test.go docs/production-checklist.md \
  docs/adr/0086-opt-in-unsafe-config-warning.md docs/retention.md README.md
git commit -m "feat(persistence): opt-in WarnUnsafeConfig + production checklist [ADR-0086]"
```

---

## Task 6: `PruneTimers` on the public `Pruner` interface

**Files:**
- Modify: `persistence/pruner.go`
- Modify: `persistence/pruner_test.go`

**Interfaces:**
- Consumes: `store.Pruner.PruneTimers(ctx, cutoff) (int64, error)` (already implemented, pruner.go:182).
- Produces: `PruneTimers(ctx context.Context, cutoff time.Time) (int64, error)` on `persistence.Pruner`.

- [ ] **Step 1: Write the failing interface test**

Add to `persistence/pruner_test.go` a test that calls `PruneTimers` **through the `persistence.Pruner` interface** (not the concrete type), backed by a raw sqlite store or the existing pruner test harness in that file. Seed two timer rows (one before, one after a cutoff), assert the return counts the pre-cutoff row and the post-cutoff row survives:

```go
func TestPruner_PruneTimers_ThroughInterface(t *testing.T) {
	t.Parallel()
	// Reuse whatever store/pruner setup the other pruner_test.go cases use to
	// obtain a persistence.Pruner over a migrated sqlite DB, plus a way to insert
	// wrkflw_timers rows (fire_at before/after cutoff). Follow the existing
	// PruneOutbox test's arrangement in this file.
	var p persistence.Pruner = newTestPruner(t) // interface-typed on purpose
	// insert one timer with fire_at = cutoff-1h and one with fire_at = cutoff+1h ...
	n, err := p.PruneTimers(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the pre-cutoff timer is pruned")
}
```

> Implementer: mirror the exact seeding/harness helper already used by the sibling `PruneOutbox`/`PruneCallLinks` tests in `persistence/pruner_test.go` (read the file first). If those tests use a testcontainers Postgres pool via `dbtest.RunTestDatabase`, follow that; if sqlite, follow that. The point is the call goes through the `persistence.Pruner` interface value.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./persistence/ -run TestPruner_PruneTimers_ThroughInterface 2>&1 | tail -15`
Expected: FAIL — `p.PruneTimers undefined (type persistence.Pruner has no field or method PruneTimers)`.

- [ ] **Step 3: Add the method to the interface**

In `persistence/pruner.go`, add to the `Pruner` interface (after `PruneProcessedMessages`):

```go
	// PruneTimers deletes timer rows whose fire_at is strictly before cutoff.
	// Fired/expired timers accumulate in wrkflw_timers; a retention job uses
	// this to drop them. Choose a cutoff safely past any window in which a timer
	// could still fire or be rescheduled. Returns the number of rows deleted.
	PruneTimers(ctx context.Context, cutoff time.Time) (int64, error)
```

The `var _ Pruner = (*store.Pruner)(nil)` compile-time check keeps compiling because `store.Pruner` already has the method.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./persistence/ -run TestPruner_PruneTimers_ThroughInterface -v 2>&1 | tail -15`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add persistence/pruner.go persistence/pruner_test.go
git commit -m "feat(persistence): expose PruneTimers on the public Pruner interface"
```

---

## Final verification (after all tasks)

- [ ] `go build ./...` clean.
- [ ] `go test -race ./... -count=1` — all packages pass (Docker up for testcontainers).
- [ ] `go test -race -coverprofile=cover.out ./persistence/... ./internal/persistence/store/... && go tool cover -func=cover.out | tail -1` — touched packages ≥85%.
- [ ] `golangci-lint run ./...` clean.
- [ ] `engine/` + `model/` are byte-for-byte unchanged: `git diff --stat main -- engine model` is empty.
- [ ] Update `docs/plans/HANDOVER.md` resume block + the memory index (next free ADR is now 0087).
- [ ] `superpowers:requesting-code-review` whole-branch review on opus; resolve findings; then `superpowers:finishing-a-development-branch` (merge to `main` + push, on maintainer confirmation).

## Self-Review (author)

**Spec coverage:** Sub-project 1 → Tasks 1–3 (+ ADR-0085). Sub-project 2 → Task 4. Sub-project 3 → Task 5 (+ ADR-0086). Sub-project 4 → Task 6. All four covered.

**Placeholder scan:** No TBD/TODO. Task 6 Step 1 intentionally defers to the sibling test harness (the file must be read first) but states the exact assertion and interface-typing requirement; the implementer has concrete guidance, not a blank.

**Type consistency:** `Migrator` methods identical across Task 1 (internal, `*store.Migrator`), Task 2 (facade interface), and Task 3 (CLI consumer). `StatusRow` (internal) → `MigrationStatus` (public) mapping is explicit. `DeploymentProfile` fields and `WarnMsg*` constants match between test (Task 5 Step 1) and impl (Step 3). Migration heads (PG 9 / MySQL 2 / SQLite 1) consistent across Tasks 1, 2, 4.
