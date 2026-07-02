package transaction_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestJoinInnerCommitIsNoopOuterControls(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tj (id int)`)

	outer, ctx, _ := transaction.Begin(t.Context(), pool)

	inner, err := transaction.JoinOrBegin(ctx, pool) // joins ambient
	if err != nil {
		t.Fatalf("joinorbegin: %v", err)
	}
	_, _ = inner.Exec(ctx, `INSERT INTO tj VALUES (1)`)
	_ = inner.Commit(ctx) // no-op; must NOT commit the real tx

	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n)
	if n != 0 {
		t.Fatalf("row visible before outer commit: %d", n)
	}
	_ = outer.Commit(ctx) // real commit
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n)
	if n != 1 {
		t.Fatalf("count after outer commit = %d, want 1", n)
	}
}

func TestJoinInnerRollbackMarksWholeUnit(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tjr (id int)`)

	outer, ctx, _ := transaction.Begin(t.Context(), pool)
	inner, _ := transaction.JoinOrBegin(ctx, pool)
	_, _ = inner.Exec(ctx, `INSERT INTO tjr VALUES (1)`)
	_ = inner.Rollback(ctx) // marks rollback-only; does not touch the real tx yet

	_ = outer.Commit(ctx) // honors mark -> rolls back
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tjr`).Scan(&n)
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

// TestJoinOrBeginFallsBackToBegin verifies that JoinOrBegin starts a fresh
// transaction when no ambient transaction is in ctx (covers the else-branch,
// previously at 50% coverage).
func TestJoinOrBeginFallsBackToBegin(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, err := base.Exec(t.Context(), `CREATE TABLE jfb (id int)`)
	require.NoError(t, err)

	// No prior Begin — JoinOrBegin must start its own leaf transaction.
	q, err := transaction.JoinOrBegin(t.Context(), pool)
	require.NoError(t, err, "JoinOrBegin with no ambient tx must succeed")

	_, err = q.Exec(t.Context(), `INSERT INTO jfb VALUES (1)`)
	require.NoError(t, err)

	// Commit the leaf owner.
	require.NoError(t, q.Commit(t.Context()))

	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM jfb`).Scan(&n)
	assert.Equal(t, 1, n, "leaf-owner commit must persist the row")
}

// TestJoinedQuerierQueryAndQueryRow exercises joinedQuerier.Query and QueryRow,
// which were at 0% coverage.
func TestJoinedQuerierQueryAndQueryRow(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, err := base.Exec(t.Context(), `CREATE TABLE jqr (id int, val text)`)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `INSERT INTO jqr VALUES (1,'one'),(2,'two')`)
	require.NoError(t, err)

	outer, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)
	defer func() { _ = outer.Rollback(ctx) }()

	inner, err := transaction.JoinOrBegin(ctx, pool)
	require.NoError(t, err)

	// QueryRow via joinedQuerier
	var val string
	require.NoError(t, inner.QueryRow(ctx, `SELECT val FROM jqr WHERE id=$1`, 1).Scan(&val))
	assert.Equal(t, "one", val)

	// Query via joinedQuerier — iterate both rows
	rows, err := inner.Query(ctx, `SELECT id, val FROM jqr ORDER BY id`)
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
