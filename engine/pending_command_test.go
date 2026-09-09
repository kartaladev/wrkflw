package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// TestPendingCommandRoundTripsThroughJSON is the durability guarantee the whole
// recovery mechanism rests on: the mark is carried in the instance snapshot, and
// the snapshot's only wire form is encoding/json. A field that does not survive
// the round trip is a command that cannot be recovered.
//
// The recovered command is compared to the ORIGINAL command, not to the
// envelope, so the test cannot pass by agreeing with the encoder about a value
// the decoder mangled.
func TestPendingCommandRoundTripsThroughJSON(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		cmd    engine.Command
		assert func(t *testing.T, got engine.Command, err error)
	}

	cases := []testCase{
		{
			name: "InvokeAction",
			cmd: engine.InvokeAction{
				CommandID: "cmd-1", Name: "charge",
				Input:         map[string]any{"amount": "12.50"},
				FireAndForget: true,
			},
			assert: func(t *testing.T, got engine.Command, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.InvokeAction{
					CommandID: "cmd-1", Name: "charge",
					Input:         map[string]any{"amount": "12.50"},
					FireAndForget: true,
				}, got)
			},
		},
		{
			name: "AwaitHuman",
			cmd: engine.AwaitHuman{
				TaskID:      "task-1",
				Eligibility: authz.AuthzSpec{Roles: []string{"manager"}, Attribute: `vars.region == "EU"`},
			},
			assert: func(t *testing.T, got engine.Command, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.AwaitHuman{
					TaskID:      "task-1",
					Eligibility: authz.AuthzSpec{Roles: []string{"manager"}, Attribute: `vars.region == "EU"`},
				}, got)
			},
		},
		{
			name: "UpdateTask",
			cmd: engine.UpdateTask{Task: humantask.HumanTask{
				TaskID: "task-2", NodeID: "approve", InstanceID: "i1", State: humantask.Unclaimed,
			}},
			assert: func(t *testing.T, got engine.Command, err error) {
				require.NoError(t, err)
				updated, ok := got.(engine.UpdateTask)
				require.True(t, ok)
				assert.Equal(t, "task-2", updated.Task.TaskID)
				assert.Equal(t, "approve", updated.Task.NodeID)
			},
		},
		{
			name: "ThrowSignal",
			cmd:  engine.ThrowSignal{Name: "escalated", Payload: map[string]any{"who": "alice"}},
			assert: func(t *testing.T, got engine.Command, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.ThrowSignal{
					Name: "escalated", Payload: map[string]any{"who": "alice"},
				}, got)
			},
		},
		{
			name: "StartSubInstance",
			cmd: engine.StartSubInstance{
				CommandID: "cmd-3",
				DefRef:    model.Version("child", 4),
				Input:     map[string]any{"seed": "v"},
			},
			assert: func(t *testing.T, got engine.Command, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StartSubInstance{
					CommandID: "cmd-3",
					DefRef:    model.Version("child", 4),
					Input:     map[string]any{"seed": "v"},
				}, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			envelope, ok := engine.NewPendingCommand(tc.cmd)
			require.True(t, ok, "%T must be recoverable", tc.cmd)

			data, err := json.Marshal(envelope)
			require.NoError(t, err)

			var decoded engine.PendingCommand
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, envelope.Kind, decoded.Kind, "the discriminator must survive the wire")

			got, err := decoded.Command()
			tc.assert(t, got, err)
		})
	}
}

// TestPendingCommandsForSelectsOnlyRecoverableCommands pins the classification
// itself: the excluded commands are excluded for stated reasons (see
// engine.PendingCommandKind), and silently marking one of them would have the
// sweep re-run a cancellation side effect or double-deliver an outbox event.
func TestPendingCommandsForSelectsOnlyRecoverableCommands(t *testing.T) {
	t.Parallel()

	cmds := []engine.Command{
		engine.ScheduleTimer{TimerID: "t1"},
		engine.CancelTimer{TimerID: "t1"},
		engine.CompleteInstance{},
		engine.FailInstance{Err: "boom"},
		engine.SendMessage{Name: "m"},
		engine.InvokeCancelAction{Name: "undo"},
		engine.Compensate{},
		engine.InvokeAction{CommandID: "c1", Name: "a"},
		engine.AwaitHuman{TaskID: "t"},
	}

	got := engine.PendingCommandsFor(cmds)
	require.Len(t, got, 2, "only InvokeAction and AwaitHuman are recoverable here")
	assert.Equal(t, engine.PendingInvokeAction, got[0].Kind)
	assert.Equal(t, engine.PendingAwaitHuman, got[1].Kind)

	assert.Nil(t, engine.PendingCommandsFor(cmds[:7]),
		"nothing recoverable must yield nil, not an empty slice, so an unmarked snapshot serialises unchanged")
}

// TestPendingCommandUnknownKindIsReported pins the loud failure: a mark written
// by a newer build must not be re-driven as something else.
func TestPendingCommandUnknownKindIsReported(t *testing.T) {
	t.Parallel()

	_, err := engine.PendingCommand{Kind: "invented_by_a_later_build"}.Command()
	require.ErrorIs(t, err, engine.ErrUnknownPendingCommand)
}
