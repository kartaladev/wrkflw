package store

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/postgres/*.sql
var postgresMigrationsFS embed.FS

// MigratePostgres applies all embedded goose migrations to the PostgreSQL
// database reachable through pool. It is idempotent: goose's internal version
// table (goose_db_version) tracks applied versions, so re-running is a safe
// no-op.
//
// MigratePostgres is intended to be called explicitly by the consumer during
// application startup, after the pool is ready. It is never auto-invoked on
// import.
//
// It uses the goose.Provider API (instance-scoped, not the package-level
// globals) so it is safe to call concurrently from parallel tests.
func MigratePostgres(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := NewPostgresMigrator(pool)
	if err != nil {
		return err
	}
	return m.Up(ctx)
}
