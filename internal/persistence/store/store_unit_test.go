package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/require"
)

// allDialects returns one dialect per name. Stores built for pure-function
// coverage (mapConflict, timeArg) must use a non-nil conn such as struct{}{}.
func allDialects() map[string]dialect.Dialect {
	return map[string]dialect.Dialect{
		"postgres": dialect.NewPostgres(),
		"mysql":    dialect.NewMySQL(),
		"sqlite":   dialect.NewSQLite(),
	}
}

// TestMapConflictPassThrough asserts that a non-retryable error is returned
// unchanged by mapConflict on every dialect (the retryable-conflict → sentinel
// mapping is exercised by driver-level errors in the conformance suite).
func TestMapConflictPassThrough(t *testing.T) {
	sentinel := errors.New("plain error")
	for name, d := range allDialects() {
		t.Run(name, func(t *testing.T) {
			s, err := store.New(struct{}{}, d)
			require.NoError(t, err)
			require.ErrorIs(t, s.MapConflictForTest(sentinel), sentinel,
				"%s: non-retryable error must pass through unchanged", name)
			require.NoError(t, s.MapConflictForTest(nil), "%s: nil stays nil", name)
			// Ensure it does NOT spuriously classify a plain error as a conflict.
			require.NotErrorIs(t, s.MapConflictForTest(sentinel), kernel.ErrConcurrentUpdate,
				"%s: plain error must not be treated as concurrent update", name)
		})
	}
}

// TestTimeArgDialect asserts the write-side time codec: SQLite formats to a
// fixed-width RFC3339 UTC string with nine fractional digits (julianday-compatible
// and lexicographically sortable, ADR-0080/ADR-0151); Postgres and MySQL bind the
// time.Time natively.
func TestTimeArgDialect(t *testing.T) {
	// A non-UTC instant to prove UTC normalization on the SQLite path. The
	// fraction ends in zeros on purpose: time.RFC3339Nano would trim them and
	// break the lexicographic ordering the TEXT comparisons depend on.
	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	ts := time.Date(2023, 11, 14, 22, 13, 20, 123400000, loc)

	t.Run("sqlite formats fixed-width RFC3339 UTC", func(t *testing.T) {
		s, err := store.New(struct{}{}, dialect.NewSQLite())
		require.NoError(t, err)
		got, ok := s.TimeArgForTest(ts).(string)
		require.True(t, ok, "sqlite timeArg must be a string")
		require.Equal(t, "2023-11-14T15:13:20.123400000Z", got)
	})

	for _, name := range []string{"postgres", "mysql"} {
		t.Run(name+" binds time.Time", func(t *testing.T) {
			d := allDialects()[name]
			s, err := store.New(struct{}{}, d)
			require.NoError(t, err)
			got, ok := s.TimeArgForTest(ts).(time.Time)
			require.True(t, ok, "%s timeArg must be a time.Time", name)
			require.True(t, got.Equal(ts), "%s: instant must be preserved", name)
		})
	}
}
