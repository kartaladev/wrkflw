package transaction

import (
	"context"

	"github.com/kartaladev/wrkflw/internal/database"
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

// Join returns a Querier over the ambient transaction stashed in ctx, reporting
// false when ctx carries none. Unlike [JoinOrBegin] it NEVER begins a
// transaction.
//
// That distinction is the reason this exists. A read path must see writes made
// earlier in the caller's unit of work — otherwise a read-your-own-write inside
// one transaction misses the row, or, on a single-connection backend such as
// SQLite, blocks forever on the connection the ambient write is holding.
// [JoinOrBegin] would fix that but at an unacceptable price on a hot read path:
// it would open (and leave the caller to close) a transaction around every
// pool-backed read that happens to run outside one. Join lets a caller say
// "use the ambient transaction if there is one, otherwise the pool" without
// creating transactions it does not need.
//
// The returned Querier is the same joined participant [JoinOrBegin] returns:
// its Commit is a no-op and its Rollback marks the whole unit rollback-only, so
// a read path should simply not call either.
func Join(ctx context.Context) (Querier, bool) {
	if h := fromCtx(ctx); h != nil {
		return &joinedQuerier{h: h}, true
	}
	return nil, false
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

// RollbackOnly reports whether the ambient transaction stashed in ctx (by
// [Begin]) has been marked rollback-only by a joined participant's Rollback
// (see [joinedQuerier.Rollback]). It is false when ctx carries no ambient
// handle. The store package's RunInTx uses this to detect the case
// where a joined participant already rolled back the shared unit — the
// owner's subsequent Commit would honor the mark and roll back, returning
// nil, which that RunInTx must instead surface as its own rolled-back
// sentinel error.
func RollbackOnly(ctx context.Context) bool {
	return IsRollbackMarked(ctx)
}
