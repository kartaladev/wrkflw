package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// catchReminderDef builds a linear definition whose intermediate catch event
// waits on the given variant and carries a recurring in-wait reminder:
//
//	Start → catch[<variant>, WithWaitAction(Every 30m, "nudge")] → End
func catchReminderDef(catchOpt event.CatchOption) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-catch-reminder", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("await", catchOpt,
				event.WithWaitAction(schedule.Every(30*time.Minute), "nudge")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "await"},
			{ID: "f2", Source: "await", Target: "end"},
		},
	}
}

// TestIntermediateCatchReminderFiresAndCancelsOnResolve verifies that an
// intermediate catch event carrying an in-wait reminder arms it, fires the nudge
// while parked, and cancels it when the awaited signal/message/timer resolves.
func TestIntermediateCatchReminderFiresAndCancelsOnResolve(t *testing.T) {
	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		catchOpt event.CatchOption
		// resolve delivers the wait-resolving trigger given the parked state and
		// the intermediate timer id (empty for signal/message).
		resolve func(t *testing.T, s engine.InstanceState, intermediateID string) engine.Trigger
	}{
		"signal": {
			catchOpt: event.WithCatchSignal("approved"),
			resolve: func(_ *testing.T, _ engine.InstanceState, _ string) engine.Trigger {
				return engine.NewSignalReceived(startAt.Add(45*time.Minute), "approved", nil)
			},
		},
		"message": {
			catchOpt: event.WithMessageCorrelator("PaymentReceived", ""),
			resolve: func(_ *testing.T, _ engine.InstanceState, _ string) engine.Trigger {
				return engine.NewMessageReceived(startAt.Add(45*time.Minute), "PaymentReceived", "", nil)
			},
		},
		"timer": {
			catchOpt: event.WithCatchTimer(schedule.AfterExpr(`"1h"`)),
			resolve: func(_ *testing.T, _ engine.InstanceState, intermediateID string) engine.Trigger {
				return engine.NewTimerFired(startAt.Add(time.Hour), intermediateID)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			def := catchReminderDef(tc.catchOpt)

			// ---- Step 1: Start → parked at catch, reminder armed ----
			r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(startAt, nil), engine.StepOptions{})
			require.NoError(t, err)

			var reminderST engine.ScheduleTimer
			var intermediateID string // AwaitCommand for the timer variant
			for _, c := range r1.Commands {
				if st, ok := c.(engine.ScheduleTimer); ok {
					switch st.Kind {
					case engine.TimerInWait:
						reminderST = st
					case engine.TimerIntermediate:
						intermediateID = st.TimerID
					}
				}
			}
			require.NotEmpty(t, reminderST.TimerID, "expected a TimerInWait ScheduleTimer on catch entry; got: %v", r1.Commands)
			assert.True(t, reminderST.Trigger.Recurring(), "reminder trigger must be recurring")

			require.Len(t, r1.State.Tokens, 1)
			assert.Equal(t, "await", r1.State.Tokens[0].NodeID)
			assert.Equal(t, engine.TokenWaitingCommand, r1.State.Tokens[0].State)

			reminderID := reminderST.TimerID

			// ---- Step 2: reminder fires while parked → nudge only, token stays ----
			r2, err := engine.Step(def, r1.State,
				engine.NewTimerFired(startAt.Add(30*time.Minute), reminderID), engine.StepOptions{})
			require.NoError(t, err)

			var nudge engine.InvokeAction
			var foundNudge bool
			for _, c := range r2.Commands {
				switch v := c.(type) {
				case engine.InvokeAction:
					nudge = v
					foundNudge = true
				case engine.CancelTimer, engine.CompleteInstance:
					t.Errorf("unexpected command %T while parked: %v", c, c)
				}
			}
			require.True(t, foundNudge, "expected InvokeAction(nudge) after reminder fire; got: %v", r2.Commands)
			assert.Equal(t, "nudge", nudge.Name)
			assert.True(t, nudge.FireAndForget, "reminder nudge must be fire-and-forget")
			require.Len(t, r2.State.Tokens, 1, "token must still be parked after nudge")

			// ---- Step 3: wait resolves → token advances + reminder cancelled ----
			r3, err := engine.Step(def, r2.State,
				tc.resolve(t, r2.State, intermediateID), engine.StepOptions{})
			require.NoError(t, err)

			var foundCancel bool
			for _, c := range r3.Commands {
				if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == reminderID {
					foundCancel = true
				}
			}
			assert.True(t, foundCancel, "resolve must emit CancelTimer for the reminder (id=%s); got: %v", reminderID, r3.Commands)
			for _, tr := range r3.State.Timers {
				assert.NotEqual(t, reminderID, tr.TimerID, "reminder record must be removed on resolve")
			}
			assert.Equal(t, engine.StatusCompleted, r3.State.Status)
		})
	}
}
