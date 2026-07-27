package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// TestTimeArgTextIsLexicographicallyOrdered pins the load-bearing contract of the
// TEXT-timestamp encoding: SQLite stores timestamps as TEXT and compares them
// lexicographically, so every `WHERE <col> <= ?` predicate (relay outbox claim,
// call-link lease reclaim, retention pruning) and every `ORDER BY <col>` only
// answers correctly when string order matches chronological order.
//
// time.RFC3339Nano does NOT satisfy this — it trims trailing zeros from the
// fractional second, so "…34.1Z" sorts after "…34.15Z". The encoding must
// therefore emit fixed-width fractional digits.
func TestTimeArgTextIsLexicographicallyOrdered(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 28, 1, 41, 34, 0, time.UTC)

	type testCase struct {
		name    string
		earlier time.Time
		later   time.Time
		assert  func(t *testing.T, earlier, later string)
	}

	cases := []testCase{
		{
			name:    "trailing-zero fraction sorts before a longer fraction",
			earlier: base.Add(100 * time.Millisecond), // RFC3339Nano renders ".1"
			later:   base.Add(150 * time.Millisecond), // RFC3339Nano renders ".15"
			assert: func(t *testing.T, earlier, later string) {
				assert.Less(t, earlier, later,
					"a due row encoded as %q must sort before now=%q, else SQLite skips it", earlier, later)
			},
		},
		{
			name:    "whole second sorts before a fractional second",
			earlier: base,                             // RFC3339Nano renders no fraction at all
			later:   base.Add(500 * time.Millisecond), // renders ".5"
			assert: func(t *testing.T, earlier, later string) {
				assert.Less(t, earlier, later,
					"whole-second %q must sort before %q", earlier, later)
			},
		},
		{
			name:    "sub-nanosecond neighbours keep order",
			earlier: base.Add(time.Nanosecond),
			later:   base.Add(2 * time.Nanosecond),
			assert: func(t *testing.T, earlier, later string) {
				assert.Less(t, earlier, later)
			},
		},
		{
			name:    "encoded width is constant so comparison is positional",
			earlier: base,
			later:   base.Add(123456789 * time.Nanosecond),
			assert: func(t *testing.T, earlier, later string) {
				assert.Equal(t, len(earlier), len(later),
					"fixed-width encoding required: %q vs %q", earlier, later)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := dialect.NewSQLite()
			require.True(t, d.TimestampsAsText(), "precondition: sqlite stores timestamps as TEXT")

			earlier, ok := store.TimeArgForDialectValue(d, tc.earlier).(string)
			require.True(t, ok, "TEXT dialect must encode to string")
			later, ok := store.TimeArgForDialectValue(d, tc.later).(string)
			require.True(t, ok, "TEXT dialect must encode to string")

			require.True(t, tc.earlier.Before(tc.later), "precondition: earlier < later chronologically")
			tc.assert(t, earlier, later)
		})
	}
}

// TestTimeArgTextRoundTrips verifies the encoding stays readable by the store's
// own parser, so changing the write format cannot orphan previously written rows.
func TestTimeArgTextRoundTrips(t *testing.T) {
	t.Parallel()

	d := dialect.NewSQLite()
	want := time.Date(2026, 7, 28, 1, 41, 34, 100000000, time.UTC)

	encoded, ok := store.TimeArgForDialectValue(d, want).(string)
	require.True(t, ok)

	got, err := time.Parse(time.RFC3339Nano, encoded)
	require.NoError(t, err, "encoded value must remain RFC3339Nano-parseable")
	assert.True(t, want.Equal(got), "round-trip mismatch: want %s got %s", want, got)
}
