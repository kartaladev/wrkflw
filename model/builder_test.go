package model_test

import (
	"context"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
)

func TestDefinitionBuilderBuildsAndValidates(t *testing.T) {
	def, err := model.NewDefinition("p", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewServiceTask("t", model.WithActionName("do"))).
		Add(model.NewEndEvent("e")).
		Connect("s", "t").
		Connect("t", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(def.Nodes) != 3 || len(def.Flows) != 2 {
		t.Fatalf("got %d nodes %d flows", len(def.Nodes), len(def.Flows))
	}
}

func TestDefinitionBuilderRejectsInvalid(t *testing.T) {
	_, err := model.NewDefinition("p", 1).Add(model.NewServiceTask("t", model.WithActionName("do"))).Build()
	if err == nil {
		t.Fatal("expected validation error (no start event)")
	}
}

func TestDefinitionBuilderConnectOptions(t *testing.T) {
	def, err := model.NewDefinition("p", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewExclusiveGateway("gw")).
		Add(model.NewServiceTask("a", model.WithActionName("act-a"))).
		Add(model.NewServiceTask("b", model.WithActionName("act-b"))).
		Add(model.NewEndEvent("e")).
		Connect("s", "gw").
		Connect("gw", "a", model.WithCondition("vars.x == 1")).
		Connect("gw", "b", model.AsDefault()).
		Connect("a", "e").
		Connect("b", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Verify the flow from gw->a has a condition set.
	var condFlow *model.SequenceFlow
	for i := range def.Flows {
		if def.Flows[i].Source == "gw" && def.Flows[i].Target == "a" {
			f := def.Flows[i]
			condFlow = &f
			break
		}
	}
	if condFlow == nil {
		t.Fatal("no flow gw->a found")
	}
	if condFlow.Condition != "vars.x == 1" {
		t.Fatalf("Condition = %q, want vars.x == 1", condFlow.Condition)
	}

	// Verify the flow from gw->b is the default flow.
	var defFlow *model.SequenceFlow
	for i := range def.Flows {
		if def.Flows[i].Source == "gw" && def.Flows[i].Target == "b" {
			f := def.Flows[i]
			defFlow = &f
			break
		}
	}
	if defFlow == nil {
		t.Fatal("no flow gw->b found")
	}
	if !defFlow.IsDefault {
		t.Fatal("IsDefault = false, want true")
	}
}

func TestDefinitionBuilderWithFlowID(t *testing.T) {
	def, err := model.NewDefinition("p", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e", model.WithFlowID("myflow")).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(def.Flows) != 1 {
		t.Fatalf("got %d flows, want 1", len(def.Flows))
	}
	if def.Flows[0].ID != "myflow" {
		t.Fatalf("flow ID = %q, want myflow", def.Flows[0].ID)
	}
}

func TestDefinitionBuilderAutoFlowID(t *testing.T) {
	def, err := model.NewDefinition("p", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewEndEvent("end")).
		Connect("start", "end").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.Flows[0].ID != "start->end" {
		t.Fatalf("auto flow ID = %q, want start->end", def.Flows[0].ID)
	}
}

// TestScopedActionNames covers ScopedActionNames across the nil-when-empty,
// single-action, and sorted-multiple-actions cases.
func TestScopedActionNames(t *testing.T) {
	t.Parallel()

	noop := action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return nil, nil
	})

	type testCase struct {
		name   string
		build  func() (*model.ProcessDefinition, error)
		assert func(t *testing.T, got []string)
	}

	cases := []testCase{
		{
			name: "no scoped actions returns nil",
			build: func() (*model.ProcessDefinition, error) {
				return model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewEndEvent("e")).
					Connect("s", "e").
					Build()
			},
			assert: func(t *testing.T, got []string) {
				if got != nil {
					t.Errorf("ScopedActionNames() = %v, want nil", got)
				}
			},
		},
		{
			name: "single scoped action returns single-element slice",
			build: func() (*model.ProcessDefinition, error) {
				return model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewEndEvent("e")).
					Connect("s", "e").
					RegisterAction("only", noop).
					Build()
			},
			assert: func(t *testing.T, got []string) {
				if len(got) != 1 || got[0] != "only" {
					t.Errorf("ScopedActionNames() = %v, want [only]", got)
				}
			},
		},
		{
			name: "multiple scoped actions returned sorted regardless of registration order",
			build: func() (*model.ProcessDefinition, error) {
				return model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewEndEvent("e")).
					Connect("s", "e").
					RegisterAction("b", noop).
					RegisterActionFunc("a", func(_ context.Context, _ map[string]any) (map[string]any, error) {
						return nil, nil
					}).
					Build()
			},
			assert: func(t *testing.T, got []string) {
				want := []string{"a", "b"}
				if len(got) != len(want) {
					t.Fatalf("ScopedActionNames() = %v, want %v", got, want)
				}
				for i := range want {
					if got[i] != want[i] {
						t.Errorf("ScopedActionNames()[%d] = %q, want %q", i, got[i], want[i])
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def, err := tc.build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			tc.assert(t, def.ScopedActionNames())
		})
	}
}

func TestDefinitionBuilderActionsFirstAndStructureFirstBothBuild(t *testing.T) {
	assert := func(t *testing.T, b model.DefinitionBuilder) {
		def, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if def.ID != "d" || len(def.Nodes) != 2 || len(def.Flows) != 1 {
			t.Fatalf("unexpected def: %+v", def)
		}
	}
	// actions-first (the established idiom)
	assert(t, model.NewDefinition("d", 1).
		RegisterActionFunc("a", func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e"))
	// structure-first
	assert(t, model.NewDefinition("d", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		RegisterActionFunc("a", func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }))
}

func TestDefinitionLoaderFromBuilderCanRegisterThenBuild(t *testing.T) {
	l := model.NewDefinition("d", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		Loader()
	def, err := l.RegisterActionFunc("a", func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.ScopedActionNames() == nil {
		t.Fatalf("expected scoped action registered via loader")
	}
}

func TestDefinitionBuilderCancelActions(t *testing.T) {
	def, err := model.NewDefinition("p", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		CancelActions("cleanup-a", "cleanup-b").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(def.CancelActions) != 2 {
		t.Fatalf("CancelActions = %v", def.CancelActions)
	}
	if def.CancelActions[0] != "cleanup-a" || def.CancelActions[1] != "cleanup-b" {
		t.Fatalf("CancelActions = %v", def.CancelActions)
	}
}
