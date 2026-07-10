package event_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// TestEndEventWireRoundTrip verifies EndEvent's force-termination fields
// (ForceTermination, TerminationReason, Outcome) survive a JSON round-trip
// through ProcessDefinition's MarshalJSON/UnmarshalJSON, which flattens nodes
// via the model.NodeWire kind registry (ADR-0119).
func TestEndEventWireRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		node   model.Node
		assert func(t *testing.T, got event.EndEvent)
	}

	cases := []testCase{
		{
			name: "plain",
			node: event.NewEnd("done"),
			assert: func(t *testing.T, got event.EndEvent) {
				require.False(t, got.ForceTermination)
				require.Empty(t, got.TerminationReason)
				require.Equal(t, event.OutcomeComplete, got.Outcome)
			},
		},
		{
			name: "abort",
			node: event.NewEnd("halt", event.WithForceTermination("fraud", event.OutcomeAbort)),
			assert: func(t *testing.T, got event.EndEvent) {
				require.True(t, got.ForceTermination)
				require.Equal(t, "fraud", got.TerminationReason)
				require.Equal(t, event.OutcomeAbort, got.Outcome)
			},
		},
		{
			name: "complete",
			node: event.NewEnd("stop", event.WithForceTermination("enough", event.OutcomeComplete)),
			assert: func(t *testing.T, got event.EndEvent) {
				require.True(t, got.ForceTermination)
				require.Equal(t, "enough", got.TerminationReason)
				require.Equal(t, event.OutcomeComplete, got.Outcome)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			def := &model.ProcessDefinition{
				ID: "d", Version: 1,
				Nodes: []model.Node{c.node},
			}

			data, err := json.Marshal(def)
			require.NoError(t, err)

			var got model.ProcessDefinition
			require.NoError(t, json.Unmarshal(data, &got))

			gotEnd, ok := got.Nodes[0].(event.EndEvent)
			require.True(t, ok, "round-tripped node is %T, want event.EndEvent", got.Nodes[0])
			c.assert(t, gotEnd)
		})
	}
}
