package runtime

import (
	"testing"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalOutboxEvent(t *testing.T) {
	tests := map[string]struct {
		prev   engine.Status
		st     engine.InstanceState
		cmds   []engine.Command
		assert func(t *testing.T, got []kernel.OutboxEvent)
	}{
		"running -> completed maps to instance.completed with vars and def": {
			prev: engine.StatusRunning,
			st: engine.InstanceState{
				InstanceID: "i1",
				DefID:      "approval",
				DefVersion: 2,
				Status:     engine.StatusCompleted,
				Variables:  map[string]any{"ok": true},
			},
			cmds: []engine.Command{engine.CompleteInstance{Result: map[string]any{"ok": true}}},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Equal(t, []kernel.OutboxEvent{{
					Topic:         "instance.completed",
					Payload:       map[string]any{"ok": true},
					InstanceID:    "i1",
					DefinitionRef: model.Version("approval", 2),
				}}, got)
			},
		},
		"running -> failed prefers first incident error": {
			prev: engine.StatusRunning,
			st: engine.InstanceState{
				InstanceID: "i2",
				DefID:      "approval",
				DefVersion: 1,
				Status:     engine.StatusFailed,
				Incidents:  []engine.Incident{{Error: "boom"}},
			},
			cmds: []engine.Command{engine.FailInstance{Err: "boom"}},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Equal(t, []kernel.OutboxEvent{{
					Topic:         "instance.failed",
					Payload:       map[string]any{"error": "boom"},
					InstanceID:    "i2",
					DefinitionRef: model.Version("approval", 1),
				}}, got)
			},
		},
		"running -> failed with no incident falls back to FailInstance command error": {
			prev: engine.StatusRunning,
			st: engine.InstanceState{
				InstanceID: "i2b",
				Status:     engine.StatusFailed,
			},
			cmds: []engine.Command{engine.FailInstance{Err: "child parked: call activity does not support human tasks"}},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Equal(t, "instance.failed", got[0].Topic)
				require.Equal(t, map[string]any{"error": "child parked: call activity does not support human tasks"}, got[0].Payload)
			},
		},
		"running -> terminated (full rollback, no command) uses status fallback": {
			prev: engine.StatusRunning,
			st: engine.InstanceState{
				InstanceID: "i3",
				DefID:      "approval",
				DefVersion: 1,
				Status:     engine.StatusTerminated,
			},
			cmds: nil,
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Equal(t, []kernel.OutboxEvent{{
					Topic:         "instance.terminated",
					Payload:       map[string]any{"error": "instance terminated"},
					InstanceID:    "i3",
					DefinitionRef: model.Version("approval", 1),
				}}, got)
			},
		},
		"running -> terminated (cancel) carries the FailInstance cancel message": {
			prev: engine.StatusRunning,
			st: engine.InstanceState{
				InstanceID: "i4",
				Status:     engine.StatusTerminated,
			},
			cmds: []engine.Command{engine.FailInstance{Err: "cancelled"}},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Equal(t, "instance.terminated", got[0].Topic)
				require.Equal(t, map[string]any{"error": "cancelled"}, got[0].Payload)
			},
		},
		"non-terminal status yields no event": {
			prev: engine.StatusRunning,
			st:   engine.InstanceState{InstanceID: "i5", Status: engine.StatusRunning},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Empty(t, got)
			},
		},
		"compensating is not a terminal edge": {
			prev: engine.StatusRunning,
			st:   engine.InstanceState{InstanceID: "i6", Status: engine.StatusCompensating},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Empty(t, got)
			},
		},
		"terminal -> terminal is not an edge (no duplicate event)": {
			prev: engine.StatusCompleted,
			st:   engine.InstanceState{InstanceID: "i7", Status: engine.StatusCompleted},
			cmds: []engine.Command{engine.CompleteInstance{}},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Empty(t, got)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, terminalOutboxEvent(tc.prev, tc.st, tc.cmds))
		})
	}
}

func TestOutboundMessageEvents(t *testing.T) {
	st := engine.InstanceState{InstanceID: "i-1", DefID: "shipping", DefVersion: 2}
	cases := []struct {
		name   string
		cmds   []engine.Command
		assert func(t *testing.T, got []kernel.OutboxEvent)
	}{
		{
			name: "no send commands yields nil",
			cmds: []engine.Command{engine.CompleteInstance{}},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				assert.Nil(t, got)
			},
		},
		{
			name: "one send command yields one message event",
			cmds: []engine.Command{engine.SendMessage{Name: "OrderPlaced", CorrelationKey: "ord-7", Payload: map[string]any{"amount": 10}}},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Len(t, got, 1)
				assert.Equal(t, "message.OrderPlaced", got[0].Topic)
				assert.Equal(t, "i-1", got[0].InstanceID)
				assert.Equal(t, model.Version("shipping", 2), got[0].DefinitionRef)
				assert.Equal(t, "OrderPlaced", got[0].Payload["messageName"])
				assert.Equal(t, "ord-7", got[0].Payload["correlationKey"])
				assert.Equal(t, map[string]any{"amount": 10}, got[0].Payload["variables"])
			},
		},
		{
			name: "multiple send commands preserve order",
			cmds: []engine.Command{
				engine.SendMessage{Name: "A"},
				engine.InvokeAction{},
				engine.SendMessage{Name: "B"},
			},
			assert: func(t *testing.T, got []kernel.OutboxEvent) {
				require.Len(t, got, 2)
				assert.Equal(t, "message.A", got[0].Topic)
				assert.Equal(t, "message.B", got[1].Topic)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, outboundMessageEvents(st, tc.cmds))
		})
	}
}
