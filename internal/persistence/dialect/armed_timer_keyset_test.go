package dialect_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
)

// TestArmedTimerKeysetPredicate pins the three-column keyset predicate and its
// deliberate per-dialect divergence. The shape is set by MEASUREMENT, not
// symmetry (ADR-0159):
//
//   - Postgres and SQLite use a row value — one index seek on the full triple.
//   - MySQL must NOT: its optimizer does not treat a row constructor as an
//     index range condition (EXPLAIN reports type: index, possible_keys: NULL),
//     so Handler_read_next grows with cursor depth and paging ends up slower
//     than the full scan it replaces.
//
// Predicate and args are two halves of one contract, so this table asserts the
// placeholder count matches the bound-args length for every dialect.
func TestArmedTimerKeysetPredicate(t *testing.T) {
	t.Parallel()

	cursorTime := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		d      dialect.Dialect
		assert func(t *testing.T, predicate string, args []any)
	}

	cases := []testCase{
		{
			name: "postgres uses a row value binding three args",
			d:    dialect.NewPostgres(),
			assert: func(t *testing.T, predicate string, args []any) {
				assert.Contains(t, predicate, "(next_run, instance_id, timer_id) > (?, ?, ?)")
				require.Len(t, args, 3)
				assert.Equal(t, cursorTime, args[0])
				assert.Equal(t, "inst-1", args[1])
				assert.Equal(t, "inst-1-tm1", args[2])
			},
		},
		{
			name: "sqlite uses a row value binding three args",
			d:    dialect.NewSQLite(),
			assert: func(t *testing.T, predicate string, args []any) {
				assert.Contains(t, predicate, "(next_run, instance_id, timer_id) > (?, ?, ?)")
				require.Len(t, args, 3)
			},
		},
		{
			name: "mysql expands lexicographically, binding five args",
			d:    dialect.NewMySQL(),
			assert: func(t *testing.T, predicate string, args []any) {
				assert.NotContains(t, predicate, "(next_run, instance_id, timer_id) >",
					"a row value on MySQL is not an index range condition — measured type: index, possible_keys: NULL")
				assert.Contains(t, predicate, "next_run > ?")
				assert.Contains(t, predicate, "next_run = ?")
				assert.Contains(t, predicate, "instance_id > ?")
				assert.Contains(t, predicate, "instance_id = ?")
				assert.Contains(t, predicate, "timer_id > ?")
				require.Len(t, args, 5)
				assert.Equal(t, cursorTime, args[0])
				assert.Equal(t, cursorTime, args[1])
				assert.Equal(t, "inst-1", args[2])
				assert.Equal(t, "inst-1", args[3])
				assert.Equal(t, "inst-1-tm1", args[4])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			predicate := tc.d.ArmedTimerKeysetPredicate()
			args := tc.d.ArmedTimerKeysetArgs(cursorTime, "inst-1", "inst-1-tm1")

			assert.Equal(t, strings.Count(predicate, "?"), len(args),
				"placeholder count must match the args the same dialect binds")
			assert.True(t, strings.HasPrefix(predicate, "AND "),
				"the fragment carries its own leading AND, matching KeysetCursorPredicate")
			assert.True(t, strings.HasSuffix(predicate, " "),
				"the fragment carries its own trailing space, matching KeysetCursorPredicate")

			tc.assert(t, predicate, args)
		})
	}
}

// TestArmedTimerKeysetArgsAcceptsTextTime verifies the nextRun parameter is
// typed `any`, not time.Time: on a TEXT-timestamp backend the store binds the
// fixed-width string produced by timeArg, and forcing a time.Time here would
// make that unrepresentable.
func TestArmedTimerKeysetArgsAcceptsTextTime(t *testing.T) {
	t.Parallel()

	const textTime = "2026-07-30T10:00:00.000000000Z"
	args := dialect.NewSQLite().ArmedTimerKeysetArgs(textTime, "inst-1", "inst-1-tm1")
	require.Len(t, args, 3)
	assert.Equal(t, textTime, args[0])
}
