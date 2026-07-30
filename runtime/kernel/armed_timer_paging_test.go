package kernel_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TestArmedCursorRoundTrip verifies EncodeArmedTimerCursor and DecodeArmedTimerCursor are
// inverses, including at sub-second precision. The cursor is opaque: the empty
// string is the structural first-page sentinel, and a zero NextRun is an
// ordinary value the engine really produces (runtime/timerops.go arms
// regardless of TriggerSpec.Next reporting ok == false), so it must survive the
// round trip rather than aliasing "no cursor" (ADR-0159). Whether such a row
// reaches the table is backend-dependent — MySQL rejects it — but the cursor
// must not depend on which.
func TestArmedCursorRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		nextRun    time.Time
		instanceID string
		timerID    string
		assert     func(t *testing.T, nextRun time.Time, instanceID, timerID string, err error)
	}

	cases := []testCase{
		{
			name:       "whole second",
			nextRun:    time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
			instanceID: "inst-7",
			timerID:    "inst-7-tm1",
			assert: func(t *testing.T, nextRun time.Time, instanceID, timerID string, err error) {
				require.NoError(t, err)
				assert.True(t, nextRun.Equal(time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)), "got %v", nextRun)
				assert.Equal(t, "inst-7", instanceID)
				assert.Equal(t, "inst-7-tm1", timerID)
			},
		},
		{
			name:       "nanosecond precision survives",
			nextRun:    time.Date(2026, 7, 30, 10, 0, 0, 123456789, time.UTC),
			instanceID: "inst-8",
			timerID:    "inst-8-tm2",
			assert: func(t *testing.T, nextRun time.Time, _, _ string, err error) {
				require.NoError(t, err)
				assert.Equal(t, 123456789, nextRun.Nanosecond())
			},
		},
		{
			name:       "zero next_run is an ordinary value, not the sentinel",
			nextRun:    time.Time{},
			instanceID: "inst-9",
			timerID:    "inst-9-tm1",
			assert: func(t *testing.T, nextRun time.Time, instanceID, timerID string, err error) {
				require.NoError(t, err)
				assert.True(t, nextRun.IsZero())
				assert.Equal(t, "inst-9", instanceID)
				assert.Equal(t, "inst-9-tm1", timerID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			enc, err := kernel.EncodeArmedTimerCursor(tc.nextRun, tc.instanceID, tc.timerID)
			require.NoError(t, err)
			require.NotEmpty(t, enc, "a real cursor must never encode to the first-page sentinel")

			nextRun, instanceID, timerID, err := kernel.DecodeArmedTimerCursor(enc)
			tc.assert(t, nextRun, instanceID, timerID, err)
		})
	}
}

// TestDecodeArmedCursorRejectsGarbage verifies malformed cursors surface
// ErrBadArmedTimerCursor — a distinct sentinel from ErrBadCursor, whose message
// names an instance cursor and would misreport a timer cursor to an operator.
func TestDecodeArmedCursorRejectsGarbage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		cursor string
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:   "not base64",
			cursor: "!!!not-base64!!!",
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor)
				assert.NotErrorIs(t, err, kernel.ErrBadCursor)
			},
		},
		{
			name:   "base64 but not json",
			cursor: "Zm9vYmFy", // "foobar"
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor)
			},
		},
		{
			// The failure this guards is silent, so it is the dangerous one: an
			// INSTANCE cursor is also base64-of-JSON and also carries an
			// "instance_id", so an undiscriminated, non-strict decode would
			// yield (zero, "inst-x", "") — and that triple as a keyset
			// predicate matches nearly every row. The operator pastes the wrong
			// cursor, gets 200 and page one back, and pages forever.
			//
			// Which guard actually fires: DisallowUnknownFields, because
			// "started_at" is unknown to armedTimerCursorPayload — NOT the kind
			// check, which this direction never reaches (ADR-0160).
			name:   "an instance cursor is not an armed-timer cursor",
			cursor: mustEncodeInstanceCursor(t, time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC), "inst-x"),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor)
			},
		},
		{
			name:   "valid JSON with no recognised fields",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{}`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor)
			},
		},
		{
			name:   "JSON null",
			cursor: base64.URLEncoding.EncodeToString([]byte(`null`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, err := kernel.DecodeArmedTimerCursor(tc.cursor)
			tc.assert(t, err)
		})
	}
}

// TestEncodeArmedTimerCursorRejectsUnencodableTime pins that a cursor which
// cannot be marshalled is reported as an error rather than returned as the
// empty string.
//
// The empty string is the first-page sentinel. Returning it here would make a
// page answer HasMore: true with an empty NextCursor, and a client following
// that cursor would re-request page one forever — precisely the aliasing the
// opaque-cursor design exists to make impossible. time.Time.MarshalJSON
// rejects years outside [0,9999], and schedule.At accepts an arbitrary absolute
// time, so this is reachable rather than theoretical.
func TestEncodeArmedTimerCursorRejectsUnencodableTime(t *testing.T) {
	t.Parallel()

	got, err := kernel.EncodeArmedTimerCursor(
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), "inst-1", "inst-1-tm1")

	require.Error(t, err, "an unencodable time must not degrade to the first-page sentinel")
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "inst-1-tm1", "the error must name the timer it could not encode")
}
