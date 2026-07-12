package idgen_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/kartaladev/wrkflw/runtime/idgen"
)

func TestXID(t *testing.T) {
	g := idgen.XID()
	id1, err := g.NewID()
	if err != nil {
		t.Fatalf("xid NewID error: %v", err)
	}
	if id1 == "" {
		t.Fatal("xid NewID returned empty string")
	}
	id2, _ := g.NewID()
	if id1 == id2 {
		t.Fatalf("xid NewID not unique: %q == %q", id1, id2)
	}
	// xid string form has no hyphens (matters for the child-suffix parsing).
	for _, c := range id1 {
		if c == '-' {
			t.Fatalf("xid contains a hyphen: %q", id1)
		}
	}
}

func TestUUIDv7(t *testing.T) {
	g := idgen.UUIDv7()
	id, err := g.NewID()
	if err != nil {
		t.Fatalf("uuidv7 NewID error: %v", err)
	}
	u, perr := uuid.Parse(id)
	if perr != nil {
		t.Fatalf("uuidv7 produced unparseable UUID %q: %v", id, perr)
	}
	if got := u.Version(); got != 7 {
		t.Fatalf("expected UUID version 7, got %d", got)
	}
}

func TestFunc(t *testing.T) {
	t.Run("returns wrapped value", func(t *testing.T) {
		g := idgen.Func(func() (string, error) { return "fixed-1", nil })
		id, err := g.NewID()
		if err != nil || id != "fixed-1" {
			t.Fatalf("Func NewID = %q, %v", id, err)
		}
	})
	t.Run("propagates wrapped error", func(t *testing.T) {
		sentinel := errors.New("boom")
		g := idgen.Func(func() (string, error) { return "", sentinel })
		_, err := g.NewID()
		if !errors.Is(err, sentinel) {
			t.Fatalf("Func did not propagate error: %v", err)
		}
	})
}
