package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// fireForgetDeadlineDef returns a user task with a 3h deadline whose breach runs
// the "notify" action and routes the token down the "escalate" flow to an end event.
func fireForgetDeadlineDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-ff-deadline", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("userTask", []string{"manager"}, activity.WithWaitDeadline(schedule.AfterExpr(`"3h"`), "escalate"), activity.WithDeadlineAction("notify")),
			event.NewEnd("normalEnd"),
			event.NewEnd("escalateNode"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "normalEnd"},
			{ID: "escalate", Source: "userTask", Target: "escalateNode"},
		},
	}
}

// fireForgetReminderDef returns a user task with a 1h reminder ("remind") and a
// 3h deadline. Firing the reminder emits a fire-once reminder InvokeAction.
func fireForgetReminderDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-ff-reminder", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("userTask", []string{"manager"}, activity.WithWaitDeadline(schedule.AfterExpr(`"3h"`), "escalate"), activity.WithDeadlineAction("notify"), activity.WithWaitAction(schedule.EveryExpr(`"1h"`), "remind")),
			event.NewEnd("normalEnd"),
			event.NewEnd("escalateNode"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "normalEnd"},
			{ID: "escalate", Source: "userTask", Target: "escalateNode"},
		},
	}
}

// scheduledTimerID drives StartInstance and returns the timer id matching kind.
func scheduledTimerID(t *testing.T, r StepResult, kind TimerKind) string {
	t.Helper()
	for _, c := range r.Commands {
		if st, ok := c.(ScheduleTimer); ok && st.Kind == kind {
			return st.TimerID
		}
	}
	t.Fatalf("no ScheduleTimer of kind %v found in commands: %v", kind, r.Commands)
	return ""
}

// TestFireOnceLifecycleActionsAreFireAndForget asserts that the deadline-breach
// and reminder fire-once InvokeActions carry FireAndForget == true. No token
// awaits their result, so the runtime must not feed an ActionCompleted back.
func TestFireOnceLifecycleActionsAreFireAndForget(t *testing.T) {
	startAt := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name       string
		def        *model.ProcessDefinition
		timerKind  TimerKind
		fireAt     time.Time
		actionName string
	}

	cases := []testCase{
		{
			name:       "deadline breach action is fire-and-forget",
			def:        fireForgetDeadlineDef(),
			timerKind:  TimerDeadline,
			fireAt:     startAt.Add(3 * time.Hour),
			actionName: "notify",
		},
		{
			name:       "reminder action is fire-and-forget",
			def:        fireForgetReminderDef(),
			timerKind:  TimerInWait,
			fireAt:     startAt.Add(time.Hour),
			actionName: "remind",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r1, err := Step(tc.def, InstanceState{InstanceID: "i1"},
				NewStartInstance(startAt, nil), StepOptions{})
			require.NoError(t, err)

			timerID := scheduledTimerID(t, r1, tc.timerKind)

			r2, err := Step(tc.def, r1.State,
				NewTimerFired(tc.fireAt, timerID), StepOptions{})
			require.NoError(t, err)

			var ia InvokeAction
			var found bool
			for _, c := range r2.Commands {
				if v, ok := c.(InvokeAction); ok && v.Name == tc.actionName {
					ia = v
					found = true
				}
			}
			require.True(t, found, "fire-once InvokeAction %q not found; got: %v", tc.actionName, r2.Commands)
			assert.True(t, ia.FireAndForget, "fire-once lifecycle action %q must be FireAndForget", tc.actionName)
		})
	}
}
