package transaction_test

import (
	"context"
	"testing"

	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRollbackOnly covers transaction.RollbackOnly (ADR-0134): it must read the
// ambient handle's rollback-only flag, defaulting to false with no ambient
// handle, and observe the mark a joined participant's Rollback leaves behind.
// Driven against SQLite (no Docker daemon required) since RollbackOnly's logic
// is dialect-independent.
func TestRollbackOnly(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		ctx    func(t *testing.T) context.Context
		assert func(t *testing.T, got bool)
	}

	cases := []testCase{
		{
			name: "no ambient handle returns false",
			ctx: func(t *testing.T) context.Context {
				return t.Context()
			},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "no ambient tx: RollbackOnly must be false")
			},
		},
		{
			name: "ambient handle not marked returns false",
			ctx: func(t *testing.T) context.Context {
				db := dbtest.RunTestSQLite(t)
				tx, ctx, err := transaction.Begin(t.Context(), db)
				require.NoError(t, err)
				t.Cleanup(func() { _ = tx.Rollback(ctx) })
				return ctx
			},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "unmarked ambient tx: RollbackOnly must be false")
			},
		},
		{
			name: "joined participant Rollback marks rollback-only",
			ctx: func(t *testing.T) context.Context {
				db := dbtest.RunTestSQLite(t)
				tx, ctx, err := transaction.Begin(t.Context(), db)
				require.NoError(t, err)
				t.Cleanup(func() { _ = tx.Rollback(ctx) })

				joined, err := transaction.JoinOrBegin(ctx, db)
				require.NoError(t, err)
				require.NoError(t, joined.Rollback(ctx))
				return ctx
			},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "joined Rollback: RollbackOnly must be true")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := tc.ctx(t)
			tc.assert(t, transaction.RollbackOnly(ctx))
		})
	}
}
