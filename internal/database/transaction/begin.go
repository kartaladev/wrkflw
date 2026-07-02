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

// JoinOrBegin joins the ambient transaction in ctx if present; otherwise begins a
// fresh one (a leaf owned by the caller — not re-propagated for deeper joins; start
// the outermost scope with Begin to compose nested joins). When joined, the returned
// Querier's Commit is a no-op and Rollback marks the whole unit rollback-only.
func JoinOrBegin(ctx context.Context, conn any) (Querier, error) {
	if h := fromCtx(ctx); h != nil {
		return &joinedQuerier{h: h}, nil
	}
	q, _, err := Begin(ctx, conn)
	return q, err
}

type joinedQuerier struct{ h *handle }

func (j *joinedQuerier) Exec(ctx context.Context, q string, a ...any) (database.Result, error) {
	return j.h.tx.Exec(ctx, q, a...)
}
func (j *joinedQuerier) Query(ctx context.Context, q string, a ...any) (database.Rows, error) {
	return j.h.tx.Query(ctx, q, a...)
}
func (j *joinedQuerier) QueryRow(ctx context.Context, q string, a ...any) database.Row {
	return j.h.tx.QueryRow(ctx, q, a...)
}
func (j *joinedQuerier) Commit(_ context.Context) error { return nil } // owner controls
func (j *joinedQuerier) Rollback(_ context.Context) error {
	j.h.rollbackOnly = true
	return nil
}
