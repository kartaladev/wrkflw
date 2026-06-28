package mysql

import (
	"context"
	"database/sql"
	"fmt"
)

// DBTX is the minimal querier seam satisfied by *sql.DB, *sql.Tx, and *sql.Conn,
// so the same repository code runs against a plain pool or an in-flight
// transaction without any adaptation.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Compile-time assertions: the three standard database/sql types must satisfy DBTX.
var (
	_ DBTX = (*sql.DB)(nil)
	_ DBTX = (*sql.Tx)(nil)
	_ DBTX = (*sql.Conn)(nil)
)

// txWith begins a transaction on db, defers a rollback, calls fn with the
// transaction, and commits on success. If fn returns a non-nil error, the
// transaction is rolled back and that error is returned unwrapped.
//
// txWith is defined here for use by repository implementations in subsequent
// tasks; it is not called in this file.
//
//nolint:unused
func txWith(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("workflow-persistence-mysql: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("workflow-persistence-mysql: commit tx: %w", err)
	}
	return nil
}
