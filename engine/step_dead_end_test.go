package engine_test

// step_dead_end_test.go — a token routed onto a node with no outgoing flow.
//
// moveAlongSingleFlow parks such a token and, until this file, said nothing:
// no incident, no log, no await field. Under the policy on raiseDefinitionDefect
// that is the "stuck" class — nothing is scheduled, no trigger can resume it,
// and re-driving lands on the same node — and a missing outgoing flow is the
// operator's own definition, so the blame exclusion does not apply. It earns an
// IncidentDefinitionDefect.
//
// model.ErrDeadEnd forbids the shape at authoring time (exempting only end
// events and event-sub-process roots), so these definitions are struct literals:
// that is the door they come through.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// deadEndAfter wraps node so that a token reaching it can never leave: node has
// an incoming flow from the start event and no outgoing flow at all.
//
//	start → node ⊣
func deadEndAfter(node model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-dead-end", Version: 1,
		Nodes: []model.Node{event.NewStart("start"), node},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "dead"}},
	}
}

// TestDeadEndParkRaisesIncident is #78's acceptance test. Its first case is the
// reproduction the ticket was filed on, verbatim: a service task with no
// outgoing flow, driven to completion, leaving the token parked on "dead" with
// no incident, no commands and status Running — permanently stuck and
// indistinguishable from an instance legitimately waiting.
//
// The cases deliberately span both sides of the call-site split, because that is
// what makes this a signature change rather than a strategy fix.
// moveAlongSingleFlow has ten call sites; seven hold a *stepCtx and three
// (resolveGatewayWin, resumeAndDrive, handleHumanCompleted) hold only ctx/s/at.
// The service-task and user-task rows land on the ctx-less resumeAndDrive and
// handleHumanCompleted respectively — so a fix confined to the strategy layer
// would leave both of them silent, including the reproduction itself.
func TestDeadEndParkRaisesIncident(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		def  *model.ProcessDefinition
		// advance drives the instance from its post-start state to the point
		// where the token is routed off "dead". Nil means the start step already
		// strands it.
		advance func(t *testing.T, def *model.ProcessDefinition, r engine.StepResult) engine.StepResult
	}

	// completeAction finds the InvokeAction the node emitted and reports it done,
	// which is what sends the token back through resumeAndDrive.
	completeAction := func(t *testing.T, def *model.ProcessDefinition, r engine.StepResult) engine.StepResult {
		t.Helper()
		var cmdID string
		for _, c := range r.Commands {
			if ia, ok := c.(engine.InvokeAction); ok {
				cmdID = ia.CommandID
			}
		}
		require.NotEmpty(t, cmdID, "expected the node to emit an InvokeAction to complete")
		r2, err := engine.Step(t.Context(), def, r.State,
			engine.NewActionCompleted(at.Add(time.Minute), cmdID, nil), engine.StepOptions{})
		require.NoError(t, err)
		return r2
	}

	// completeTask closes the human task the user-task node opened, which is what
	// sends the token through handleHumanCompleted — the other ctx-less site.
	completeTask := func(t *testing.T, def *model.ProcessDefinition, r engine.StepResult) engine.StepResult {
		t.Helper()
		var taskID string
		for _, c := range r.Commands {
			if ot, ok := c.(engine.AwaitHuman); ok {
				taskID = ot.TaskID
			}
		}
		require.NotEmpty(t, taskID, "expected the user task to open a human task")
		r2, err := engine.Step(t.Context(), def, r.State,
			engine.NewHumanCompleted(at.Add(time.Minute), taskID, engine.CompletionInput{}, authz.Actor{ID: "alice"}),
			engine.StepOptions{})
		require.NoError(t, err)
		return r2
	}

	cases := []testCase{
		{
			name:    "service task with no outgoing flow (the #78 repro, via resumeAndDrive)",
			def:     deadEndAfter(activity.NewServiceTask("dead", activity.WithTaskAction("a"))),
			advance: completeAction,
		},
		{
			name:    "user task with no outgoing flow (via handleHumanCompleted)",
			def:     deadEndAfter(activity.NewUserTask("dead")),
			advance: completeTask,
		},
		{
			name: "signal throw with no outgoing flow (via intermediateThrowEventStrategy)",
			def:  deadEndAfter(event.NewIntermediateThrow("dead", event.WithThrowSignalName("go"))),
		},
		{
			name: "send task with no outgoing flow (via sendTaskStrategy)",
			def:  deadEndAfter(activity.NewSendTask("dead", "m")),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := engine.Step(t.Context(), tc.def,
				engine.InstanceState{InstanceID: "dead-end-1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)
			if tc.advance != nil {
				r = tc.advance(t, tc.def, r)
			}

			require.Len(t, r.State.Tokens, 1)
			tok := r.State.Tokens[0]
			assert.Equal(t, "dead", tok.NodeID, "the token must still be on the node it could not leave")
			assert.Equal(t, engine.TokenIncident, tok.State,
				"a token that can never advance must not sit in TokenWaiting, which is what a legitimate wait looks like")

			require.Len(t, r.State.Incidents, 1)
			inc := r.State.Incidents[0]
			assert.Equal(t, engine.IncidentDefinitionDefect, inc.Kind)
			assert.Equal(t, "dead", inc.NodeID)
			assert.Equal(t, tok.ID, inc.TokenID)
			assert.NotEmpty(t, inc.ID)
			assert.Contains(t, inc.Error, "outgoing flow",
				"the incident must name what the node is missing, not merely that something is")

			assert.Equal(t, engine.StatusRunning, r.State.Status)
		})
	}
}

// TestDeadEndParkCancellable pins the operator's exit path, as #38's precedent
// test does: a defect incident must be a stop, not a wedge.
func TestDeadEndParkCancellable(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	def := deadEndAfter(event.NewIntermediateThrow("dead", event.WithThrowSignalName("go")))

	r, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "dead-end-cancel"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r.State.Incidents, 1)

	r2, err := engine.Step(t.Context(), def, r.State,
		engine.NewCancelRequested(at.Add(time.Minute)), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, r2.State.Status,
		"an operator must be able to retire an instance carrying a defect incident")
}
