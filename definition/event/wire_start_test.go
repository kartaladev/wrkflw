package event_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestStartEventMessageSingletonWireRoundTrip verifies that a StartEvent's
// MessageStartSingleton flag (ADR-0121 review) survives a JSON round-trip
// through ProcessDefinition's MarshalJSON/UnmarshalJSON.
func TestStartEventMessageSingletonWireRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		node   model.Node
		assert func(t *testing.T, got event.StartEvent)
	}

	cases := []testCase{
		{
			name: "default keyless start is not a singleton",
			node: event.NewStart("s", event.WithMessageCorrelator("order.placed", "")),
			assert: func(t *testing.T, got event.StartEvent) {
				assert.False(t, got.MessageStartSingleton)
				assert.Equal(t, "order.placed", got.MessageName)
			},
		},
		{
			name: "singleton flag round-trips true",
			node: event.NewStart("s",
				event.WithMessageCorrelator("order.placed", ""),
				event.WithMessageStartSingleton()),
			assert: func(t *testing.T, got event.StartEvent) {
				assert.True(t, got.MessageStartSingleton)
				assert.Equal(t, "order.placed", got.MessageName)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			def := &model.ProcessDefinition{ID: "d", Version: 1, Nodes: []model.Node{c.node}}

			data, err := json.Marshal(def)
			require.NoError(t, err)

			var got model.ProcessDefinition
			require.NoError(t, json.Unmarshal(data, &got))

			gotStart, ok := got.Nodes[0].(event.StartEvent)
			require.True(t, ok, "round-tripped node is %T, want event.StartEvent", got.Nodes[0])
			c.assert(t, gotStart)
		})
	}
}
