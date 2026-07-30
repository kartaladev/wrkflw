package kernel_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// mustEncodeInstanceCursor encodes an instance cursor for use as test input,
// failing the test if encoding errors. It exists so call sites that only need a
// valid cursor stay one line after EncodeCursor gained an error return
// (ADR-0160).
func mustEncodeInstanceCursor(t *testing.T, startedAt time.Time, instanceID string) string {
	t.Helper()
	c, err := kernel.EncodeCursor(startedAt, instanceID)
	require.NoError(t, err)
	return c
}

// TestCursorRoundTrip verifies that EncodeCursor and DecodeCursor are inverses.
func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	gotTS, gotID, err := kernel.DecodeCursor(mustEncodeInstanceCursor(t, ts, "inst-7"))

	require.NoError(t, err)
	assert.True(t, gotTS.Equal(ts), "got %v", gotTS)
	assert.Equal(t, "inst-7", gotID)
}

// TestEncodeCursorReportsUnrepresentableTime pins the second defect ADR-0160
// fixes: EncodeCursor used to discard its json.Marshal error and return "",
// which IS the first-page sentinel — so a page could answer HasMore: true with
// an empty NextCursor and a conforming client would re-request page one
// forever. time.Time.MarshalJSON rejects a year outside [0,9999].
func TestEncodeCursorReportsUnrepresentableTime(t *testing.T) {
	t.Parallel()

	got, err := kernel.EncodeCursor(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), "inst-z")

	require.Error(t, err)
	assert.Empty(t, got, "the sentinel must never be returned as a value alongside no error")
	assert.Contains(t, err.Error(), "inst-z", "the error should name the instance whose cursor failed")
}

// TestDecodeCursorRejects covers every way an instance cursor can be invalid.
//
// Each case names the ONE guard it is meant to trip, and asserts a distinct
// message rather than only errors.Is, because several guards share
// ErrBadCursor. This matters concretely: DisallowUnknownFields alone rejects an
// armed-timer cursor here (its "kind"/"next_run"/"timer_id" are all unknown to
// cursorPayload), so the kind check is reached ONLY by an old-format cursor. A
// table without that case still passes with the kind comparison deleted
// (ADR-0160).
func TestDecodeCursorRejects(t *testing.T) {
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
				require.ErrorIs(t, err, kernel.ErrBadCursor)
			},
		},
		{
			name:   "base64 but not json",
			cursor: "Zm9vYmFy", // "foobar"
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
			},
		},
		{
			// The silent failure this delivery exists to stop. An armed-timer
			// cursor is also base64-of-JSON and also carries an "instance_id",
			// so before ADR-0160 it decoded with NO error as (zero, "inst-x").
			// Instance listing is DESC, so that predicate matches NOTHING and
			// the operator gets an empty page with a 200 — quieter than the
			// armed-timer side's infinite loop, and quieter is worse.
			name: "an armed-timer cursor is not an instance cursor",
			cursor: func() string {
				c, err := kernel.EncodeArmedTimerCursor(
					time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC), "inst-x", "tm-1")
				if err != nil {
					panic(err)
				}
				return c
			}(),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "unknown field")
			},
		},
		{
			// LOAD-BEARING: the only payload that reaches the kind check.
			// Well-formed, no unknown fields, non-empty identity — every other
			// guard passes it. Delete the kind comparison and this is the one
			// case that fails.
			name:   "an old-format cursor without a kind is rejected",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"started_at":"2026-07-30T09:00:00Z","instance_id":"inst-x"}`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "not an instance cursor")
			},
		},
		{
			name:   "empty JSON object",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{}`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "not an instance cursor")
			},
		},
		{
			name:   "JSON null",
			cursor: base64.URLEncoding.EncodeToString([]byte(`null`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "not an instance cursor")
			},
		},
		{
			name:   "unknown field",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"kind":"instance","instance_id":"i","nope":1}`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "unknown field")
			},
		},
		{
			// An empty instance id is the LOWEST key, and a cursor is always
			// minted from a real row (ADR-0152 forbids empty identity keys), so
			// an empty one means the payload was fabricated or truncated.
			name:   "correct kind but empty instance id",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"kind":"instance","started_at":"2026-07-30T09:00:00Z","instance_id":""}`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "no instance identity")
			},
		},
		{
			// The last vector of the same silent-empty-page failure, found by
			// /code-review after two audits missed it. "started_at" simply
			// absent survives base64, DisallowUnknownFields, the trailing-data
			// check, the kind check AND the identity check, then decodes to the
			// zero time — which under DESC is the lowest key, so it matches
			// nothing and the operator gets a 200 with an empty page.
			//
			// Safe to reject, unlike the armed-timer sibling where a zero
			// next_run is a legitimate armed value (ADR-0159): StartedAt is
			// minted from Trigger.OccurredAt, and a zero-StartedAt row sorts
			// LAST under DESC, so a cursor is never legitimately minted from
			// one.
			name:   "correct kind and identity but no started_at",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"kind":"instance","instance_id":"inst-x"}`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "no start time")
			},
		},
		{
			name:   "trailing JSON value",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"kind":"instance","instance_id":"i"}{"kind":"evil"}`)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, kernel.ErrBadCursor)
				assert.Contains(t, err.Error(), "trailing data")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := kernel.DecodeCursor(tc.cursor)
			tc.assert(t, err)
		})
	}
}
