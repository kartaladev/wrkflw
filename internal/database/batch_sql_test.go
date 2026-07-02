package database_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

// TestSQLBatcherEmulates verifies that a Querier backed by *sql.DB implements
// [database.Batcher] and emulates batching by executing queued statements
// sequentially — identical observable results, no round-trip savings.
func TestSQLBatcherEmulates(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	q, err := database.From(db)
	require.NoError(t, err)

	// Use a non-temporary table so both inserts and the count query run in
	// the same connection pool without temp-table scope issues.
	_, err = q.Exec(t.Context(), `CREATE TABLE IF NOT EXISTS batcher_emulate_t (id INT NOT NULL)`)
	require.NoError(t, err, "create table")

	b, ok := q.(database.Batcher)
	require.True(t, ok, "sqlQuerier should implement database.Batcher")

	batch := database.NewBatch()
	batch.Queue(`INSERT INTO batcher_emulate_t VALUES (?)`, 1)
	batch.Queue(`INSERT INTO batcher_emulate_t VALUES (?)`, 2)

	br := b.SendBatch(t.Context(), batch)
	defer func() { _ = br.Close() }()

	for i := range 2 {
		_, execErr := br.Exec()
		require.NoError(t, execErr, "batch exec item %d", i)
	}

	var n int
	require.NoError(t, q.QueryRow(t.Context(), `SELECT count(*) FROM batcher_emulate_t`).Scan(&n))
	require.Equal(t, 2, n, "expected 2 rows after batch insert")
}
