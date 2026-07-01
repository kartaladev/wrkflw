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
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tb (id int)`)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tb VALUES (1)`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tb`).Scan(&n)
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestBeginMarkRollbackRollsBack(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tr (id int)`)

	tx, ctx, _ := transaction.Begin(t.Context(), pool)
	_, _ = tx.Exec(ctx, `INSERT INTO tr VALUES (1)`)
	transaction.MarkRollback(ctx)
	if err := tx.Commit(ctx); err != nil { // honors mark -> rolls back
		t.Fatalf("commit: %v", err)
	}
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tr`).Scan(&n)
	if n != 0 {
		t.Fatalf("count = %d, want 0 (rolled back)", n)
	}
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
	base, _ := database.From(pool)
	_, err := base.Exec(t.Context(), `CREATE TABLE orb (id int)`)
	require.NoError(t, err)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `INSERT INTO orb VALUES (1)`)
	require.NoError(t, err)

	// Explicitly call Rollback (not Commit) on the ownerQuerier.
	require.NoError(t, tx.Rollback(ctx))

	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM orb`).Scan(&n)
	assert.Equal(t, 0, n, "rolled back: row must not persist")
}
