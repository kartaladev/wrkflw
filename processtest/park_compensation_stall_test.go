package processtest_test

// A compensation-stall timer must not look like an ordinary armed timer to a
// consumer's test harness.

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
	"github.com/kartaladev/wrkflw/processtest"
)

// stalledCompensationState drives a two-record saga to a cancel-started
// compensation walk with stall detection ON, returning the mid-walk snapshot
// whose only armed timer is the stall guard.
func stalledCompensationState(t *testing.T) engine.InstanceState {
	t.Helper()

	def := &model.ProcessDefinition{
		ID: "stall-park", Version: 1,
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
	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	opt := engine.StepOptions{CompensationStallAfter: 30 * time.Minute}

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), opt)
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		var cmdID string
		for _, c := range res.Commands {
			if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
				cmdID = ia.CommandID
			}
		}
		require.NotEmpty(t, cmdID, "control: %s invoked", name)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmdID, nil), opt)
		require.NoError(t, err)
	}

	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewCancelRequested(at.Add(time.Minute)), opt)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status, "control: a walk is in flight")
	require.NotEmpty(t, res.State.Timers, "control: the walk armed its stall timer")
	return res.State
}

// TestStallTimerIsExcludedFromHasArmedTimers pins the decision this shipped
// consumer harness makes about a stall timer.
//
// A TimerCompensationStall record is a DETECTION deadline, not work the instance
// is waiting to do. Counting it as an armed timer has two consequences, both
// wrong: Classify reports ReasonTimer for an instance that is really parked on a
// compensation walk, and — the damaging one — processtest.AutoTimers() fires the
// stall timer BY ITSELF inside the consumer's harness, manufacturing the very
// incident the window exists to detect.
//
// processtest is shipped to consumers, so this has to be a decision rather than
// a side effect.
func TestStallTimerIsExcludedFromHasArmedTimers(t *testing.T) {
	t.Parallel()

	// Built by the ENGINE rather than hand-assembled: timerRecord is unexported,
	// so a consumer package cannot fabricate a stall record — which is exactly
	// why the exclusion has to live behind engine.InstanceState.HasArmedTimers.
	state := stalledCompensationState(t)
	p := processtest.Classify(state)

	assert.False(t, p.HasArmedTimers,
		"a stall timer is a detection deadline, not work the harness may fire")
	assert.NotEqual(t, processtest.ReasonTimer, p.Reason,
		"and it must not classify the park as a timer park")
}
