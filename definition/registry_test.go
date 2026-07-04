package definition

import (
	"errors"
	"testing"
)

// TestFromWireUnregisteredKindIsLoud verifies the deserializer fails loudly when
// a kind has no registered spec (e.g. its leaf package was not imported), rather
// than silently producing a zero value. Registration-dependent round-trip tests
// live in definition/kinds (which imports every leaf).
func TestFromWireUnregisteredKindIsLoud(t *testing.T) {
	_, err := fromWire(NodeWire{ID: "x", Kind: NodeKind(9999)})
	if err == nil || !errors.Is(err, ErrKindNotRegistered) {
		t.Fatalf("err = %v, want ErrKindNotRegistered", err)
	}
}

// TestRegisterKindDuplicatePanics verifies double-registration is a loud
// programmer error.
func TestRegisterKindDuplicatePanics(t *testing.T) {
	const k = NodeKind(9998)
	RegisterKind(k, NodeSpec{Name: "synthetic-9998", ToWire: func(Node, *NodeWire) {}})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate RegisterKind")
		}
	}()
	RegisterKind(k, NodeSpec{Name: "synthetic-9998-dup", ToWire: func(Node, *NodeWire) {}})
}
