package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all embedded goose migrations to the PostgreSQL database
// reachable through pool. It is idempotent: goose's internal version table
// (goose_db_version) tracks applied versions, so re-running is a safe no-op.
//
// Migrate is intended to be called explicitly by the consumer during application
// startup, after the pool is ready. It is never auto-invoked on import.
//
// It uses the goose.Provider API (instance-scoped, not the package-level globals)
// so it is safe to call concurrently from parallel tests.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// stdlib.OpenDBFromPool wraps the existing pool in a *sql.DB shim.
	// Closing the shim does NOT close the underlying pool — it merely
	// unregisters the driver registration, which is safe to do here.
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	// Use the instance-scoped Provider rather than the deprecated package-level
	// SetBaseFS / SetDialect globals. The Provider holds its own state and is
	// safe to construct and run concurrently from multiple goroutines.
	//
	// migrationsFS embeds files under migrations/, so we sub into that
	// directory to give the Provider a root that contains the *.sql files
	// directly (as required by goose.NewProvider).
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("workflow-postgres: migrate: sub fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		return fmt.Errorf("workflow-postgres: migrate: new provider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("workflow-postgres: migrate: up: %w", err)
	}

	return nil
}
