package event_test

import (
	"encoding/json"
	"testing"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestNewCompensateThrow covers construction of a CompensationThrowEvent: the
// scope-wide (whole-instance) default, scope-local narrowing, and a targeted
// throw via WithCompensateRef.
func TestNewCompensateThrow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		node   func() model.Node
		assert func(t *testing.T, n model.Node)
	}{
		{"scope-wide default", func() model.Node { return event.NewCompensateThrow("rb") },
			func(t *testing.T, n model.Node) {
				c := n.(event.CompensationThrowEvent)
				if c.CompensateRef != "" || c.ScopeLocal {
					t.Fatalf("want scope-wide whole-instance, got %+v", c)
				}
				if c.Kind() != model.KindCompensationThrowEvent {
					t.Fatalf("Kind = %v", c.Kind())
				}
			}},
		{"scope-local", func() model.Node { return event.NewCompensateThrow("rb", event.WithScopeLocalCompensation()) },
			func(t *testing.T, n model.Node) {
				if !n.(event.CompensationThrowEvent).ScopeLocal {
					t.Fatal("want ScopeLocal")
				}
			}},
		{"targeted", func() model.Node { return event.NewCompensateThrow("rb", event.WithCompensateRef("sub")) },
			func(t *testing.T, n model.Node) {
				if n.(event.CompensationThrowEvent).CompensateRef != "sub" {
					t.Fatal("want CompensateRef=sub")
				}
			}},
		{"name", func() model.Node { return event.NewCompensateThrow("rb", event.WithCompensateThrowName("Rollback")) },
			func(t *testing.T, n model.Node) {
				if n.(event.CompensationThrowEvent).Name() != "Rollback" {
					t.Fatal("want Name=Rollback")
				}
			}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { t.Parallel(); c.assert(t, c.node()) })
	}
}

// TestCompensationThrowWireRoundTrip mirrors TestEventRoundTrip: a
// CompensationThrowEvent (scope-wide, scope-local, and targeted) must survive
// a JSON marshal/unmarshal round-trip through model.ProcessDefinition, coming
// back as event.CompensationThrowEvent with CompensateRef/ScopeLocal intact.
func TestCompensationThrowWireRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		node   model.Node
		assert func(t *testing.T, n model.Node)
	}{
		{"scope-wide", event.NewCompensateThrow("rb-wide"),
			func(t *testing.T, n model.Node) {
				c := n.(event.CompensationThrowEvent)
				if c.CompensateRef != "" || c.ScopeLocal {
					t.Fatalf("scope-wide round-trip = %+v", c)
				}
			}},
		{"scope-local", event.NewCompensateThrow("rb-local", event.WithScopeLocalCompensation()),
			func(t *testing.T, n model.Node) {
				if !n.(event.CompensationThrowEvent).ScopeLocal {
					t.Fatal("ScopeLocal lost in round-trip")
				}
			}},
		{"targeted", event.NewCompensateThrow("rb-target", event.WithCompensateRef("sub")),
			func(t *testing.T, n model.Node) {
				if n.(event.CompensationThrowEvent).CompensateRef != "sub" {
					t.Fatal("CompensateRef lost in round-trip")
				}
			}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			def := &model.ProcessDefinition{
				ID: "e-" + c.name, Version: 1,
				Nodes: []model.Node{c.node},
			}
			data, err := json.Marshal(def)
			if err != nil {
				t.Fatal(err)
			}
			var got model.ProcessDefinition
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			n := got.Nodes[0]
			if _, ok := n.(event.CompensationThrowEvent); !ok {
				t.Fatalf("Nodes[0] type = %T, want event.CompensationThrowEvent", n)
			}
			c.assert(t, n)
		})
	}
}
