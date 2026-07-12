package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
)

// TestHandleReminderFiredStaleness pins the generalized (token-parked, not just
// HumanTask) staleness rule for handleReminderFired:
//
//   - A reminder whose parked token is gone (wait resolved and advanced) is
//     stale: clean no-op, record removed.
//   - A reminder whose token is still parked with NO HumanTask (ReceiveTask /
//     catch) is live: the nudge InvokeAction is emitted, the record persists.
func TestHandleReminderFiredStaleness(t *testing.T) {
	// A catch node carrying a reminder action so the live path can resolve it.
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewIntermediateCatch("await",
				event.WithSignalName("approved"),
				event.WithWaitAction(schedule.Every(1), "nudge")),
		},
	}
	rec := timerRecord{TimerID: "tm1", Kind: TimerInWait, Token: "tok1", NodeID: "await"}

	t.Run("stale when token gone", func(t *testing.T) {
		s := &InstanceState{
			InstanceID: "i1",
			Tokens:     nil, // token advanced away
			Timers:     []timerRecord{rec},
		}
		res, err := handleReminderFired(def, s, rec)
		require.NoError(t, err)
		assert.Empty(t, res.Commands, "stale reminder must emit no commands")
		assert.Empty(t, res.State.Timers, "stale reminder record must be removed")
	})

	t.Run("live while parked with no human task", func(t *testing.T) {
		s := &InstanceState{
			InstanceID: "i1",
			Tokens: []Token{{
				ID:          "tok1",
				NodeID:      "await",
				State:       TokenWaitingCommand,
				AwaitSignal: "approved",
			}},
			Timers: []timerRecord{rec},
		}
		res, err := handleReminderFired(def, s, rec)
		require.NoError(t, err)
		require.Len(t, res.Commands, 1, "live reminder must emit the nudge")
		ia, ok := res.Commands[0].(InvokeAction)
		require.True(t, ok, "expected InvokeAction, got %T", res.Commands[0])
		assert.Equal(t, "nudge", ia.Name)
		assert.True(t, ia.FireAndForget)
		assert.Len(t, res.State.Timers, 1, "live reminder record must persist (native recurrence)")
	})
}
