package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
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

// eventSubProcessDef builds:
//
// outer:  outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-user (KindUserTask) → inner-end
//	[KindEventSubProcess "evtsub"] triggered by signal "cancel"
//	  evtsub-inner:  evtsub-start(signal "cancel") → evtsub-svc(ServiceTask "cancel-action") → evtsub-end
//
// If interrupting==true the event sub-process is interrupting (NonInterrupting=false).
// If interrupting==false the event sub-process is NON-interrupting (NonInterrupting=true).
func eventSubProcessDef(nonInterrupting bool) *model.ProcessDefinition {
	evtsubInner := &model.ProcessDefinition{
		ID: "evtsub-inner", Version: 1,
		Nodes: []model.Node{
			{ID: "evtsub-start", Kind: model.KindStartEvent, SignalName: "cancel"},
			{ID: "evtsub-svc", Kind: model.KindServiceTask, Action: "cancel-action"},
			{ID: "evtsub-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "ef1", Source: "evtsub-start", Target: "evtsub-svc"},
			{ID: "ef2", Source: "evtsub-svc", Target: "evtsub-end"},
		},
	}

	inner := &model.ProcessDefinition{
		ID: "inner-evtsub", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "inner-user", Kind: model.KindUserTask},
			{ID: "inner-end", Kind: model.KindEndEvent},
			{ID: "evtsub", Kind: model.KindEventSubProcess, NonInterrupting: nonInterrupting, Subprocess: evtsubInner},
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-user"},
			{ID: "if2", Source: "inner-user", Target: "inner-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "outer-evtsub", Version: 1,
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

// TestInterruptingEventSubprocessCancelsParentScope verifies the interrupting event
// sub-process scenario:
//
//  1. Start → enters sub → inner scope opens, user-task "inner-user" parks.
//  2. Deliver SignalReceived{"cancel"} → the interrupting event sub-process fires:
//     - The user-task token in the inner scope is cancelled.
//     - A new scope opens for the event sub-process; evtsub-svc fires (InvokeAction "cancel-action").
//  3. Complete "cancel-action" → evtsub scope drains → since interrupting, this
//     completes the enclosing sub-process scope → outer-end → CompleteInstance.
//  4. A late HumanCompleted for the cancelled task is a clean no-op.
func TestInterruptingEventSubprocessCancelsParentScope(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := eventSubProcessDef(false) // interrupting

	// ---- Step 1: StartInstance — outer-start → sub → inner-start → inner-user (parks) ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-evtsub"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// inner-user should be parked (AwaitHuman command).
	require.Len(t, r1.State.Tokens, 1, "expected one parked token at inner-user")
	assert.Equal(t, "inner-user", r1.State.Tokens[0].NodeID)
	taskToken := r1.State.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken, "user-task token must have AwaitCommand set")

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "expected one scope open for sub")
	innerScopeID := r1.State.Scopes[0].ID

	// ---- Step 2: SignalReceived{"cancel"} — interrupting event sub-process fires ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// The user-task token in the inner scope must be cancelled (gone).
	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "inner-user", tok.NodeID,
			"inner-user token must be cancelled by interrupting event sub-process")
	}

	// A new (child) scope must be open for the event sub-process.
	// The original inner scope may be closed (interrupting) OR still listed.
	// The evtsub scope parent is the inner scope.
	var evtsubScope *engine.Scope
	for i := range r2.State.Scopes {
		sc := &r2.State.Scopes[i]
		if sc.ParentID == innerScopeID {
			evtsubScope = sc
			break
		}
	}
	require.NotNil(t, evtsubScope, "expected a child scope for the event sub-process")

	// InvokeAction for "cancel-action" must have been emitted.
	var cancelCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-action" {
			cancelCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cancelCmdID, "expected InvokeAction for cancel-action")

	// ---- Step 3: ActionCompleted for cancel-action — evtsub scope drains → outer completes ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cancelCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Empty(t, r3.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r3.State.Scopes, "all scopes must be closed on completion")
	require.NotNil(t, r3.State.EndedAt)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CompleteInstance command after event sub-process completion")

	// ---- Step 4: Late HumanCompleted is a clean no-op ----
	r4, err := engine.Step(def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Second), taskToken, nil, authz.Actor{ID: "alice"}), engine.StepOptions{})
	// Should error with ErrTokenNotFound (task token no longer exists) OR be a no-op.
	// Either is acceptable; we just verify it doesn't panic and the state is unchanged.
	if err == nil {
		assert.Equal(t, engine.StatusCompleted, r4.State.Status,
			"state should remain completed on late HumanCompleted")
	}
}

// TestNonInterruptingEventSubprocessRunsAlongside verifies the non-interrupting
// event sub-process scenario:
//
//  1. Start → sub enters → inner-user parks.
//  2. SignalReceived{"cancel"} → non-interrupting event sub-process spawns ALONGSIDE:
//     - inner-user is NOT cancelled (still parked).
//     - evtsub-svc fires (InvokeAction "cancel-action").
//  3. Complete "cancel-action" → evtsub scope drains → but inner scope still has
//     inner-user; instance still running.
//  4. Complete inner-user → inner scope drains → outer-end → CompleteInstance.
func TestNonInterruptingEventSubprocessRunsAlongside(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := eventSubProcessDef(true) // non-interrupting

	// ---- Step 1: StartInstance ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-nonintr"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "inner-user", r1.State.Tokens[0].NodeID)
	taskToken := r1.State.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken)

	// ---- Step 2: SignalReceived{"cancel"} — non-interrupting: spawn alongside ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// inner-user token must STILL be present (non-interrupting: host not cancelled).
	userTaskPresent := false
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "inner-user" {
			userTaskPresent = true
		}
	}
	assert.True(t, userTaskPresent, "inner-user must still be parked (non-interrupting)")

	// InvokeAction for "cancel-action" must have been emitted.
	var cancelCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-action" {
			cancelCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cancelCmdID, "expected InvokeAction for cancel-action")

	// ---- Step 3: Complete cancel-action — evtsub scope drains, instance still running ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cancelCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status, "instance still running: inner-user still pending")

	// inner-user token must STILL be present.
	userTaskStillPresent := false
	for _, tok := range r3.State.Tokens {
		if tok.NodeID == "inner-user" {
			userTaskStillPresent = true
		}
	}
	assert.True(t, userTaskStillPresent, "inner-user must still be parked after evtsub completes")

	// ---- Step 4: Complete inner-user → inner scope drains → outer completes ----
	task := r3.State.Tasks[0]
	r4, err := engine.Step(def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Second), task.TaskToken, nil, authz.Actor{ID: "alice"}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status)
	assert.Empty(t, r4.State.Tokens, "all tokens consumed on completion")
	assert.Empty(t, r4.State.Scopes, "all scopes closed on completion")

	found := false
	for _, cmd := range r4.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
		}
	}
	assert.True(t, found, "expected CompleteInstance")
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

// timerEventSubProcessDef builds:
//
// outer:  outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-svc (ServiceTask "inner-action") → inner-end
//	[KindEventSubProcess "evtsub"] triggered by timer "1h" (interrupting)
//	  evtsub-inner:  evtsub-start(timer "1h") → evtsub-svc(ServiceTask "timeout-action") → evtsub-end
func timerEventSubProcessDef() *model.ProcessDefinition {
	evtsubInner := &model.ProcessDefinition{
		ID: "evtsub-timer-inner", Version: 1,
		Nodes: []model.Node{
			{ID: "evtsub-start", Kind: model.KindStartEvent, TimerDuration: `"1h"`},
			{ID: "evtsub-svc", Kind: model.KindServiceTask, Action: "timeout-action"},
			{ID: "evtsub-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "ef1", Source: "evtsub-start", Target: "evtsub-svc"},
			{ID: "ef2", Source: "evtsub-svc", Target: "evtsub-end"},
		},
	}
	inner := &model.ProcessDefinition{
		ID: "inner-timer-evtsub", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "inner-svc", Kind: model.KindServiceTask, Action: "inner-action"},
			{ID: "inner-end", Kind: model.KindEndEvent},
			{ID: "evtsub", Kind: model.KindEventSubProcess, NonInterrupting: false, Subprocess: evtsubInner},
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-timer-evtsub", Version: 1,
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

// TestTimerEventSubprocessArmsOnScopeOpen verifies that a timer-triggered event
// sub-process arms correctly (emits ScheduleTimer) when the sub-process scope opens.
// When the timer fires, the interrupting event sub-process fires, cancelling the
// normal inner-svc path and running the timeout path instead.
func TestTimerEventSubprocessArmsOnScopeOpen(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := timerEventSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → inner-svc fires InvokeAction + ScheduleTimer (evtsub arm) ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-timer-esp"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Should have: InvokeAction for inner-action + ScheduleTimer for the timer arm.
	var schedTimer engine.ScheduleTimer
	var innerCmdID string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.InvokeAction:
			innerCmdID = c.CommandID
		case engine.ScheduleTimer:
			schedTimer = c
		}
	}
	require.NotEmpty(t, innerCmdID, "expected InvokeAction for inner-action")
	require.NotEmpty(t, schedTimer.TimerID, "expected ScheduleTimer for event sub-process timer arm")

	// The event sub-process arm must be recorded in state (1 arm for "evtsub").
	assert.Len(t, r1.State.EventSubprocesses, 1,
		"expected one event sub-process arm recorded")

	// ---- Step 2: Timer fires (interrupting) → inner-svc token cancelled, evtsub-svc fires ----
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(at.Add(time.Hour), schedTimer.TimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// inner-svc token must be gone (interrupting).
	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "inner-svc", tok.NodeID, "inner-svc must be cancelled")
	}
	// InvokeAction for timeout-action.
	var timeoutCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "timeout-action" {
			timeoutCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, timeoutCmdID, "expected InvokeAction for timeout-action")

	// ---- Step 3: Complete timeout-action → evtsub scope drains → outer completes ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Hour), timeoutCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Empty(t, r3.State.Tokens)
	assert.Empty(t, r3.State.Scopes)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
		}
	}
	assert.True(t, found, "expected CompleteInstance")

	// The evtsub arm was cancelled (CancelTimer) as part of arming cleanup?
	// Actually: timer arm fires = removeEventSubprocessArmsForScope (all arms removed on fire),
	// so no CancelTimer for the winner's timer. No other timer arms to cancel.
	// Normal inner-action's ScheduleTimer was the evtsub arm timer — it was the fired timer.
	// A CancelTimer for the inner-action's InvokeAction command is NOT emitted (no way to cancel
	// in-flight service invocations in this plan). That's expected.
}
