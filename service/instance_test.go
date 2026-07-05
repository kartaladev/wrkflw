// Package service_test is the black-box test suite for the service facade.
package service_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
	"github.com/zakyalvan/krtlwrkflw/service"
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
}

// TestInstanceJSONMatchesLegacyViewSnapshot verifies that
// json.Marshal(service.NewProcessInstance(def, st)) is byte-for-byte JSON
// equivalent to json.Marshal(view.NewInstanceSnapshot(st, def)) for a populated
// definition and state. This guards the projection-logic move from runtime/view.
//
// This test is intentionally deleted in Task 10 when view.NewInstanceSnapshot is
// retired.
func TestInstanceJSONMatchesLegacyViewSnapshot(t *testing.T) {
	def := buildPopulatedDef(t)
	st := buildPopulatedState(t)
	got, err := json.Marshal(service.NewProcessInstance(def, st))
	require.NoError(t, err)
	want, err := json.Marshal(view.NewInstanceSnapshot(st, def))
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}

// buildPopulatedDef creates a definition with a serviceTask, a businessRuleTask,
// and plain event nodes so that action_bindings is exercised in the projection.
func buildPopulatedDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID:      "populated",
		Version: 2,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc-node", activity.WithActionName("do-work")),
			activity.NewBusinessRuleTask("rule-node", activity.WithActionName("eval-rule")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc-node"},
			{ID: "f2", Source: "svc-node", Target: "rule-node"},
			{ID: "f3", Source: "rule-node", Target: "end"},
		},
	}
}

// buildPopulatedState creates an InstanceState with tokens, history, tasks, and
// incidents so all slice fields of the projection are exercised.
func buildPopulatedState(t *testing.T) engine.InstanceState {
	t.Helper()
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	actorID := "alice"
	return engine.InstanceState{
		InstanceID: "pop-1",
		DefID:      "populated",
		DefVersion: 2,
		Status:     engine.StatusRunning,
		Variables:  map[string]any{"x": 1},
		StartedAt:  now,
		Tokens: []engine.Token{
			{
				ID:            "t-1",
				NodeID:        "svc-node",
				ScopeID:       "",
				State:         engine.TokenActive,
				Payload:       map[string]any{"k": "v"},
				EnteredAt:     now,
				RetryAttempts: 1,
			},
		},
		History: []engine.NodeVisit{
			{
				NodeID:    "start",
				TokenID:   "t-1",
				EnteredAt: now,
				LeftAt:    &later,
				ActorID:   &actorID,
			},
		},
		Tasks: []humantask.HumanTask{
			{
				TaskToken:  "tt-1",
				NodeID:     "approve",
				InstanceID: "pop-1",
				State:      humantask.Unclaimed,
				Candidates: []string{"alice"},
				CreatedAt:  now,
			},
		},
		Incidents: []engine.Incident{
			{
				ID:        "inc-1",
				TokenID:   "t-1",
				NodeID:    "svc-node",
				ScopeID:   "",
				Error:     "timeout",
				Attempts:  3,
				CreatedAt: now,
			},
		},
	}
}
