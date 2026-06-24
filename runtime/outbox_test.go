package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestTerminalOutboxEvent(t *testing.T) {
	tests := map[string]struct {
		prev   engine.Status
		st     engine.InstanceState
		cmds   []engine.Command
		assert func(t *testing.T, got []OutboxEvent)
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
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Equal(t, []OutboxEvent{{
					Topic:      "instance.completed",
					Payload:    map[string]any{"ok": true},
					InstanceID: "i1",
					Def:        "approval:2",
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
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Equal(t, []OutboxEvent{{
					Topic:      "instance.failed",
					Payload:    map[string]any{"error": "boom"},
					InstanceID: "i2",
					Def:        "approval:1",
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
			assert: func(t *testing.T, got []OutboxEvent) {
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
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Equal(t, []OutboxEvent{{
					Topic:      "instance.terminated",
					Payload:    map[string]any{"error": "instance terminated"},
					InstanceID: "i3",
					Def:        "approval:1",
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
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Equal(t, "instance.terminated", got[0].Topic)
				require.Equal(t, map[string]any{"error": "cancelled"}, got[0].Payload)
			},
		},
		"non-terminal status yields no event": {
			prev: engine.StatusRunning,
			st:   engine.InstanceState{InstanceID: "i5", Status: engine.StatusRunning},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Empty(t, got)
			},
		},
		"compensating is not a terminal edge": {
			prev: engine.StatusRunning,
			st:   engine.InstanceState{InstanceID: "i6", Status: engine.StatusCompensating},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Empty(t, got)
			},
		},
		"terminal -> terminal is not an edge (no duplicate event)": {
			prev: engine.StatusCompleted,
			st:   engine.InstanceState{InstanceID: "i7", Status: engine.StatusCompleted},
			cmds: []engine.Command{engine.CompleteInstance{}},
			assert: func(t *testing.T, got []OutboxEvent) {
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
