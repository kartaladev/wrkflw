// Package service_test is the black-box test suite for the service facade.
package service_test

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
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

// TestProcessInstanceActiveTasks verifies ActiveTasks returns only open
// (Unclaimed|Claimed) tasks, sorted by TaskToken, as a non-nil slice.
func TestProcessInstanceActiveTasks(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		tasks  []humantask.HumanTask
		assert func(t *testing.T, got []humantask.HumanTask)
	}

	cases := []testCase{
		{
			name:  "no tasks yields non-nil empty slice",
			tasks: nil,
			assert: func(t *testing.T, got []humantask.HumanTask) {
				assert.NotNil(t, got)
				assert.Empty(t, got)
			},
		},
		{
			name: "only resolved tasks yields non-nil empty slice",
			tasks: []humantask.HumanTask{
				{TaskToken: "t-1", NodeID: "n-1", State: humantask.Completed},
				{TaskToken: "t-2", NodeID: "n-2", State: humantask.Cancelled},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				assert.NotNil(t, got)
				assert.Empty(t, got)
			},
		},
		{
			name: "returns open tasks sorted by task token",
			tasks: []humantask.HumanTask{
				{TaskToken: "t-3", NodeID: "n-3", State: humantask.Claimed, ClaimedBy: "u-b"},
				{TaskToken: "t-1", NodeID: "n-1", State: humantask.Unclaimed},
				{TaskToken: "t-2", NodeID: "n-2", State: humantask.Completed},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				require.Len(t, got, 2)
				assert.Equal(t, "t-1", got[0].TaskToken)
				assert.Equal(t, humantask.Unclaimed, got[0].State)
				assert.Equal(t, "t-3", got[1].TaskToken)
				assert.Equal(t, humantask.Claimed, got[1].State)
			},
		},
		{
			// Locks the ORDER contract as lexicographic (NOT numeric/creation):
			// with realistic <InstanceID>-hN tokens, "i-1-h10" sorts BEFORE
			// "i-1-h2". A future switch to numeric/creation order would break this.
			name: "ordering is lexicographic by task token (h10 before h2)",
			tasks: []humantask.HumanTask{
				{TaskToken: "i-1-h2", NodeID: "n-a", State: humantask.Unclaimed},
				{TaskToken: "i-1-h10", NodeID: "n-b", State: humantask.Claimed},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				require.Len(t, got, 2)
				assert.Equal(t, "i-1-h10", got[0].TaskToken)
				assert.Equal(t, "i-1-h2", got[1].TaskToken)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning, Tasks: tc.tasks}
			pi := service.NewProcessInstance(nil, st)
			tc.assert(t, pi.ActiveTasks())
		})
	}
}

// TestProcessInstanceActiveTasksConsistentWithState verifies ActiveTasks returns
// exactly the open subset of State().Tasks, in the same TaskToken order — the
// consistency contract from the spec.
func TestProcessInstanceActiveTasksConsistentWithState(t *testing.T) {
	t.Parallel()

	st := engine.InstanceState{
		InstanceID: "i-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "i-1-h3", NodeID: "n-c", State: humantask.Completed},
			{TaskToken: "i-1-h1", NodeID: "n-a", State: humantask.Unclaimed},
			{TaskToken: "i-1-h2", NodeID: "n-b", State: humantask.Claimed},
		},
	}
	pi := service.NewProcessInstance(nil, st)

	// Independently derive the expected open subset from State().Tasks.
	var want []humantask.HumanTask
	for _, task := range pi.State().Tasks {
		if task.IsOpen() {
			want = append(want, task)
		}
	}
	slices.SortFunc(want, func(a, b humantask.HumanTask) int {
		return cmp.Compare(a.TaskToken, b.TaskToken)
	})

	assert.Equal(t, want, pi.ActiveTasks())
}

// TestProcessInstanceActiveTask verifies ActiveTask returns the open task at a
// node (Unclaimed or Claimed), false for resolved/unknown nodes, and the first
// in task-token order when a pathological state has more than one open task at
// the same node.
func TestProcessInstanceActiveTask(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		nodeID string
		tasks  []humantask.HumanTask
		assert func(t *testing.T, got humantask.HumanTask, ok bool)
	}

	cases := []testCase{
		{
			name:   "unclaimed task at node",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Unclaimed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "t-1", got.TaskToken)
				assert.Equal(t, humantask.Unclaimed, got.State)
			},
		},
		{
			name:   "claimed task at node",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Claimed, ClaimedBy: "u-jane"}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "u-jane", got.ClaimedBy)
			},
		},
		{
			name:   "completed task at node is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Completed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
				assert.Zero(t, got)
			},
		},
		{
			name:   "cancelled task at node is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Cancelled}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			// Locks the "only Unclaimed|Claimed are active" contract: an
			// out-of-range TaskState is not open, so it is never returned.
			name:   "out-of-range task state is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.TaskState(999)}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			name:   "unknown node id",
			nodeID: "missing",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Unclaimed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			name:   "two open tasks at same node returns first in token order",
			nodeID: "approve",
			tasks: []humantask.HumanTask{
				{TaskToken: "t-2", NodeID: "approve", State: humantask.Claimed},
				{TaskToken: "t-1", NodeID: "approve", State: humantask.Unclaimed},
			},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "t-1", got.TaskToken)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning, Tasks: tc.tasks}
			pi := service.NewProcessInstance(nil, st)
			got, ok := pi.ActiveTask(tc.nodeID)
			tc.assert(t, got, ok)
		})
	}
}

// TestProcessInstanceActiveTaskConsistentWithState verifies a matched ActiveTask
// equals the corresponding open State().Tasks entry — the spec consistency
// contract for the by-node accessor.
func TestProcessInstanceActiveTaskConsistentWithState(t *testing.T) {
	t.Parallel()

	st := engine.InstanceState{
		InstanceID: "i-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "i-1-h1", NodeID: "approve", State: humantask.Claimed, ClaimedBy: "u-jane"},
		},
	}
	pi := service.NewProcessInstance(nil, st)

	got, ok := pi.ActiveTask("approve")
	require.True(t, ok)
	assert.Equal(t, pi.State().Tasks[0], got)
}

// ExampleProcessInstance_activeTasks shows how a consumer reads the open human
// tasks of a running instance.
func ExampleProcessInstance_activeTasks() {
	st := engine.InstanceState{
		InstanceID: "inst-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "t-1", NodeID: "manager-approval", State: humantask.Claimed, ClaimedBy: "u-jane"},
			{TaskToken: "t-0", NodeID: "validate", State: humantask.Completed},
		},
	}
	inst := service.NewProcessInstance(nil, st)

	task, ok := inst.ActiveTask("manager-approval")
	fmt.Println(ok, task.ClaimedBy)
	fmt.Println(len(inst.ActiveTasks()))
	// Output:
	// true u-jane
	// 1
}
