package store

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Compile-time check: *Store satisfies the TxRunner capability (ADR-0134).
var _ kernel.TxRunner = (*Store)(nil)

// RunInTx runs fn inside one store transaction: every write method invoked
// with the txCtx handed to fn (via [transaction.JoinOrBegin]) joins it. fn's
// error rolls the whole unit back. A joined participant that calls Rollback
// directly (rather than returning an error from fn) marks the unit
// rollback-only; the owner's Commit then honors the mark and rolls back,
// which would otherwise surface as a misleading nil — RunInTx detects this via
// [transaction.RollbackOnly] and returns [ErrTxRolledBack] instead, so success
// always means committed.
func (s *Store) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	q, txCtx, err := transaction.Begin(ctx, s.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: run in tx: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	if err := fn(txCtx); err != nil {
		return err
	}

	// Rollback-only detection (ADR-0134): a joined participant's Rollback marks
	// the unit rollback-only; the owner Commit would then roll back and return
	// nil. Surface that as an error — success must mean COMMITTED.
	if transaction.RollbackOnly(txCtx) {
		return ErrTxRolledBack
	}

	if err := q.Commit(ctx); err != nil {
		return s.mapConflict(fmt.Errorf("workflow-store: run in tx: commit: %w", err))
	}
	committed = true
	return nil
}
