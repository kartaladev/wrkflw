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

// SendBatch emulates batching for database/sql drivers by executing each queued
// statement sequentially. The results are identical to a native batch — every
// queued statement is executed in order — but there are no round-trip savings.
func (q sqlQuerier) SendBatch(ctx context.Context, b Batch) BatchResults {
	return &sqlBatchResults{ctx: ctx, q: q, items: b.(*batch).items}
}

// sqlBatchResults steps through queued statements one by one, delegating to the
// underlying sqlQuerier. It satisfies [BatchResults].
type sqlBatchResults struct {
	ctx   context.Context
	q     sqlQuerier
	items []queued
	i     int
}

func (r *sqlBatchResults) Exec() (Result, error) {
	it := r.items[r.i]
	r.i++
	return r.q.Exec(r.ctx, it.query, it.args...)
}

func (r *sqlBatchResults) Query() (Rows, error) {
	it := r.items[r.i]
	r.i++
	return r.q.Query(r.ctx, it.query, it.args...)
}

// Close is a no-op for the sql emulation path — there is no server-side cursor
// or pipeline to release.
func (r *sqlBatchResults) Close() error { return nil }

// Compile-time assertion: sqlQuerier implements Batcher.
var _ Batcher = sqlQuerier{}

// Compile-time assertions: all three driver types satisfy sqlDBTX.
var (
	_ sqlDBTX = (*sql.DB)(nil)
	_ sqlDBTX = (*sql.Tx)(nil)
	_ sqlDBTX = (*sql.Conn)(nil)
)
