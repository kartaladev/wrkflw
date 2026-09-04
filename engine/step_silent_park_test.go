package engine_test

// step_silent_park_test.go — the remaining silent token-park sites, worked
// against the diagnostic policy written down on raiseDefinitionDefect
// (engine/step_nodes.go). #38 settled the shape for the trigger-less catch
// event; these are its structural twins.
//
// Each site here strands its token permanently: nothing is scheduled, no
// trigger can resume it, and re-driving lands on the same node. Under the
// policy that is the "stuck" class, which owes an IncidentDefinitionDefect
// rather than a WARN.

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
)

// triggerlessThrowDef returns a linear definition whose intermediate throw
// declares no signal — the only trigger an IntermediateThrowEvent carries:
//
//	Start → IntermediateThrow(no signal) → ServiceTask(complete) → End
//
// A struct literal deliberately, exactly as triggerlessCatchDef is: this is the
// shape that reaches the engine unvalidated.
func triggerlessThrowDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-triggerless-throw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateThrow("throw-nothing"),
			activity.NewServiceTask("complete", activity.WithTaskAction("complete-action")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "throw-nothing"},
			{ID: "f2", Source: "throw-nothing", Target: "complete"},
			{ID: "f3", Source: "complete", Target: "end"},
		},
	}
}

// TestTriggerlessThrowRaisesIncident is candidate 1 of #54, and the closest twin
// of the trigger-less catch #38 fixed. intermediateThrowEventStrategy's else
// branch parked the token and said nothing — the comment called it "parks for
// future plans", but a plan is not a diagnostic, and the instance stayed Running
// forever looking exactly like one legitimately waiting.
//
// Unlike the catch, this shape was not even rejected at authoring time: there was
// no KindIntermediateThrowEvent rule in validate.go at all. Both halves land
// together, mirroring #38 — model.ErrThrowEventMissingTrigger for the authoring
// route, IncidentDefinitionDefect for the definition that skips it.
func TestTriggerlessThrowRaisesIncident(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

	start := func(t *testing.T) engine.StepResult {
		t.Helper()
		r, err := engine.Step(t.Context(), triggerlessThrowDef(),
			engine.InstanceState{InstanceID: "triggerless-throw-1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		return r
	}

	cases := []struct {
		name   string
		assert func(t *testing.T, r engine.StepResult)
	}{
		{
			name: "an incident is raised naming the throw node",
			assert: func(t *testing.T, r engine.StepResult) {
				require.Len(t, r.State.Incidents, 1)
				inc := r.State.Incidents[0]
				assert.Equal(t, engine.IncidentDefinitionDefect, inc.Kind)
				assert.Equal(t, "throw-nothing", inc.NodeID)
				assert.NotEmpty(t, inc.ID)
				assert.Equal(t, at, inc.CreatedAt)
				assert.Contains(t, inc.Error, "signal",
					"the incident must say what is missing, not merely that something is")
			},
		},
		{
			name: "the token is parked as an incident, not silently waiting",
			assert: func(t *testing.T, r engine.StepResult) {
				require.Len(t, r.State.Tokens, 1)
				tok := r.State.Tokens[0]
				assert.Equal(t, engine.TokenIncident, tok.State)
				assert.Equal(t, "throw-nothing", tok.NodeID)
				require.Len(t, r.State.Incidents, 1)
				assert.Equal(t, tok.ID, r.State.Incidents[0].TokenID)
			},
		},
		{
			name: "the instance stays running and schedules no work",
			assert: func(t *testing.T, r engine.StepResult) {
				assert.Equal(t, engine.StatusRunning, r.State.Status)
				assert.Empty(t, r.Commands,
					"a dead throw must not broadcast a signal or invoke the downstream action")
			},
		},
		{
			name: "cancellation still terminates the instance",
			assert: func(t *testing.T, r engine.StepResult) {
				r2, err := engine.Step(t.Context(), triggerlessThrowDef(), r.State,
					engine.NewCancelRequested(at.Add(time.Minute)), engine.StepOptions{})
				require.NoError(t, err)
				assert.Equal(t, engine.StatusTerminated, r2.State.Status,
					"an operator must be able to retire an instance carrying a defect incident")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, start(t))
		})
	}
}

// nilSubprocessDef returns a definition whose KindSubProcess node carries no
// nested definition:
//
//	Start → SubProcess(nil) → End
//
// model.ErrMissingSubprocess rejects this, which is precisely the argument #38
// made for the catch event: forbidden at authoring time, reachable anyway
// through a registry that cannot reject, and silent when it is.
func nilSubprocessDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-nil-subprocess", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", nil),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sp"},
			{ID: "f2", Source: "sp", Target: "end"},
		},
	}
}

// TestNilSubprocessRaisesIncident is candidate 2 of #54. subProcessStrategy's
// nil-Subprocess branch parks the token to avoid an infinite drive loop and its
// comment notes "model.Validate prevents this" — true, and the same was true of
// the catch event. Validation is the authoring gate, not the only door: a
// definition assembled through kernel.NewMapDefinitionRegistry, or handed
// straight to engine.Step, never passes it.
//
// The park itself is right — the alternative is a spin — so what changes is only
// that it now says so.
func TestNilSubprocessRaisesIncident(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

	start := func(t *testing.T) engine.StepResult {
		t.Helper()
		r, err := engine.Step(t.Context(), nilSubprocessDef(),
			engine.InstanceState{InstanceID: "nil-sp-1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		return r
	}

	cases := []struct {
		name   string
		assert func(t *testing.T, r engine.StepResult)
	}{
		{
			name: "an incident is raised naming the sub-process node",
			assert: func(t *testing.T, r engine.StepResult) {
				require.Len(t, r.State.Incidents, 1)
				inc := r.State.Incidents[0]
				assert.Equal(t, engine.IncidentDefinitionDefect, inc.Kind)
				assert.Equal(t, "sp", inc.NodeID)
				assert.Equal(t, at, inc.CreatedAt)
				assert.Contains(t, inc.Error, "nested definition",
					"the incident must name what the sub-process is missing")
			},
		},
		{
			name: "the token is parked as an incident, not silently waiting",
			assert: func(t *testing.T, r engine.StepResult) {
				require.Len(t, r.State.Tokens, 1)
				tok := r.State.Tokens[0]
				assert.Equal(t, engine.TokenIncident, tok.State)
				assert.Equal(t, "sp", tok.NodeID)
			},
		},
		{
			name: "no scope is opened and no work is scheduled",
			assert: func(t *testing.T, r engine.StepResult) {
				assert.Empty(t, r.Commands,
					"a sub-process with no nested definition has nothing to run")
				assert.Equal(t, engine.StatusRunning, r.State.Status)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, start(t))
		})
	}
}
