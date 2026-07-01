package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxDBTX is the minimal pgx querier satisfied by *pgxpool.Pool and pgx.Tx.
type pgxDBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
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

// compile-time checks that the concrete pgx types satisfy the internal interface.
var (
	_ pgxDBTX = (*pgxpool.Pool)(nil)
	_ pgxDBTX = (pgx.Tx)(nil)
)
