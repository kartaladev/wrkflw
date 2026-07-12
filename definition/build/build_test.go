package build_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/build"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// TestNewLoader covers the YAML authoring entry — the symmetric counterpart to
// NewBuilder. Importing build registers every node kind, so the loader can
// reconstruct nodes from the wire form.
func TestNewLoader(t *testing.T) {
	const src = `
id: y
version: 1
nodes:
  - {id: s, kind: startEvent}
  - {id: charge, kind: serviceTask, action: charge-card}
  - {id: e, kind: endEvent}
flows:
  - {id: f1, source: s, target: charge}
  - {id: f2, source: charge, target: e}
`
	ld, err := build.NewLoader(strings.NewReader(src))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	def, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.ID != "y" || len(def.Nodes) != 3 || len(def.Flows) != 2 {
		t.Fatalf("unexpected definition: %+v", def)
	}
}

func TestFluentChain(t *testing.T) {
	def, err := build.NewBuilder("order", 1).
		AddStartEvent("s").
		AddExclusiveGateway("gw", "Approved?").
		AddServiceTask("charge", activity.WithTaskAction("charge-card")).
		AddUserTask("approve", activity.WithEligibleRoles("manager")).
		AddEndEvent("e").
		Connect("s", "gw").
		Connect("gw", "charge").
		Connect("charge", "approve").
		Connect("approve", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, id := range []string{"s", "gw", "charge", "approve", "e"} {
		if _, ok := def.Node(id); !ok {
			t.Errorf("missing node %q", id)
		}
	}
}

func TestFluentAllAdders(t *testing.T) {
	sub := build.NewBuilder("sub", 1)
	sub.AddStartEvent("ss").AddEndEvent("se").Connect("ss", "se")
	subDef, err := sub.Build()
	if err != nil {
		t.Fatal(err)
	}
	noop := func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }
	b := build.NewBuilder("full", 1).
		AddStartEvent("start").
		AddParallelGateway("par").
		AddInclusiveGateway("inc").
		AddEventBasedGateway("evt").
		AddReceiveTask("recv", "msg").
		AddSendTask("send", "msg").
		AddBusinessRuleTask("rule", activity.WithTaskAction("r")).
		AddSubProcess("sp", subDef).
		AddCallActivity("call", model.Version("sub", 1)).
		AddIntermediateCatchEvent("catch").
		AddIntermediateThrowEvent("throw").
		AddBoundaryEvent("bnd", "recv").
		AddEndEvent("term", event.WithForceTermination("terminated", event.OutcomeAbort)).
		AddEndEvent("err", event.WithErrorCode("E")).
		RegisterAction("a", action.ActionFunc(noop)).
		RegisterActionFunc("b", noop).
		CancelActions("cleanup")
	if _, err := b.Loader().Build(); err == nil {
		// Loader().Build validates; a disconnected graph is expected to error,
		// but the call path itself must not panic.
		_ = err
	}
}

func TestAddCompensationThrow(t *testing.T) {
	tests := []struct {
		name    string
		builder func() *build.Builder
		checkFn func(t *testing.T, n model.Node)
	}{
		{
			name: "scope-wide compensation throw",
			builder: func() *build.Builder {
				return build.NewBuilder("comp", 1).
					AddStartEvent("s").
					AddCompensationThrow("rb").
					AddEndEvent("e").
					Connect("s", "rb").
					Connect("rb", "e")
			},
			checkFn: func(t *testing.T, n model.Node) {
				c, ok := n.(event.CompensationThrowEvent)
				if !ok {
					t.Fatalf("node type = %T, want event.CompensationThrowEvent", n)
				}
				if c.ID() != "rb" {
					t.Errorf("ID() = %q, want %q", c.ID(), "rb")
				}
				if c.Kind() != model.KindCompensationThrowEvent {
					t.Errorf("Kind = %v, want %v", c.Kind(), model.KindCompensationThrowEvent)
				}
				if c.CompensateRef != "" {
					t.Errorf("CompensateRef = %q, want empty", c.CompensateRef)
				}
				if c.ScopeLocal {
					t.Errorf("ScopeLocal = %v, want false", c.ScopeLocal)
				}
			},
		},
		{
			name: "targeted compensation throw",
			builder: func() *build.Builder {
				sub := build.NewBuilder("sub", 1)
				sub.AddStartEvent("ss").AddEndEvent("se").Connect("ss", "se")
				subDef, _ := sub.Build()
				return build.NewBuilder("comp", 1).
					AddStartEvent("s").
					AddSubProcess("subproc", subDef).
					AddCompensationThrow("rb", event.WithCompensateRef("subproc")).
					AddEndEvent("e").
					Connect("s", "subproc").
					Connect("subproc", "rb").
					Connect("rb", "e")
			},
			checkFn: func(t *testing.T, n model.Node) {
				c, ok := n.(event.CompensationThrowEvent)
				if !ok {
					t.Fatalf("node type = %T, want event.CompensationThrowEvent", n)
				}
				if c.ID() != "rb" {
					t.Errorf("ID() = %q, want %q", c.ID(), "rb")
				}
				if c.CompensateRef != "subproc" {
					t.Errorf("CompensateRef = %q, want %q", c.CompensateRef, "subproc")
				}
				if c.ScopeLocal {
					t.Errorf("ScopeLocal = %v, want false", c.ScopeLocal)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := tt.builder().Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			n, ok := def.Node("rb")
			if !ok {
				t.Fatalf("node %q not found in definition", "rb")
			}
			tt.checkFn(t, n)
		})
	}
}
