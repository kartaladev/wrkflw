package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// TestBaseline_HumanClaimedOnCompletedInstance characterizes the completed /
// HumanClaimed row of openspec/specs/human-task-lifecycle "Terminal instance":
// an instance that COMPLETED (rather than failed or terminated) and still
// records its human task refuses a HumanClaimed for that task with
// engine.ErrInstanceTerminal.
func TestBaseline_HumanClaimedOnCompletedInstance(t *testing.T) {
	t.Parallel()

	def := openTaskDef()
	st := startParkedOnTask(t, def, "i-baseline-claim-completed").State
	taskID := st.Tasks[0].TaskID

	r, err := engine.Step(t.Context(), def, st,
		engine.NewHumanClaimed(taskLifetimeT0.Add(time.Minute), taskID, taskLifetimeActor), engine.StepOptions{})
	require.NoError(t, err)
	r, err = engine.Step(t.Context(), def, r.State,
		engine.NewHumanCompleted(taskLifetimeT0.Add(2*time.Minute), taskID, engine.CompletionInput{}, taskLifetimeActor),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, r.State.Status, "setup: completing the only task completes the instance")
	require.Len(t, r.State.Tasks, 1, "setup: the completed instance still records the human task")
	require.Equal(t, humantask.Completed, r.State.Tasks[0].State)

	_, err = engine.Step(t.Context(), def, r.State,
		engine.NewHumanClaimed(taskLifetimeT0.Add(3*time.Minute), taskID, taskLifetimeActor), engine.StepOptions{})

	require.ErrorIs(t, err, engine.ErrInstanceTerminal)
	assert.NotErrorIs(t, err, engine.ErrTaskNotOpen,
		"dispatch's instance-status guard answers before the task-lifetime guard")
}
