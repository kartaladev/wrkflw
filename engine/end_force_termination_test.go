package engine_test

// end_force_termination_test.go — behaviour test for the force-termination end
// event (ADR-0119). When a token enters an EndEvent whose ForceTermination is
// true, the engine must cancel ALL remaining parallel work (open tasks, timers,
// arms/boundaries, event sub-process arms) and end the instance at the
// outcome-selected terminal status:
//
//   - OutcomeAbort    → StatusTerminated + FailInstance{Err: reason}
//   - OutcomeComplete → StatusCompleted  + CompleteInstance{Result: vars}
//
// The def models a parallel fork whose branch A parks a user task and whose
// branch B is the force-termination end. Branch A is listed first so its token
// is driven (and its task created) BEFORE branch B force-terminates — proving
// the sweep cancels the sibling's already-open work.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// firstCommand returns the first command of concrete type T in cmds, and
// whether one was found.
func firstCommand[T engine.Command](cmds []engine.Command) (T, bool) {
	for _, c := range cmds {
		if v, ok := c.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func TestForceTerminationOutcome(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)

	// forceDef builds: start → fork(parallel) → { [0] user (parks), [1] halt (end, endOpt) }.
	// Branch A (user) is the first outgoing flow so its token is placed and driven
	// first: it parks and creates an open human task. Branch B (halt) is driven
	// next and force-terminates, sweeping the parked task.
	forceDef := func(endOpt event.EndOption) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "p-force-term", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				gateway.NewParallel("fork"),
				activity.NewUserTask("user", activity.WithEligibleRoles("r")),
				event.NewEnd("halt", endOpt),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f0", Source: "start", Target: "fork"},
				// user branch FIRST so it is driven before halt.
				{ID: "f1", Source: "fork", Target: "user"},
				{ID: "f2", Source: "fork", Target: "halt"},
			},
		}
	}

	cases := []struct {
		name   string
		endOpt event.EndOption
		assert func(t *testing.T, res engine.StepResult)
	}{
		{
			name:   "abort",
			endOpt: event.WithForceTermination("fraud", event.OutcomeAbort),
			assert: func(t *testing.T, res engine.StepResult) {
				assert.Equal(t, engine.StatusTerminated, res.State.Status,
					"abort force-termination ends at StatusTerminated")

				fail, ok := firstCommand[engine.FailInstance](res.Commands)
				require.True(t, ok, "a FailInstance command must be emitted")
				assert.Equal(t, "fraud", fail.Err, "FailInstance carries the termination reason")

				_, hasComplete := firstCommand[engine.CompleteInstance](res.Commands)
				assert.False(t, hasComplete, "no CompleteInstance on abort")

				assertSiblingTaskCancelled(t, res)
				assertParallelWorkSwept(t, res)
			},
		},
		{
			name:   "complete",
			endOpt: event.WithForceTermination("enough", event.OutcomeComplete),
			assert: func(t *testing.T, res engine.StepResult) {
				assert.Equal(t, engine.StatusCompleted, res.State.Status,
					"complete force-termination ends at StatusCompleted")

				done, ok := firstCommand[engine.CompleteInstance](res.Commands)
				require.True(t, ok, "a CompleteInstance command must be emitted")
				require.NotNil(t, done.Result, "CompleteInstance carries the instance vars")
				assert.EqualValues(t, 100, done.Result["amount"],
					"CompleteInstance.Result carries the instance variables")

				_, hasFail := firstCommand[engine.FailInstance](res.Commands)
				assert.False(t, hasFail, "no FailInstance on complete")

				assertSiblingTaskCancelled(t, res)
				assertParallelWorkSwept(t, res)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := forceDef(tc.endOpt)

			// One Step in Macro mode drives the whole fork: branch A parks (creates
			// an open task), branch B force-terminates and sweeps it.
			res, err := engine.Step(def, engine.InstanceState{InstanceID: "i-force"},
				engine.NewStartInstance(at, map[string]any{"amount": 100}), engine.StepOptions{})
			require.NoError(t, err)

			// Precondition sanity: branch A must have created an open task before the
			// force path ran, otherwise the sweep assertion is vacuous.
			require.Len(t, res.State.Tasks, 1, "branch A must have created a human task")

			tc.assert(t, res)
		})
	}
}

// assertSiblingTaskCancelled verifies branch A's parked user task was reconciled
// to Cancelled — both in the returned state and via an emitted UpdateTask.
func assertSiblingTaskCancelled(t *testing.T, res engine.StepResult) {
	t.Helper()
	require.Len(t, res.State.Tasks, 1)
	assert.Equal(t, humantask.Cancelled, res.State.Tasks[0].State,
		"the sibling's open task must be Cancelled in the terminated state")

	uts := findUpdateTasks(res.Commands)
	require.Len(t, uts, 1, "exactly one UpdateTask must cancel the open sibling task")
	assert.Equal(t, humantask.Cancelled, uts[0].Task.State)
}

// assertParallelWorkSwept verifies all remaining parallel work was dropped: no
// live tokens and no leftover armed timers/boundaries/arms.
func assertParallelWorkSwept(t *testing.T, res engine.StepResult) {
	t.Helper()
	assert.Empty(t, res.State.Tokens, "all tokens must be dropped on force-termination")
	assert.Empty(t, res.State.Timers, "no leftover timers")
	assert.Empty(t, res.State.ArmedEvents, "no leftover armed events")
	assert.Empty(t, res.State.Boundaries, "no leftover boundary arms")
	require.NotNil(t, res.State.EndedAt, "EndedAt must be set on force-termination")
}
