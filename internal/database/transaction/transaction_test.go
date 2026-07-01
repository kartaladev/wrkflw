package transaction_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
)

func TestMarkRollbackNoAmbientIsNoop(t *testing.T) {
	// No transaction in ctx: MarkRollback is a no-op and IsRollbackMarked is false.
	ctx := t.Context()
	transaction.MarkRollback(ctx)
	if transaction.IsRollbackMarked(ctx) {
		t.Fatal("want false with no ambient tx")
	}
}
