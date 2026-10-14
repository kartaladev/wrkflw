package engine_test

// baseline_process_variables_test.go — characterization tests for rows of the
// process-variables capability spec (openspec/specs/process-variables) that no
// earlier test asserted: a payload delivered to a completed or terminated
// instance is not merged, and a target reverse whose start-of-visit snapshot is
// empty leaves the variables untouched.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// haltBesideWaitDef ends an instance with the given force-termination outcome
// while a sibling branch has parked a token on wait:
//
//	start → fork ⇉ { wait → end-a ; halt(force termination, outcome) }
//
// OutcomeComplete ends the instance StatusCompleted, OutcomeAbort ends it
// StatusTerminated; either way forceTerminate drops every token.
func haltBesideWaitDef(id string, wait model.Node, outcome event.TerminationOutcome) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: id, Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			wait,
			event.NewEnd("end-a"),
			event.NewEnd("halt", event.WithForceTermination("baseline halt", outcome)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: wait.ID()},
			{ID: "f3", Source: "fork", Target: "halt"},
			{ID: "f4", Source: wait.ID(), Target: "end-a"},
		},
	}
}

// TestBaselineDroppedPayloadIsNotMerged covers the completed and terminated rows
// of "A dropped payload is not merged".
//
// The "reached naturally" rows end the instance through a force-termination end
// event, which drops every token, so no token awaits the trigger at all. Those
// rows are the spec's literal Given, but they would stay green even without the
// terminal guard. The "surviving token" rows make the guard load-bearing: they
// take the failed fixture whose wait token is still parked on its waiter
// (terminalWithSurvivingToken) and stamp the completed or terminated status on
// it, so only the terminal STATUS stands between the payload and a merge. Every
// row starts from non-empty variables, so "unchanged" cannot be satisfied by an
// emptied map.
func TestBaselineDroppedPayloadIsNotMerged(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	payload := map[string]any{"x": 1}
	startVars := map[string]any{"v": "start"}

	signalCatch := func() model.Node {
		return event.NewIntermediateCatch("await", event.WithSignalName("approved"))
	}
	messageCatch := func() model.Node {
		return event.NewIntermediateCatch("await", event.WithMessageCorrelator("approved-msg", ""))
	}
	signal := func() engine.Trigger {
		return engine.NewSignalReceived(at.Add(time.Hour), "approved", payload)
	}
	message := func() engine.Trigger {
		return engine.NewMessageReceived(at.Add(time.Hour), "approved-msg", "", payload)
	}

	// reached drives haltBesideWaitDef from start to its terminal status.
	reached := func(wait func() model.Node, outcome event.TerminationOutcome, want engine.Status) func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
		return func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
			t.Helper()
			def := haltBesideWaitDef("p-baseline-halt", wait(), outcome)
			r, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-baseline-halt"},
				engine.NewStartInstance(at, startVars), engine.StepOptions{})
			require.NoError(t, err)
			require.Equal(t, want, r.State.Status, "fixture: the halt end event must end the instance")
			return def, r.State
		}
	}

	// stamped takes the failed fixture, whose wait token survives, and gives it
	// the requested terminal status.
	stamped := func(def func() *model.ProcessDefinition, status engine.Status) func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
		return func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
			t.Helper()
			d := def()
			st := terminalWithSurvivingToken(t, d)
			st.Status = status
			st.Variables = map[string]any{"v": "start"}
			return d, st
		}
	}

	type testCase struct {
		name    string
		state   func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState)
		trigger func() engine.Trigger
		assert  func(t *testing.T, before engine.InstanceState, r engine.StepResult, err error)
	}

	assertDropped := func(t *testing.T, before engine.InstanceState, r engine.StepResult, err error) {
		t.Helper()
		require.NoError(t, err, "a trigger against a terminal instance is dropped silently")
		assert.Empty(t, r.Commands, "a dropped trigger dispatches nothing")
		after := committedAfter(before, r, err)
		assert.Equal(t, map[string]any{"v": "start"}, after.Variables,
			"a dropped payload must not be merged into the instance variables")
		assert.Equal(t, before.Status, after.Status, "a dropped trigger leaves the terminal status alone")
	}

	cases := []testCase{
		{
			name:    "completed instance (reached naturally), signal",
			state:   reached(signalCatch, event.OutcomeComplete, engine.StatusCompleted),
			trigger: signal,
			assert:  assertDropped,
		},
		{
			name:    "completed instance (reached naturally), message",
			state:   reached(messageCatch, event.OutcomeComplete, engine.StatusCompleted),
			trigger: message,
			assert:  assertDropped,
		},
		{
			name:    "terminated instance (reached naturally), signal",
			state:   reached(signalCatch, event.OutcomeAbort, engine.StatusTerminated),
			trigger: signal,
			assert:  assertDropped,
		},
		{
			name:    "terminated instance (reached naturally), message",
			state:   reached(messageCatch, event.OutcomeAbort, engine.StatusTerminated),
			trigger: message,
			assert:  assertDropped,
		},
		{
			name:    "completed instance with a surviving awaiting token, signal",
			state:   stamped(terminalGuardSignalDef, engine.StatusCompleted),
			trigger: signal,
			assert:  assertDropped,
		},
		{
			name:    "completed instance with a surviving awaiting token, message",
			state:   stamped(terminalGuardMessageDef, engine.StatusCompleted),
			trigger: message,
			assert:  assertDropped,
		},
		{
			name:    "terminated instance with a surviving awaiting token, signal",
			state:   stamped(terminalGuardSignalDef, engine.StatusTerminated),
			trigger: signal,
			assert:  assertDropped,
		},
		{
			name:    "terminated instance with a surviving awaiting token, message",
			state:   stamped(terminalGuardMessageDef, engine.StatusTerminated),
			trigger: message,
			assert:  assertDropped,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def, before := tc.state(t)
			r, err := engine.Step(t.Context(), def, before, tc.trigger(), engine.StepOptions{})
			tc.assert(t, before, r, err)
		})
	}
}

// TestBaselineReverseToNodeWithEmptySnapshotKeepsVariables covers "An empty
// start-of-visit snapshot leaves the variables untouched": an instance started
// with no variables records step1 with an empty Input, and a target reverse to
// step1 must not replace the variables set since with that empty snapshot.
func TestBaselineReverseToNodeWithEmptySnapshotKeepsVariables(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	def := threeCompensableDef()

	r, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-empty-snapshot"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	for _, step := range []struct {
		action string
		output map[string]any
	}{
		{action: "a1", output: map[string]any{"a": 2}},
		{action: "a2", output: nil},
		{action: "a3", output: nil},
	} {
		r, err = engine.Step(t.Context(), def, r.State,
			engine.NewActionCompleted(at, findInvokeActionID(t, r.Commands, step.action), step.output),
			engine.StepOptions{})
		require.NoError(t, err)
	}
	require.Equal(t, engine.StatusRunning, r.State.Status, "fixture: parked on the user task")
	require.NotEmpty(t, r.State.RootCompensations, "fixture: step1 was recorded")
	require.Equal(t, "step1", r.State.RootCompensations[0].NodeID, "fixture: step1 is the first record")
	require.Empty(t, r.State.RootCompensations[0].Input, "fixture: step1's start-of-visit snapshot is empty")
	require.Equal(t, 2, r.State.Variables["a"], "fixture: a later step set a = 2")

	// Deliver the target reverse, then complete each compensation undo action
	// until the walk finishes.
	r, err = engine.Step(t.Context(), def, r.State, engine.NewReverseToNode(at, "step1"), engine.StepOptions{})
	require.NoError(t, err)
	for r.State.Status == engine.StatusCompensating {
		var undoID string
		for _, c := range r.Commands {
			if ia, ok := c.(engine.InvokeAction); ok {
				undoID = ia.CommandID
			}
		}
		require.NotEmpty(t, undoID, "a compensation InvokeAction must be in flight while compensating")
		r, err = engine.Step(t.Context(), def, r.State, engine.NewActionCompleted(at, undoID, nil), engine.StepOptions{})
		require.NoError(t, err)
	}

	assert.Equal(t, engine.StatusRunning, r.State.Status, "the walk finished and the instance resumed")
	require.Len(t, r.State.Tokens, 1)
	assert.Equal(t, "step1", r.State.Tokens[0].NodeID, "the instance resumed at the target")
	assert.Equal(t, 2, r.State.Variables["a"], "an empty snapshot must leave the variable a = 2 untouched")
}
