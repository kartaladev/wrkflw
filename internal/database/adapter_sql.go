package database

import (
	"context"
	"database/sql"
)

// sqlTx wraps a *sql.Tx as a database.Tx, delegating query methods to the
// embedded sqlQuerier and providing Commit/Rollback via the underlying tx.
// Note: database/sql's Commit and Rollback do not accept a context; ctx is
// accepted for interface conformance but is ignored.
type sqlTx struct {
	sqlQuerier
	tx *sql.Tx
}

func (t sqlTx) Commit(_ context.Context) error   { return t.tx.Commit() }
func (t sqlTx) Rollback(_ context.Context) error { return t.tx.Rollback() }

// sqlDBTX is the minimal database/sql querier satisfied by *sql.DB, *sql.Tx, *sql.Conn.
type sqlDBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlQuerier struct{ db sqlDBTX }

func (q sqlQuerier) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	res, err := q.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlResult{res}, nil
}

func (q sqlQuerier) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{rows}, nil
}

func (q sqlQuerier) QueryRow(ctx context.Context, query string, args ...any) Row {
	return sqlRow{q.db.QueryRowContext(ctx, query, args...)}
}

type sqlResult struct{ res sql.Result }

func (r sqlResult) RowsAffected() (int64, error) { return r.res.RowsAffected() }

type sqlRows struct{ rows *sql.Rows }

func (r sqlRows) Next() bool             { return r.rows.Next() }
func (r sqlRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r sqlRows) Err() error             { return r.rows.Err() }
func (r sqlRows) Close() error           { return r.rows.Close() }

type sqlRow struct{ row *sql.Row }

func (r sqlRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

// Compile-time assertions: all three driver types satisfy sqlDBTX.
var (
	_ sqlDBTX = (*sql.DB)(nil)
	_ sqlDBTX = (*sql.Tx)(nil)
	_ sqlDBTX = (*sql.Conn)(nil)
)
