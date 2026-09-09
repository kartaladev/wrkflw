package transaction_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJoinInnerCommitIsNoopOuterControls(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE tj (id int)`)
	require.NoError(t, err)

	outer, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)

	inner, err := transaction.JoinOrBegin(ctx, pool) // joins ambient
	require.NoError(t, err)
	_, err = inner.Exec(ctx, `INSERT INTO tj VALUES (1)`)
	require.NoError(t, err)
	require.NoError(t, inner.Commit(ctx)) // no-op; must NOT commit the real tx

	var n int
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n))
	assert.Equal(t, 0, n, "row must not be visible before outer commit")

	require.NoError(t, outer.Commit(ctx)) // real commit
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n))
	assert.Equal(t, 1, n, "count after outer commit")
}

func TestJoinInnerRollbackMarksWholeUnit(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE tjr (id int)`)
	require.NoError(t, err)

	outer, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)
	inner, err := transaction.JoinOrBegin(ctx, pool)
	require.NoError(t, err)
	_, err = inner.Exec(ctx, `INSERT INTO tjr VALUES (1)`)
	require.NoError(t, err)
	require.NoError(t, inner.Rollback(ctx)) // marks rollback-only; does not touch the real tx yet

	require.NoError(t, outer.Commit(ctx)) // honors mark -> rolls back
	var n int
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM tjr`).Scan(&n))
	assert.Equal(t, 0, n)
}

// TestJoinOrBeginFallsBackToBegin verifies that JoinOrBegin starts a fresh
// transaction when no ambient transaction is in ctx (covers the else-branch).
func TestJoinOrBeginFallsBackToBegin(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE jfb (id int)`)
	require.NoError(t, err)

	// No prior Begin — JoinOrBegin must start its own leaf transaction.
	q, err := transaction.JoinOrBegin(t.Context(), pool)
	require.NoError(t, err, "JoinOrBegin with no ambient tx must succeed")

	_, err = q.Exec(t.Context(), `INSERT INTO jfb VALUES (1)`)
	require.NoError(t, err)

	// Commit the leaf owner.
	require.NoError(t, q.Commit(t.Context()))

	var n int
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM jfb`).Scan(&n))
	assert.Equal(t, 1, n, "leaf-owner commit must persist the row")
}

// TestJoinedQuerierQueryAndQueryRow exercises joinedQuerier.Query and QueryRow,
// which were at 0% coverage.
func TestJoinedQuerierQueryAndQueryRow(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE jqr (id int, val text)`)
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

// TestJoinReportsAmbientPresence pins Join's contract: it returns the ambient
// transaction when one exists and reports absence otherwise, and — unlike
// JoinOrBegin — it never begins one.
//
// The distinction is what read paths depend on. A read must see writes made
// earlier in the same unit, but wrapping every uncontextualised read in a fresh
// transaction to achieve that would be a real cost on a hot path.
func TestJoinReportsAmbientPresence(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	require.NoError(t, err)
	_, err = base.Exec(t.Context(), `CREATE TABLE tjoin (id int)`)
	require.NoError(t, err)

	// No ambient transaction: Join reports absence and hands back nothing.
	q, ok := transaction.Join(t.Context())
	assert.False(t, ok, "Join must report absence when ctx carries no transaction")
	assert.Nil(t, q, "Join must not fabricate a Querier when there is no transaction")

	// With an ambient transaction: Join returns a participant that can read the
	// unit's own uncommitted writes — the property read paths need.
	outer, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)
	_, err = outer.Exec(ctx, `INSERT INTO tjoin VALUES (7)`)
	require.NoError(t, err)

	joined, ok := transaction.Join(ctx)
	require.True(t, ok, "Join must find the ambient transaction")
	require.NotNil(t, joined)

	var n int
	require.NoError(t, joined.QueryRow(ctx, `SELECT count(*) FROM tjoin WHERE id = 7`).Scan(&n))
	assert.Equal(t, 1, n, "a joined reader must see the unit's own uncommitted write")

	// The pool, by contrast, must NOT see it — which is exactly why a read path
	// that skipped Join would miss a row the same unit had just written.
	require.NoError(t, base.QueryRow(t.Context(), `SELECT count(*) FROM tjoin WHERE id = 7`).Scan(&n))
	assert.Equal(t, 0, n, "the pool must not see the uncommitted write")

	require.NoError(t, outer.Rollback(ctx))
}
