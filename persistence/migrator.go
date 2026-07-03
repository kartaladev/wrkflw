package persistence

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
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

// ErrNilDependency is returned by the migrator constructors when the connection
// or pool passed to them is nil (typed-nil included). Use errors.Is(err,
// persistence.ErrNilDependency) to test for it — consumers cannot import the
// internal store package directly.
var ErrNilDependency = store.ErrNilDependency

// NewPostgresMigrator constructs a Migrator over a pgx pool. Returns a wrapped
// ErrNilDependency (matchable via errors.Is(err, persistence.ErrNilDependency))
// if pool is nil.
func NewPostgresMigrator(pool *pgxpool.Pool) (Migrator, error) {
	m, err := store.NewPostgresMigrator(pool)
	if err != nil {
		return nil, err
	}
	return &migrator{inner: m}, nil
}

// NewMySQLMigrator constructs a Migrator over a MySQL *sql.DB. Returns a wrapped
// ErrNilDependency (matchable via errors.Is(err, persistence.ErrNilDependency))
// if db is nil.
func NewMySQLMigrator(db *sql.DB) (Migrator, error) {
	m, err := store.NewMySQLMigrator(db)
	if err != nil {
		return nil, err
	}
	return &migrator{inner: m}, nil
}

// NewSQLiteMigrator constructs a Migrator over a SQLite *sql.DB. Returns a
// wrapped ErrNilDependency (matchable via errors.Is(err, persistence.ErrNilDependency))
// if db is nil.
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
