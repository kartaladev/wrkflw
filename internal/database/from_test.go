package database_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromRejectsUnsupportedConn(t *testing.T) {
	_, err := database.From("not a conn")
	if !errors.Is(err, database.ErrUnsupportedConn) {
		t.Fatalf("want ErrUnsupportedConn, got %v", err)
	}
}

func TestBeginTxRejectsUnsupportedConn(t *testing.T) {
	_, err := database.BeginTx(t.Context(), 42)
	if !errors.Is(err, database.ErrUnsupportedConn) {
		t.Fatalf("want ErrUnsupportedConn, got %v", err)
	}
}

// TestFromPgxTx verifies that From accepts a raw pgx.Tx (obtained via pool.Begin)
// and returns a working Querier — covering the pgx.Tx branch in From.
// We obtain the raw pgx.Tx via BeginTx → the pgxTx.tx field; instead, we use
// pool.Begin directly (which the pgxpool.Pool.Begin method exposes as pgx.Tx).
func TestFromPgxTx(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	// pool.Begin returns a pgx.Tx — the exact type From's switch recognises.
	rawTx, err := pool.Begin(t.Context())
	require.NoError(t, err, "pool.Begin")
	defer func() { _ = rawTx.Rollback(t.Context()) }()

	q, err := database.From(rawTx)
	require.NoError(t, err, "From(pgx.Tx)")

	var n int
	require.NoError(t, q.QueryRow(t.Context(), `SELECT 1`).Scan(&n))
	assert.Equal(t, 1, n)
}

// TestFromSQLTx verifies that From accepts a *sql.Tx — covering that branch.
func TestFromSQLTx(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	sqlTx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err, "db.BeginTx")
	defer func() { _ = sqlTx.Rollback() }()

	q, err := database.From(sqlTx)
	require.NoError(t, err, "From(*sql.Tx)")

	var n int
	require.NoError(t, q.QueryRow(t.Context(), `SELECT 1`).Scan(&n))
	assert.Equal(t, 1, n)
}

// TestFromSQLConn verifies that From accepts a *sql.Conn — covering that branch.
func TestFromSQLConn(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	conn, err := db.Conn(t.Context())
	require.NoError(t, err, "db.Conn")
	defer func() { _ = conn.Close() }()

	q, err := database.From(conn)
	require.NoError(t, err, "From(*sql.Conn)")

	var n int
	require.NoError(t, q.QueryRow(t.Context(), `SELECT 1`).Scan(&n))
	assert.Equal(t, 1, n)
}
