package database

import "context"

// Querier runs SQL without revealing the driver or whether the underlying handle
// is a pool (non-transactional) or an in-flight transaction.
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) Row
}

// Result is the neutral outcome of an Exec.
type Result interface{ RowsAffected() (int64, error) }

// Rows is the neutral iterator over a Query result set.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Row is the neutral single-row result of a QueryRow.
type Row interface{ Scan(dest ...any) error }
