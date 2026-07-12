package service_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/idgen"
	"github.com/kartaladev/wrkflw/service"
)

// startEndDef returns a minimal start → end definition keyed as "d" (bare ID)
// so callers can use DefRef: "d". No service task means no action catalog is
// needed and the instance drives straight to completion.
func startEndDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "d",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
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
	pi, err := eng.StartInstance(t.Context(), service.StartInstanceRequest{DefRef: model.Latest("d"), Vars: map[string]any{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if pi.State().InstanceID != "svc-gen-1" {
		t.Fatalf("expected svc-gen-1, got %q", pi.State().InstanceID)
	}
	if pi.State().Status != engine.StatusCompleted {
		t.Fatalf("expected StatusCompleted, got %v", pi.State().Status)
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
	_, err = eng.StartInstance(t.Context(), service.StartInstanceRequest{DefRef: model.Latest("d"), Vars: map[string]any{}})
	if !errors.Is(err, boom) {
		t.Fatalf("expected generator error, got %v", err)
	}
}
