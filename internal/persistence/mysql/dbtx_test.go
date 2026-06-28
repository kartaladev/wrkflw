package mysql_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

// TestTxWith exercises the txWith helper: success, fn-error, and closed-DB paths.
func TestTxWith(t *testing.T) {
	t.Run("success path commits", func(t *testing.T) {
		db := database.RunTestMySQL(t)
		called := false
		err := mypkg.TxWith(t.Context(), db, func(_ *sql.Tx) error {
			called = true
			return nil
		})
		require.NoError(t, err, "txWith must commit when fn returns nil")
		require.True(t, called, "fn must be invoked")
	})

	t.Run("fn error rolls back and is returned", func(t *testing.T) {
		db := database.RunTestMySQL(t)
		injected := errors.New("fn-level error")
		err := mypkg.TxWith(t.Context(), db, func(_ *sql.Tx) error {
			return injected
		})
		require.ErrorIs(t, err, injected, "txWith must propagate fn error unchanged")
	})

	t.Run("begin error on closed db wraps prefix", func(t *testing.T) {
		db := database.RunTestMySQL(t)
		require.NoError(t, db.Close())
		err := mypkg.TxWith(t.Context(), db, func(_ *sql.Tx) error { return nil })
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflow-persistence-mysql")
	})
}
