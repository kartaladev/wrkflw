package transaction_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
)

func TestMarkRollbackNoAmbientIsNoop(t *testing.T) {
	// No transaction in ctx: MarkRollback is a no-op and IsRollbackMarked is false.
	ctx := t.Context()
	transaction.MarkRollback(ctx)
	if transaction.IsRollbackMarked(ctx) {
		t.Fatal("want false with no ambient tx")
	}
}

// TestIsRollbackMarkedTruePath verifies that IsRollbackMarked returns true after
// MarkRollback is called on a context that holds an ambient transaction.
// This covers the true-return branch.
func TestIsRollbackMarkedTruePath(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	assert.False(t, transaction.IsRollbackMarked(ctx), "not yet marked")

	transaction.MarkRollback(ctx)

	assert.True(t, transaction.IsRollbackMarked(ctx), "must be marked after MarkRollback")

	// Roll back explicitly so the pool connection is released before cleanup.
	_ = tx.Rollback(ctx)
}
