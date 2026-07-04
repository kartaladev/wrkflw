package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
)

// sendTaskDef: start → send(msg "m") → end. The SendTask optionally carries a
// correlation-key expression.
func sendTaskDef(corr string) *definition.ProcessDefinition {
	send := definition.NewSendTask("send", "m")
	if corr != "" {
		send = definition.NewSendTask("send", "m", definition.WithCorrelationKey(corr))
	}
	return &definition.ProcessDefinition{
		ID: "p-send", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			send,
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "send"},
			{ID: "f2", Source: "send", Target: "end"},
		},
	}
}

// firstSendMessage returns the single SendMessage command in cmds, or fails.
func firstSendMessage(t *testing.T, cmds []engine.Command) engine.SendMessage {
	t.Helper()
	for _, c := range cmds {
		if sm, ok := c.(engine.SendMessage); ok {
			return sm
		}
	}
	t.Fatalf("no SendMessage command emitted; got %#v", cmds)
	return engine.SendMessage{}
}

// TestSendTaskEmitsAndCompletes asserts that a SendTask is fire-and-forget: a
// single Step from StartInstance emits a SendMessage command AND auto-advances
// the token through the end event to instance completion. Previously SendTask was
// an unimplemented fall-through that parked the token forever.
func TestSendTaskEmitsAndCompletes(t *testing.T) {
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		corr   string
		vars   map[string]any
		assert func(t *testing.T, sm engine.SendMessage)
	}
	cases := []testCase{
		{
			name: "no correlation key",
			corr: "",
			vars: map[string]any{"k": "v"},
			assert: func(t *testing.T, sm engine.SendMessage) {
				assert.Equal(t, "m", sm.Name)
				assert.Empty(t, sm.CorrelationKey)
				assert.Equal(t, "v", sm.Payload["k"])
			},
		},
		{
			name: "resolved correlation key",
			corr: `orderID`,
			vars: map[string]any{"orderID": "o-42"},
			assert: func(t *testing.T, sm engine.SendMessage) {
				assert.Equal(t, "m", sm.Name)
				assert.Equal(t, "o-42", sm.CorrelationKey)
				assert.Equal(t, "o-42", sm.Payload["orderID"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := sendTaskDef(tc.corr)
			r, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(t0, tc.vars), engine.StepOptions{})
			require.NoError(t, err)

			sm := firstSendMessage(t, r.Commands)
			tc.assert(t, sm)

			assert.Equal(t, engine.StatusCompleted, r.State.Status,
				"SendTask is fire-and-forget: the instance must complete in the same Step")
		})
	}
}

// TestSendTaskBadCorrelationKey asserts that a SendTask whose correlation key is
// an invalid expr expression surfaces a wrapped error on entry rather than
// silently advancing.
func TestSendTaskBadCorrelationKey(t *testing.T) {
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	def := sendTaskDef("this is not valid expr ++")

	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "correlation key")
}
