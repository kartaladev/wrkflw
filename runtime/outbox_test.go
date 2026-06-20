package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestOutboxEventsFor(t *testing.T) {
	tests := map[string]struct {
		cmds   []engine.Command
		assert func(t *testing.T, got []OutboxEvent)
	}{
		"complete instance maps to instance.completed": {
			cmds: []engine.Command{engine.CompleteInstance{Result: map[string]any{"ok": true}}},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Equal(t, []OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"ok": true}}}, got)
			},
		},
		"fail instance maps to instance.failed": {
			cmds: []engine.Command{engine.FailInstance{Err: "boom"}},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Equal(t, []OutboxEvent{{Topic: "instance.failed", Payload: map[string]any{"error": "boom"}}}, got)
			},
		},
		"preserves order across multiple terminal commands": {
			cmds: []engine.Command{
				engine.CompleteInstance{Result: nil},
				engine.FailInstance{Err: "x"},
			},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Equal(t, "instance.completed", got[0].Topic)
				require.Equal(t, "instance.failed", got[1].Topic)
			},
		},
		"non-terminal commands contribute nothing": {
			cmds: []engine.Command{
				engine.InvokeAction{CommandID: "c1", Name: "n"},
				engine.AwaitHuman{TaskToken: "t1"},
				engine.UpdateTask{},
				engine.ScheduleTimer{TimerID: "tm"},
				engine.CancelTimer{TimerID: "tm"},
				engine.ThrowSignal{Name: "s"},
				engine.StartSubInstance{CommandID: "c2", DefRef: "d"},
				engine.Compensate{},
			},
			assert: func(t *testing.T, got []OutboxEvent) {
				require.Empty(t, got)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, outboxEventsFor(tc.cmds))
		})
	}
}
