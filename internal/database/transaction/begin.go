package transaction

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

// Begin starts a transaction on conn, stashes it into the returned context (so
// downstream JoinOrBegin calls join it), and returns the owner Querier.
func Begin(ctx context.Context, conn any) (Querier, context.Context, error) {
	tx, err := database.BeginTx(ctx, conn)
	if err != nil {
		return nil, ctx, err
	}
	h := &handle{tx: tx}
	ctx = context.WithValue(ctx, ctxKey{}, h)
	return &ownerQuerier{h: h}, ctx, nil
}

type ownerQuerier struct{ h *handle }

func (o *ownerQuerier) Exec(ctx context.Context, q string, a ...any) (database.Result, error) {
	return o.h.tx.Exec(ctx, q, a...)
}
func (o *ownerQuerier) Query(ctx context.Context, q string, a ...any) (database.Rows, error) {
	return o.h.tx.Query(ctx, q, a...)
}
func (o *ownerQuerier) QueryRow(ctx context.Context, q string, a ...any) database.Row {
	return o.h.tx.QueryRow(ctx, q, a...)
}
func (o *ownerQuerier) Commit(ctx context.Context) error {
	if o.h.rollbackOnly {
		return o.h.tx.Rollback(ctx)
	}
	return o.h.tx.Commit(ctx)
}
func (o *ownerQuerier) Rollback(ctx context.Context) error { return o.h.tx.Rollback(ctx) }
