package database_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLQuerierRoundTrip(t *testing.T) {
	db := dbtest.RunTestMySQL(t) // testcontainers MySQL; *sql.DB with parseTime=true&loc=UTC
	q, err := database.From(db)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if _, err := q.Exec(t.Context(), `CREATE TEMPORARY TABLE t (id int, name varchar(16))`); err != nil {
		t.Fatalf("exec create: %v", err)
	}
	res, err := q.Exec(t.Context(), `INSERT INTO t VALUES (?,?)`, 1, "a")
	if err != nil {
		t.Fatalf("exec insert: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	var name string
	if err := q.QueryRow(t.Context(), `SELECT name FROM t WHERE id=?`, 1).Scan(&name); err != nil {
		t.Fatalf("queryrow: %v", err)
	}
	if name != "a" {
		t.Fatalf("name = %q, want a", name)
	}
}

// TestSQLQuerierQueryRows exercises the sqlQuerier.Query → sqlRows iteration path
// (Next/Scan/Err/Close).
func TestSQLQuerierQueryRows(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	q, err := database.From(db)
	require.NoError(t, err)

	_, err = q.Exec(t.Context(), `CREATE TABLE qrows_sql (id INT NOT NULL, name VARCHAR(32) NOT NULL)`)
	require.NoError(t, err)
	_, err = q.Exec(t.Context(), `INSERT INTO qrows_sql VALUES (?,?),(?,?)`, 1, "alpha", 2, "beta")
	require.NoError(t, err)

	rows, err := q.Query(t.Context(), `SELECT id, name FROM qrows_sql ORDER BY id`)
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

// TestSQLBatchResultsQuery exercises sqlBatchResults.Query, which was at 0% coverage.
func TestSQLBatchResultsQuery(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	q, err := database.From(db)
	require.NoError(t, err)

	_, err = q.Exec(t.Context(), `CREATE TABLE bq_sql (id INT NOT NULL)`)
	require.NoError(t, err)
	_, err = q.Exec(t.Context(), `INSERT INTO bq_sql VALUES (?),(?)`, 10, 20)
	require.NoError(t, err)

	b, ok := q.(database.Batcher)
	require.True(t, ok, "sql querier must implement Batcher")

	batch := database.NewBatch()
	batch.Queue(`SELECT id FROM bq_sql WHERE id = ?`, 10)
	batch.Queue(`SELECT id FROM bq_sql WHERE id = ?`, 20)

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

// TestSQLQuerierExecError exercises the sqlQuerier.Exec error path (returns nil Result on error).
func TestSQLQuerierExecError(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	q, err := database.From(db)
	require.NoError(t, err)

	// Query against a non-existent table causes an error.
	_, err = q.Exec(t.Context(), `INSERT INTO nonexistent_table_xyz VALUES (1)`)
	require.Error(t, err)
}
