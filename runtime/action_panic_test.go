package runtime_test

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// panicTaskDef builds start → task("p") → end with no retry policy, so a single
// action failure drives the instance terminal (StatusFailed).
func panicTaskDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "panic-test", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("task", definition.WithActionName("p")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestRunnerRecoversActionPanic asserts that a ServiceAction that panics does NOT
// crash the runner; the panic is recovered and converted to an action failure, so
// the instance reaches a normal terminal state (StatusFailed) instead of taking
// down the whole replica with every in-flight instance.
func TestRunnerRecoversActionPanic(t *testing.T) {
	fc := clockwork.NewFakeClock()
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"p": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			panic("action blew up")
		}),
	})
	r := runtimetest.MustRunner(t, cat, runtimetest.MustMemStore(t), runtime.WithClock(fc))

	// Must not panic.
	st, err := r.Run(t.Context(), panicTaskDef(), "p1", nil)
	require.NoError(t, err, "a panicking action must not surface as a Go error from Run")
	assert.Equal(t, engine.StatusFailed, st.Status,
		"a panicking action (no retry policy) must drive the instance to StatusFailed, not crash")
}

// TestRunnerRecoversCancelActionPanic asserts that a panicking cancel action is
// recovered and logged best-effort — CancelInstance must still succeed and reach
// StatusTerminated (ADR-0028: cancellation reports success regardless).
func TestRunnerRecoversCancelActionPanic(t *testing.T) {
	fc := clockwork.NewFakeClock()
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"boom": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			panic("cancel action blew up")
		}),
	})
	r := cancelRunner(t, cat, fc)
	def := cancelDef([]string{"boom"})

	_, err := r.Run(t.Context(), def, "c1", nil)
	require.NoError(t, err)

	st, err := r.CancelInstance(t.Context(), def, "c1")
	require.NoError(t, err, "a panicking cancel action must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, st.Status)
}
