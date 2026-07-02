package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/mysql/*.sql
var mysqlMigrationsFS embed.FS

// MigrateMySQL applies all embedded goose migrations to the MySQL database
// reachable through db. It is idempotent: goose's internal version table
// (goose_db_version) tracks applied versions, so re-running is a safe no-op.
//
// MigrateMySQL is intended to be called explicitly by the consumer during
// application startup, after the DB connection is ready. It is never
// auto-invoked on import.
//
// It uses the goose.Provider API (instance-scoped, not the package-level
// globals) so it is safe to call concurrently from parallel tests.
func MigrateMySQL(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(mysqlMigrationsFS, "migrations/mysql")
	if err != nil {
		return fmt.Errorf("workflow-store: migrate mysql: sub fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectMySQL, db, sub)
	if err != nil {
		return fmt.Errorf("workflow-store: migrate mysql: new provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("workflow-store: migrate mysql: up: %w", err)
	}
	return nil
}
