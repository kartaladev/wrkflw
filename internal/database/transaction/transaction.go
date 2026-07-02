package transaction

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

// Control commits or rolls back a transaction. Commit honors a rollback-only mark.
type Control interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Querier is the transactional handle: a database.Querier you can also commit/roll back.
type Querier interface {
	database.Querier
	Control
}

type ctxKey struct{}

type handle struct {
	tx           database.Tx
	rollbackOnly bool
}

func fromCtx(ctx context.Context) *handle {
	h, _ := ctx.Value(ctxKey{}).(*handle)
	return h
}

// MarkRollback flags the ambient transaction in ctx rollback-only. No-op if none.
func MarkRollback(ctx context.Context) {
	if h := fromCtx(ctx); h != nil {
		h.rollbackOnly = true
	}
}

// IsRollbackMarked reports whether the ambient transaction is rollback-only.
func IsRollbackMarked(ctx context.Context) bool {
	h := fromCtx(ctx)
	return h != nil && h.rollbackOnly
}
