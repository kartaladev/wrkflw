package database_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPgxQuerierRoundTrip(t *testing.T) {
	pool := dbtest.RunTestDatabase(t) // testcontainers PG; returns *pgxpool.Pool
	q, err := database.From(pool)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if _, err := q.Exec(t.Context(), `CREATE TEMP TABLE t (id int, name text)`); err != nil {
		t.Fatalf("exec create: %v", err)
	}
	res, err := q.Exec(t.Context(), `INSERT INTO t VALUES ($1,$2)`, 1, "a")
	if err != nil {
		t.Fatalf("exec insert: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	var name string
	if err := q.QueryRow(t.Context(), `SELECT name FROM t WHERE id=$1`, 1).Scan(&name); err != nil {
		t.Fatalf("queryrow: %v", err)
	}
	if name != "a" {
		t.Fatalf("name = %q, want a", name)
	}
}

// TestPgxQuerierQueryRows exercises the pgxQuerier.Query → pgxRows iteration
// path (Next/Scan/Err/Close) which was previously at 0% coverage.
func TestPgxQuerierQueryRows(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	q, err := database.From(pool)
	require.NoError(t, err)

	_, err = q.Exec(t.Context(), `CREATE TEMP TABLE qrows (id int, name text)`)
	require.NoError(t, err)
	_, err = q.Exec(t.Context(), `INSERT INTO qrows VALUES ($1,$2),($3,$4)`, 1, "alpha", 2, "beta")
	require.NoError(t, err)

	rows, err := q.Query(t.Context(), `SELECT id, name FROM qrows ORDER BY id`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()

	type row struct {
		id   int
		name string
	}
	var got []row
	for rows.Next() {
		var r row
		require.NoError(t, rows.Scan(&r.id, &r.name))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 2)
	assert.Equal(t, row{1, "alpha"}, got[0])
	assert.Equal(t, row{2, "beta"}, got[1])
}

// TestPgxBatchResultsQuery exercises pgxBatchResults.Query, which was at 0% coverage.
func TestPgxBatchResultsQuery(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	q, err := database.From(pool)
	require.NoError(t, err)

	_, err = q.Exec(t.Context(), `CREATE TEMP TABLE bq (id int)`)
	require.NoError(t, err)
	_, err = q.Exec(t.Context(), `INSERT INTO bq VALUES (10),(20)`)
	require.NoError(t, err)

	b, ok := q.(database.Batcher)
	require.True(t, ok, "pgx querier must implement Batcher")

	batch := database.NewBatch()
	batch.Queue(`SELECT id FROM bq WHERE id = $1`, 10)
	batch.Queue(`SELECT id FROM bq WHERE id = $1`, 20)

	br := b.SendBatch(t.Context(), batch)
	defer func() { require.NoError(t, br.Close()) }()

	for _, want := range []int{10, 20} {
		rows, qErr := br.Query()
		require.NoError(t, qErr)
		require.True(t, rows.Next())
		var id int
		require.NoError(t, rows.Scan(&id))
		assert.Equal(t, want, id)
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
}
