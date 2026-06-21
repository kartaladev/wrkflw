package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the minimal querier seam satisfied by *pgxpool.Pool, *pgx.Conn, and
// pgx.Tx, so the same repo code runs against a pool or an in-flight transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Compile-time assertion: *pgxpool.Pool must satisfy DBTX so a future pgx
// upgrade that drops a method is caught immediately at build time.
var _ DBTX = (*pgxpool.Pool)(nil)
