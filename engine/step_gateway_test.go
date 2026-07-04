package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
)

// exclusiveDef: start -> xor -{amount > 100}-> big ; -default-> small ; both -> end
func exclusiveDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "xor", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewExclusiveGateway("xor"),
			definition.NewServiceTask("big", definition.WithActionName("big")),
			definition.NewServiceTask("small", definition.WithActionName("small")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "amount > 100"},
			{ID: "f3", Source: "xor", Target: "small", IsDefault: true},
			{ID: "f4", Source: "big", Target: "end"},
			{ID: "f5", Source: "small", Target: "end"},
		},
	}
}

func TestExclusiveGatewayTakesConditionalBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 150}), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 1)
	ia := res.Commands[0].(engine.InvokeAction)
	assert.Equal(t, "big", ia.Name)
	require.Len(t, res.State.Tokens, 1)
	assert.Equal(t, "big", res.State.Tokens[0].NodeID)
}

func TestExclusiveGatewayTakesDefaultBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 1)
	assert.Equal(t, "small", res.Commands[0].(engine.InvokeAction).Name)
	require.Len(t, res.State.Tokens, 1)
	assert.Equal(t, "small", res.State.Tokens[0].NodeID)
}

// parallelForkDef: start -> fork => a, b (service tasks) -> end (each)
func parallelForkDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "par", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewParallelGateway("fork"),
			definition.NewServiceTask("a", definition.WithActionName("a")),
			definition.NewServiceTask("b", definition.WithActionName("b")),
			definition.NewEndEvent("enda"),
			definition.NewEndEvent("endb"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "enda"},
			{ID: "f5", Source: "b", Target: "endb"},
		},
	}
}

func TestParallelGatewayForksAllBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(parallelForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Both branches fire their service action in one macro step.
	require.Len(t, res.Commands, 2)
	names := []string{
		res.Commands[0].(engine.InvokeAction).Name,
		res.Commands[1].(engine.InvokeAction).Name,
	}
	assert.ElementsMatch(t, []string{"a", "b"}, names)

	// Two tokens, one parked on each service task; the fork token is gone.
	require.Len(t, res.State.Tokens, 2)
	nodes := []string{res.State.Tokens[0].NodeID, res.State.Tokens[1].NodeID}
	assert.ElementsMatch(t, []string{"a", "b"}, nodes)
	for _, tk := range res.State.Tokens {
		assert.Equal(t, engine.TokenWaitingCommand, tk.State)
	}
}

// diamondDef: start -> fork => a,b -> join -> end. Join waits for both a and b.
func diamondDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "diamond", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewParallelGateway("fork"),
			definition.NewServiceTask("a", definition.WithActionName("a")),
			definition.NewServiceTask("b", definition.WithActionName("b")),
			definition.NewParallelGateway("join"),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "join"},
			{ID: "f5", Source: "b", Target: "join"},
			{ID: "f6", Source: "join", Target: "end"},
		},
	}
}

func TestParallelJoinWaitsForAllBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := diamondDef()

	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r0.Commands, 2) // a and b invoked
	cmdA := r0.Commands[0].(engine.InvokeAction)
	cmdB := r0.Commands[1].(engine.InvokeAction)

	// Complete the first branch: token parks at the join, instance not done.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdA.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r1.Commands)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)
	require.Len(t, r1.State.Tokens, 2)

	// Complete the second branch: join fires, reaches end, instance completes.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdB.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	_, ok := r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
}

// dualSubProcessParallelDef builds a definition to test scope-local join behaviour.
//
// Outer: outer-start → pfork (parallel) → [subA, subB] → pouter-join (parallel) → outer-end
//
// Both subA and subB use THE SAME inner *ProcessDefinition:
//
//	inner: inner-start → ifork (parallel) → [inner-a, inner-b] (ServiceTasks) → ijoin (parallel) → inner-end
//
// Since both subprocesses share the same inner definition, the inner join node ID
// "ijoin" is identical across both scopes. The scope-local join test verifies that
// tokens from different scopes are NOT counted together.
func dualSubProcessParallelDef() *definition.ProcessDefinition {
	inner := &definition.ProcessDefinition{
		ID: "dual-inner", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("inner-start"),
			definition.NewParallelGateway("ifork"),
			definition.NewServiceTask("inner-a", definition.WithActionName("action-a")),
			definition.NewServiceTask("inner-b", definition.WithActionName("action-b")),
			definition.NewParallelGateway("ijoin"),
			definition.NewEndEvent("inner-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "di1", Source: "inner-start", Target: "ifork"},
			{ID: "di2", Source: "ifork", Target: "inner-a"},
			{ID: "di3", Source: "ifork", Target: "inner-b"},
			{ID: "di4", Source: "inner-a", Target: "ijoin"},
			{ID: "di5", Source: "inner-b", Target: "ijoin"},
			{ID: "di6", Source: "ijoin", Target: "inner-end"},
		},
	}

	return &definition.ProcessDefinition{
		ID: "dual-sub-par", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("outer-start"),
			definition.NewParallelGateway("pfork"),
			definition.NewSubProcess("subA", inner),
			definition.NewSubProcess("subB", inner),
			definition.NewParallelGateway("pouter-join"),
			definition.NewEndEvent("outer-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "pfork"},
			{ID: "of2", Source: "pfork", Target: "subA"},
			{ID: "of3", Source: "pfork", Target: "subB"},
			{ID: "of4", Source: "subA", Target: "pouter-join"},
			{ID: "of5", Source: "subB", Target: "pouter-join"},
			{ID: "of6", Source: "pouter-join", Target: "outer-end"},
		},
	}
}

// TestParallelJoinIsScopeLocal verifies that a converging parallel gateway inside a
// sub-process only counts tokens from its OWN scope, not tokens from sibling scopes
// that happen to have the same inner node IDs.
//
// Two concurrent sub-processes (subA, subB) each contain a parallel diamond.
// Both use the same inner *ProcessDefinition so their inner node IDs (including "ijoin")
// are identical. The join must only fire when all incoming branches within the SAME scope
// have arrived — cross-scope token counting must NOT trigger a premature join.
func TestParallelJoinIsScopeLocal(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := dualSubProcessParallelDef()

	// ---- Step 1: StartInstance — outer-start → pfork → subA + subB enter scopes ----
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "scope-local-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r0.State.Status)

	// Two scopes must be open (one per sub-process).
	require.Len(t, r0.State.Scopes, 2, "expected two open scopes (one per sub-process)")
	scopeAID := r0.State.Scopes[0].ID
	scopeBID := r0.State.Scopes[1].ID

	// Four tokens: inner-a and inner-b in each scope.
	require.Len(t, r0.State.Tokens, 4, "expected 4 tokens: inner-a and inner-b in each scope")
	// Exactly two InvokeAction commands for each scope's inner-a and inner-b.
	require.Len(t, r0.Commands, 4, "expected 4 InvokeAction commands")

	// Map tokens by scope and node: find command IDs for inner-a and inner-b per scope.
	type tokenKey struct{ scope, node string }
	cmdByToken := make(map[tokenKey]string) // scopeID+nodeID → commandID
	for _, tok := range r0.State.Tokens {
		cmdByToken[tokenKey{tok.ScopeID, tok.NodeID}] = tok.AwaitCommand
	}
	cmdA_scopeA := cmdByToken[tokenKey{scopeAID, "inner-a"}]
	cmdB_scopeA := cmdByToken[tokenKey{scopeAID, "inner-b"}]
	cmdA_scopeB := cmdByToken[tokenKey{scopeBID, "inner-a"}]
	cmdB_scopeB := cmdByToken[tokenKey{scopeBID, "inner-b"}]
	require.NotEmpty(t, cmdA_scopeA, "expected command for scopeA/inner-a")
	require.NotEmpty(t, cmdB_scopeA, "expected command for scopeA/inner-b")
	require.NotEmpty(t, cmdA_scopeB, "expected command for scopeB/inner-a")
	require.NotEmpty(t, cmdB_scopeB, "expected command for scopeB/inner-b")

	// ---- Step 2: Complete inner-a for scopeA only ----
	// With the cross-scope bug: scopeA's inner-a completes → 1 token at "ijoin" (scopeA).
	// But scopeB also has tokens at "inner-a" and "inner-b", so 3 tokens total could be
	// at "ijoin" if the fix is wrong (or the count logic is incorrect).
	// Actually the bug: after this step, "ijoin" in scopeA has 1 token. Current buggy code
	// counts ALL tokens at node "ijoin" regardless of scope. If scopeB's tokens land on
	// "ijoin" first or after, they'd be cross-counted. But at this step, only scopeA's
	// inner-a arrives at ijoin. The count is 1, incoming is 2, so it should still wait.
	// The premature fire happens when BOTH scopes each have 1 token at ijoin → count=2 == incoming.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdA_scopeA, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// ScopeA: inner-a token moved to ijoin (TokenAtJoin), inner-b still at inner-b.
	// ScopeB: inner-a and inner-b still at their nodes.
	// Instance must still be running, 2 scopes still open.
	require.Len(t, r1.State.Scopes, 2, "both scopes must still be open after completing scopeA/inner-a")

	// ---- Step 3: Complete inner-a for scopeB — CRITICAL cross-scope test ----
	// After this step, both scopes each have 1 token at "ijoin".
	// Buggy code: counts all "ijoin" tokens regardless of scope → arrived=2 == incoming(2) → fires BOTH joins prematurely.
	// Fixed code: each scope's join only counts its own "ijoin" tokens → arrived=1 < 2 → both joins wait.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdA_scopeB, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status,
		"instance must still be running after completing inner-a in both scopes (joins must not fire prematurely)")

	// CRITICAL: both scopes must still be open (joins did NOT fire).
	require.Len(t, r2.State.Scopes, 2, "SCOPE-LOCAL: both scopes must still be open after completing inner-a in each (joins must wait for their own inner-b)")

	// At least one "inner-b" token must remain in each scope.
	scopeAHasInnerB := false
	scopeBHasInnerB := false
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "inner-b" && tok.ScopeID == scopeAID {
			scopeAHasInnerB = true
		}
		if tok.NodeID == "inner-b" && tok.ScopeID == scopeBID {
			scopeBHasInnerB = true
		}
	}
	assert.True(t, scopeAHasInnerB, "scopeA must still have inner-b pending (join not fired prematurely)")
	assert.True(t, scopeBHasInnerB, "scopeB must still have inner-b pending (join not fired prematurely)")

	// ---- Step 4: Complete inner-b for scopeA — scopeA join fires, scope closes ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(3*time.Second), cmdB_scopeA, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status, "instance still running (scopeB not done)")

	// ScopeA must be closed (its join fired and the inner-end drained the scope).
	// ScopeB must still be open.
	scopeAOpen := false
	scopeBOpen := false
	for _, sc := range r3.State.Scopes {
		if sc.ID == scopeAID {
			scopeAOpen = true
		}
		if sc.ID == scopeBID {
			scopeBOpen = true
		}
	}
	assert.False(t, scopeAOpen, "scopeA must be closed after its join fires and inner-end drains")
	assert.True(t, scopeBOpen, "scopeB must still be open")

	// ---- Step 5: Complete inner-b for scopeB — scopeB join fires, outer join fires, instance completes ----
	r4, err := engine.Step(def, r3.State,
		engine.NewActionCompleted(at.Add(4*time.Second), cmdB_scopeB, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status, "instance must complete after both scopes drain")
	assert.Empty(t, r4.State.Tokens, "all tokens consumed on completion")
	assert.Empty(t, r4.State.Scopes, "all scopes closed on completion")
	require.NotNil(t, r4.State.EndedAt)

	require.Len(t, r4.Commands, 1, "expected exactly one CompleteInstance command")
	_, ok := r4.Commands[0].(engine.CompleteInstance)
	require.True(t, ok, "expected CompleteInstance, got %T", r4.Commands[0])
}

func TestExclusiveGatewayNoMatchNoDefaultErrors(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := &definition.ProcessDefinition{
		ID: "xor", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewExclusiveGateway("xor"),
			definition.NewServiceTask("big", definition.WithActionName("big")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "amount > 100"},
			{ID: "f3", Source: "big", Target: "end"},
		},
	}
	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrNoMatchingFlow)
}
