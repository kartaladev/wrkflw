package model

import "testing"

// TestBaseRawLabelIsEmptyWhenUnset covers rawLabel(), the unexported carrier
// used by toWire so an unset label is omitted from the wire (unlike Label(),
// which falls back to Name).
func TestBaseRawLabelIsEmptyWhenUnset(t *testing.T) {
	b := NewBase("id1", "Name")
	if b.rawLabel() != "" {
		t.Fatalf("rawLabel() = %q, want empty when unset", b.rawLabel())
	}
	b.SetLabel("L")
	if b.rawLabel() != "L" {
		t.Fatalf("rawLabel() = %q, want L", b.rawLabel())
	}
}
