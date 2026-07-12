package event_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// TestEndEventWireRoundTrip verifies EndEvent's behavior discriminator and its
// terminate payload (Behavior, TerminationReason, Outcome) survive a JSON
// round-trip through ProcessDefinition's MarshalJSON/UnmarshalJSON, which
// flattens nodes via the model.NodeWire kind registry. The wire shape now uses
// the name-based endBehavior discriminator, not the retired forceTermination
// bool (ADR-0127).
func TestEndEventWireRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		node        model.Node
		wantWireKey bool // whether the raw wire must carry "endBehavior"
		assert      func(t *testing.T, got event.EndEvent)
	}

	cases := []testCase{
		{
			name:        "plain",
			node:        event.NewEnd("done"),
			wantWireKey: false,
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndNormal, got.Behavior)
				require.Empty(t, got.TerminationReason)
				require.Equal(t, event.OutcomeComplete, got.Outcome)
			},
		},
		{
			name:        "abort",
			node:        event.NewEnd("halt", event.WithForceTermination("fraud", event.OutcomeAbort)),
			wantWireKey: true,
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndTerminate, got.Behavior)
				require.Equal(t, "fraud", got.TerminationReason)
				require.Equal(t, event.OutcomeAbort, got.Outcome)
			},
		},
		{
			name:        "complete",
			node:        event.NewEnd("stop", event.WithForceTermination("enough", event.OutcomeComplete)),
			wantWireKey: true,
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndTerminate, got.Behavior)
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

			if c.wantWireKey {
				require.Contains(t, string(data), `"endBehavior":"terminate"`)
			} else {
				require.NotContains(t, string(data), "endBehavior")
				require.NotContains(t, string(data), "forceTermination")
			}

			var got model.ProcessDefinition
			require.NoError(t, json.Unmarshal(data, &got))

			gotEnd, ok := got.Nodes[0].(event.EndEvent)
			require.True(t, ok, "round-tripped node is %T, want event.EndEvent", got.Nodes[0])
			c.assert(t, gotEnd)
		})
	}
}
