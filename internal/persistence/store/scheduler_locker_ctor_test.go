package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// TestNewSQLiteLocker_SatisfiesDialectLocker asserts the exported SQLite locker
// constructor returns a dialect.Locker whose advisory methods report unsupported
// (SQLite has no advisory locking). It also anchors the exported-constructor
// surface used by the persistence scheduler-lock bridge.
func TestNewSQLiteLocker_SatisfiesDialectLocker(t *testing.T) {
	l := store.NewSQLiteLocker() // returns dialect.Locker

	ok, err := l.TryLock(t.Context(), "k")
	require.False(t, ok)
	require.ErrorIs(t, err, dialect.ErrUnsupported)
	require.ErrorIs(t, l.Unlock(t.Context(), "k"), dialect.ErrUnsupported)
}
