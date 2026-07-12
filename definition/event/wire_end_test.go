package event_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
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
		name     string
		node     model.Node
		wantWire string // expected endBehavior discriminator on the raw wire ("" = absent)
		assert   func(t *testing.T, got event.EndEvent)
	}

	cases := []testCase{
		{
			name:     "plain",
			node:     event.NewEnd("done"),
			wantWire: "",
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndNormal, got.Behavior)
				require.Empty(t, got.TerminationReason)
				require.Equal(t, event.OutcomeComplete, got.Outcome)
			},
		},
		{
			name:     "abort",
			node:     event.NewEnd("halt", event.WithForceTermination("fraud", event.OutcomeAbort)),
			wantWire: "terminate",
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndTerminate, got.Behavior)
				require.Equal(t, "fraud", got.TerminationReason)
				require.Equal(t, event.OutcomeAbort, got.Outcome)
			},
		},
		{
			name:     "complete",
			node:     event.NewEnd("stop", event.WithForceTermination("enough", event.OutcomeComplete)),
			wantWire: "terminate",
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndTerminate, got.Behavior)
				require.Equal(t, "enough", got.TerminationReason)
				require.Equal(t, event.OutcomeComplete, got.Outcome)
			},
		},
		{
			name:     "error",
			node:     event.NewEnd("boom", event.WithErrorCode("ORDER_REJECTED")),
			wantWire: "error",
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndError, got.Behavior)
				require.Equal(t, "ORDER_REJECTED", got.ErrorCode)
			},
		},
		{
			name:     "error catch-all (empty code)",
			node:     event.NewEnd("boom", event.WithErrorCode("")),
			wantWire: "error",
			assert: func(t *testing.T, got event.EndEvent) {
				require.Equal(t, event.EndError, got.Behavior)
				require.Empty(t, got.ErrorCode)
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

			if c.wantWire != "" {
				require.Contains(t, string(data), `"endBehavior":"`+c.wantWire+`"`)
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
