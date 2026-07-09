package model

import (
	"testing"
)

func TestNodeWire_CompletionActionRoundTrip(t *testing.T) {
	w := NodeWire{ID: "u1", Kind: KindUserTask, CompletionAction: "recordApproval"}
	got := w.Activity()
	if got.CompletionAction != "recordApproval" {
		t.Fatalf("Activity() dropped CompletionAction: %q", got.CompletionAction)
	}
	var back NodeWire
	back.PutActivity(got)
	if back.CompletionAction != "recordApproval" {
		t.Fatalf("PutActivity() dropped CompletionAction: %q", back.CompletionAction)
	}
}
