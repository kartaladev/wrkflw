package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// subProcessDef builds an outer definition:
//
//	outer-start → sub (KindSubProcess, Subprocess = inner) → outer-end
//
// inner definition:
//
//	inner-start → inner-svc (ServiceTask "inner-action") → inner-end
func subProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "inner-svc", Kind: model.KindServiceTask, Action: "inner-action"},
			{ID: "inner-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer", Version: 1,
		Nodes: []model.Node{
			{ID: "outer-start", Kind: model.KindStartEvent},
			{ID: "sub", Kind: model.KindSubProcess, Subprocess: inner},
			{ID: "outer-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestEmbeddedSubProcessRunsAndContinues is the primary scenario test:
//
//  1. StartInstance drives: outer-start → sub (entry: opens scope, inner-start →
//     inner-svc fires InvokeAction for "inner-action").
//  2. ActionCompleted for inner-svc drives: inner-svc → inner-end (inner scope
//     drains, scope closed) → outer flow resumes: outer-end → CompleteInstance.
//
// Asserts:
//   - After StartInstance: exactly one InvokeAction for "inner-action".
//   - A scope was opened (len(Scopes)==1 after entry, ==0 after exit).
//   - The inner token carries the scope ID.
//   - After ActionCompleted: instance StatusCompleted, exactly one CompleteInstance.
func TestEmbeddedSubProcessRunsAndContinues(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := subProcessDef()

	// ---- Step 1: StartInstance ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Exactly one InvokeAction for the inner service task.
	require.Len(t, r1.Commands, 1, "expected exactly one command after start")
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction, got %T", r1.Commands[0])
	assert.Equal(t, "inner-action", ia.Name)

	// One token: the inner-svc token, parked, in the sub-process scope.
	require.Len(t, r1.State.Tokens, 1)
	innerTok := r1.State.Tokens[0]
	assert.Equal(t, "inner-svc", innerTok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, innerTok.State)
	assert.NotEmpty(t, innerTok.ScopeID, "inner token must carry a scope ID")

	// Exactly one scope is open.
	require.Len(t, r1.State.Scopes, 1, "expected one open scope after sub-process entry")
	scope := r1.State.Scopes[0]
	assert.Equal(t, "sub", scope.NodeID, "scope.NodeID must be the sub-process activity node")
	assert.Equal(t, "", scope.ParentID, "scope.ParentID must be empty (root parent)")
	assert.Equal(t, innerTok.ScopeID, scope.ID)

	// ---- Step 2: ActionCompleted for inner-svc ----
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), ia.CommandID, map[string]any{"result": "done"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// Instance must be completed.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r2.State.Scopes, "scope must be closed after sub-process exits")
	require.NotNil(t, r2.State.EndedAt)

	// Exactly one CompleteInstance command.
	require.Len(t, r2.Commands, 1, "expected exactly one command on completion")
	_, ok = r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok, "expected CompleteInstance, got %T", r2.Commands[0])
}

// TestEmbeddedSubProcessTokenTagging verifies that:
//   - Outer (root-scope) tokens carry an empty ScopeID.
//   - Inner tokens carry the sub-process scope ID.
//
// This is folded into the main test above but verified explicitly here as a
// focused assertion for clarity in the audit trail.
func TestEmbeddedSubProcessTokenTagging(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := subProcessDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i2"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]

	require.Len(t, r1.State.Scopes, 1)
	scopeID := r1.State.Scopes[0].ID

	assert.NotEmpty(t, tok.ScopeID, "inner token ScopeID must not be empty")
	assert.Equal(t, scopeID, tok.ScopeID, "inner token ScopeID must match the open scope")
}

// TestEmbeddedSubProcessScopeIDFormat verifies the scope ID follows the
// deterministic "<instanceID>-s<N>" format established by openScope.
func TestEmbeddedSubProcessScopeIDFormat(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := subProcessDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "proc-42"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r1.State.Scopes, 1)
	assert.Equal(t, "proc-42-s1", r1.State.Scopes[0].ID)
}

// parallelSubProcessDef builds an outer definition:
//
//	outer-start → sub (KindSubProcess, Subprocess = inner) → outer-end
//
// inner definition (parallel fork-join):
//
//	inner-start → pfork (parallel gateway, diverging) → inner-a, inner-b (ServiceTasks)
//	inner-a → pjoin (parallel gateway, converging)
//	inner-b → pjoin
//	pjoin → inner-end
func parallelSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-parallel", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "pfork", Kind: model.KindParallelGateway},
			{ID: "inner-a", Kind: model.KindServiceTask, Action: "action-a"},
			{ID: "inner-b", Kind: model.KindServiceTask, Action: "action-b"},
			{ID: "pjoin", Kind: model.KindParallelGateway},
			{ID: "inner-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "pfork"},
			{ID: "if2", Source: "pfork", Target: "inner-a"},
			{ID: "if3", Source: "pfork", Target: "inner-b"},
			{ID: "if4", Source: "inner-a", Target: "pjoin"},
			{ID: "if5", Source: "inner-b", Target: "pjoin"},
			{ID: "if6", Source: "pjoin", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-parallel", Version: 1,
		Nodes: []model.Node{
			{ID: "outer-start", Kind: model.KindStartEvent},
			{ID: "sub", Kind: model.KindSubProcess, Subprocess: inner},
			{ID: "outer-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestParallelGatewayInsideSubProcess verifies that a parallel fork-join nested
// inside a sub-process keeps all forked tokens in the sub-process scope.
//
// Topology: outer-start → sub [inner-start → pfork → (inner-a ∥ inner-b) → pjoin → inner-end] → outer-end
//
// Expected RED (before fix): forked tokens have ScopeID="" → resolve against top def
// → wrong routing / premature scope-drain / error.
// Expected GREEN (after fix): forked tokens tagged with sub-process ScopeID; both
// service tasks invoke within scope; join fires within scope; scope drains; outer completes.
func TestParallelGatewayInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := parallelSubProcessDef()

	// ---- Step 1: StartInstance — drives outer-start → sub → inner-start → pfork → forks to (inner-a, inner-b) ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "pi1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Exactly one scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID
	assert.Equal(t, "sub", r1.State.Scopes[0].NodeID)

	// Exactly two tokens: one parked at inner-a, one parked at inner-b.
	require.Len(t, r1.State.Tokens, 2, "parallel fork must produce two tokens")

	nodeIDs := []string{r1.State.Tokens[0].NodeID, r1.State.Tokens[1].NodeID}
	assert.ElementsMatch(t, []string{"inner-a", "inner-b"}, nodeIDs, "forked tokens must land on inner-a and inner-b")

	// CRITICAL: both forked tokens must carry the sub-process ScopeID.
	for _, tok := range r1.State.Tokens {
		assert.Equal(t, scopeID, tok.ScopeID,
			"forked token at %q must carry sub-process ScopeID %q, got %q", tok.NodeID, scopeID, tok.ScopeID)
		assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	}

	// Exactly two InvokeAction commands: one for action-a, one for action-b.
	require.Len(t, r1.Commands, 2, "expected two InvokeAction commands after parallel fork")
	cmdsByName := make(map[string]string) // action name → commandID
	for _, cmd := range r1.Commands {
		ia, ok := cmd.(engine.InvokeAction)
		require.True(t, ok, "expected InvokeAction, got %T", cmd)
		cmdsByName[ia.Name] = ia.CommandID
	}
	assert.Contains(t, cmdsByName, "action-a")
	assert.Contains(t, cmdsByName, "action-b")

	// ---- Step 2: Complete action-a ----
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdsByName["action-a"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status, "instance still running after first branch completes")
	// scope still open; inner-b still parked.
	require.Len(t, r2.State.Scopes, 1, "scope must still be open after first branch completes")
	assert.Empty(t, r2.Commands, "no commands expected while waiting for inner-b")

	// ---- Step 3: Complete action-b — join fires, scope drains, outer resumes, instance completes ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdsByName["action-b"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status, "instance must complete after join and scope drain")
	assert.Empty(t, r3.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r3.State.Scopes, "scope must be closed after sub-process exits")
	require.NotNil(t, r3.State.EndedAt)

	require.Len(t, r3.Commands, 1, "expected exactly one CompleteInstance command")
	_, ok := r3.Commands[0].(engine.CompleteInstance)
	require.True(t, ok, "expected CompleteInstance, got %T", r3.Commands[0])
}
