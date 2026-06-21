package runtime_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestCursorRoundTrip verifies that EncodeCursor and DecodeCursor are inverses.
func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	enc := runtime.EncodeCursor(ts, "inst-7")
	gotTS, gotID, err := runtime.DecodeCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotTS.Equal(ts) || gotID != "inst-7" {
		t.Fatalf("round-trip mismatch: got (%v,%q)", gotTS, gotID)
	}
}

// TestDecodeCursorRejectsGarbage verifies malformed cursors produce ErrBadCursor.
func TestDecodeCursorRejectsGarbage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		cursor string
		assert func(t *testing.T, err error)
	}{
		{
			name:   "not base64",
			cursor: "!!!not-base64!!!",
			assert: func(t *testing.T, err error) {
				if err == nil {
					t.Fatal("want error for non-base64 cursor")
				}
			},
		},
		{
			name:   "base64 but not json",
			cursor: "Zm9vYmFy", // "foobar"
			assert: func(t *testing.T, err error) {
				if err == nil {
					t.Fatal("want error for non-json cursor")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := runtime.DecodeCursor(tc.cursor)
			tc.assert(t, err)
		})
	}
}
