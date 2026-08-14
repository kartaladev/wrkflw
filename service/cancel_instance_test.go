package service_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
)

// cancelDef returns start → userTask("approve", role "manager") → end.
// It can be parked at the human task for testing cancellation of a Running instance.
func cancelDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "cancel-test",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", activity.WithEligibleRoles("manager")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// cancelSagaDef returns a saga whose admin PARTIAL rollback makes a cancel
// inapplicable (ADR-0180): start → a (undoA) → b (undoB) → wait → end.
func cancelSagaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "cancel-saga",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("a", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("b", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			activity.NewReceiveTask("wait", "go"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "a"},
			{ID: "f2", Source: "a", Target: "b"},
			{ID: "f3", Source: "b", Target: "wait"},
			{ID: "f4", Source: "wait", Target: "end"},
		},
	}
}

// seedDroppedCancelInstance persists an instance parked in an admin PARTIAL
// rollback whose compensation action is still out with a worker — the state
// whose CancelRequested the engine drops. Built with the pure engine and written
// straight to the store, so no action catalog entry is needed.
func seedDroppedCancelInstance(t *testing.T, store *kernel.MemInstanceStore, def *model.ProcessDefinition, instanceID string) {
	t.Helper()

	ctx := t.Context()
	at := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		var cmdID string
		for _, c := range res.Commands {
			if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
				cmdID = ia.CommandID
			}
		}
		require.NotEmpty(t, cmdID, "fixture: %q must be dispatched", name)
		res, err = engine.Step(ctx, def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmdID, nil), engine.StepOptions{})
		require.NoError(t, err)
	}
	res, err = engine.Step(ctx, def, res.State,
		engine.NewCompensateRequested(at.Add(time.Minute), "a"), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status)
	require.NotEmpty(t, res.State.Compensating.ToNode, "fixture: the walk must be a PARTIAL rollback")
	require.NotEmpty(t, res.State.Compensating.ActiveCmdID, "fixture: the walk must be in flight")

	_, err = store.Create(ctx, kernel.AppliedStep{State: res.State, Trigger: engine.NewStartInstance(at, nil)})
	require.NoError(t, err)
}

// newCancelTestService builds a ProcessEngine seeded with:
//   - "ci-run": Running instance parked at a human task (cancelDef)
//   - "ci-done": Completed terminal instance (linearDef)
//   - "ci-dropped": Compensating instance in an admin partial rollback (cancelSagaDef)
func newCancelTestService(t *testing.T) *service.ProcessEngine {
	t.Helper()

	def := cancelDef()
	done := linearDef()
	saga := cancelSagaDef()

	h := newHarness(t, def, done, saga)
	ctx := t.Context()

	// Seed "ci-dropped": a cancel delivered here is DROPPED by the engine.
	seedDroppedCancelInstance(t, h.store, saga, "ci-dropped")

	// Seed "ci-run": start a cancelDef instance — parks at the human task.
	parked, err := h.driver.Drive(ctx, def, "ci-run", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "ci-run must park at user task")

	// Seed "ci-done": start a linearDef instance — completes immediately.
	done2, err := h.driver.Drive(ctx, done, "ci-done", map[string]any{"name": "test"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, done2.Status, "ci-done must be terminal")

	return h.newProcessEngine(t)
}

func TestCancelInstance(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T, svc *service.ProcessEngine)
	}{
		{
			name: "cancels a running instance",
			assert: func(t *testing.T, svc *service.ProcessEngine) {
				st, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "ci-run"})
				require.NoError(t, err)
				assert.Equal(t, engine.StatusTerminated, st.State().Status)
				assert.Empty(t, st.State().Tokens)
			},
		},
		{
			name: "already-terminal returns ErrConflict",
			assert: func(t *testing.T, svc *service.ProcessEngine) {
				_, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "ci-done"})
				require.ErrorIs(t, err, service.ErrConflict)
			},
		},
		{
			// ADR-0180: a cancel the engine DROPPED must not answer 200. The
			// instance is alive, nothing happened, and the operator has to learn it.
			name: "dropped cancel returns ErrConflict",
			assert: func(t *testing.T, svc *service.ProcessEngine) {
				_, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "ci-dropped"})
				require.ErrorIs(t, err, service.ErrConflict,
					"a dropped cancel is a state conflict, not a success and not a 500")
				assert.ErrorIs(t, err, engine.ErrCancelNotApplicable,
					"the engine cause must stay inspectable through the service wrapping")
			},
		},
		{
			name: "unknown instance returns ErrInstanceNotFound",
			assert: func(t *testing.T, svc *service.ProcessEngine) {
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
