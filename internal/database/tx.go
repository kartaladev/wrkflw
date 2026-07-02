package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tx is a Querier that can additionally be committed or rolled back. It is the
// raw driver transaction wrapped as a Querier, with no ambient/rollback-mark
// semantics (that layer lives in the transaction package).
type Tx interface {
	Querier
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// From adapts a raw driver handle to a Querier. Supported: *pgxpool.Pool, pgx.Tx,
// *sql.DB, *sql.Tx, *sql.Conn. Any other type yields ErrUnsupportedConn.
func From(conn any) (Querier, error) {
	switch c := conn.(type) {
	case *pgxpool.Pool:
		return pgxQuerier{c}, nil
	case pgx.Tx:
		return pgxQuerier{c}, nil
	case *sql.DB:
		return sqlQuerier{c}, nil
	case *sql.Tx:
		return sqlQuerier{c}, nil
	case *sql.Conn:
		return sqlQuerier{c}, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedConn, conn)
	}
}

// BeginTx starts a transaction on conn and returns it as a Tx. Supported conns
// are the pool/db types (*pgxpool.Pool, *sql.DB). Any other type yields
// ErrUnsupportedConn.
func BeginTx(ctx context.Context, conn any) (Tx, error) {
	switch c := conn.(type) {
	case *pgxpool.Pool:
		tx, err := c.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("workflow-database: begin pgx tx: %w", err)
		}
		return pgxTx{pgxQuerier{tx}, tx}, nil
	case *sql.DB:
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("workflow-database: begin sql tx: %w", err)
		}
		return sqlTx{sqlQuerier{tx}, tx}, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedConn, conn)
	}
}
