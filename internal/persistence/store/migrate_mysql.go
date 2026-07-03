package store

import (
	"context"
	"database/sql"
	"embed"
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
	m, err := NewMySQLMigrator(db)
	if err != nil {
		return err
	}
	return m.Up(ctx)
}
