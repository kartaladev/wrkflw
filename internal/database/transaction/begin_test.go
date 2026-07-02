package transaction_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestBeginCommitPersists(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE tb (id int)`)
	require.NoError(t, err)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO tb VALUES (1)`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var n int
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM tb`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestBeginMarkRollbackRollsBack(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE tr (id int)`)
	require.NoError(t, err)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO tr VALUES (1)`)
	require.NoError(t, err)
	transaction.MarkRollback(ctx)
	require.NoError(t, tx.Commit(ctx)) // honors mark -> rolls back

	var n int
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM tr`).Scan(&n))
	assert.Equal(t, 0, n, "rolled back")
}

// TestBeginRejectsUnsupportedConn covers the error path in Begin (83.3% → 100%).
func TestBeginRejectsUnsupportedConn(t *testing.T) {
	_, _, err := transaction.Begin(t.Context(), "not-a-conn")
	require.Error(t, err, "Begin must error on unsupported conn type")
}

// TestOwnerQuerierQueryAndQueryRow exercises ownerQuerier.Query and QueryRow,
// which were at 0% coverage.
func TestOwnerQuerierQueryAndQueryRow(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE oqr (id int, val text)`)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `INSERT INTO oqr VALUES (1,'one'),(2,'two')`)
	require.NoError(t, err)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// QueryRow via ownerQuerier
	var val string
	require.NoError(t, tx.QueryRow(ctx, `SELECT val FROM oqr WHERE id=$1`, 1).Scan(&val))
	assert.Equal(t, "one", val)

	// Query via ownerQuerier — iterate both rows
	rows, err := tx.Query(ctx, `SELECT id, val FROM oqr ORDER BY id`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()

	type row struct {
		id  int
		val string
	}
	var got []row
	for rows.Next() {
		var r row
		require.NoError(t, rows.Scan(&r.id, &r.val))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 2)
	assert.Equal(t, row{1, "one"}, got[0])
	assert.Equal(t, row{2, "two"}, got[1])
}

// TestOwnerQuerierRollback directly exercises ownerQuerier.Rollback (0% coverage).
func TestOwnerQuerierRollback(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE orb (id int)`)
	require.NoError(t, err)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `INSERT INTO orb VALUES (1)`)
	require.NoError(t, err)

	// Explicitly call Rollback (not Commit) on the ownerQuerier.
	require.NoError(t, tx.Rollback(ctx))

	var n int
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM orb`).Scan(&n))
	assert.Equal(t, 0, n, "rolled back: row must not persist")
}
