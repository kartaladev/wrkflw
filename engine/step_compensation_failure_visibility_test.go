package engine

// step_compensation_failure_visibility_test.go — ADR-0179 Decision 1: a
// compensation action that replies ActionFailed must leave a trace.
//
// Before this, handleActionFailed's StatusCompensating short-circuit skipped the
// record in total silence — measured on 2026-08-17 against the base of this
// branch: after the failure, incidents=0, timers=0, and `grep -c slog` over both
// handleActionFailed and stepCompensationAdvance returned 0. A compensation that
// never ran was indistinguishable from one that succeeded.
//
// White-box by necessity, for two independent reasons:
//   - the vanished-source row must WRITE an unexported compensationCursor; it
//     cannot be produced by driving the engine any more (see
//     step_compensation_cursor_migration_test.go for the same constraint), and
//   - installCaptureHandler, which is what makes the WARN line assertable, lives
//     in package engine.

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// twoCompensableDef is
//
//	start → alpha(CompensateAction:"undo-alpha") → beta(CompensateAction:"undo-beta")
//	      → park(user task) → end
//
// The user task keeps the instance alive after both compensable activities have
// completed, which is what gives a CancelRequested a two-record walk to run.
func twoCompensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-compensation-failure-visibility", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("alpha",
				activity.WithTaskAction("do-alpha"), activity.WithCompensateAction("undo-alpha")),
			activity.NewServiceTask("beta",
				activity.WithTaskAction("do-beta"), activity.WithCompensateAction("undo-beta")),
			activity.NewUserTask("park"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "alpha"},
			{ID: "f2", Source: "alpha", Target: "beta"},
			{ID: "f3", Source: "beta", Target: "park"},
			{ID: "f4", Source: "park", Target: "end"},
		},
	}
}

// driveToCompensationWalk runs twoCompensableDef to a cancel-started compensation
// walk whose first dispatch is beta's undo action, and returns that state
// together with the dispatched command id.
//
// opt is threaded into the CancelRequested step, which is where beginCompensation
// arms the walk's first stall guard: a caller that needs a live
// TimerCompensationStall record must set CompensationStallAfter HERE, because no
// later Step arms one for a dispatch that already happened.
//
// Every require here is a control: if the drive ever stops producing a walk with
// two records in flight, the assertions below would go vacuously green.
func driveToCompensationWalk(t *testing.T, def *model.ProcessDefinition, at time.Time, opt StepOptions) (InstanceState, string) {
	t.Helper()

	r1, err := Step(t.Context(), def, InstanceState{InstanceID: "i-comp-fail"},
		NewStartInstance(at, nil), StepOptions{})
	require.NoError(t, err)

	r2, err := Step(t.Context(), def, r1.State,
		NewActionCompleted(at.Add(time.Second), invokeIDForAction(t, r1.Commands, "do-alpha"), nil),
		StepOptions{})
	require.NoError(t, err)

	r3, err := Step(t.Context(), def, r2.State,
		NewActionCompleted(at.Add(2*time.Second), invokeIDForAction(t, r2.Commands, "do-beta"), nil),
		StepOptions{})
	require.NoError(t, err)
	require.Equal(t, StatusRunning, r3.State.Status,
		"control: the user task must keep the instance alive")

	r4, err := Step(t.Context(), def, r3.State,
		NewCancelRequested(at.Add(3*time.Second)), opt)
	require.NoError(t, err)
	require.Equal(t, StatusCompensating, r4.State.Status,
		"control: the cancel must have started a compensation walk")
	require.Equal(t, 1, r4.State.Compensating.NextIndex,
		"control: the walk must start at the SECOND of two records, or the "+
			"forward-progress assertion below has nothing left to advance to")

	return r4.State, invokeIDForAction(t, r4.Commands, "undo-beta")
}

// TestFailedCompensationActionIsVisible covers ADR-0179 Decision 1 on both of the
// node-resolution branches: the in-flight record is readable, and it is not.
//
// No ctx modifier and no cancelled-context row: ctx reaches this code path for
// slog correlation only, and no branch of it consults Done or Err — cancellation
// cannot change the output.
//
// Sequential by construction (no t.Parallel anywhere): installCaptureHandler
// swaps the process-global slog.Default.
func TestFailedCompensationActionIsVisible(t *testing.T) {
	const failureMsg = "compensation action failed"

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		at     time.Time
		setup  func(t *testing.T, def *model.ProcessDefinition, at time.Time) (InstanceState, string)
		assert func(t *testing.T, h *captureHandler, commandID string, res StepResult, err error)
	}

	failedAt := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)

	cases := []testCase{
		{
			name: "the in-flight record names the node, and the walk still advances",
			def:  twoCompensableDef(),
			at:   failedAt,
			setup: func(t *testing.T, def *model.ProcessDefinition, at time.Time) (InstanceState, string) {
				return driveToCompensationWalk(t, def, at.Add(-time.Minute), StepOptions{})
			},
			assert: func(t *testing.T, h *captureHandler, commandID string, res StepResult, err error) {
				require.NoError(t, err)

				rec, ok := h.find(failureMsg)
				require.True(t, ok, "a skipped compensation must leave a WARN trace")
				assert.Equal(t, slog.LevelWarn, rec.Level)
				assertAttr(t, rec, "instance_id", "i-comp-fail")
				assertAttr(t, rec, "command_id", commandID)
				assertAttr(t, rec, "node_id", "beta")
				assertAttr(t, rec, "scope_id", "")
				assertAttr(t, rec, "error", "undo-beta blew up")

				require.Len(t, res.State.Incidents, 1, "exactly one incident per failed record")
				inc := res.State.Incidents[0]
				assert.Equal(t, IncidentCompensationFailed, inc.Kind)
				assert.Empty(t, inc.TokenID,
					"walk-scoped: the walk is not driven by a token of its own")
				assert.Equal(t, "beta", inc.NodeID)
				assert.Empty(t, inc.ScopeID, "the root-scope walk's cursor carries no scope")
				assert.Equal(t, commandID, inc.CommandID, "it names the dispatch that failed")
				assert.Equal(t, "undo-beta blew up", inc.Error)
				assert.Equal(t, failedAt, inc.CreatedAt)

				assert.Equal(t, "undo-alpha", invokeActionName(res.Commands),
					"ADR-0034 Decision 4 is preserved: the walk skips the record and advances")
			},
		},
		{
			name: "a vanished record source raises the incident with no node, and does not panic",
			def:  twoCompensableDef(),
			at:   failedAt,
			setup: func(t *testing.T, _ *model.ProcessDefinition, at time.Time) (InstanceState, string) {
				// A walk persisted before ADR-0171: no pinned Records, and the scope
				// its ScopeID names has since been closed, so cursorRecords falls back
				// to a live read that now returns nothing. NextIndex still says 1.
				// This is the shape that panics an unguarded records[NextIndex].
				state := InstanceState{
					InstanceID: "i-comp-fail-vanished",
					DefID:      "p-compensation-failure-visibility",
					DefVersion: 1,
					Status:     StatusCompensating,
					StartedAt:  at,
					Compensating: compensationCursor{
						ScopeID:          "vanished-s1",
						ResumeNode:       "end",
						StartRecordCount: 2,
						NextIndex:        1,
						ActiveCmdID:      "vanished-c9",
					},
				}
				require.Empty(t, cursorRecords(&state, state.Compensating),
					"control: len(records)==0 while NextIndex==1 — the shape that panics")
				return state, "vanished-c9"
			},
			assert: func(t *testing.T, h *captureHandler, commandID string, res StepResult, err error) {
				require.NoError(t, err)

				rec, ok := h.find(failureMsg)
				require.True(t, ok, "an unreadable record must NOT cost the operator the trace")
				assertAttr(t, rec, "node_id", "")
				assertAttr(t, rec, "scope_id", "vanished-s1")

				require.Len(t, res.State.Incidents, 1)
				inc := res.State.Incidents[0]
				assert.Equal(t, IncidentCompensationFailed, inc.Kind)
				assert.Empty(t, inc.NodeID,
					"out of range: raise the incident with no node rather than panicking")
				assert.Equal(t, "vanished-s1", inc.ScopeID)
				assert.Equal(t, commandID, inc.CommandID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, commandID := tc.setup(t, tc.def, tc.at)
			handler := installCaptureHandler(t)

			var res StepResult
			var err error
			require.NotPanics(t, func() {
				res, err = Step(t.Context(), tc.def, state,
					NewActionFailed(tc.at, commandID, "undo-beta blew up", true), StepOptions{})
			})
			tc.assert(t, handler, commandID, res, err)
		})
	}
}

// assertAttr asserts that r carries key with exactly want.
func assertAttr(t *testing.T, r slog.Record, key, want string) {
	t.Helper()
	got, ok := attrString(r, key)
	assert.True(t, ok, "expected a %q attribute", key)
	assert.Equal(t, want, got, "attribute %q", key)
}

// invokeActionName returns the Name of the single pending InvokeAction in cmds,
// or "" when there is none.
func invokeActionName(cmds []Command) string {
	for _, c := range cmds {
		if ia, ok := c.(InvokeAction); ok && !ia.FireAndForget {
			return ia.Name
		}
	}
	return ""
}
