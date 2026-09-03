package engine_test

// step_task_lifetime_guard_test.go — the task-lifetime companion guard on the
// three human-task handlers.
//
// The instance-status guard in dispatch closes one key: a trigger arriving after
// the instance died. This file covers the SECOND, independent key it cannot see —
// a task closed while its instance keeps running. That case is reachable:
// an interrupting boundary and a breached deadline both cancel the task attached
// to the token they consume, and the instance carries on down the alternative
// path. Every case that exercises the task-lifetime key therefore asserts
// StatusRunning as an explicit precondition; without it the dispatch guard fires
// first and the case passes for the wrong reason (it would return
// ErrInstanceTerminal, not ErrTaskNotOpen). The one exception is the last case,
// the precedence pin, which asserts StatusTerminated on purpose: it exists to
// prove which guard wins when BOTH keys are live.
//
// The cases share one call shape — engine.Step(def, state, trigger) — and vary
// only in the setup closure and the trigger, so they are one table per the
// project's table-test rule. The brief's three prescribed names map to the case
// names one-for-one:
//
//	TestHumanClaimedOnClosedTaskErrors    → "HumanClaimed on a closed task errors"
//	TestHumanReassignedOnClosedTaskErrors → "HumanReassigned on a closed task errors"
//	TestHumanCompletedOnClosedTaskErrors  → "HumanCompleted on a closed task errors"

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
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// taskLifetimeT0 is the shared clock origin for this file.
var taskLifetimeT0 = time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

// taskLifetimeActor is the caller whose late trigger must be refused.
var taskLifetimeActor = authz.Actor{ID: "alice", Roles: []string{"ops"}}

// boundaryClosedTaskDef returns
//
//	start → work(UserTask) → end
//	          ↑ interrupting message boundary "cancel" → escalate(ServiceTask) → end2
//
// The boundary's outgoing branch parks on a service action, so firing it leaves
// the instance RUNNING with the host task already Cancelled — the exact shape the
// task-lifetime guard exists for.
func boundaryClosedTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-task-lifetime-bnd", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work", activity.WithEligibleRoles("ops")),
			event.NewBoundary("bnd-cancel", "work", event.WithMessageCorrelator("cancel", "")),
			activity.NewServiceTask("escalate", activity.WithTaskAction("escalate-action")),
			event.NewEnd("end"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-escalate", Source: "bnd-cancel", Target: "escalate"},
			{ID: "f-escalate-end", Source: "escalate", Target: "end2"},
		},
	}
}

// deadlineClosedTaskDef returns
//
//	start → work(UserTask, deadline 3h → flow "f-esc") → end
//	f-esc: work → escalate(ServiceTask) → end2
//
// Unlike the deadlineDef in step_timers_test.go, the alternative path parks on a
// service action instead of ending the instance, so the breach leaves the
// instance RUNNING.
func deadlineClosedTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-task-lifetime-deadline", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work",
				activity.WithEligibleRoles("ops"),
				activity.WithWaitDeadline(schedule.AfterExpr(`"3h"`), "f-esc")),
			activity.NewServiceTask("escalate", activity.WithTaskAction("escalate-action")),
			event.NewEnd("end"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-esc", Source: "work", Target: "escalate"},
			{ID: "f-escalate-end", Source: "escalate", Target: "end2"},
		},
	}
}

// openTaskDef returns the plain start → work(UserTask) → end shape used by the
// control case (task still open) and the precedence case (instance terminated
// with the task open, so endInstance's sweep closes it).
func openTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-task-lifetime-open", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work", activity.WithEligibleRoles("ops")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
		},
	}
}

// startParkedOnTask starts def and asserts the instance is parked on exactly one
// open human task. It returns the whole StepResult because the deadline case
// needs the armed timer's id out of the command stream.
func startParkedOnTask(t *testing.T, def *model.ProcessDefinition, id string) engine.StepResult {
	t.Helper()

	r, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: id},
		engine.NewStartInstance(taskLifetimeT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r.State.Tasks, 1, "setup: the instance must park on exactly one human task")
	require.True(t, r.State.Tasks[0].IsOpen(), "setup: that task must start out open")
	require.Equal(t, engine.StatusRunning, r.State.Status)
	return r
}

// scheduledTimerID returns the id of the single ScheduleTimer command in cmds.
func scheduledTimerID(t *testing.T, cmds []engine.Command) string {
	t.Helper()

	var ids []string
	for _, c := range cmds {
		if st, ok := c.(engine.ScheduleTimer); ok {
			ids = append(ids, st.TimerID)
		}
	}
	require.Len(t, ids, 1, "setup: expected exactly one ScheduleTimer")
	return ids[0]
}

// closedTaskOnRunningInstance drives boundaryClosedTaskDef to the state the guard
// is about: the message boundary interrupts the host, the task is Cancelled, and
// the instance keeps running on the escalation branch.
func closedTaskOnRunningInstance(t *testing.T, id string) (*model.ProcessDefinition, engine.InstanceState) {
	t.Helper()

	def := boundaryClosedTaskDef()
	st := startParkedOnTask(t, def, id).State

	r, err := engine.Step(t.Context(), def, st,
		engine.NewMessageReceived(taskLifetimeT0.Add(time.Minute), "cancel", "", nil),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusRunning, r.State.Status,
		"setup precondition: the instance must still be RUNNING, or dispatch's terminal guard fires first")
	require.Len(t, r.State.Tasks, 1)
	require.Equal(t, humantask.Cancelled, r.State.Tasks[0].State,
		"setup: the interrupting boundary must have closed the task")
	require.Nil(t, r.State.Tasks[0].Claim, "setup: the closed task carries no claim yet")
	return def, r.State
}

// TestHumanTaskTriggersOnClosedTaskError pins the task-lifetime guard on the
// three human-task handlers, its non-interference with an open task, and its
// precedence against dispatch's instance-status guard.
func TestHumanTaskTriggersOnClosedTaskError(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		setup func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState)
		// trigger builds the trigger under test from the state produced by setup.
		trigger func(st engine.InstanceState) engine.Trigger
		assert  func(t *testing.T, before engine.InstanceState, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "HumanClaimed on a closed task errors",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				return closedTaskOnRunningInstance(t, "i-claim-closed")
			},
			trigger: func(st engine.InstanceState) engine.Trigger {
				return engine.NewHumanClaimed(taskLifetimeT0.Add(2*time.Minute), st.Tasks[0].TaskID, taskLifetimeActor)
			},
			assert: func(t *testing.T, before engine.InstanceState, res engine.StepResult, err error) {
				// LOAD-BEARING: before the guard this call returned a nil error, so
				// the sentinel assertion on its own captures the whole pre-change
				// behaviour — the success, the flip to Claimed, and the UpdateTask
				// that carried it. That is why the task/claim assertions below are
				// controls: there is nothing left over for them to add.
				require.ErrorIs(t, err, engine.ErrTaskNotOpen)
				// LOAD-BEARING: pins WHICH key answered. The instance is RUNNING, so
				// a pass here on ErrInstanceTerminal would mean the setup drifted and
				// dispatch's status guard fired instead.
				assert.NotErrorIs(t, err, engine.ErrInstanceTerminal,
					"the instance is RUNNING: this must be the task-lifetime key, not dispatch's status guard")
				// CONTROL: inherited from ErrTaskNotOpen's own definition — a
				// classification pin, not evidence of this guard.
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"a too-late human trigger classifies as an invalid transition (422)")
				// CONTROL: Step returns the zero StepResult on any error, so
				// res.Commands is nil for free and this CANNOT fail here.
				assert.Empty(t, res.Commands)
				// CONTROL, deliberately: cloneState deep-copies Tasks, so `before` is
				// immutable whether or not the guard exists — the pre-guard handler
				// mutated its own clone and these two hold identically either way.
				// Asserting on res.State instead would be no better: it is the zero
				// value on the error path. Kept as a purity tripwire on Step only.
				assert.Equal(t, humantask.Cancelled, before.Tasks[0].State,
					"control: Step must not mutate the caller's state")
				assert.Nil(t, before.Tasks[0].Claim, "control: same")
			},
		},
		{
			name: "HumanReassigned on a closed task errors",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				return closedTaskOnRunningInstance(t, "i-reassign-closed")
			},
			trigger: func(st engine.InstanceState) engine.Trigger {
				return engine.NewHumanReassigned(taskLifetimeT0.Add(2*time.Minute), st.Tasks[0].TaskID,
					"alice", "bob", taskLifetimeActor)
			},
			assert: func(t *testing.T, before engine.InstanceState, res engine.StepResult, err error) {
				// LOAD-BEARING: identical accounting to HumanClaimed above — this
				// returned a nil error before the guard, so the sentinel assertion
				// captures the entire pre-change behaviour on its own, which is why
				// the task/claim assertions below are controls.
				require.ErrorIs(t, err, engine.ErrTaskNotOpen)
				// LOAD-BEARING: pins which key answered.
				assert.NotErrorIs(t, err, engine.ErrInstanceTerminal,
					"the instance is RUNNING: this must be the task-lifetime key, not dispatch's status guard")
				// CONTROL: classification, inherited from the sentinel.
				assert.ErrorIs(t, err, engine.ErrInvalidTransition)
				// CONTROL: zero StepResult on error — cannot fail.
				assert.Empty(t, res.Commands)
				// CONTROL: `before` is a deep copy, immutable with or without the
				// guard. Purity tripwire on Step, nothing more.
				assert.Equal(t, humantask.Cancelled, before.Tasks[0].State,
					"control: Step must not mutate the caller's state")
				assert.Nil(t, before.Tasks[0].Claim, "control: same")
			},
		},
		{
			name: "HumanCompleted on a closed task errors",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				return closedTaskOnRunningInstance(t, "i-complete-closed")
			},
			trigger: func(st engine.InstanceState) engine.Trigger {
				return engine.NewHumanCompleted(taskLifetimeT0.Add(2*time.Minute), st.Tasks[0].TaskID,
					engine.CompletionInput{}, taskLifetimeActor)
			},
			assert: func(t *testing.T, before engine.InstanceState, res engine.StepResult, err error) {
				// LOAD-BEARING: this path ALREADY errored before the guard — with
				// ErrTokenNotFound, because the interrupting boundary consumed the
				// host token and tokenAwaiting ran first. Asserting "an error came
				// back" would be worthless here; it is the sentinel identity, and
				// its separation from the sentinel this path used to return, that
				// pins the change.
				require.ErrorIs(t, err, engine.ErrTaskNotOpen)
				assert.NotErrorIs(t, err, engine.ErrTokenNotFound,
					"the reorder must report the closed TASK, not the detached token; "+
						"the two sentinels are siblings and must not silently merge")
				// LOAD-BEARING: pins which key answered.
				assert.NotErrorIs(t, err, engine.ErrInstanceTerminal,
					"the instance is RUNNING: this must be the task-lifetime key, not dispatch's status guard")
				// CONTROL: zero StepResult on error — cannot fail.
				assert.Empty(t, res.Commands)
				// CONTROL: `before` is a deep copy; Step never mutates it, guard or
				// no guard. Purity tripwire only.
				assert.Equal(t, humantask.Cancelled, before.Tasks[0].State,
					"control: Step must not mutate the caller's state")
			},
		},
		{
			name: "HumanCompleted after a deadline breach reports the closed task, not the detached token",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				def := deadlineClosedTaskDef()
				r0 := startParkedOnTask(t, def, "i-complete-deadline")
				deadlineTimerID := scheduledTimerID(t, r0.Commands)

				r, err := engine.Step(t.Context(), def, r0.State,
					engine.NewTimerFired(taskLifetimeT0.Add(3*time.Hour), deadlineTimerID), engine.StepOptions{})
				require.NoError(t, err)
				require.Equal(t, engine.StatusRunning, r.State.Status,
					"setup precondition: the escalation branch parks on an action, so the instance keeps running")
				require.Len(t, r.State.Tasks, 1)
				require.Equal(t, humantask.Cancelled, r.State.Tasks[0].State,
					"setup: the deadline breach must have cancelled the task")
				return def, r.State
			},
			trigger: func(st engine.InstanceState) engine.Trigger {
				return engine.NewHumanCompleted(taskLifetimeT0.Add(4*time.Hour), st.Tasks[0].TaskID,
					engine.CompletionInput{}, taskLifetimeActor)
			},
			assert: func(t *testing.T, _ engine.InstanceState, _ engine.StepResult, err error) {
				// LOAD-BEARING: this is the deliberate error UPGRADE the reorder
				// buys. The deadline breach clears the token's AwaitCommand but
				// leaves the token alive on the alternative path, so before the
				// reorder tokenAwaiting ran first and reported ErrTokenNotFound —
				// which reads as "unknown task" to a caller. It is now
				// ErrTaskNotOpen: "the task exists, you are too late".
				require.ErrorIs(t, err, engine.ErrTaskNotOpen)
				assert.NotErrorIs(t, err, engine.ErrTokenNotFound,
					"the deadline-breach path upgrades from ErrTokenNotFound to ErrTaskNotOpen")
			},
		},
		{
			name: "HumanClaimed on an OPEN task on a running instance still succeeds",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				def := openTaskDef()
				return def, startParkedOnTask(t, def, "i-claim-open").State
			},
			trigger: func(st engine.InstanceState) engine.Trigger {
				return engine.NewHumanClaimed(taskLifetimeT0.Add(time.Minute), st.Tasks[0].TaskID, taskLifetimeActor)
			},
			assert: func(t *testing.T, _ engine.InstanceState, res engine.StepResult, err error) {
				// LOAD-BEARING, all of them: this is the non-interference witness.
				// The guard must key on IsOpen rather than reject every human
				// trigger, so an inverted or over-broad condition fails here. The
				// error assertion alone would not be enough — the happy path has to
				// still produce the claim AND the UpdateTask that carries it, which
				// is what the rest of this closure checks.
				require.NoError(t, err)
				require.Len(t, res.State.Tasks, 1)
				assert.Equal(t, humantask.Claimed, res.State.Tasks[0].State)
				uts := findUpdateTasks(res.Commands)
				require.Len(t, uts, 1, "an open task still emits exactly one UpdateTask")
				assert.Equal(t, humantask.Claimed, uts[0].Task.State)
			},
		},
		{
			name: "HumanClaimed on a terminated instance reports the instance, not the task it swept closed",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				def := openTaskDef()
				st := startParkedOnTask(t, def, "i-claim-terminated").State

				r, err := engine.Step(t.Context(), def, st,
					engine.NewCancelRequested(taskLifetimeT0.Add(time.Minute)), engine.StepOptions{})
				require.NoError(t, err)
				require.Equal(t, engine.StatusTerminated, r.State.Status)
				require.Len(t, r.State.Tasks, 1)
				require.Equal(t, humantask.Cancelled, r.State.Tasks[0].State,
					"setup: endInstance's sweep closes the open task, so BOTH keys are live here")
				return def, r.State
			},
			trigger: func(st engine.InstanceState) engine.Trigger {
				return engine.NewHumanClaimed(taskLifetimeT0.Add(2*time.Minute), st.Tasks[0].TaskID, taskLifetimeActor)
			},
			assert: func(t *testing.T, _ engine.InstanceState, _ engine.StepResult, err error) {
				// LOAD-BEARING: in THIS setup both keys are live at once — the
				// setup block above asserts StatusTerminated and a Cancelled task,
				// because CancelRequested routes through endInstance, whose
				// cancelOpenTasks sweep closes the task on the way out. With both
				// guards eligible, only the ordering decides which sentinel a
				// caller sees: dispatch runs ahead of every handler, and the
				// instance-level answer is the more informative one. This pins that
				// precedence against a later inversion.
				//
				// Deliberately scoped to this setup. It is tempting to write "every
				// terminal path routes through endInstance, so the task is ALWAYS
				// closed too" — but that is an unverified universal, and five
				// resurrection routes were found across three successive passes,
				// each after the previous pass had declared the set closed. The
				// precedence this case pins does not need the universal to hold.
				require.ErrorIs(t, err, engine.ErrInstanceTerminal)
				assert.NotErrorIs(t, err, engine.ErrTaskNotOpen,
					"dispatch's instance-status guard must win over the task-lifetime guard")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def, st := tc.setup(t)
			res, err := engine.Step(t.Context(), def, st, tc.trigger(st), engine.StepOptions{})
			tc.assert(t, st, res, err)
		})
	}
}
