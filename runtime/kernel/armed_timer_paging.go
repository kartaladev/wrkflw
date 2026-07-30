package kernel

import (
	"errors"
	"fmt"
	"time"
)

// ErrBadArmedTimerCursor is returned by DecodeArmedTimerCursor when the cursor is
// malformed. It is deliberately distinct from [ErrBadCursor], whose message
// names an *instance* cursor: an operator paging armed timers must not be told
// their instance cursor is broken.
var ErrBadArmedTimerCursor = errors.New("workflow-runtime: malformed armed-timer cursor")

// armedTimerCursorPayload is the JSON envelope embedded inside the opaque
// armed-timer cursor string. time.Time's JSON form is lossless, so the
// envelope carries full nanosecond precision without any layout choice of its
// own — the fixed-width text encoding a TEXT-timestamp backend needs belongs
// at the SQL bind, not here (ADR-0159).
type armedTimerCursorPayload struct {
	Kind       string    `json:"kind"`
	NextRun    time.Time `json:"next_run"`
	InstanceID string    `json:"instance_id"`
	TimerID    string    `json:"timer_id"`
}

// armedTimerCursorKind discriminates this cursor from every other opaque cursor
// the library hands out.
//
// It is load-bearing, not decoration. An instance cursor ([EncodeCursor]) is
// also base64-of-JSON and also carries an "instance_id", so without a
// discriminator it decodes into this payload with NO error, yielding the triple
// (zero, "<instance>", "") — which as a keyset predicate matches nearly every
// row. An operator who pastes the wrong cursor would get 200 and page one back,
// forever, with no diagnostic: exactly the silent failure
// [ErrBadArmedTimerCursor] exists to prevent.
const armedTimerCursorKind = "armed_timer"

// EncodeArmedTimerCursor produces an opaque keyset cursor for armed-timer paging.
// The cursor encodes the last-seen (nextRun, instanceID, timerID) triple —
// the full ORDER BY key — so the next page continues from exactly where this
// one left off.
//
// The cursor is opaque and the empty string is the first-page sentinel. Do not
// infer "first page" from a zero nextRun: the engine arms a timer even when its
// TriggerSpec cannot compute a next run (runtime/timerops.go leaves nextRun at
// the zero value when Next reports ok == false), and such a row sorts first, so
// a value-inferred sentinel would alias a real row and page forever.
//
// Whether that row reaches the table is backend-dependent — Postgres and SQLite
// store it, MySQL's DATETIME(6) rejects the zero time under strict mode — but
// the cursor must not depend on which: a sentinel that is only safe on one
// backend is not a sentinel.
//
// It returns an error rather than swallowing one, because the only way to fail
// is time.Time.MarshalJSON rejecting a year outside [0,9999] — and the zero
// value it would otherwise return is the empty string, which IS the first-page
// sentinel. A caller that ignored that would answer HasMore: true with an empty
// NextCursor, and a client following it would re-request page one forever. The
// sentinel is only structural if nothing else can produce it.
//
// That year range is reachable, not theoretical: schedule.At takes an arbitrary
// absolute time and Postgres TIMESTAMPTZ stores well past year 9999.
func EncodeArmedTimerCursor(nextRun time.Time, instanceID, timerID string) (string, error) {
	c, err := encodeCursorPayload(armedTimerCursorPayload{
		Kind:       armedTimerCursorKind,
		NextRun:    nextRun,
		InstanceID: instanceID,
		TimerID:    timerID,
	})
	if err != nil {
		return "", fmt.Errorf("workflow-runtime: encode armed-timer cursor for %q/%q: %w", instanceID, timerID, err)
	}
	return c, nil
}

// DecodeArmedTimerCursor parses an opaque cursor produced by EncodeArmedTimerCursor.
// Returns [ErrBadArmedTimerCursor] when the cursor is not valid base64, does not
// hold a valid JSON payload, carries trailing data after that payload, belongs
// to another cursor family, or carries no timer identity.
func DecodeArmedTimerCursor(cursor string) (nextRun time.Time, instanceID, timerID string, err error) {
	// decodeCursorInto carries the strict-decoding guards shared by every cursor
	// family: base64 framing, DisallowUnknownFields (plain json.Unmarshal
	// silently ignores fields it does not recognise, which is what lets a
	// foreign cursor through as a zero-ish triple), and rejection of trailing
	// data after the payload (ADR-0160).
	var p armedTimerCursorPayload
	if err := decodeCursorInto(cursor, &p); err != nil {
		return time.Time{}, "", "", fmt.Errorf("%w: %w", ErrBadArmedTimerCursor, err)
	}
	if p.Kind != armedTimerCursorKind {
		return time.Time{}, "", "", fmt.Errorf("%w: not an armed-timer cursor", ErrBadArmedTimerCursor)
	}
	// A cursor is always minted from a real row, and an armed timer can have
	// neither an empty instance id nor an empty timer id (ADR-0152), so empty
	// here means the payload was fabricated or truncated. Rejecting matters
	// because an empty pair is the LOWEST key: it would match the whole table.
	if p.InstanceID == "" || p.TimerID == "" {
		return time.Time{}, "", "", fmt.Errorf("%w: cursor carries no timer identity", ErrBadArmedTimerCursor)
	}
	return p.NextRun, p.InstanceID, p.TimerID, nil
}

// ArmedTimerFilter controls which armed timers a paged listing returns.
//
// It mirrors [InstanceFilter]: a filter struct rather than positional
// arguments, so a later predicate (by kind, by instance) is an additive field
// rather than a signature break. Field order mirrors [InstanceFilter]
// (Limit, Cursor, IncludeTotal) so the two filters read as one family.
type ArmedTimerFilter struct {
	// Limit is the page size. ≤0 defaults to 50; >200 is clamped to 200, via
	// [NormalizeLimit]. An out-of-range limit is clamped, never rejected.
	Limit int
	// Cursor is the opaque keyset pagination token from the previous page,
	// produced by EncodeArmedTimerCursor. Empty string means start from the first
	// page.
	Cursor string
	// IncludeTotal, when true, requests a total count of all armed timers
	// independent of Limit and Cursor. It defaults to false so a plain paged
	// request issues no count query.
	IncludeTotal bool
}

// ArmedTimerPage is one page of armed timers.
type ArmedTimerPage struct {
	// Items holds this page, ordered by (NextRun, InstanceID, TimerID) —
	// the same total order [TimerStore.ListArmed] uses.
	Items []ArmedTimer
	// NextCursor is the opaque cursor to pass as ArmedTimerFilter.Cursor to
	// retrieve the following page. Empty when HasMore is false.
	NextCursor string
	// HasMore is true when there are additional armed timers beyond this page.
	HasMore bool
	// TotalCount is the total number of armed timers (ignoring Limit and
	// Cursor). Set only when the filter requested IncludeTotal; 0 otherwise.
	// It is a table total, so it does NOT equal len(Items).
	TotalCount int
}
