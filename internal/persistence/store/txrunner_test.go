package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errRunInTxBoom is an injected sentinel distinct from any package error, so
// assertions can tell "fn's own error propagated unwrapped" apart from a
// store-internal error.
var errRunInTxBoom = errors.New("run in tx: boom")

// TestStoreRunInTx is the 3-dialect conformance suite for Store.RunInTx
// (ADR-0134, Task 8): the commit path, the fn-error rollback path, and the
// rollback-only-by-a-joined-participant path (never a silent nil).
func TestStoreRunInTx(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		var _ kernel.TxRunner = s // compile-time capability check

		t.Run("fn success commits the write", func(t *testing.T) {
			err := s.RunInTx(t.Context(), func(txCtx context.Context) error {
				_, cerr := s.Create(txCtx, appliedStep("tx-commit", "a"))
				return cerr
			})
			require.NoError(t, err, "%s: RunInTx success", b.name)

			_, _, lerr := s.Load(t.Context(), "tx-commit")
			require.NoError(t, lerr, "%s: committed row must be loadable", b.name)
		})

		t.Run("fn error rolls back the write", func(t *testing.T) {
			err := s.RunInTx(t.Context(), func(txCtx context.Context) error {
				if _, cerr := s.Create(txCtx, appliedStep("tx-rollback", "a")); cerr != nil {
					return cerr
				}
				return errRunInTxBoom
			})
			require.ErrorIs(t, err, errRunInTxBoom, "%s: RunInTx must surface fn's error unchanged", b.name)

			_, _, lerr := s.Load(t.Context(), "tx-rollback")
			require.ErrorIs(t, lerr, kernel.ErrInstanceNotFound,
				"%s: rolled-back Create must leave no row", b.name)
		})

		t.Run("joined participant Rollback yields ErrTxRolledBack, never nil", func(t *testing.T) {
			err := s.RunInTx(t.Context(), func(txCtx context.Context) error {
				if _, cerr := s.Create(txCtx, appliedStep("tx-rollback-only", "a")); cerr != nil {
					return cerr
				}
				joined, jerr := transaction.JoinOrBegin(txCtx, b.conn)
				if jerr != nil {
					return jerr
				}
				// A joined participant's Rollback marks the shared unit
				// rollback-only and returns nil (see joinedQuerier.Rollback) —
				// fn itself returns nil here, mirroring the real "someone else
				// already rolled us back" case RunInTx must still catch.
				return joined.Rollback(txCtx)
			})
			require.ErrorIs(t, err, store.ErrTxRolledBack,
				"%s: RunInTx must never return nil when a participant marked rollback-only", b.name)

			_, _, lerr := s.Load(t.Context(), "tx-rollback-only")
			require.ErrorIs(t, lerr, kernel.ErrInstanceNotFound,
				"%s: rollback-only must leave no row", b.name)
		})
	})
}

// TestStoreRunInTxWithoutAmbientCapability is a light sanity check (SQLite
// only — dialect-independent) that RunInTx is reachable through the
// kernel.TxRunner interface, not only the concrete *Store type.
func TestStoreRunInTxWithoutAmbientCapability(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	s, err := store.New(db, dialect.NewSQLite())
	require.NoError(t, err)

	var runner kernel.TxRunner = s
	called := false
	err = runner.RunInTx(t.Context(), func(context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called, "fn must have run")
}
