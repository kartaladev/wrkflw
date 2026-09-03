package kernel_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// validInstancePayload is a well-formed instance-cursor envelope. Tests below
// append to it or corrupt it to exercise one guard at a time.
//
// It deliberately carries only the fields the decoder is being tested on, and
// no "kind": these cases cover the SHARED decoder, so they must not depend on
// any one family's discriminator. A payload missing a known field is valid to
// DisallowUnknownFields, so this stays correct once cursorPayload gains Kind.
const validInstancePayload = `{"started_at":"2026-07-30T09:00:00Z","instance_id":"inst-1"}`

// TestDecodeCursorInto covers the shared strict decoder that both cursor
// families delegate to. Each case names the single guard it is meant to trip;
// a case that trips a different guard would pass for the wrong reason and
// certify nothing.
func TestDecodeCursorInto(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		cursor string
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:   "well-formed payload decodes",
			cursor: base64.URLEncoding.EncodeToString([]byte(validInstancePayload)),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "not base64",
			cursor: "!!!not-base64!!!",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
		{
			name:   "unknown field is rejected",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"instance_id":"i","nope":1}`)),
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unknown field")
			},
		},
		{
			// json.Decoder.Decode reads only the FIRST JSON
			// value and ignores whatever follows, where the json.Unmarshal it
			// replaces rejects it. Without this guard the "hardened" decoder is
			// strictly weaker than the code it supersedes on this axis.
			name:   "trailing JSON value is rejected",
			cursor: base64.URLEncoding.EncodeToString([]byte(validInstancePayload + `{"kind":"evil"}`)),
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "trailing data")
			},
		},
		{
			name:   "trailing garbage is rejected",
			cursor: base64.URLEncoding.EncodeToString([]byte(validInstancePayload + `XYZ`)),
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
		{
			// Whitespace after a value is legal JSON framing, not an injected
			// second value, so it must still be accepted — otherwise the guard
			// above would reject pretty-printed payloads.
			name:   "trailing whitespace is accepted",
			cursor: base64.URLEncoding.EncodeToString([]byte(validInstancePayload + "\n  \t")),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := kernel.DecodeCursorIntoForTest(tc.cursor, kernel.InstanceCursorPayloadForTest())
			tc.assert(t, err)
		})
	}
}

// TestDecodeArmedTimerCursorRejectsTrailingData is a regression test for a
// defect in the SHIPPED armed-timer cursor, found by an audit: the decoder
// accepted a valid payload with a second JSON value appended, returning
// err=<nil> and the first value's contents. It is asserted separately from the
// codec table because it exercises the public armed-timer entry point rather
// than the shared helper.
func TestDecodeArmedTimerCursorRejectsTrailingData(t *testing.T) {
	t.Parallel()

	payload := `{"kind":"armed_timer","next_run":"2026-07-30T09:00:00Z","instance_id":"i","timer_id":"tm"}`
	cursor := base64.URLEncoding.EncodeToString([]byte(payload + `{"kind":"evil"}`))

	_, _, _, err := kernel.DecodeArmedTimerCursor(cursor)

	require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor)
}
