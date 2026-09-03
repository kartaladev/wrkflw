package engine_test

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

// outcomeTaskDef returns a linear definition whose single user-task node carries
// the given options, so each case can vary only the outcome declaration.
//
//	Start → UserTask(approve, opts...) → End
func outcomeTaskDef(opts ...activity.UserTaskOption) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-outcome", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", opts...),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// TestHumanCompletedOutcomeValidation covers the fail-closed completion guards.
// A declared outcome set is closed AND mandatory: an outcome outside it
// is rejected with ErrInvalidOutcome and a missing one with ErrOutcomeRequired.
// An empty declaration leaves the task unconstrained. A wait-mode manual task —
// which declares no outcomes and would therefore fail OPEN — rejects an outcome
// or a note exactly as it already rejects an output, and is never required to
// supply one.
func TestHumanCompletedOutcomeValidation(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	doneAt := at.Add(5 * time.Minute)
	actor := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	type testCase struct {
		name       string
		nodeOpts   []activity.UserTaskOption
		completion engine.CompletionInput
		assert     func(t *testing.T, res engine.StepResult, err error)
	}

	// rejected asserts the trigger was refused with sentinel and that nothing
	// leaked out of the failed step.
	rejected := func(sentinel error) func(*testing.T, engine.StepResult, error) {
		return func(t *testing.T, res engine.StepResult, err error) {
			require.ErrorIs(t, err, sentinel)
			assert.Empty(t, res.Commands, "a rejected completion must emit no commands")
			assert.Empty(t, res.State.InstanceID, "a rejected completion must return the zero result")
		}
	}

	// completedWith asserts the task closed and audited the given outcome.
	completedWith := func(outcome string) func(*testing.T, engine.StepResult, error) {
		return func(t *testing.T, res engine.StepResult, err error) {
			require.NoError(t, err)
			require.Len(t, res.State.Tasks, 1)
			require.NotNil(t, res.State.Tasks[0].Completion)
			assert.Equal(t, outcome, res.State.Tasks[0].Completion.Outcome)
		}
	}

	cases := []testCase{
		{
			name:       "an outcome inside the declared set is accepted",
			nodeOpts:   []activity.UserTaskOption{activity.WithOutcomes("approve", "reject")},
			completion: engine.CompletionInput{Outcome: "approve", Output: map[string]any{"ok": true}},
			assert:     completedWith("approve"),
		},
		{
			name:       "an outcome outside the declared set is rejected",
			nodeOpts:   []activity.UserTaskOption{activity.WithOutcomes("approve", "reject")},
			completion: engine.CompletionInput{Outcome: "escalate", Output: map[string]any{"ok": true}},
			assert:     rejected(engine.ErrInvalidOutcome),
		},
		{
			name:       "a node declaring no outcomes accepts any outcome",
			completion: engine.CompletionInput{Outcome: "whatever-the-caller-sends"},
			assert:     completedWith("whatever-the-caller-sends"),
		},
		{
			name:       "an empty outcome is rejected when the node declares outcomes",
			nodeOpts:   []activity.UserTaskOption{activity.WithOutcomes("approve", "reject")},
			completion: engine.CompletionInput{Output: map[string]any{"ok": true}},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				rejected(engine.ErrOutcomeRequired)(t, res, err)
				assert.NotErrorIs(t, err, engine.ErrInvalidOutcome,
					"a missing outcome is a distinct failure from an undeclared one")
			},
		},
		{
			// model.Validate rejects Manual+Outcomes (ErrManualTaskOutcome), so this
			// definition can only reach the engine unvalidated. The engine must still
			// not demand an outcome from a task that is forbidden to carry one.
			name:       "a wait-mode manual task never requires an outcome",
			nodeOpts:   []activity.UserTaskOption{activity.WithManual(false), activity.WithOutcomes("approve")},
			completion: engine.CompletionInput{},
			assert:     completedWith(""),
		},
		{
			name:       "a wait-mode manual task rejects an outcome",
			nodeOpts:   []activity.UserTaskOption{activity.WithManual(false)},
			completion: engine.CompletionInput{Outcome: "approve"},
			assert:     rejected(engine.ErrManualTaskPayload),
		},
		{
			name:       "a wait-mode manual task rejects a note",
			nodeOpts:   []activity.UserTaskOption{activity.WithManual(false)},
			completion: engine.CompletionInput{Note: "did it by hand"},
			assert:     rejected(engine.ErrManualTaskPayload),
		},
		{
			name:       "a wait-mode manual task still rejects an output",
			nodeOpts:   []activity.UserTaskOption{activity.WithManual(false)},
			completion: engine.CompletionInput{Output: map[string]any{"ok": true}},
			assert:     rejected(engine.ErrManualTaskPayload),
		},
		{
			name:       "a bare manual completion is accepted",
			nodeOpts:   []activity.UserTaskOption{activity.WithManual(false)},
			completion: engine.CompletionInput{},
			assert:     completedWith(""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := outcomeTaskDef(tc.nodeOpts...)
			state := startUserTask(t, def, at).State
			require.Len(t, state.Tasks, 1, "precondition: the user task must be parked")
			taskID := state.Tasks[0].TaskID

			res, err := engine.Step(t.Context(), def, state,
				engine.NewHumanCompleted(doneAt, taskID, tc.completion, actor), engine.StepOptions{})
			tc.assert(t, res, err)
		})
	}
}

// TestHumanCompletedOutcomeExposure covers the hybrid opt-in projection: an
// explicit OutcomeVariable wins, ExposeOutcome falls back to the
// "<node id>_outcome" convention, and a node opting into neither keeps the
// outcome audit-only. The projection runs after the output merge, so it wins a
// key collision, and the value written is the outcome string itself.
func TestHumanCompletedOutcomeExposure(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	doneAt := at.Add(5 * time.Minute)
	actor := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	// conventionVar is the variable name ExposeOutcome publishes under for the
	// "approve" node of outcomeTaskDef.
	const conventionVar = "approve_outcome"

	type testCase struct {
		name       string
		nodeOpts   []activity.UserTaskOption
		completion engine.CompletionInput
		assert     func(t *testing.T, res engine.StepResult)
	}

	cases := []testCase{
		{
			name: "an explicit outcome variable wins over the convention",
			nodeOpts: []activity.UserTaskOption{
				activity.WithOutcomes("approve", "reject"),
				activity.WithExposeOutcome(),
				activity.WithOutcomeVariable("decision"),
			},
			completion: engine.CompletionInput{Outcome: "approve"},
			assert: func(t *testing.T, res engine.StepResult) {
				assert.Equal(t, "approve", res.State.Variables["decision"])
				assert.NotContains(t, res.State.Variables, conventionVar,
					"the explicit name replaces the convention, it does not add to it")
			},
		},
		{
			name: "expose-outcome publishes under the node-id convention",
			nodeOpts: []activity.UserTaskOption{
				activity.WithOutcomes("approve", "reject"),
				activity.WithExposeOutcome(),
			},
			completion: engine.CompletionInput{Outcome: "reject"},
			assert: func(t *testing.T, res engine.StepResult) {
				assert.Equal(t, "reject", res.State.Variables[conventionVar],
					"the exposed value is the outcome string, not a wrapper")
			},
		},
		{
			name:       "without an opt-in the outcome stays audit-only",
			nodeOpts:   []activity.UserTaskOption{activity.WithOutcomes("approve", "reject")},
			completion: engine.CompletionInput{Outcome: "approve"},
			assert: func(t *testing.T, res engine.StepResult) {
				assert.NotContains(t, res.State.Variables, conventionVar)
				assert.NotContains(t, res.State.Variables, "decision")
				require.Len(t, res.State.Tasks, 1)
				require.NotNil(t, res.State.Tasks[0].Completion)
				assert.Equal(t, "approve", res.State.Tasks[0].Completion.Outcome,
					"audit is recorded independently of exposure")
			},
		},
		{
			// Defence in depth: model.Validate rejects exposure without a declared
			// set (ErrOutcomeExposureWithoutOutcomes) and a declared set makes the
			// outcome mandatory (ErrOutcomeRequired), so a validated definition can
			// no longer reach applyOutcomeExposure with a blank outcome. Step does
			// not re-validate, so the guard must still hold for an unvalidated one.
			name: "an empty outcome writes no variable",
			nodeOpts: []activity.UserTaskOption{
				activity.WithExposeOutcome(),
				activity.WithOutcomeVariable("decision"),
			},
			completion: engine.CompletionInput{Output: map[string]any{"ok": true}},
			assert: func(t *testing.T, res engine.StepResult) {
				assert.NotContains(t, res.State.Variables, "decision")
				assert.NotContains(t, res.State.Variables, conventionVar)
				assert.Equal(t, true, res.State.Variables["ok"], "the output merge still applies")
			},
		},
		{
			name: "the outcome wins over an output targeting the same variable",
			nodeOpts: []activity.UserTaskOption{
				activity.WithOutcomes("approve", "reject"),
				activity.WithOutcomeVariable("decision"),
			},
			completion: engine.CompletionInput{
				Outcome: "approve",
				Output:  map[string]any{"decision": "reject"},
			},
			assert: func(t *testing.T, res engine.StepResult) {
				assert.Equal(t, "approve", res.State.Variables["decision"],
					"the projection runs after the output merge")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := outcomeTaskDef(tc.nodeOpts...)
			state := startUserTask(t, def, at).State
			require.Len(t, state.Tasks, 1, "precondition: the user task must be parked")
			taskID := state.Tasks[0].TaskID

			res, err := engine.Step(t.Context(), def, state,
				engine.NewHumanCompleted(doneAt, taskID, tc.completion, actor), engine.StepOptions{})
			require.NoError(t, err)
			tc.assert(t, res)
		})
	}
}
