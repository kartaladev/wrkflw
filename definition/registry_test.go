package definition

import (
	"errors"
	"testing"
)

func TestRegistryLookup(t *testing.T) {
	s, ok := specFor(KindServiceTask)
	if !ok {
		t.Fatal("KindServiceTask must be registered")
	}
	if s.Name != "serviceTask" {
		t.Fatalf("Name = %q, want serviceTask", s.Name)
	}
}

func TestFromWireUnregisteredKindIsLoud(t *testing.T) {
	_, err := fromWire(NodeWire{ID: "x", Kind: NodeKind(9999)})
	if err == nil || !errors.Is(err, ErrKindNotRegistered) {
		t.Fatalf("err = %v, want ErrKindNotRegistered", err)
	}
}

func TestAllKindsRegistered(t *testing.T) {
	for k := KindStartEvent; k <= KindEventBasedGateway; k++ {
		if _, ok := specFor(k); !ok {
			t.Errorf("kind %v (%s) is not registered", int(k), k)
		}
	}
}
