package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
)

// buildStartEndDefinition returns the simplest process that drives straight to
// completion: a Start event flowing directly to an End event. No service task
// means no action catalog is needed.
func buildStartEndDefinition(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID:      "idgen-startend",
		Version: 1,
		Nodes:   []model.Node{event.NewStart("start"), event.NewEnd("end")},
		Flows:   []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
}

func TestRunGeneratesWhenInstanceIDEmpty(t *testing.T) {
	t.Parallel()

	def := buildStartEndDefinition(t)
	r, err := runtime.NewProcessDriver(
		runtime.WithIDGenerator(idgen.Func(func() (string, error) { return "gen-123", nil })),
	)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	st, err := r.Run(t.Context(), def, "", map[string]any{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.InstanceID != "gen-123" {
		t.Fatalf("expected generated id gen-123, got %q", st.InstanceID)
	}
}

func TestRunUsesExplicitInstanceID(t *testing.T) {
	t.Parallel()

	def := buildStartEndDefinition(t)
	r, err := runtime.NewProcessDriver(
		runtime.WithIDGenerator(idgen.Func(func() (string, error) { return "SHOULD-NOT-BE-USED", nil })),
	)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	st, err := r.Run(t.Context(), def, "explicit-1", map[string]any{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.InstanceID != "explicit-1" {
		t.Fatalf("expected explicit-1, got %q", st.InstanceID)
	}
}

func TestRunPropagatesGeneratorError(t *testing.T) {
	t.Parallel()

	def := buildStartEndDefinition(t)
	boom := errors.New("no entropy")
	r, err := runtime.NewProcessDriver(
		runtime.WithIDGenerator(idgen.Func(func() (string, error) { return "", boom })),
	)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	_, err = r.Run(t.Context(), def, "", map[string]any{})
	if !errors.Is(err, boom) {
		t.Fatalf("expected generator error to propagate, got %v", err)
	}
}
