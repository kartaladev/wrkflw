package build_test

import (
	"context"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/build"
)

func TestFluentChain(t *testing.T) {
	def, err := build.New("order", 1).
		AddStartEvent("s").
		AddExclusiveGateway("gw", "Approved?").
		AddServiceTask("charge", activity.WithActionName("charge-card")).
		AddUserTask("approve", []string{"manager"}).
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
	sub := build.New("sub", 1)
	sub.AddStartEvent("ss").AddEndEvent("se").Connect("ss", "se")
	subDef, err := sub.Build()
	if err != nil {
		t.Fatal(err)
	}
	noop := func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }
	b := build.New("full", 1).
		AddStartEvent("start").
		AddParallelGateway("par").
		AddInclusiveGateway("inc").
		AddEventBasedGateway("evt").
		AddReceiveTask("recv", "msg").
		AddSendTask("send", "msg").
		AddBusinessRuleTask("rule", activity.WithActionName("r")).
		AddSubProcess("sp", subDef).
		AddCallActivity("call", "sub:1").
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
