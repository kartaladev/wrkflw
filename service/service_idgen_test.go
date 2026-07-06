package service_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// startEndDef returns a minimal start → serviceTask("greet") → end definition
// keyed as "d" (bare ID) so callers can use DefRef: "d".
func startEndDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "d",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("greet", activity.WithActionName("greet")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestStartInstanceGeneratesID(t *testing.T) {
	def := startEndDef()
	eng, err := service.NewEngine(
		service.WithDefinitions(regWith(t, def)),
		service.WithIDGenerator(idgen.Func(func() (string, error) { return "svc-gen-1", nil })),
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	pi, err := eng.StartInstance(t.Context(), service.StartInstanceRequest{DefRef: "d", Vars: map[string]any{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if pi.State().InstanceID != "svc-gen-1" {
		t.Fatalf("expected svc-gen-1, got %q", pi.State().InstanceID)
	}
}

func TestStartInstancePropagatesGeneratorError(t *testing.T) {
	boom := errors.New("no entropy")
	def := startEndDef()
	eng, err := service.NewEngine(
		service.WithDefinitions(regWith(t, def)),
		service.WithIDGenerator(idgen.Func(func() (string, error) { return "", boom })),
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	_, err = eng.StartInstance(t.Context(), service.StartInstanceRequest{DefRef: "d", Vars: map[string]any{}})
	if !errors.Is(err, boom) {
		t.Fatalf("expected generator error, got %v", err)
	}
}
