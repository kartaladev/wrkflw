package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// cancelDef returns start → userTask("approve", role "manager") → end.
// It can be parked at the human task for testing cancellation of a Running instance.
func cancelDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      "cancel-test",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewUserTask("approve", []string{"manager"}),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// newCancelTestService builds an Engine seeded with:
//   - "ci-run": Running instance parked at a human task (cancelDef)
//   - "ci-done": Completed terminal instance (linearDef)
func newCancelTestService(t *testing.T) *service.Engine {
	t.Helper()

	def := cancelDef()
	done := linearDef()

	h := newHarness(t, def, done)
	ctx := t.Context()

	// Seed "ci-run": start a cancelDef instance — parks at the human task.
	parked, err := h.runner.Run(ctx, def, "ci-run", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "ci-run must park at user task")

	// Seed "ci-done": start a linearDef instance — completes immediately.
	done2, err := h.runner.Run(ctx, done, "ci-done", map[string]any{"name": "test"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, done2.Status, "ci-done must be terminal")

	return service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))
}

func TestCancelInstance(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T, svc *service.Engine)
	}{
		{
			name: "cancels a running instance",
			assert: func(t *testing.T, svc *service.Engine) {
				st, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "ci-run"})
				require.NoError(t, err)
				assert.Equal(t, engine.StatusTerminated, st.Status)
				assert.Empty(t, st.Tokens)
			},
		},
		{
			name: "already-terminal returns ErrConflict",
			assert: func(t *testing.T, svc *service.Engine) {
				_, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "ci-done"})
				require.ErrorIs(t, err, service.ErrConflict)
			},
		},
		{
			name: "unknown instance returns ErrInstanceNotFound",
			assert: func(t *testing.T, svc *service.Engine) {
				_, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "nope"})
				require.ErrorIs(t, err, kernel.ErrInstanceNotFound)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newCancelTestService(t)
			tc.assert(t, svc)
		})
	}
}
