// Package service_test is the black-box test suite for the service facade.
package service_test

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/idgen"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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

// TestProcessInstanceMarshalEnvelope pins the ProcessInstance envelope (ADR-0144):
// the definition template is embedded verbatim under "definition" via its own
// canonical MarshalJSON, the derived projections it supersedes (action_bindings
// and the top-level scoped_actions mirror) are gone, and the incident audit is
// still carried. The def_id / def_version identity is NOT derived from the
// template — it comes from the instance state and survives; it is pinned by
// TestProcessInstanceMarshalDefinitionIdentity.
func TestProcessInstanceMarshalEnvelope(t *testing.T) {
	t.Parallel()

	// derivedKeys are the projection keys the embedded definition replaces. None
	// of them may appear in the envelope any more, with or without a definition.
	derivedKeys := []string{"action_bindings", "scoped_actions"}

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		state  engine.InstanceState
		assert func(t *testing.T, doc map[string]any)
	}

	cases := []testCase{
		{
			name:  "embeds the definition and keeps the envelope keys",
			def:   sampleDefinition(t),
			state: engine.InstanceState{InstanceID: "i-1", DefID: "order-approval", DefVersion: 3, Status: engine.StatusRunning},
			assert: func(t *testing.T, doc map[string]any) {
				assert.Equal(t, "i-1", doc["instance_id"])
				assert.Equal(t, "running", doc["status"])
				assert.Equal(t, "order-approval", doc["def_id"])
				assert.InEpsilon(t, 3.0, doc["def_version"], 0)

				def, ok := doc["definition"].(map[string]any)
				require.True(t, ok, "definition must be embedded as an object")
				assert.Equal(t, "order-approval", def["id"])
				assert.InEpsilon(t, 3.0, def["version"], 0)
				assert.Equal(t, []any{"charge-card"}, def["scoped_actions"],
					"scoped_actions moves inside the embedded definition")
				assert.Contains(t, def, "nodes")
				assert.Contains(t, def, "flows")
			},
		},
		{
			name:  "nil definition omits the definition key",
			def:   nil,
			state: engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning},
			assert: func(t *testing.T, doc map[string]any) {
				assert.NotContains(t, doc, "definition", "nil def omits definition")
				assert.Contains(t, doc, "def_id", "the identity does not depend on the embed")
				assert.Contains(t, doc, "def_version")
			},
		},
		{
			name: "an open incident is still rendered",
			def:  nil,
			state: engine.InstanceState{
				InstanceID: "i-1",
				Status:     engine.StatusRunning,
				Incidents: []engine.Incident{{
					ID:        "inc-1",
					TokenID:   "tok-1",
					NodeID:    "charge",
					ScopeID:   "scope-1",
					Error:     "gateway timeout",
					Attempts:  3,
					CreatedAt: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
				}},
			},
			assert: func(t *testing.T, doc map[string]any) {
				incidents, ok := doc["incidents"].([]any)
				require.True(t, ok, "incidents must survive the envelope trim")
				require.Len(t, incidents, 1)
				inc, ok := incidents[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "inc-1", inc["id"])
				assert.Equal(t, "tok-1", inc["token_id"])
				assert.Equal(t, "charge", inc["node_id"])
				assert.Equal(t, "scope-1", inc["scope_id"])
				assert.Equal(t, "gateway timeout", inc["error"])
				assert.InEpsilon(t, 3.0, inc["attempts"], 0)
				assert.Equal(t, "2026-07-27T10:00:00Z", inc["created_at"])
			},
		},
		{
			name:  "no incidents omits the key",
			def:   nil,
			state: engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning},
			assert: func(t *testing.T, doc map[string]any) {
				assert.NotContains(t, doc, "incidents")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(service.NewProcessInstance(tc.def, tc.state))
			require.NoError(t, err)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(data, &doc))

			for _, key := range derivedKeys {
				assert.NotContains(t, doc, key, "%s is superseded by the embedded definition", key)
			}
			tc.assert(t, doc)
		})
	}
}

// TestProcessInstanceMarshalDefinitionIdentity pins the definition identity as
// UNCONDITIONAL: def_id / def_version are read off the instance state, so they
// are present whether or not the template itself could be resolved. They are the
// stable key a consumer routes or groups on; the `definition` embed is a
// best-effort convenience that disappears when the definition is nil.
func TestProcessInstanceMarshalDefinitionIdentity(t *testing.T) {
	t.Parallel()

	state := func() engine.InstanceState {
		return engine.InstanceState{
			InstanceID: "i-1",
			DefID:      "order-approval",
			DefVersion: 3,
			Status:     engine.StatusRunning,
		}
	}

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		state  engine.InstanceState
		assert func(t *testing.T, doc map[string]any)
	}

	cases := []testCase{
		{
			name:  "nil definition still carries the identity",
			def:   nil,
			state: state(),
			assert: func(t *testing.T, doc map[string]any) {
				assert.Equal(t, "order-approval", doc["def_id"])
				assert.InEpsilon(t, 3.0, doc["def_version"], 0)
				assert.NotContains(t, doc, "definition", "an unresolved definition is omitted")
			},
		},
		{
			name:  "resolved definition carries the identity and the embed",
			def:   sampleDefinition(t),
			state: state(),
			assert: func(t *testing.T, doc map[string]any) {
				assert.Equal(t, "order-approval", doc["def_id"])
				assert.InEpsilon(t, 3.0, doc["def_version"], 0)

				def, ok := doc["definition"].(map[string]any)
				require.True(t, ok, "definition must be embedded as an object")
				assert.Equal(t, doc["def_id"], def["id"], "identity agrees with the embed")
				assert.Equal(t, doc["def_version"], def["version"])
			},
		},
		{
			name:  "an unstamped state renders the zero identity",
			def:   nil,
			state: engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning},
			assert: func(t *testing.T, doc map[string]any) {
				assert.Equal(t, "", doc["def_id"], "identity is unconditional, not omitempty")
				assert.Equal(t, 0.0, doc["def_version"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(service.NewProcessInstance(tc.def, tc.state))
			require.NoError(t, err)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(data, &doc))
			require.Contains(t, doc, "def_id")
			require.Contains(t, doc, "def_version")

			tc.assert(t, doc)
		})
	}
}

// TestProcessInstanceMarshalTasks pins the human-task audit shape (ADR-0147):
// candidates, claim and completion are rendered by embedding the humantask /
// authz types, which carry their own wire tags — the view adds no DTO and no
// re-mapping, so an actor is passed through verbatim as {id, roles?, attributes?}.
func TestProcessInstanceMarshalTasks(t *testing.T) {
	t.Parallel()

	jane := authz.Actor{
		ID:         "u-jane",
		Roles:      []string{"manager"},
		Attributes: map[string]any{"email": "jane.doe@acme.com"},
	}
	claimedAt := time.Date(2026, 7, 27, 10, 5, 0, 0, time.UTC)
	completedAt := time.Date(2026, 7, 27, 10, 15, 0, 0, time.UTC)

	type testCase struct {
		name   string
		task   humantask.HumanTask
		assert func(t *testing.T, task map[string]any)
	}

	cases := []testCase{
		{
			name: "full audit renders candidates, claim and completion",
			task: humantask.HumanTask{
				TaskID:     "t-1",
				NodeID:     "manager_review",
				State:      humantask.Completed,
				Candidates: []authz.Actor{jane},
				Claim:      &humantask.Claim{Actor: jane, At: claimedAt},
				Completion: &humantask.Completion{
					Actor:   jane,
					At:      completedAt,
					Outcome: "approve",
					Note:    "Budget confirmed.",
				},
				CreatedAt: time.Date(2026, 7, 27, 10, 0, 30, 0, time.UTC),
			},
			assert: func(t *testing.T, task map[string]any) {
				assert.Equal(t, "t-1", task["task_id"])
				assert.Equal(t, "manager_review", task["node_id"])
				assert.Equal(t, "completed", task["state"])

				wantActor := map[string]any{
					"id":         "u-jane",
					"roles":      []any{"manager"},
					"attributes": map[string]any{"email": "jane.doe@acme.com"},
				}
				assert.Equal(t, []any{wantActor}, task["candidates"])
				assert.Equal(t, map[string]any{
					"actor":     wantActor,
					"timestamp": "2026-07-27T10:05:00Z",
				}, task["claim"])
				assert.Equal(t, map[string]any{
					"actor":     wantActor,
					"timestamp": "2026-07-27T10:15:00Z",
					"outcome":   "approve",
					"note":      "Budget confirmed.",
				}, task["completion"])
			},
		},
		{
			name: "unclaimed task omits claim and completion",
			task: humantask.HumanTask{
				TaskID:     "t-2",
				NodeID:     "finance_approval",
				State:      humantask.Unclaimed,
				Candidates: []authz.Actor{{ID: "u-raj"}},
			},
			assert: func(t *testing.T, task map[string]any) {
				assert.Equal(t, "unclaimed", task["state"])
				assert.NotContains(t, task, "claim")
				assert.NotContains(t, task, "completion")
				assert.Equal(t, []any{map[string]any{"id": "u-raj"}}, task["candidates"],
					"an actor with no roles or attributes renders as id only")
			},
		},
		{
			name: "claimed but not completed renders claim without completion",
			task: humantask.HumanTask{
				TaskID: "t-3",
				NodeID: "finance_approval",
				State:  humantask.Claimed,
				Claim:  &humantask.Claim{Actor: jane, At: claimedAt},
			},
			assert: func(t *testing.T, task map[string]any) {
				assert.Equal(t, "claimed", task["state"])
				assert.Contains(t, task, "claim")
				assert.NotContains(t, task, "completion")
			},
		},
		{
			// A Manual && ManualImmediate user task is marked Completed inline by
			// the engine, with no actor and no Completion record (ADR-0147
			// amendment #5). "completed" therefore does NOT imply a completion key.
			name: "immediate manual task is completed with no completion record",
			task: humantask.HumanTask{
				TaskID: "t-4",
				NodeID: "acknowledge",
				State:  humantask.Completed,
			},
			assert: func(t *testing.T, task map[string]any) {
				assert.Equal(t, "completed", task["state"])
				assert.NotContains(t, task, "completion",
					"an inline manual completion has no actor to record")
			},
		},
		{
			name: "empty candidate list is omitted",
			task: humantask.HumanTask{
				TaskID:     "t-5",
				NodeID:     "review",
				State:      humantask.Unclaimed,
				Candidates: []authz.Actor{},
			},
			assert: func(t *testing.T, task map[string]any) {
				assert.NotContains(t, task, "candidates")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := engine.InstanceState{
				InstanceID: "i-1",
				Status:     engine.StatusRunning,
				Tasks:      []humantask.HumanTask{tc.task},
			}
			data, err := json.Marshal(service.NewProcessInstance(nil, st))
			require.NoError(t, err)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(data, &doc))

			tasks, ok := doc["tasks"].([]any)
			require.True(t, ok, "tasks must be rendered")
			require.Len(t, tasks, 1)
			task, ok := tasks[0].(map[string]any)
			require.True(t, ok, "a task must be a JSON object")

			assert.NotContains(t, task, "claimed_by", "claimed_by is superseded by claim")
			tc.assert(t, task)
		})
	}
}

// TestProcessInstanceMarshalHistory pins the node-visit audit linkage
// (ADR-0145): a user-task visit carries the task_id of the task it minted, and
// close_kind is emitted only for an ABNORMAL close — a normal advance omits it.
func TestProcessInstanceMarshalHistory(t *testing.T) {
	t.Parallel()

	enteredAt := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	leftAt := time.Date(2026, 7, 27, 10, 15, 0, 0, time.UTC)

	type testCase struct {
		name   string
		visit  engine.NodeVisit
		assert func(t *testing.T, visit map[string]any)
	}

	cases := []testCase{
		{
			name:  "normal advance omits task_id and close_kind",
			visit: engine.NodeVisit{NodeID: "charge", TokenID: "tok-1", EnteredAt: enteredAt, LeftAt: &leftAt},
			assert: func(t *testing.T, visit map[string]any) {
				assert.Equal(t, "charge", visit["node_id"])
				assert.Equal(t, "tok-1", visit["token_id"])
				assert.Equal(t, "2026-07-27T10:00:00Z", visit["entered_at"])
				assert.Equal(t, "2026-07-27T10:15:00Z", visit["left_at"])
				assert.NotContains(t, visit, "task_id")
				assert.NotContains(t, visit, "close_kind")
			},
		},
		{
			name:  "user-task visit links to its task",
			visit: engine.NodeVisit{NodeID: "manager_review", TokenID: "tok-1", EnteredAt: enteredAt, TaskID: "t-1"},
			assert: func(t *testing.T, visit map[string]any) {
				assert.Equal(t, "t-1", visit["task_id"])
				assert.NotContains(t, visit, "left_at", "an open visit has not left yet")
			},
		},
		{
			name: "abnormal close carries close_kind",
			visit: engine.NodeVisit{
				NodeID: "manager_review", TokenID: "tok-1", EnteredAt: enteredAt, LeftAt: &leftAt,
				TaskID: "t-1", CloseKind: engine.CloseKindInstanceCancelled,
			},
			assert: func(t *testing.T, visit map[string]any) {
				// The document carries the enum's wire value, not the Go type.
				assert.Equal(t, engine.CloseKindInstanceCancelled.String(), visit["close_kind"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := engine.InstanceState{
				InstanceID: "i-1",
				Status:     engine.StatusRunning,
				History:    []engine.NodeVisit{tc.visit},
			}
			data, err := json.Marshal(service.NewProcessInstance(nil, st))
			require.NoError(t, err)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(data, &doc))

			history, ok := doc["history"].([]any)
			require.True(t, ok, "history must be rendered")
			require.Len(t, history, 1)
			visit, ok := history[0].(map[string]any)
			require.True(t, ok, "a visit must be a JSON object")

			assert.NotContains(t, visit, "actor_id", "the actor is resolved from the linked task")
			tc.assert(t, visit)
		})
	}
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
			name:       "TokenWaiting maps to waiting",
			tokenState: engine.TokenWaiting,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "waiting", got)
			},
		},
		{
			name:       "TokenJoining maps to joining",
			tokenState: engine.TokenJoining,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "joining", got)
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
// (Unclaimed|Claimed) tasks, sorted by TaskID, as a non-nil slice.
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
				{TaskID: "t-1", NodeID: "n-1", State: humantask.Completed},
				{TaskID: "t-2", NodeID: "n-2", State: humantask.Cancelled},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				assert.NotNil(t, got)
				assert.Empty(t, got)
			},
		},
		{
			name: "returns open tasks sorted by task id",
			tasks: []humantask.HumanTask{
				{TaskID: "t-3", NodeID: "n-3", State: humantask.Claimed, Claim: &humantask.Claim{Actor: authz.Actor{ID: "u-b"}}},
				{TaskID: "t-1", NodeID: "n-1", State: humantask.Unclaimed},
				{TaskID: "t-2", NodeID: "n-2", State: humantask.Completed},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				require.Len(t, got, 2)
				assert.Equal(t, "t-1", got[0].TaskID)
				assert.Equal(t, humantask.Unclaimed, got[0].State)
				assert.Equal(t, "t-3", got[1].TaskID)
				assert.Equal(t, humantask.Claimed, got[1].State)
			},
		},
		{
			// Locks the ORDER contract as lexicographic (NOT numeric/creation):
			// with realistic <InstanceID>-hN tokens, "i-1-h10" sorts BEFORE
			// "i-1-h2". A future switch to numeric/creation order would break this.
			name: "ordering is lexicographic by task id (h10 before h2)",
			tasks: []humantask.HumanTask{
				{TaskID: "i-1-h2", NodeID: "n-a", State: humantask.Unclaimed},
				{TaskID: "i-1-h10", NodeID: "n-b", State: humantask.Claimed},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				require.Len(t, got, 2)
				assert.Equal(t, "i-1-h10", got[0].TaskID)
				assert.Equal(t, "i-1-h2", got[1].TaskID)
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
// exactly the open subset of State().Tasks, in the same TaskID order — the
// consistency contract from the spec.
func TestProcessInstanceActiveTasksConsistentWithState(t *testing.T) {
	t.Parallel()

	st := engine.InstanceState{
		InstanceID: "i-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskID: "i-1-h3", NodeID: "n-c", State: humantask.Completed},
			{TaskID: "i-1-h1", NodeID: "n-a", State: humantask.Unclaimed},
			{TaskID: "i-1-h2", NodeID: "n-b", State: humantask.Claimed},
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
		return cmp.Compare(a.TaskID, b.TaskID)
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
			tasks:  []humantask.HumanTask{{TaskID: "t-1", NodeID: "approve", State: humantask.Unclaimed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "t-1", got.TaskID)
				assert.Equal(t, humantask.Unclaimed, got.State)
			},
		},
		{
			name:   "claimed task at node",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskID: "t-1", NodeID: "approve", State: humantask.Claimed, Claim: &humantask.Claim{Actor: authz.Actor{ID: "u-jane"}}}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				require.NotNil(t, got.Claim)
				assert.Equal(t, "u-jane", got.Claim.Actor.ID)
			},
		},
		{
			name:   "completed task at node is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskID: "t-1", NodeID: "approve", State: humantask.Completed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
				assert.Zero(t, got)
			},
		},
		{
			name:   "cancelled task at node is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskID: "t-1", NodeID: "approve", State: humantask.Cancelled}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			// Locks the "only Unclaimed|Claimed are active" contract: an
			// out-of-range TaskState is not open, so it is never returned.
			name:   "out-of-range task state is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskID: "t-1", NodeID: "approve", State: humantask.TaskState(999)}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			name:   "unknown node id",
			nodeID: "missing",
			tasks:  []humantask.HumanTask{{TaskID: "t-1", NodeID: "approve", State: humantask.Unclaimed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			name:   "two open tasks at same node returns first in token order",
			nodeID: "approve",
			tasks: []humantask.HumanTask{
				{TaskID: "t-2", NodeID: "approve", State: humantask.Claimed},
				{TaskID: "t-1", NodeID: "approve", State: humantask.Unclaimed},
			},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "t-1", got.TaskID)
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
			{TaskID: "i-1-h1", NodeID: "approve", State: humantask.Claimed, Claim: &humantask.Claim{Actor: authz.Actor{ID: "u-jane"}}},
		},
	}
	pi := service.NewProcessInstance(nil, st)

	got, ok := pi.ActiveTask("approve")
	require.True(t, ok)
	assert.Equal(t, pi.State().Tasks[0], got)
}

// auditedTaskState returns an instance state holding ONE open task with every
// mutable audit field populated — candidates, claim, completion, eligibility
// slices, the variable snapshot and the due date. It is the fixture for the
// defensive-copy contract: each of those fields is reachable by pointer or by
// slice/map header from the state the engine owns.
func auditedTaskState() engine.InstanceState {
	at := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	due := at.Add(72 * time.Hour)
	jane := authz.Actor{
		ID:         "u-jane",
		Roles:      []string{"manager"},
		Attributes: map[string]any{"email": "jane.doe@acme.com"},
	}

	return engine.InstanceState{
		InstanceID: "i-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{{
			TaskID:      "i-1-h1",
			InstanceID:  "i-1",
			NodeID:      "approve",
			State:       humantask.Claimed,
			Eligibility: authz.AuthzSpec{Roles: []string{"manager"}, Privileges: []string{"order approve"}},
			Candidates:  []authz.Actor{jane},
			Claim:       &humantask.Claim{Actor: jane, At: at},
			Completion:  &humantask.Completion{Actor: jane, At: at, Outcome: "approve", Note: "ok"},
			CreatedAt:   at,
			DueAt:       &due,
			Vars:        map[string]any{"region": "EU"},
		}},
	}
}

// TestProcessInstanceActiveTasksDefensiveCopy pins the isolation contract: the
// tasks handed out by ActiveTasks / ActiveTask are clones, so a consumer writing
// through the returned value — including through the Claim pointer, the
// Candidates slice or the Vars map — cannot corrupt the instance's audit record.
// Mirrors runtime/view's "Clone before exposing" guard.
func TestProcessInstanceActiveTasksDefensiveCopy(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		mutate func(t *testing.T, got *humantask.HumanTask)
		assert func(t *testing.T, live humantask.HumanTask)
	}

	cases := []testCase{
		{
			name: "claim actor id",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotNil(t, got.Claim)
				got.Claim.Actor.ID = "spoofed"
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				require.NotNil(t, live.Claim)
				assert.Equal(t, "u-jane", live.Claim.Actor.ID)
			},
		},
		{
			name: "claim actor roles",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotNil(t, got.Claim)
				got.Claim.Actor.Roles[0] = "admin"
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				require.NotNil(t, live.Claim)
				assert.Equal(t, []string{"manager"}, live.Claim.Actor.Roles)
			},
		},
		{
			name: "claim actor attributes",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotNil(t, got.Claim)
				got.Claim.Actor.Attributes["email"] = "attacker@evil.example"
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				require.NotNil(t, live.Claim)
				assert.Equal(t, "jane.doe@acme.com", live.Claim.Actor.Attributes["email"])
			},
		},
		{
			name: "completion record",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotNil(t, got.Completion)
				got.Completion.Outcome = "reject"
				got.Completion.Actor.ID = "spoofed"
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				require.NotNil(t, live.Completion)
				assert.Equal(t, "approve", live.Completion.Outcome)
				assert.Equal(t, "u-jane", live.Completion.Actor.ID)
			},
		},
		{
			name: "candidate element",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotEmpty(t, got.Candidates)
				got.Candidates[0].ID = "spoofed"
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				require.Len(t, live.Candidates, 1)
				assert.Equal(t, "u-jane", live.Candidates[0].ID)
			},
		},
		{
			name: "candidate slice append",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				got.Candidates = append(got.Candidates, authz.Actor{ID: "u-intruder"})
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				require.Len(t, live.Candidates, 1)
				assert.Equal(t, "u-jane", live.Candidates[0].ID)
			},
		},
		{
			name: "eligibility slices",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotEmpty(t, got.Eligibility.Roles)
				require.NotEmpty(t, got.Eligibility.Privileges)
				got.Eligibility.Roles[0] = "anyone"
				got.Eligibility.Privileges[0] = "order *"
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				assert.Equal(t, []string{"manager"}, live.Eligibility.Roles)
				assert.Equal(t, []string{"order approve"}, live.Eligibility.Privileges)
			},
		},
		{
			name: "variable snapshot",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotNil(t, got.Vars)
				got.Vars["region"] = "US"
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				assert.Equal(t, "EU", live.Vars["region"])
			},
		},
		{
			name: "due date",
			mutate: func(t *testing.T, got *humantask.HumanTask) {
				require.NotNil(t, got.DueAt)
				*got.DueAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
			},
			assert: func(t *testing.T, live humantask.HumanTask) {
				require.NotNil(t, live.DueAt)
				assert.Equal(t, time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC), *live.DueAt)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// ActiveTasks: mutate the returned task, then re-read the state.
			listInstance := service.NewProcessInstance(nil, auditedTaskState())
			list := listInstance.ActiveTasks()
			require.Len(t, list, 1)
			tc.mutate(t, &list[0])
			tc.assert(t, listInstance.State().Tasks[0])

			// ActiveTask must inherit the same isolation (it delegates to ActiveTasks).
			byNodeInstance := service.NewProcessInstance(nil, auditedTaskState())
			byNode, ok := byNodeInstance.ActiveTask("approve")
			require.True(t, ok)
			tc.mutate(t, &byNode)
			tc.assert(t, byNodeInstance.State().Tasks[0])
		})
	}
}

// sampleDocumentPath is the canonical target shape for the marshalled
// ProcessInstance document (ADR-0144/0145/0147). It is authored by hand and this
// package's fixtures reproduce it, so a drift in either direction fails loudly.
const sampleDocumentPath = "../docs/specs/assets/process-instance-sample.json"

// sampleDefinition builds the "order-approval" template of the sample document
// through the public Go authoring API. Flow ids are the builder's auto-generated
// "source->target" form, which is exactly what the sample records.
func sampleDefinition(t *testing.T) *model.ProcessDefinition {
	t.Helper()

	noopAction := func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }

	def, err := model.NewBuilder("order-approval", 3).
		RegisterActionFunc("charge-card", noopAction).
		CancelActions("refund").
		Add(event.NewStart("start",
			event.WithName("Start"),
			event.WithLabel("Order received"))).
		Add(activity.NewServiceTask("charge",
			activity.WithName("Charge Card"),
			activity.WithLabel("Charge the customer's card"),
			activity.WithTaskAction("charge-card"))).
		Add(activity.NewUserTask("manager_review",
			activity.WithName("Manager Review"),
			activity.WithLabel("Manager sign-off on order"),
			activity.WithEligibleRoles("manager"),
			activity.WithOutcomes("approve", "reject", "revise"),
			activity.WithOutcomeVariable("manager_approved"))).
		Add(gateway.NewExclusive("manager_decision",
			gateway.WithName("Manager Approved?"),
			gateway.WithLabel("Did the manager approve?"))).
		Add(activity.NewUserTask("finance_approval",
			activity.WithName("Finance Approval"),
			activity.WithLabel("Finance approval of spend"),
			activity.WithEligibleRoles("finance"),
			activity.WithOutcomes("approve", "reject"),
			activity.WithOutcomeVariable("finance_approved"))).
		Add(gateway.NewExclusive("finance_decision",
			gateway.WithName("Finance Approved?"),
			gateway.WithLabel("Did finance approve?"))).
		Add(activity.NewServiceTask("fulfill",
			activity.WithName("Fulfill Order"),
			activity.WithLabel("Ship the order to the customer"),
			activity.WithTaskAction("ship-order"))).
		Add(activity.NewServiceTask("notify_rejection",
			activity.WithName("Notify Rejection"),
			activity.WithLabel("Email the customer about the rejection"),
			activity.WithTaskAction("send-rejection-email"))).
		Add(event.NewEnd("approved_end",
			event.WithName("Approved End"),
			event.WithLabel("Order completed"))).
		Add(event.NewEnd("rejected_end",
			event.WithName("Rejected End"),
			event.WithLabel("Order rejected"))).
		Connect("start", "charge").
		Connect("charge", "manager_review").
		Connect("manager_review", "manager_decision").
		Connect("manager_decision", "finance_approval", flow.WithCondition(`manager_approved == "approve"`)).
		Connect("manager_decision", "notify_rejection", flow.AsDefault()).
		Connect("finance_approval", "finance_decision").
		Connect("finance_decision", "fulfill", flow.WithCondition(`finance_approved == "approve"`)).
		Connect("finance_decision", "notify_rejection", flow.AsDefault()).
		Connect("fulfill", "approved_end").
		Connect("notify_rejection", "rejected_end").
		Build()
	require.NoError(t, err)

	return def
}

// sampleState reproduces the running state of the sample document: the manager
// review is completed with a full claim/completion audit, and the token waits on
// a finance approval that is claimed but not yet completed.
func sampleState() engine.InstanceState {
	at := func(hour, min, sec int) time.Time {
		return time.Date(2026, 7, 27, hour, min, sec, 0, time.UTC)
	}
	ptr := func(ts time.Time) *time.Time { return &ts }

	jane := authz.Actor{
		ID:         "u-jane",
		Roles:      []string{"manager"},
		Attributes: map[string]any{"username": "jane.doe", "email": "jane.doe@acme.com"},
	}
	mike := authz.Actor{
		ID:         "u-mike",
		Roles:      []string{"manager"},
		Attributes: map[string]any{"username": "mike.ross", "email": "mike.ross@acme.com"},
	}
	raj := authz.Actor{
		ID:         "u-raj",
		Roles:      []string{"finance"},
		Attributes: map[string]any{"username": "raj.patel", "email": "raj.patel@acme.com"},
	}

	const (
		tokenID       = "d9jiglp83g3g3kqpfod0"
		managerTaskID = "d9jiglp83g3g3kqpfodg"
		financeTaskID = "d9jiglp83g3g3kqpfoe0"
	)

	return engine.InstanceState{
		InstanceID: "d9jgd3p83g3m1pr6i6d0",
		DefID:      "order-approval",
		DefVersion: 3,
		Status:     engine.StatusRunning,
		Variables: map[string]any{
			"amount":           250,
			"currency":         "USD",
			"manager_approved": "approve",
		},
		StartedAt: at(10, 0, 0),
		Tokens: []engine.Token{{
			ID:        tokenID,
			NodeID:    "finance_approval",
			State:     engine.TokenWaiting,
			EnteredAt: at(10, 15, 0),
		}},
		History: []engine.NodeVisit{
			{NodeID: "start", TokenID: tokenID, EnteredAt: at(10, 0, 0), LeftAt: ptr(at(10, 0, 0))},
			{NodeID: "charge", TokenID: tokenID, EnteredAt: at(10, 0, 0), LeftAt: ptr(at(10, 0, 30))},
			{NodeID: "manager_review", TokenID: tokenID, EnteredAt: at(10, 0, 30), LeftAt: ptr(at(10, 15, 0)), TaskID: managerTaskID},
			{NodeID: "manager_decision", TokenID: tokenID, EnteredAt: at(10, 15, 0), LeftAt: ptr(at(10, 15, 0))},
			{NodeID: "finance_approval", TokenID: tokenID, EnteredAt: at(10, 15, 0), TaskID: financeTaskID},
		},
		Tasks: []humantask.HumanTask{
			{
				TaskID:     managerTaskID,
				InstanceID: "d9jgd3p83g3m1pr6i6d0",
				NodeID:     "manager_review",
				State:      humantask.Completed,
				Candidates: []authz.Actor{jane, mike},
				Claim:      &humantask.Claim{Actor: jane, At: at(10, 5, 0)},
				Completion: &humantask.Completion{
					Actor:   jane,
					At:      at(10, 15, 0),
					Outcome: "approve",
					Note:    "Budget confirmed, approved for processing.",
				},
				CreatedAt: at(10, 0, 30),
			},
			{
				TaskID:     financeTaskID,
				InstanceID: "d9jgd3p83g3m1pr6i6d0",
				NodeID:     "finance_approval",
				State:      humantask.Claimed,
				Candidates: []authz.Actor{raj},
				Claim:      &humantask.Claim{Actor: raj, At: at(10, 16, 0)},
				CreatedAt:  at(10, 15, 0),
			},
		},
	}
}

// TestProcessInstanceMarshalMatchesSampleDocument asserts the whole marshalled
// document against docs/specs/assets/process-instance-sample.json — the canonical
// target shape of the audit view.
//
// The fixture is built through the Go API rather than the HTTP transport simply
// because it is the cheaper way to drive a whole graph deterministically.
//
// ⚠ It is NOT because the transport cannot carry attributes. It used to be: ADR-0147
// amendment #5 recorded that httpcore.Actor was {id, roles} only, so claim.actor and
// completion.actor could never carry attributes over HTTP. ADR-0189 deleted that type
// and the actor now reaches the engine whole, so the limitation is gone.
// The comparison is marshal-side only — scoped_actions is derived, marshal-only
// state (ADR-0144), so a JSON→struct→JSON round trip would drop it.
func TestProcessInstanceMarshalMatchesSampleDocument(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(sampleDocumentPath)
	require.NoError(t, err)

	got, err := json.Marshal(service.NewProcessInstance(sampleDefinition(t), sampleState()))
	require.NoError(t, err)

	assert.JSONEq(t, string(want), string(got))
}

// countingIDGenerator returns a deterministic counter generator ("id-1", "id-2",
// …) so an engine-driven fixture has stable ids. Product wiring uses idgen.XID().
func countingIDGenerator() idgen.Generator {
	var (
		mu sync.Mutex
		n  int
	)
	return idgen.Func(func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("id-%d", n), nil
	})
}

// TestProcessInstanceMarshalFromDrivenEngine drives a real user task through the
// service Go API — start, claim, complete — and asserts the marshalled document
// carries the audit the engine actually produced: resolver-sourced candidates
// with their attributes, the claim and completion records, and the history entry
// linked to the task by task_id.
//
// The whole graph runs on a deterministic id generator, so the document is
// reproducible; the transport is bypassed for convenience, not necessity.
// ⚠ ADR-0189 removed httpcore.Actor and the HTTP path now carries actor attributes,
// so ADR-0147 amendment #5's "over HTTP those two slots can never carry attributes"
// no longer holds.
func TestProcessInstanceMarshalFromDrivenEngine(t *testing.T) {
	t.Parallel()

	jane := authz.Actor{
		ID:         "u-jane",
		Roles:      []string{"manager"},
		Attributes: map[string]any{"username": "jane.doe", "email": "jane.doe@acme.com"},
	}

	def, err := model.NewBuilder("driven-approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review",
			activity.WithName("Review"),
			activity.WithEligibleRoles("manager"),
			activity.WithOutcomes("approve", "reject"),
			activity.WithOutcomeVariable("decision"))).
		Add(event.NewEnd("end")).
		Connect("start", "review").
		Connect("review", "end").
		Build()
	require.NoError(t, err)

	gen := countingIDGenerator()
	clk := clockwork.NewFakeClockAt(time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC))
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {jane}})
	az := authz.RoleAuthorizer{}

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	reg := kernel.NewMapDefinitionRegistry(def)

	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(store),
		runtime.WithDefinitions(reg),
		runtime.WithClock(clk),
		runtime.WithIDGenerator(gen),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	require.NoError(t, err)

	eng, err := service.NewProcessEngine(
		service.WithProcessDriver(driver),
		service.WithInstanceStore(store),
		service.WithDefinitions(reg),
		service.WithLister(store),
		service.WithHumanTasks(taskStore, az),
		service.WithClock(clk),
		service.WithIDGenerator(gen),
	)
	require.NoError(t, err)

	ctx := t.Context()

	parked, err := eng.StartInstance(ctx, service.StartInstanceRequest{
		DefRef: def.Qualifier(),
		Vars:   map[string]any{"amount": 250},
	})
	require.NoError(t, err)
	require.Equal(t, "id-1", parked.State().InstanceID, "the injected counter mints the instance id first")

	open, ok := parked.ActiveTask("review")
	require.True(t, ok, "instance must park on the user task")

	_, err = eng.ClaimTask(ctx, service.ClaimTaskRequest{TaskID: open.TaskID, Actor: jane})
	require.NoError(t, err)

	done, err := eng.CompleteTask(ctx, service.CompleteTaskRequest{
		TaskID:  open.TaskID,
		Actor:   jane,
		Outcome: "approve",
		Note:    "Budget confirmed.",
	})
	require.NoError(t, err)

	data, err := json.Marshal(done)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))

	assert.Equal(t, "id-1", doc["instance_id"])
	assert.Equal(t, "driven-approval", doc["def_id"], "the engine stamps the identity onto the state")
	assert.InEpsilon(t, 1.0, doc["def_version"], 0)
	assert.Contains(t, doc, "definition", "a driven instance carries its template")

	tasks, ok := doc["tasks"].([]any)
	require.True(t, ok, "tasks must be rendered")
	require.Len(t, tasks, 1)
	task, ok := tasks[0].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, open.TaskID, task["task_id"])
	assert.Equal(t, "review", task["node_id"])
	assert.Equal(t, "completed", task["state"])
	assert.NotContains(t, task, "claimed_by", "claimed_by is superseded by claim")

	candidates, ok := task["candidates"].([]any)
	require.True(t, ok, "resolver-sourced candidates must reach the view")
	require.Len(t, candidates, 1)
	assert.Equal(t, map[string]any{
		"id":         "u-jane",
		"roles":      []any{"manager"},
		"attributes": map[string]any{"username": "jane.doe", "email": "jane.doe@acme.com"},
	}, candidates[0], "candidates are passed through verbatim")

	claim, ok := task["claim"].(map[string]any)
	require.True(t, ok, "claim must be an object")
	assert.Equal(t, "u-jane", claim["actor"].(map[string]any)["id"])
	assert.Contains(t, claim, "timestamp")

	completion, ok := task["completion"].(map[string]any)
	require.True(t, ok, "completion must be an object")
	assert.Equal(t, "u-jane", completion["actor"].(map[string]any)["id"])
	assert.Equal(t, "approve", completion["outcome"])
	assert.Equal(t, "Budget confirmed.", completion["note"])
	assert.Contains(t, completion, "timestamp")

	history, ok := doc["history"].([]any)
	require.True(t, ok)
	var reviewVisit map[string]any
	for _, entry := range history {
		visit, isObject := entry.(map[string]any)
		require.True(t, isObject)
		if visit["node_id"] == "review" {
			reviewVisit = visit
		}
	}
	require.NotNil(t, reviewVisit, "history must record the user-task visit")
	assert.Equal(t, open.TaskID, reviewVisit["task_id"], "the visit links to its task by task_id")
	assert.NotContains(t, reviewVisit, "close_kind", "a normal completion is not an abnormal close")
}

// TestProcessInstanceMarshalDefinitionEmbedPolicy pins the embed opt-out: the
// ADR-0144 embed stays the DEFAULT, service.WithoutEmbeddedDefinition() drops
// the `definition` key from every document the facade hands out, and neither
// setting touches the def_id / def_version identity or the Definition() Go
// accessor.
func TestProcessInstanceMarshalDefinitionEmbedPolicy(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opts   []service.Option
		assert func(t *testing.T, doc map[string]any, pi service.ProcessInstance)
	}

	cases := []testCase{
		{
			name: "default embeds the definition",
			opts: nil,
			assert: func(t *testing.T, doc map[string]any, pi service.ProcessInstance) {
				def, ok := doc["definition"].(map[string]any)
				require.True(t, ok, "the ADR-0144 embed is the default")
				assert.Equal(t, "greeting", def["id"])
				assert.NotNil(t, pi.Definition())
			},
		},
		{
			name: "WithoutEmbeddedDefinition drops the embed",
			opts: []service.Option{service.WithoutEmbeddedDefinition()},
			assert: func(t *testing.T, doc map[string]any, pi service.ProcessInstance) {
				assert.NotContains(t, doc, "definition",
					"the opt-out suppresses the template, not the identity")
				assert.Equal(t, "greeting", doc["def_id"])
				assert.InEpsilon(t, 1.0, doc["def_version"], 0)
				assert.NotNil(t, pi.Definition(),
					"the Go accessor is unaffected by the marshalling policy")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// linearDef's service task has no registered action, so the instance
			// parks on an incident — irrelevant here: what is under test is the
			// envelope, which is identical for every terminal status.
			def := linearDef()
			eng, err := service.NewProcessEngine(
				append([]service.Option{service.WithDefinitions(regWith(t, def))}, tc.opts...)...)
			require.NoError(t, err)

			started, err := eng.StartInstance(t.Context(), service.StartInstanceRequest{DefRef: defRefFor(def)})
			require.NoError(t, err)
			fetched, err := eng.GetInstance(t.Context(), started.State().InstanceID)
			require.NoError(t, err)

			// Both the write path and the read path a UI polls must agree.
			paths := []struct {
				name string
				pi   service.ProcessInstance
			}{
				{name: "StartInstance", pi: started},
				{name: "GetInstance", pi: fetched},
			}
			for _, path := range paths {
				t.Run(path.name, func(t *testing.T) {
					data, err := json.Marshal(path.pi)
					require.NoError(t, err)

					var doc map[string]any
					require.NoError(t, json.Unmarshal(data, &doc))
					require.Contains(t, doc, "def_id", "identity is unconditional")
					require.Contains(t, doc, "def_version")

					tc.assert(t, doc, path.pi)
				})
			}
		})
	}
}

// TestNewProcessInstanceEmbedsByDefault pins the exported fabrication
// constructor: it is unaffected by the engine-level opt-out and always embeds a
// non-nil definition, so a consumer building a ProcessInstance by hand keeps the
// ADR-0144 self-contained document.
func TestNewProcessInstanceEmbedsByDefault(t *testing.T) {
	t.Parallel()

	st := engine.InstanceState{InstanceID: "i-1", DefID: "greeting", DefVersion: 1, Status: engine.StatusRunning}
	data, err := json.Marshal(service.NewProcessInstance(linearDef(), st))
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	assert.Contains(t, doc, "definition")
	assert.Equal(t, "greeting", doc["def_id"])
	assert.InEpsilon(t, 1.0, doc["def_version"], 0)
}

// ExampleWithoutEmbeddedDefinition shows how a consumer that already holds the
// process template — a UI polling a single instance, say — drops the duplicated
// `definition` embed from every marshalled document while keeping the def_id /
// def_version identity and the in-process Definition() accessor.
func ExampleWithoutEmbeddedDefinition() {
	def := &model.ProcessDefinition{
		ID: "greeting", Version: 1,
		Nodes: []model.Node{event.NewStart("start"), event.NewEnd("end")},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}

	eng, err := service.NewProcessEngine(
		service.WithDefinitions(kernel.NewMapDefinitionRegistry(def)),
		service.WithoutEmbeddedDefinition(),
	)
	if err != nil {
		panic(err)
	}

	instance, err := eng.StartInstance(context.Background(), service.StartInstanceRequest{DefRef: def.Qualifier()})
	if err != nil {
		panic(err)
	}

	data, err := json.Marshal(instance)
	if err != nil {
		panic(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		panic(err)
	}

	_, embedded := doc["definition"]
	fmt.Println("definition embedded:", embedded)
	fmt.Printf("identity: %v v%v\n", doc["def_id"], doc["def_version"])
	fmt.Println("accessor still resolves:", instance.Definition() != nil)
	// Output:
	// definition embedded: false
	// identity: greeting v1
	// accessor still resolves: true
}

// ExampleProcessInstance_activeTasks shows how a consumer reads the open human
// tasks of a running instance.
func ExampleProcessInstance_activeTasks() {
	st := engine.InstanceState{
		InstanceID: "inst-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskID: "t-1", NodeID: "manager-approval", State: humantask.Claimed, Claim: &humantask.Claim{Actor: authz.Actor{ID: "u-jane"}}},
			{TaskID: "t-0", NodeID: "validate", State: humantask.Completed},
		},
	}
	inst := service.NewProcessInstance(nil, st)

	task, ok := inst.ActiveTask("manager-approval")
	fmt.Println(ok, task.Claim.Actor.ID)
	fmt.Println(len(inst.ActiveTasks()))
	// Output:
	// true u-jane
	// 1
}
