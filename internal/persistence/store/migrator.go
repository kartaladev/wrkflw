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
	conn         any // *pgxpool.Pool or *sql.DB
	gooseDialect goose.Dialect
	fsys         fs.FS // sub-FS rooted at the dialect's *.sql files
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
// cleanup closes the database/sql shim (which does NOT close the underlying pool —
// it merely unregisters the driver registration); for a *sql.DB the cleanup is a
// no-op (the caller owns the DB).
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
