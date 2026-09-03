package engine_test

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

// triggerlessCatchDef returns a linear definition whose catch event declares no
// trigger family at all:
//
//	Start → IntermediateCatch(no trigger) → ServiceTask(complete) → End
//
// It is deliberately a struct literal rather than a builder chain.
// model.ErrCatchEventMissingTrigger rejects this shape, and every authoring
// route — definitionCore.Build, and the YAML loader that ends in the same
// Build — runs model.Validate. A raw *model.ProcessDefinition handed to
// runtime.RegisterDefinition does not, and that bypass is exactly the case the
// engine-side guard exists for.
func triggerlessCatchDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-triggerless-catch", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("catch-nothing"),
			activity.NewServiceTask("complete", activity.WithTaskAction("complete-action")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "catch-nothing"},
			{ID: "f2", Source: "catch-nothing", Target: "complete"},
			{ID: "f3", Source: "complete", Target: "end"},
		},
	}
}

// TestTriggerlessCatchRaisesIncident is the runtime half of the trigger-less
// catch fix. Before it, intermediateCatchEventStrategy's final else branch
// parked the token and said nothing: no command, no incident, no log line, and
// no trigger that could ever resume it. The instance stayed Running forever and
// looked identical to one legitimately waiting on a signal.
//
// The catch now raises an IncidentDefinitionDefect instead, which is visible to
// the instance lister and to processtest's park predicates. It is NOT
// resolvable through ResolveIncident — handleResolveIncident whitelists
// IncidentAction alone — because re-driving the token would park it again on
// the same dead node; the fix is to correct the definition and redeploy. The
// last case pins the operator's exit path: cancellation still terminates the
// instance, so a defect incident is a stop, not a wedge.
func TestTriggerlessCatchRaisesIncident(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

	// start drives a fresh instance to the trigger-less catch.
	start := func(t *testing.T) engine.StepResult {
		t.Helper()
		r, err := engine.Step(t.Context(), triggerlessCatchDef(),
			engine.InstanceState{InstanceID: "triggerless-1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		return r
	}

	cases := []struct {
		name   string
		assert func(t *testing.T, r engine.StepResult)
	}{
		{
			name: "an incident is raised naming the catch node",
			assert: func(t *testing.T, r engine.StepResult) {
				require.Len(t, r.State.Incidents, 1)
				inc := r.State.Incidents[0]
				assert.Equal(t, engine.IncidentDefinitionDefect, inc.Kind)
				assert.Equal(t, "catch-nothing", inc.NodeID)
				assert.NotEmpty(t, inc.ID)
				assert.Equal(t, at, inc.CreatedAt)
				assert.Contains(t, inc.Error, "no timer, signal or message trigger",
					"the incident must say what is wrong, not merely that something is")
			},
		},
		{
			name: "the token is parked as an incident, not silently waiting",
			assert: func(t *testing.T, r engine.StepResult) {
				require.Len(t, r.State.Tokens, 1)
				tok := r.State.Tokens[0]
				assert.Equal(t, engine.TokenIncident, tok.State)
				assert.Equal(t, "catch-nothing", tok.NodeID)
				require.Len(t, r.State.Incidents, 1)
				assert.Equal(t, tok.ID, r.State.Incidents[0].TokenID)
			},
		},
		{
			name: "the instance stays running and schedules no work",
			assert: func(t *testing.T, r engine.StepResult) {
				assert.Equal(t, engine.StatusRunning, r.State.Status)
				assert.Empty(t, r.Commands,
					"a dead catch must not schedule a timer or invoke an action")
			},
		},
		{
			name: "cancellation still terminates the instance",
			assert: func(t *testing.T, r engine.StepResult) {
				r2, err := engine.Step(t.Context(), triggerlessCatchDef(), r.State,
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
