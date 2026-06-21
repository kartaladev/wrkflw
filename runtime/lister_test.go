package runtime_test

import (
	"errors"
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
	}{
		{
			name:   "not base64",
			cursor: "!!!not-base64!!!",
		},
		{
			name:   "base64 but not json",
			cursor: "Zm9vYmFy", // "foobar"
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := runtime.DecodeCursor(tc.cursor)
			if err == nil {
				t.Fatalf("want error for %s cursor", tc.name)
			}
			if !errors.Is(err, runtime.ErrBadCursor) {
				t.Fatalf("want ErrBadCursor, got %v", err)
			}
		})
	}
}
