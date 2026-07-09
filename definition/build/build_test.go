package build_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/build"
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
		AddEventSubProcess("esp", subDef).
		AddIntermediateCatchEvent("catch").
		AddIntermediateThrowEvent("throw").
		AddBoundaryEvent("bnd", "recv").
		AddTerminateEndEvent("term").
		AddErrorEndEvent("err", "E").
		RegisterAction("a", action.ActionFunc(noop)).
		RegisterActionFunc("b", noop).
		CancelActions("cleanup")
	if _, err := b.Loader().Build(); err == nil {
		// Loader().Build validates; a disconnected graph is expected to error,
		// but the call path itself must not panic.
		_ = err
	}
}
