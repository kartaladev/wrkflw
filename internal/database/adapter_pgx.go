package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxTx wraps a pgx.Tx as a database.Tx, delegating query methods to the
// embedded pgxQuerier and providing Commit/Rollback via the underlying tx.
type pgxTx struct {
	pgxQuerier
	tx pgx.Tx
}

func (t pgxTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgxTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

// pgxDBTX is the minimal pgx querier satisfied by *pgxpool.Pool and pgx.Tx.
// Both types also provide SendBatch, so it is included here for clarity instead
// of being asserted inline inside SendBatch.
type pgxDBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

type pgxQuerier struct{ db pgxDBTX }

func (q pgxQuerier) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	ct, err := q.db.Exec(ctx, sql, args...)
	return pgxResult{ct}, err
}

func (q pgxQuerier) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := q.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgxRows{rows}, nil
}

func (q pgxQuerier) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return pgxRow{q.db.QueryRow(ctx, sql, args...)}
}

type pgxResult struct{ ct pgconn.CommandTag }

func (r pgxResult) RowsAffected() (int64, error) { return r.ct.RowsAffected(), nil }

type pgxRows struct{ rows pgx.Rows }

func (r pgxRows) Next() bool             { return r.rows.Next() }
func (r pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r pgxRows) Err() error             { return r.rows.Err() }
func (r pgxRows) Close() error           { r.rows.Close(); return nil } // pgx Close is void

type pgxRow struct{ row pgx.Row }

func (r pgxRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

// SendBatch implements [Batcher] by forwarding to the underlying pgx native batch
// pipeline. It panics if b is not the concrete *batch type returned by [NewBatch].
func (q pgxQuerier) SendBatch(ctx context.Context, b Batch) BatchResults {
	pb := &pgx.Batch{}
	for _, it := range b.(*batch).items {
		pb.Queue(it.query, it.args...)
	}
	return pgxBatchResults{q.db.SendBatch(ctx, pb)}
}

type pgxBatchResults struct{ br pgx.BatchResults }

func (r pgxBatchResults) Exec() (Result, error) {
	ct, err := r.br.Exec()
	return pgxResult{ct}, err
}

func (r pgxBatchResults) Query() (Rows, error) {
	rows, err := r.br.Query()
	if err != nil {
		return nil, err
	}
	return pgxRows{rows}, nil
}

func (r pgxBatchResults) Close() error { return r.br.Close() }

// compile-time checks that the concrete pgx types satisfy the internal interface.
var (
	_ pgxDBTX = (*pgxpool.Pool)(nil)
	_ pgxDBTX = (pgx.Tx)(nil)
)
