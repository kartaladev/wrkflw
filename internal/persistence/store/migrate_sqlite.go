// Package store is the vendor-neutral persistence entry point for the wrkflw
// engine. It provides migration helpers for each supported backend that do not
// belong to any single backend's sub-package (postgres, mysql). Later tasks
// will extend this package with a shared [database.Querier]-based store
// implementation.
package store

import (
	"context"
	"database/sql"
	"embed"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrationsFS embed.FS

// MigrateSQLite applies all embedded goose migrations to the SQLite database
// reachable through db. It is idempotent: goose's internal version table
// (goose_db_version) tracks applied versions, so re-running is a safe no-op.
//
// MigrateSQLite is intended to be called explicitly by the consumer (or test
// helper) after the *sql.DB is opened and configured. It is never auto-invoked
// on import.
//
// It uses the goose.Provider API (instance-scoped, not the deprecated
// package-level globals) so it is safe to call concurrently from parallel tests.
func MigrateSQLite(ctx context.Context, db *sql.DB) error {
	m, err := NewSQLiteMigrator(db)
	if err != nil {
		return err
	}
	return m.Up(ctx)
}
