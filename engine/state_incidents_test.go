package engine_test

// state_incidents_test.go — incidents are token-attached, so cancelling
// a token must retire them; and every UpdateTask handed to a consumer-supplied
// TaskStore must be a deep copy rather than an alias of committed engine state.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// TestRemoveIncidentsForTokenDropsOnlyThatToken asserts the helper retires the
// incidents raised against one token and leaves every other record in its
// original relative order.
func TestRemoveIncidentsForTokenDropsOnlyThatToken(t *testing.T) {
	t.Parallel()

	s := &engine.InstanceState{
		InstanceID: "i1",
		Incidents: []engine.Incident{
			{ID: "inc1", TokenID: "tokA", NodeID: "n1"},
			{ID: "inc2", TokenID: "tokB", NodeID: "n2"},
			{ID: "inc3", TokenID: "tokA", NodeID: "n3"},
			{ID: "inc4", TokenID: "tokC", NodeID: "n4"},
		},
	}

	engine.RemoveIncidentsForToken(s, "tokA")

	got := make([]string, 0, len(s.Incidents))
	for _, inc := range s.Incidents {
		got = append(got, inc.ID)
	}
	assert.Equal(t, []string{"inc2", "inc4"}, got,
		"tokA's incidents are dropped; the rest keep their order")
}

// TestRemoveIncidentsForTokenIgnoresEmptyID asserts an empty token ID matches
// nothing: an empty key names nothing and must never be treated
// as a wildcard.
func TestRemoveIncidentsForTokenIgnoresEmptyID(t *testing.T) {
	t.Parallel()

	// ⚠ The blank-keyed incident is load-bearing. With only a "tokA" record the
	// test passes with OR without the guard, because slices.DeleteFunc would
	// evaluate "tokA" == "" as false and delete nothing either way — a test that
	// certifies nothing. inc2 is the record the MISSING guard would wipe.
	// (Corrected 2026-08-03 after review; the original fixture here was
	// vacuous.)
	s := &engine.InstanceState{
		InstanceID: "i1",
		Incidents: []engine.Incident{
			{ID: "inc1", TokenID: "tokA"},
			{ID: "inc2", TokenID: ""},
		},
	}

	engine.RemoveIncidentsForToken(s, "")

	assert.Len(t, s.Incidents, 2,
		"an empty token ID matches nothing, not even a blank-keyed incident")
}

// TestCancelOpenTasksEmitsDeepCopy asserts the UpdateTask handed to a
// consumer-supplied TaskStore does not alias the record committed as instance
// state. TaskStore is public API; a store that retains the value verbatim would
// otherwise share the Claim pointee and the Vars map with live engine state.
func TestCancelOpenTasksEmitsDeepCopy(t *testing.T) {
	t.Parallel()

	claimedAt := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	s := &engine.InstanceState{
		InstanceID: "i1",
		Tasks: []humantask.HumanTask{{
			TaskID:     "i1-h1",
			InstanceID: "i1",
			NodeID:     "review",
			State:      humantask.Claimed,
			Claim:      &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: claimedAt},
			Vars:       map[string]any{"k": "v"},
		}},
	}

	cmds := engine.CancelOpenTasks(s)
	require.Len(t, cmds, 1)
	emitted, ok := cmds[0].(engine.UpdateTask)
	require.True(t, ok)

	// Mutate through the emitted command, as a retaining store would.
	emitted.Task.Claim.Actor.ID = "mallory"
	emitted.Task.Vars["k"] = "tampered"

	assert.Equal(t, "alice", s.Tasks[0].Claim.Actor.ID, "Claim pointee must not be shared")
	assert.Equal(t, "v", s.Tasks[0].Vars["k"], "Vars map must not be shared")
}

// claimedTaskWithVars drives def to a UserTask claimed by "alice", carrying a
// {"k":"v"} variable snapshot, and returns the state to step from. It is the
// shared setup for the two aliasing tests below: a claim makes the Claim pointee
// observable and the variable makes the Vars map observable, which are the two
// fields a shallow UpdateTask would share with committed engine state.
func claimedTaskWithVars(t *testing.T, def *model.ProcessDefinition, instanceID string, at time.Time) engine.InstanceState {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(at, map[string]any{"k": "v"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tasks, 1, "setup: the user task must be minted")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanClaimed(at.Add(time.Minute), r1.State.Tasks[0].TaskID, authz.Actor{ID: "alice"}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, humantask.Claimed, r2.State.Tasks[0].State, "setup: the task must be claimed")
	require.Equal(t, "v", r2.State.Tasks[0].Vars["k"], "setup: the task must carry a variable snapshot")

	return r2.State
}

// assertUpdateTaskIsDeepCopy mutates the Claim pointee and the Vars map through
// the single UpdateTask in cmds, as a TaskStore retaining the value verbatim
// would, and asserts the record committed in state is untouched.
func assertUpdateTaskIsDeepCopy(t *testing.T, res engine.StepResult) {
	t.Helper()

	updates := findUpdateTasks(res.Commands)
	require.Len(t, updates, 1, "exactly one UpdateTask must be emitted")
	require.NotNil(t, updates[0].Task.Claim, "the emitted task must carry the claim")

	updates[0].Task.Claim.Actor.ID = "mallory"
	updates[0].Task.Vars["k"] = "tampered"

	require.Len(t, res.State.Tasks, 1)
	assert.Equal(t, "alice", res.State.Tasks[0].Claim.Actor.ID, "Claim pointee must not be shared")
	assert.Equal(t, "v", res.State.Tasks[0].Vars["k"], "Vars map must not be shared")
}

// TestDeadlineBreachEmitsDeepCopy asserts the UpdateTask emitted when a user
// task's deadline expires (engine/step_timers.go) does not alias the record
// committed as instance state. This is the sharpest of the shallow emitters:
// it is one of only three places the engine writes humantask.Cancelled, so it
// handed an aliased record to the store on exactly the teardown path this file
// is about.
func TestDeadlineBreachEmitsDeepCopy(t *testing.T) {
	t.Parallel()

	def := deadlineDef()
	at := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	st := claimedTaskWithVars(t, def, "dl1", at)
	require.Len(t, st.Timers, 1, "setup: the deadline timer must be armed")
	timerID := st.Timers[0].TimerID

	res, err := engine.Step(t.Context(), def, st,
		engine.NewTimerFired(at.Add(3*time.Hour), timerID), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, humantask.Cancelled, res.State.Tasks[0].State,
		"setup: the deadline breach must cancel the task")

	assertUpdateTaskIsDeepCopy(t, res)
}

// TestTaskCompletionEmitsDeepCopy asserts the UpdateTask emitted when a user
// task is completed (engine/step_triggers.go) does not alias the record
// committed as instance state. It stands in for the four handleHuman* emitters,
// which take the identical one-token edit.
func TestTaskCompletionEmitsDeepCopy(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-complete-deepcopy", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("review", activity.WithEligibleRoles("r")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "end"},
		},
	}
	at := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	st := claimedTaskWithVars(t, def, "tc1", at)

	res, err := engine.Step(t.Context(), def, st,
		engine.NewHumanCompleted(at.Add(time.Hour), st.Tasks[0].TaskID,
			engine.CompletionInput{}, authz.Actor{ID: "alice"}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, humantask.Completed, res.State.Tasks[0].State,
		"setup: the task must be completed")

	assertUpdateTaskIsDeepCopy(t, res)
}

// TestScopeTeardownRetiresTokenIncidents asserts that tearing a scope down
// retires the incidents raised against the tokens it cancels. An incident names
// a token (Incident.TokenID); once that token is gone there is nothing left to
// resolve, so leaving the record behind would park an unresolvable incident on a
// completed instance.
//
// It exercises the wiring in cancelTokenWaits through a real teardown path
// (engine/step_errors.go's enclosing-scope walk), not the helper in isolation.
func TestScopeTeardownRetiresTokenIncidents(t *testing.T) {
	t.Parallel()

	inner := &model.ProcessDefinition{
		ID: "inner-incident", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewParallel("pfork"),
			activity.NewServiceTask("flaky", activity.WithTaskAction("flaky-action"),
				activity.WithRetryPolicy(&model.RetryPolicy{MaxAttempts: 1})),
			activity.NewServiceTask("work", activity.WithTaskAction("work-action")),
			gateway.NewParallel("pjoin"),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "pfork"},
			{ID: "if2", Source: "pfork", Target: "flaky"},
			{ID: "if3", Source: "pfork", Target: "work"},
			{ID: "if4", Source: "flaky", Target: "pjoin"},
			{ID: "if5", Source: "work", Target: "pjoin"},
			{ID: "if6", Source: "pjoin", Target: "inner-end"},
		},
	}
	def := &model.ProcessDefinition{
		ID: "outer-incident-teardown", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewBoundary("bnd-sub-err", "sub", event.WithBoundaryErrorCode("E1")),
			event.NewEnd("outer-end"),
			event.NewEnd("end-escalated"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
			{ID: "of3", Source: "bnd-sub-err", Target: "end-escalated"},
		},
	}
	at := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "inc1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	cmdIDs := map[string]string{}
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			cmdIDs[ia.Name] = ia.CommandID
		}
	}
	require.Contains(t, cmdIDs, "flaky-action")
	require.Contains(t, cmdIDs, "work-action")

	// "flaky" exhausts its single attempt with an error no boundary matches, so
	// the raiseIncident policy parks its token on an incident.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionFailed(at.Add(time.Minute), cmdIDs["flaky-action"], "boom", true), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Incidents, 1, "setup: the exhausted branch must raise an incident")

	// "work" fails with the boundary's code: the whole sub-process scope, incident
	// token included, is torn down.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionFailed(at.Add(time.Hour), cmdIDs["work-action"], "E1", false), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Empty(t, r3.State.Incidents,
		"the incident's token was cancelled, so the incident must be retired with it")
}
