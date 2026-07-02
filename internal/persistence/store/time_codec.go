package store

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// timeArg converts t into the correct bind argument for the given dialect.
// Postgres (TIMESTAMPTZ) and MySQL (DATETIME, DSN loc=UTC) accept a
// [time.Time] value natively. SQLite timestamp columns are TEXT: the
// modernc.org/sqlite driver stringifies a bound [time.Time] via its default
// String() form, which is not ISO8601 and cannot be scanned back. For SQLite
// the value is therefore formatted as an RFC3339Nano UTC string, which is
// julianday-compatible and round-trips exactly (ADR-0080).
//
// The TEXT path is activated by [dialect.Dialect.TimestampsAsText]; callers
// must not compare [dialect.Dialect.Name] to "sqlite" directly.
func timeArg(d dialect.Dialect, t time.Time) any {
	if d.TimestampsAsText() {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return t
}

// parseTimeText parses an RFC3339Nano UTC string as written by [timeArg] on
// the TEXT-timestamp path (ADR-0080). Returns the parsed instant
// UTC-normalised. An error is returned if s is not a valid RFC3339Nano value.
func parseTimeText(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("workflow-store: parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}
