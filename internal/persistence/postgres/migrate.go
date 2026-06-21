package postgres

import (
	"context"
	"embed"
	"fmt"

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
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// stdlib.OpenDBFromPool wraps the existing pool in a *sql.DB shim.
	// Closing the shim does NOT close the underlying pool — it merely
	// unregisters the driver registration, which is safe to do here.
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrationsFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("postgres: migrate: set dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("postgres: migrate: up: %w", err)
	}

	return nil
}
