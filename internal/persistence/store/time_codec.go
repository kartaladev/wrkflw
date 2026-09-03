package store

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
)

// textTimeLayout is the TEXT-timestamp encoding: RFC3339 in UTC with a
// fixed-width nine-digit fractional second.
//
// The fixed width is load-bearing, not cosmetic. SQLite compares TEXT
// lexicographically, so every `WHERE <col> <= ?` predicate (relay outbox claim,
// call-link lease reclaim, retention pruning) and every `ORDER BY <col>` is only
// correct while string order matches chronological order. [time.RFC3339Nano]
// breaks that: it trims trailing zeros from the fraction, so "…34.1Z" sorts
// after "…34.15Z" and a row that is genuinely due is silently skipped. The Go
// standard library documents this ("removes trailing zeros … may not sort
// correctly once formatted"). Padding every value to nine digits makes the
// comparison positional and restores the invariant.
//
// Values written with the previous trimmed encoding remain readable — see
// [parseTimeText] — because RFC3339Nano parsing accepts any number of
// fractional digits.
const textTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// timeArg converts t into the correct bind argument for the given dialect.
// Postgres (TIMESTAMPTZ) and MySQL (DATETIME, DSN loc=UTC) accept a
// [time.Time] value natively. SQLite timestamp columns are TEXT: the
// modernc.org/sqlite driver stringifies a bound [time.Time] via its default
// String() form, which is not ISO8601 and cannot be scanned back. For SQLite
// the value is therefore formatted as a [textTimeLayout] UTC string, which is
// julianday-compatible, round-trips exactly, and sorts lexicographically.
//
// The TEXT path is activated by [dialect.Dialect.TimestampsAsText]; callers
// must not compare [dialect.Dialect.Name] to "sqlite" directly.
func timeArg(d dialect.Dialect, t time.Time) any {
	if d.TimestampsAsText() {
		return t.UTC().Format(textTimeLayout)
	}
	return t
}

// parseTimeText parses a UTC RFC3339 string as written by [timeArg] on the
// TEXT-timestamp path. Returns the parsed instant UTC-normalised. An
// error is returned if s is not a valid RFC3339Nano value.
//
// Parsing deliberately uses [time.RFC3339Nano], which accepts any number of
// fractional digits. That keeps rows written by the earlier trimmed encoding
// readable after the move to [textTimeLayout]; only comparison
// ordering, not readability, was affected by the change.
func parseTimeText(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("workflow-store: parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}
