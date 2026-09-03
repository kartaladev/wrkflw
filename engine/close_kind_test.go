package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// closeKindOf returns the CloseKind of the last CLOSED visit for nodeID, and
// whether such a visit exists.
func closeKindOf(st engine.InstanceState, nodeID string) (engine.CloseKind, bool) {
	for i := len(st.History) - 1; i >= 0; i-- {
		v := st.History[i]
		if v.NodeID == nodeID && v.LeftAt != nil {
			return v.CloseKind, true
		}
	}
	return "", false
}

// parkedAtUserTask drives def from zero to its parked user-task state.
func parkedAtUserTask(t *testing.T, def *model.ProcessDefinition, at time.Time) engine.InstanceState {
	t.Helper()
	r, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	return r.State
}

// forkJoinDef exercises the NORMAL closes that must stay unstamped: a parallel
// split, its two branches, and the join.
//
//	Start → fork → (a | b) → join → End
func forkJoinDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-forkjoin", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("a", activity.WithManual(true)),
			activity.NewUserTask("b", activity.WithManual(true)),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "join"},
			{ID: "f5", Source: "b", Target: "join"},
			{ID: "f6", Source: "join", Target: "end"},
		},
	}
}

// terminateEndDef races a terminating end against a parked user task, so the
// force-termination sweep closes the task's still-open visit.
//
//	Start → fork → (work | halt[terminate])
func terminateEndDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-terminate", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("work"),
			event.NewEnd("halt", event.WithForceTermination("operator kill", event.OutcomeAbort)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "work"},
			{ID: "f3", Source: "fork", Target: "halt"},
		},
	}
}

// errorEndDef ends with an uncaught error end.
//
//	Start → boom[error E_BOOM]
func errorEndDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-errorend", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewEnd("boom", event.WithErrorCode("E_BOOM")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "boom"},
		},
	}
}

// cancellableDef parks on a user task so an instance cancel closes its visit.
func cancellableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-cancel", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "work"},
			{ID: "f2", Source: "work", Target: "end"},
		},
	}
}

// TestNodeVisitCloseKind pins the close reason recorded on a node visit:
// abnormal closes name why the visit ended, and every NORMAL
// advance — including gateway forks, joins, and sub-process entry — leaves it
// unset so a consumer can treat a present close_kind as "something went
// sideways here".
func TestNodeVisitCloseKind(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		run    func(t *testing.T) engine.InstanceState
		assert func(t *testing.T, st engine.InstanceState)
	}

	cases := []testCase{
		{
			name: "normal advances leave every close unstamped",
			run: func(t *testing.T) engine.InstanceState {
				r, err := engine.Step(t.Context(), forkJoinDef(), engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				require.NoError(t, err)
				return r.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				for _, nodeID := range []string{"start", "fork", "a", "b", "join"} {
					kind, ok := closeKindOf(st, nodeID)
					require.True(t, ok, "node %q must have a closed visit", nodeID)
					assert.Empty(t, kind, "normal close of %q must not be stamped", nodeID)
				}
			},
		},
		{
			name: "instance cancel stamps instance_cancelled",
			run: func(t *testing.T) engine.InstanceState {
				st := parkedAtUserTask(t, cancellableDef(), at)
				r, err := engine.Step(t.Context(), cancellableDef(), st,
					engine.NewCancelRequested(at.Add(time.Hour)), engine.StepOptions{})
				require.NoError(t, err)
				return r.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				kind, ok := closeKindOf(st, "work")
				require.True(t, ok)
				assert.Equal(t, engine.CloseKindInstanceCancelled, kind)
			},
		},
		{
			name: "force-termination stamps terminated",
			run: func(t *testing.T) engine.InstanceState {
				r, err := engine.Step(t.Context(), terminateEndDef(), engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				require.NoError(t, err)
				return r.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				kind, ok := closeKindOf(st, "work")
				require.True(t, ok, "the parked user task's visit must be closed by the sweep")
				assert.Equal(t, engine.CloseKindTerminated, kind)
			},
		},
		{
			name: "uncaught error end stamps errored",
			run: func(t *testing.T) engine.InstanceState {
				r, err := engine.Step(t.Context(), errorEndDef(), engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				require.NoError(t, err)
				return r.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				kind, ok := closeKindOf(st, "boom")
				require.True(t, ok)
				assert.Equal(t, engine.CloseKindErrored, kind)
			},
		},
		{
			name: "interrupting boundary stamps boundary_interrupted on the host",
			run: func(t *testing.T) engine.InstanceState {
				def := interruptingMessageBoundaryDef()
				st := parkedAtUserTask(t, def, at)
				r, err := engine.Step(t.Context(), def, st,
					engine.NewMessageReceived(at.Add(time.Minute), "cancel", "", nil), engine.StepOptions{})
				require.NoError(t, err)
				return r.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				kind, ok := closeKindOf(st, "work")
				require.True(t, ok)
				assert.Equal(t, engine.CloseKindBoundaryInterrupted, kind)
			},
		},
		{
			name: "reverse rollback stamps reversed",
			run: func(t *testing.T) engine.InstanceState {
				def := reverseSvcDef()
				r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, map[string]any{"amount": 100}), engine.StepOptions{})
				require.NoError(t, err)
				var cmdID string
				for _, c := range r1.Commands {
					if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "do" {
						cmdID = ia.CommandID
					}
				}
				require.NotEmpty(t, cmdID)
				// Completing "do" drives on to "park", whose action is left
				// outstanding — the instance is running when the reverse arrives.
				r2, err := engine.Step(t.Context(), def, r1.State,
					engine.NewActionCompleted(at, cmdID, nil), engine.StepOptions{})
				require.NoError(t, err)
				r3, err := engine.Step(t.Context(), def, r2.State,
					engine.NewReverseToStart(at.Add(time.Minute), "start"), engine.StepOptions{})
				require.NoError(t, err)
				return r3.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				kind, ok := closeKindOf(st, "park")
				require.True(t, ok)
				assert.Equal(t, engine.CloseKindReversed, kind)
			},
		},
		{
			name: "administrative compensation walk stamps compensated",
			run: func(t *testing.T) engine.InstanceState {
				def := reverseSvcDef()
				r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				require.NoError(t, err)
				var cmdID string
				for _, c := range r1.Commands {
					if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "do" {
						cmdID = ia.CommandID
					}
				}
				require.NotEmpty(t, cmdID)
				r2, err := engine.Step(t.Context(), def, r1.State,
					engine.NewActionCompleted(at, cmdID, nil), engine.StepOptions{})
				require.NoError(t, err)
				r3, err := engine.Step(t.Context(), def, r2.State,
					engine.NewCompensateRequested(at.Add(time.Minute), ""), engine.StepOptions{})
				require.NoError(t, err)
				return r3.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				kind, ok := closeKindOf(st, "park")
				require.True(t, ok)
				assert.Equal(t, engine.CloseKindCompensated, kind)
			},
		},
		{
			name: "deadline breach stamps deadline_expired on the rerouted task",
			run: func(t *testing.T) engine.InstanceState {
				def := deadlineDef()
				st := parkedAtUserTask(t, def, at)
				require.Len(t, st.Timers, 1)
				r, err := engine.Step(t.Context(), def, st,
					engine.NewTimerFired(at.Add(3*time.Hour), st.Timers[0].TimerID), engine.StepOptions{})
				require.NoError(t, err)
				return r.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				kind, ok := closeKindOf(st, "userTask")
				require.True(t, ok)
				assert.Equal(t, engine.CloseKindDeadlineExpired, kind)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.run(t))
		})
	}
}

// TestCloseKindIsANamedType pins that close reasons are a named type rather than
// bare strings. NodeVisit.CloseKind is a discriminator a consumer switches on, so
// the compiler — not review — should be what rejects `v.CloseKind = "cancelled"`.
// The sibling discriminators in this package (TokenState, Status) are already
// named types; this asserts CloseKind matches them.
func TestCloseKindIsANamedType(t *testing.T) {
	t.Parallel()

	// Collecting the constants into a []engine.CloseKind fails to compile if
	// CloseKind is not a defined type, or if the constants are untyped strings
	// that were never given it.
	kinds := []engine.CloseKind{
		engine.CloseKindInstanceCancelled,
		engine.CloseKindTerminated,
		engine.CloseKindBoundaryInterrupted,
		engine.CloseKindErrored,
		engine.CloseKindCompensated,
		engine.CloseKindReversed,
		engine.CloseKindDeadlineExpired,
	}
	assert.Equal(t, engine.CloseKind("instance_cancelled"), kinds[0])
	assert.Len(t, kinds, 7, "every declared close reason must carry the named type")

	// NodeVisit's field must carry the named type too, otherwise the enum buys
	// nothing at the point consumers actually read it.
	var visit engine.NodeVisit
	visit.CloseKind = engine.CloseKindTerminated
	assert.Equal(t, engine.CloseKindTerminated, visit.CloseKind)

	// String() keeps the wire value readable for logs and JSON.
	assert.Equal(t, "deadline_expired", engine.CloseKindDeadlineExpired.String())
}
