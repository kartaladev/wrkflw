package view_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

// TestStatusString verifies that StatusString maps every engine.Status value to
// its canonical string representation.
func TestStatusString(t *testing.T) {
	cases := map[engine.Status]string{
		engine.StatusRunning:      "running",
		engine.StatusCompleted:    "completed",
		engine.StatusFailed:       "failed",
		engine.StatusCompensating: "compensating",
		engine.StatusTerminated:   "terminated",
	}
	for in, want := range cases {
		if got := view.StatusString(in); got != want {
			t.Errorf("StatusString(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestStatusString_Unknown verifies that an out-of-range Status maps to "unknown".
func TestStatusString_Unknown(t *testing.T) {
	if got := view.StatusString(engine.Status(99)); got != "unknown" {
		t.Errorf("StatusString(99) = %q, want %q", got, "unknown")
	}
}
