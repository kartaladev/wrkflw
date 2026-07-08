package engine

// message_target_test.go — white-box tests for MessageTargetNode. Package
// engine (not engine_test) because fixtures directly construct the unexported
// armedEvent/boundaryArm/eventSubprocessArm bookkeeping types to pin down the
// exact 4-tier dispatch priority MessageTargetNode must mirror from
// handleMessageReceived (see step_triggers.go:654).

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMessageTargetNode exercises the 4-tier dispatch priority MessageTargetNode
// must mirror from handleMessageReceived: event-based-gateway arm, message
// boundary arm, event sub-process arm, then standalone parked message token.
// Each case seeds only the tiers relevant to the assertion so that a wrong tier
// order or a wrong match predicate would make the wrong node win.
func TestMessageTargetNode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  InstanceState
		msg    string
		key    string
		assert func(t *testing.T, nodeID string, ok bool)
	}

	cases := []testCase{
		{
			name: "tier 1: event-gateway arm wins over all lower tiers",
			state: InstanceState{
				ArmedEvents: []armedEvent{
					{GatewayToken: "gw-tok", CatchNode: "gw-catch", Message: "M", MessageKey: "K"},
				},
				Boundaries: []boundaryArm{
					{HostToken: "host-tok", HostNode: "host", BoundaryNode: "bnd", Message: "M", MessageKey: "K"},
				},
				EventSubprocesses: []eventSubprocessArm{
					{EventSubprocessNode: "esp", Message: "M", MessageKey: "K"},
				},
				Tokens: []Token{
					{ID: "t1", NodeID: "recv", AwaitMessage: "M", AwaitMessageKey: "K"},
				},
			},
			msg: "M",
			key: "K",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "gw-catch", nodeID)
			},
		},
		{
			name: "tier 2: boundary arm wins when no gateway arm matches",
			state: InstanceState{
				Boundaries: []boundaryArm{
					{HostToken: "host-tok", HostNode: "host", BoundaryNode: "bnd", Message: "M", MessageKey: "K"},
				},
				EventSubprocesses: []eventSubprocessArm{
					{EventSubprocessNode: "esp", Message: "M", MessageKey: "K"},
				},
				Tokens: []Token{
					{ID: "t1", NodeID: "recv", AwaitMessage: "M", AwaitMessageKey: "K"},
				},
			},
			msg: "M",
			key: "K",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "bnd", nodeID)
			},
		},
		{
			name: "tier 3: event-subprocess arm wins when no gateway/boundary arm matches",
			state: InstanceState{
				EventSubprocesses: []eventSubprocessArm{
					{EventSubprocessNode: "esp", Message: "M", MessageKey: "K"},
				},
				Tokens: []Token{
					{ID: "t1", NodeID: "recv", AwaitMessage: "M", AwaitMessageKey: "K"},
				},
			},
			msg: "M",
			key: "K",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "esp", nodeID)
			},
		},
		{
			name: "tier 4: standalone parked message token (ReceiveTask/IntermediateCatchEvent) used when no arm matches",
			state: InstanceState{
				Tokens: []Token{
					{ID: "t1", NodeID: "recv", AwaitMessage: "OrderPlaced", AwaitMessageKey: "ORD-1"},
				},
			},
			msg: "OrderPlaced",
			key: "ORD-1",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "recv", nodeID)
			},
		},
		{
			name:  "no match on empty state returns false",
			state: InstanceState{},
			msg:   "OrderPlaced",
			key:   "ORD-1",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.False(t, ok)
				assert.Equal(t, "", nodeID)
			},
		},
		{
			name: "no match when correlation key does not match the parked token",
			state: InstanceState{
				Tokens: []Token{
					{ID: "t1", NodeID: "recv", AwaitMessage: "OrderPlaced", AwaitMessageKey: "ORD-1"},
				},
			},
			msg: "OrderPlaced",
			key: "ORD-OTHER",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.False(t, ok)
				assert.Equal(t, "", nodeID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := tc.state
			nodeID, ok := st.MessageTargetNode(tc.msg, tc.key)
			tc.assert(t, nodeID, ok)
		})
	}
}
