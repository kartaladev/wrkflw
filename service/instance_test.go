// Package service_test is the black-box test suite for the service facade.
package service_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/service"
)

// TestProcessInstanceStateAndDefinition verifies that the State() and Definition()
// accessors return the raw inputs passed to NewProcessInstance.
func TestProcessInstanceStateAndDefinition(t *testing.T) {
	def := &model.ProcessDefinition{ID: "greeting", Version: 1}
	st := engine.InstanceState{InstanceID: "i-1", DefID: "greeting", DefVersion: 1, Status: engine.StatusRunning}
	pi := service.NewProcessInstance(def, st)
	assert.Equal(t, def, pi.Definition())
	assert.Equal(t, st, pi.State())
}

// TestProcessInstanceMarshalJSON verifies that MarshalJSON produces a projection
// with expected top-level keys and that status serializes to the correct string.
func TestProcessInstanceMarshalJSON(t *testing.T) {
	def := &model.ProcessDefinition{ID: "greeting", Version: 1}
	st := engine.InstanceState{InstanceID: "i-1", DefID: "greeting", DefVersion: 1, Status: engine.StatusRunning}
	data, err := json.Marshal(service.NewProcessInstance(def, st))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, "i-1", m["instance_id"])
	assert.Equal(t, "running", m["status"])
}

// TestProcessInstanceMarshalNilDefinition verifies that MarshalJSON does not panic
// when the definition is nil and that def-derived fields are omitted.
func TestProcessInstanceMarshalNilDefinition(t *testing.T) {
	st := engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning}
	data, err := json.Marshal(service.NewProcessInstance(nil, st))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	_, hasBindings := m["action_bindings"]
	assert.False(t, hasBindings, "nil def omits action_bindings")
	_, hasScopedActions := m["scoped_actions"]
	assert.False(t, hasScopedActions, "nil def omits scoped_actions")
}

// TestTokenStateString exercises every branch of the unexported tokenStateString
// mapping by building an InstanceState with a token in each TokenState, marshaling
// via service.NewProcessInstance, and inspecting the "state" field of the first
// token in the resulting JSON.
func TestTokenStateString(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		tokenState engine.TokenState
		assert     func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:       "TokenActive maps to active",
			tokenState: engine.TokenActive,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "active", got)
			},
		},
		{
			name:       "TokenWaitingCommand maps to waitingCommand",
			tokenState: engine.TokenWaitingCommand,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "waitingCommand", got)
			},
		},
		{
			name:       "TokenAtJoin maps to atJoin",
			tokenState: engine.TokenAtJoin,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "atJoin", got)
			},
		},
		{
			name:       "TokenIncident maps to incident",
			tokenState: engine.TokenIncident,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "incident", got)
			},
		},
		{
			name:       "out-of-range value maps to unknown",
			tokenState: engine.TokenState(999),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "unknown", got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := engine.InstanceState{
				InstanceID: "test-instance",
				Status:     engine.StatusRunning,
				Tokens: []engine.Token{
					{ID: "tok-1", NodeID: "node-1", State: tc.tokenState},
				},
			}

			data, err := json.Marshal(service.NewProcessInstance(nil, st))
			require.NoError(t, err)

			var m map[string]any
			require.NoError(t, json.Unmarshal(data, &m))

			tokens, ok := m["tokens"].([]any)
			require.True(t, ok, "tokens field must be a JSON array")
			require.Len(t, tokens, 1, "expected exactly one token")

			tok, ok := tokens[0].(map[string]any)
			require.True(t, ok, "token must be a JSON object")

			got, ok := tok["state"].(string)
			require.True(t, ok, "token state must be a string")

			tc.assert(t, got)
		})
	}
}
