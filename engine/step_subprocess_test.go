package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
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
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
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
			event.NewStart("inner-start"),
			gateway.NewParallel("pfork"),
			activity.NewServiceTask("inner-a", activity.WithTaskAction("action-a")),
			activity.NewServiceTask("inner-b", activity.WithTaskAction("action-b")),
			gateway.NewParallel("pjoin"),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
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
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
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
			event.NewStart("evtsub-start", event.WithSignalName("cancel")),
			activity.NewServiceTask("evtsub-svc", activity.WithTaskAction("cancel-action")),
			event.NewEnd("evtsub-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "evtsub-start", Target: "evtsub-svc"},
			{ID: "ef2", Source: "evtsub-svc", Target: "evtsub-end"},
		},
	}

	evtsubNode := event.NewEventSubProcess("evtsub", evtsubInner)
	if nonInterrupting {
		evtsubNode = event.NewEventSubProcess("evtsub", evtsubInner, event.WithEventSubProcessNonInterrupting())
	}

	inner := &model.ProcessDefinition{
		ID: "inner-evtsub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("inner-user"),
			event.NewEnd("inner-end"),
			evtsubNode,
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-user"},
			{ID: "if2", Source: "inner-user", Target: "inner-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "outer-evtsub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestInterruptingEventSubprocessCancelsParentScope verifies the interrupting event
// sub-process scenario:
//
//  1. Start → enters sub → inner scope opens, user-task "inner-user" parks.
//  2. ApplyTrigger SignalReceived{"cancel"} → the interrupting event sub-process fires:
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

	// ---- Step 4: Late HumanCompleted must error with ErrTokenNotFound ----
	// A completed/cancelled task's token is gone; the engine must reject the trigger.
	_, err = engine.Step(def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Second), taskToken, nil, authz.Actor{ID: "alice"}), engine.StepOptions{})
	require.Error(t, err, "late HumanCompleted on a cancelled task token must return an error")
	require.ErrorIs(t, err, engine.ErrTokenNotFound,
		"late HumanCompleted must wrap ErrTokenNotFound")
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
			event.NewStart("evtsub-start", event.WithStartTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("evtsub-svc", activity.WithTaskAction("timeout-action")),
			event.NewEnd("evtsub-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "evtsub-start", Target: "evtsub-svc"},
			{ID: "ef2", Source: "evtsub-svc", Target: "evtsub-end"},
		},
	}
	inner := &model.ProcessDefinition{
		ID: "inner-timer-evtsub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewEnd("inner-end"),
			event.NewEventSubProcess("evtsub", evtsubInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-timer-evtsub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
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
	assert.Len(t, r1.State.EventTriggeredSubprocesses, 1,
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
	// Actually: timer arm fires = removeEventTriggeredSubprocessArmsForScope (all arms removed on fire),
	// so no CancelTimer for the winner's timer. No other timer arms to cancel.
	// Normal inner-action's ScheduleTimer was the evtsub arm timer — it was the fired timer.
	// A CancelTimer for the inner-action's InvokeAction command is NOT emitted (no way to cancel
	// in-flight service invocations in this plan). That's expected.
}

// espWithEventGatewayDef builds a definition for Fix 1 testing:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → evtgw (KindEventBasedGateway)
//	  → timer-catch (IntermediateCatchEvent timer "2h") → normal-end
//	  → signal-catch (IntermediateCatchEvent signal "done") → normal-end
//	[KindEventSubProcess "evtsub"] triggered by signal "cancel" (interrupting)
//	  evtsub-inner: evtsub-start(signal "cancel") → evtsub-svc("cancel-action") → evtsub-end
//
// The event-based gateway parks and arms a timer (2h) and a signal arm.
// An interrupting ESP is armed for "cancel" signal.
// When the ESP fires, it must cancel the gateway's timer arm and leave no stale ArmedEvents.
func espWithEventGatewayDef() *model.ProcessDefinition {
	evtsubInner := &model.ProcessDefinition{
		ID: "esp-gw-evtsub-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("evtsub-start", event.WithSignalName("cancel")),
			activity.NewServiceTask("evtsub-svc", activity.WithTaskAction("cancel-action")),
			event.NewEnd("evtsub-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "evtsub-start", Target: "evtsub-svc"},
			{ID: "ef2", Source: "evtsub-svc", Target: "evtsub-end"},
		},
	}

	inner := &model.ProcessDefinition{
		ID: "inner-esp-gw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("timer-catch", event.WithCatchTimer(schedule.AfterExpr(`"2h"`))),
			event.NewIntermediateCatch("signal-catch", event.WithSignalName("done")),
			event.NewEnd("normal-end"),
			event.NewEventSubProcess("evtsub", evtsubInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "evtgw"},
			{ID: "if2", Source: "evtgw", Target: "timer-catch"},
			{ID: "if3", Source: "evtgw", Target: "signal-catch"},
			{ID: "if4", Source: "timer-catch", Target: "normal-end"},
			{ID: "if5", Source: "signal-catch", Target: "normal-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "outer-esp-gw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestInterruptingEventSubprocessCancelsGatewayArms is the Fix 1 TDD test.
//
// Scenario: A sub-process contains an event-based gateway (parked, with a timer arm)
// AND an interrupting event sub-process. When the ESP fires ("cancel" signal), it
// must cancel the enclosing-scope tokens — but the event-gateway-parked token's
// ArmedEvents entries must ALSO be cancelled (the timer arm for timer-catch).
//
// Before fix: the gateway token is consumed but its ArmedEvents entries remain →
// a late TimerFired for the gateway timer fires against a missing token (no-op today,
// but the ArmedEvents entry is stale and could misroute in other scenarios).
//
// After fix: firing the ESP emits CancelTimer for the gateway's timer arm and
// removes all ArmedEvents for that gateway so a late TimerFired is a clean no-op
// with no ArmedEvents remaining.
func TestInterruptingEventSubprocessCancelsGatewayArms(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := espWithEventGatewayDef()

	// ---- Step 1: StartInstance → inner starts → event gateway parks with timer arm ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-esp-gw"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// The event gateway must be parked with ArmedEvents (timer arm + signal arm).
	require.NotEmpty(t, r1.State.ArmedEvents, "event gateway must have armed events")

	// Find the timer arm's TimerID from commands.
	var gwTimerID string
	for _, cmd := range r1.Commands {
		if st, ok := cmd.(engine.ScheduleTimer); ok {
			gwTimerID = st.TimerID
		}
	}
	require.NotEmpty(t, gwTimerID, "expected ScheduleTimer for event gateway timer arm")

	// Gateway token must be parked with "evtgw:" prefix.
	var gwTokID string
	for _, tok := range r1.State.Tokens {
		if len(tok.AwaitCommand) > 6 && tok.AwaitCommand[:6] == "evtgw:" {
			gwTokID = tok.ID
			break
		}
	}
	require.NotEmpty(t, gwTokID, "expected a gateway-parked token with evtgw: prefix")

	// ESP arm must be recorded (signal "cancel").
	require.NotEmpty(t, r1.State.EventTriggeredSubprocesses, "ESP arm must be recorded")

	// ---- Step 2: SignalReceived{"cancel"} — interrupting ESP fires ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// FIX 1 ASSERTION: ArmedEvents must be empty (gateway arms cleaned up).
	assert.Empty(t, r2.State.ArmedEvents,
		"ArmedEvents must be empty after interrupting ESP cancels the enclosing scope (gateway arms must be cleaned up)")

	// FIX 1 ASSERTION: A CancelTimer for the gateway's timer arm must have been emitted.
	cancelTimerFound := false
	for _, cmd := range r2.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == gwTimerID {
			cancelTimerFound = true
			break
		}
	}
	assert.True(t, cancelTimerFound,
		"expected CancelTimer for gateway timer arm %q in commands %v", gwTimerID, r2.Commands)

	// FIX 1 ASSERTION: A late TimerFired for the cancelled gateway timer must be a clean no-op
	// (no error, no state change, no commands).
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(at.Add(2*time.Hour), gwTimerID), engine.StepOptions{})
	require.NoError(t, err, "late TimerFired for cancelled gateway timer must not error")
	assert.Empty(t, r3.Commands, "late TimerFired for cancelled gateway timer must produce no commands")
	// Status must still be running (cancel-action InvokeAction was emitted but not yet completed).
	assert.Equal(t, engine.StatusRunning, r3.State.Status)
}

// rootLevelESPDef builds a definition for Fix 2 testing:
//
// Root-level (no outer KindSubProcess wrapper):
//
//	root-start → root-svc (ServiceTask "normal-action") → root-end
//	[KindEventSubProcess "root-esp"] triggered by signal "cancel" (interrupting)
//	  root-esp-inner: esp-start(signal "cancel") → esp-svc("esp-action") → esp-end
//
// When the ESP fires, it cancels root-svc and runs esp-svc.
// On esp-svc completion, the instance should complete (no outer sub-process to resume).
func rootLevelESPDef() *model.ProcessDefinition {
	espInner := &model.ProcessDefinition{
		ID: "root-esp-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithSignalName("cancel")),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "re1", Source: "esp-start", Target: "esp-svc"},
			{ID: "re2", Source: "esp-svc", Target: "esp-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "root-esp-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("root-start"),
			activity.NewServiceTask("root-svc", activity.WithTaskAction("normal-action")),
			event.NewEnd("root-end"),
			event.NewEventSubProcess("root-esp", espInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "rf1", Source: "root-start", Target: "root-svc"},
			{ID: "rf2", Source: "root-svc", Target: "root-end"},
		},
	}
}

// TestRootLevelEventSubprocessCompletes is the Fix 2 TDD test.
//
// Scenario: A ROOT-LEVEL (no enclosing KindSubProcess) interrupting event sub-process
// fires. The root-svc token is cancelled; the ESP child scope opens and runs esp-svc.
// On esp-svc completion, the ESP child scope drains. Since the ESP is at the root level
// (parentScopeID == ""), the isEventSubProcess detection must still work (Fix 2),
// and the instance should complete cleanly.
func TestRootLevelEventSubprocessCompletes(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := rootLevelESPDef()

	// ---- Step 1: StartInstance → root-svc parks ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-root-esp"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// root-svc must be parked (InvokeAction emitted).
	var normalCmdID string
	for _, cmd := range r1.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "normal-action" {
			normalCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, normalCmdID, "expected InvokeAction for normal-action")

	// Root-level ESP arm must be recorded (EnclosingScopeID == "").
	require.NotEmpty(t, r1.State.EventTriggeredSubprocesses, "root-level ESP arm must be recorded")
	assert.Equal(t, "", r1.State.EventTriggeredSubprocesses[0].EnclosingScopeID,
		"root-level ESP arm must have empty EnclosingScopeID")

	// ---- Step 2: SignalReceived{"cancel"} → interrupting ESP fires at root level ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// root-svc token must be gone (cancelled by interrupting ESP).
	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "root-svc", tok.NodeID,
			"root-svc token must be cancelled by interrupting root-level ESP")
	}

	// A child scope must be open for the ESP (parented to "").
	// The ESP child scope's NodeID == "root-esp".
	var espScope *engine.Scope
	for i := range r2.State.Scopes {
		if r2.State.Scopes[i].NodeID == "root-esp" {
			espScope = &r2.State.Scopes[i]
			break
		}
	}
	require.NotNil(t, espScope, "expected a child scope for root-level ESP")
	assert.Equal(t, "", espScope.ParentID, "root-level ESP scope parent must be empty (root)")

	// InvokeAction for esp-action must have been emitted.
	var espCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "esp-action" {
			espCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, espCmdID, "expected InvokeAction for esp-action")

	// ---- Step 3: Complete esp-action → ESP child scope drains → instance completes ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), espCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"instance must complete when root-level ESP child scope drains")
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
	assert.True(t, found, "expected CompleteInstance after root-level ESP completes")
}

// ---- Call Activity tests ----

// callActivityDef builds a parent definition:
//
//	parent-start → call (KindCallActivity, DefRef:"child") → parent-end
//
// The child definition is referenced by DefRef only; the engine does not need
// the actual child definition (it just emits StartSubInstance with the DefRef).
func callActivityDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "parent", Version: 1,
		Nodes: []model.Node{
			event.NewStart("parent-start"),
			activity.NewCallActivity("call", model.Latest("child")),
			event.NewEnd("parent-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "pf1", Source: "parent-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "parent-end"},
		},
	}
}

// TestCallActivityEmitsStartSubInstanceAndParks verifies that the engine:
//  1. On StartInstance: drives to the call-activity node, emits a StartSubInstance
//     command, and parks the token (TokenWaitingCommand, AwaitCommand == CommandID).
//  2. On SubInstanceCompleted: merges Output into vars, resumes the token past the
//     call-activity node, drives to parent-end → CompleteInstance.
func TestCallActivityEmitsStartSubInstanceAndParks(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	// ---- Step 1: StartInstance ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "ca-i1"},
		engine.NewStartInstance(at, map[string]any{"x": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Exactly one StartSubInstance command must have been emitted.
	require.Len(t, r1.Commands, 1, "expected exactly one command after start (StartSubInstance)")
	ssi, ok := r1.Commands[0].(engine.StartSubInstance)
	require.True(t, ok, "expected StartSubInstance, got %T", r1.Commands[0])
	assert.Equal(t, model.Latest("child"), ssi.DefRef)
	assert.NotEmpty(t, ssi.CommandID, "StartSubInstance.CommandID must be non-empty")

	// Input must be a copy of the parent variables.
	assert.Equal(t, map[string]any{"x": 1}, ssi.Input)

	// Token must be parked at the call-activity node with AwaitCommand == CommandID.
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "call", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, ssi.CommandID, tok.AwaitCommand)

	// No scope opened (call-activity is a separate instance, not an embedded scope).
	assert.Empty(t, r1.State.Scopes, "call-activity must not open a scope")

	// ---- Step 2: SubInstanceCompleted ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSubInstanceCompleted(at.Add(time.Second), ssi.CommandID, map[string]any{"result": "done"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// Instance must be completed.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens, "all tokens must be consumed on completion")
	require.NotNil(t, r2.State.EndedAt)

	// Child output must be merged into parent variables.
	assert.Equal(t, "done", r2.State.Variables["result"])
	assert.Equal(t, 1, r2.State.Variables["x"], "original parent vars must be retained")

	// Exactly one CompleteInstance command.
	require.Len(t, r2.Commands, 1)
	_, ok = r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok, "expected CompleteInstance, got %T", r2.Commands[0])
}

// TestCallActivitySubInstanceFailedFailsParent verifies that SubInstanceFailed
// (with a matching CommandID) transitions the parent instance to StatusFailed and
// emits FailInstance + cancellation commands.
func TestCallActivitySubInstanceFailedFailsParent(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	// ---- Step 1: StartInstance → parks at call ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "ca-i2"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ssi, ok := r1.Commands[0].(engine.StartSubInstance)
	require.True(t, ok)

	// ---- Step 2: SubInstanceFailed ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSubInstanceFailed(at.Add(time.Second), ssi.CommandID, "child blew up"),
		engine.StepOptions{})
	require.NoError(t, err)

	// Parent must be failed.
	assert.Equal(t, engine.StatusFailed, r2.State.Status)
	require.NotNil(t, r2.State.EndedAt)

	// FailInstance must be the first command.
	require.NotEmpty(t, r2.Commands)
	fi, ok := r2.Commands[0].(engine.FailInstance)
	require.True(t, ok, "expected FailInstance, got %T", r2.Commands[0])
	assert.Contains(t, fi.Err, "child blew up")
}

// TestSubInstanceCompletedUnknownCommandID verifies that a SubInstanceCompleted
// with an unrecognised CommandID returns ErrTokenNotFound (mirrors ActionCompleted).
func TestSubInstanceCompletedUnknownCommandID(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "ca-i3"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	_, err = engine.Step(def, r1.State,
		engine.NewSubInstanceCompleted(at.Add(time.Second), "nonexistent-cmd", nil),
		engine.StepOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// callActivityWithParallelUserTaskDef builds a definition where a parallel gateway
// splits into two concurrent branches:
//
//	Branch A: user-task with deadline "1h" → merge-join (adds a timerRecord to Timers)
//	Branch B: call-activity (DefRef "child") → merge-join
//	merge-join (parallel, converging) → end
//
// The deadline timer on the user-task records a timerRecord in state.Timers.
// When SubInstanceFailed arrives for the call-activity, cancelAllTimers must
// emit CancelTimer for the deadline timer — proving the cleanup path is complete.
func callActivityWithParallelUserTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "ca-sla-parent", Version: 1,
		Nodes: []model.Node{
			event.NewStart("p-start"),
			gateway.NewParallel("p-fork"),
			activity.NewUserTask("p-user", activity.WithWaitDeadline(schedule.AfterExpr(`"1h"`), "")),
			activity.NewCallActivity("p-call", model.Latest("child")),
			gateway.NewParallel("p-join"),
			event.NewEnd("p-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "p-start", Target: "p-fork"},
			{ID: "f2", Source: "p-fork", Target: "p-user"},
			{ID: "f3", Source: "p-fork", Target: "p-call"},
			{ID: "f4", Source: "p-user", Target: "p-join"},
			{ID: "f5", Source: "p-call", Target: "p-join"},
			{ID: "f6", Source: "p-join", Target: "p-end"},
		},
	}
}

// TestCallActivitySubInstanceFailedCancelsOutstandingTimers (Fix 3 assertion):
//
// A parent definition has both a parallel user-task (with deadline timer) and a
// call-activity branch. When StartInstance drives, both branches start:
//   - A ScheduleTimer (Deadline) is emitted for the user-task → timerRecord in state.Timers.
//   - A StartSubInstance is emitted for the call-activity → token parks.
//
// When SubInstanceFailed arrives, the engine must:
//  1. Emit FailInstance (transition to StatusFailed).
//  2. Emit CancelTimer for the deadline timer, proving cancelAllTimers runs on the
//     SubInstanceFailed path and cleans up all outstanding timer records.
func TestCallActivitySubInstanceFailedCancelsOutstandingTimers(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityWithParallelUserTaskDef()

	// ---- Step 1: StartInstance → parallel fork → deadline timer + call-activity starts ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "ca-sla-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Find the ScheduleTimer (Deadline) and StartSubInstance commands.
	var deadlineTimerID string
	var ssiCmdID string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.ScheduleTimer:
			if c.Kind == engine.TimerDeadline {
				deadlineTimerID = c.TimerID
			}
		case engine.StartSubInstance:
			ssiCmdID = c.CommandID
		}
	}
	require.NotEmpty(t, deadlineTimerID, "expected ScheduleTimer (Deadline) for the user-task branch")
	require.NotEmpty(t, ssiCmdID, "expected StartSubInstance for the call-activity branch")

	// Deadline timer must be recorded in state.Timers.
	require.NotEmpty(t, r1.State.Timers, "deadline timerRecord must be recorded in Timers")

	// ---- Step 2: SubInstanceFailed → parent must fail AND cancel the deadline timer ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSubInstanceFailed(at.Add(time.Second), ssiCmdID, "child blew up"),
		engine.StepOptions{})
	require.NoError(t, err)

	// Parent must be failed.
	assert.Equal(t, engine.StatusFailed, r2.State.Status, "parent must be StatusFailed")
	require.NotNil(t, r2.State.EndedAt)

	// FailInstance must be present.
	failInstFound := false
	cancelTimerFound := false
	for _, cmd := range r2.Commands {
		switch c := cmd.(type) {
		case engine.FailInstance:
			failInstFound = true
			assert.Contains(t, c.Err, "child blew up")
		case engine.CancelTimer:
			if c.TimerID == deadlineTimerID {
				cancelTimerFound = true
			}
		}
	}
	assert.True(t, failInstFound, "FailInstance must be emitted on SubInstanceFailed")
	assert.True(t, cancelTimerFound,
		"CancelTimer for deadline timer %q must be emitted on SubInstanceFailed (cancelAllTimers path)", deadlineTimerID)

	// Timer must be gone from state after cleanup.
	assert.Empty(t, r2.State.Timers, "Timers must be empty after cancelAllTimers on failure path")
}

// TestSubInstanceFailedUnknownCommandID verifies that a SubInstanceFailed with
// an unrecognised CommandID returns ErrTokenNotFound.
func TestSubInstanceFailedUnknownCommandID(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "ca-i4"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	_, err = engine.Step(def, r1.State,
		engine.NewSubInstanceFailed(at.Add(time.Second), "nonexistent-cmd", "err"),
		engine.StepOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// TestEventSubprocessArmCancelledOnNormalScopeClose is Fix 3 / M2.
//
// Scenario: A sub-process contains a TIMER event-subprocess arm that does NOT fire.
// The inner activity completes normally → scope drains → the ESP timer arm must be
// cancelled (CancelTimer emitted) and s.EventTriggeredSubprocesses must be empty.
func TestEventSubprocessArmCancelledOnNormalScopeClose(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := timerEventSubProcessDef()

	// ---- Step 1: StartInstance → inner-svc parks + ESP timer arm scheduled ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-esp-cancel-scope"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	var innerCmdID string
	var espTimerID string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.InvokeAction:
			if c.Name == "inner-action" {
				innerCmdID = c.CommandID
			}
		case engine.ScheduleTimer:
			espTimerID = c.TimerID
		}
	}
	require.NotEmpty(t, innerCmdID, "expected InvokeAction for inner-action")
	require.NotEmpty(t, espTimerID, "expected ScheduleTimer for ESP timer arm")

	// One ESP arm must be recorded.
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1, "one ESP arm must be recorded")

	// ---- Step 2: Complete inner-svc normally (ESP timer never fires) → scope drains ----
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Minute), innerCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status, "instance must complete normally")

	// M2 ASSERTION: CancelTimer for the ESP timer arm must have been emitted.
	cancelFound := false
	for _, cmd := range r2.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == espTimerID {
			cancelFound = true
			break
		}
	}
	assert.True(t, cancelFound,
		"CancelTimer for ESP timer arm %q must be emitted on normal scope close", espTimerID)

	// M2 ASSERTION: EventTriggeredSubprocesses must be empty after scope closes.
	assert.Empty(t, r2.State.EventTriggeredSubprocesses,
		"EventTriggeredSubprocesses must be empty after scope closes normally")
}

// ---- Inner-scope topology tests (Task 6) ----

// boundaryTimerInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-svc (ServiceTask "inner-action") → inner-end
//	[KindBoundaryEvent "bnd-timer"] attached to inner-svc, interrupting, timer "2h"
//	  bnd-timer → bnd-target (ServiceTask "escalate-action") → bnd-end
func boundaryTimerInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-bnd-timer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewBoundary("bnd-timer", "inner-svc", event.WithBoundaryTimer(schedule.AfterExpr(`"2h"`))),
			activity.NewServiceTask("bnd-target", activity.WithTaskAction("escalate-action")),
			event.NewEnd("inner-end"),
			event.NewEnd("bnd-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
			{ID: "if3", Source: "bnd-timer", Target: "bnd-target"},
			{ID: "if4", Source: "bnd-target", Target: "bnd-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-bnd-timer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestBoundaryTimerInsideSubProcess verifies that an interrupting boundary timer
// attached to an activity nested inside a sub-process:
//
//  1. Arms the boundary timer (ScheduleTimer) within the child scope on scope entry.
//  2. When the timer fires, the host token (inner-svc) is cancelled, a new token
//     lands on bnd-target (InvokeAction for "escalate-action") — all within the
//     child scope.
//  3. Completing the escalation action drains the inner scope → outer-end →
//     StatusCompleted (sub-process exits cleanly to the parent).
func TestBoundaryTimerInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := boundaryTimerInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → inner-svc parks + boundary timer armed ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "bnd-sub-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// InvokeAction for inner-svc + ScheduleTimer for boundary.
	var innerCmdID string
	var bndTimerID string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.InvokeAction:
			if c.Name == "inner-action" {
				innerCmdID = c.CommandID
			}
		case engine.ScheduleTimer:
			bndTimerID = c.TimerID
		}
	}
	require.NotEmpty(t, innerCmdID, "expected InvokeAction for inner-action")
	require.NotEmpty(t, bndTimerID, "expected ScheduleTimer for boundary timer")

	// One token parked at inner-svc, in the sub-process scope.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "inner-svc", r1.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r1.State.Tokens[0].ScopeID,
		"inner-svc token must carry the sub-process scope ID")

	// Boundary arm must be recorded.
	require.Len(t, r1.State.Boundaries, 1, "boundary arm must be recorded in state")

	// ---- Step 2: Boundary timer fires → host cancelled, escalation path runs ----
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(at.Add(2*time.Hour), bndTimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// inner-svc token must be gone (interrupting boundary cancelled it).
	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "inner-svc", tok.NodeID,
			"inner-svc token must be cancelled by interrupting boundary timer")
	}

	// InvokeAction for "escalate-action" must have been emitted.
	var escalateCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "escalate-action" {
			escalateCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, escalateCmdID, "expected InvokeAction for escalate-action")

	// bnd-target token must be within the same scope.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "bnd-target", r2.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r2.State.Tokens[0].ScopeID,
		"bnd-target token must remain in the sub-process scope")

	// Scope still open (boundary path not yet drained).
	require.Len(t, r2.State.Scopes, 1, "scope must still be open after boundary fires")

	// ---- Step 3: Complete escalation → inner scope drains → outer-end → StatusCompleted ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Hour+time.Second), escalateCmdID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"instance must complete after boundary path drains the sub-process scope")
	assert.Empty(t, r3.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r3.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r3.State.EndedAt)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CompleteInstance after sub-process exits via boundary path")
}

// eventBasedGatewayInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → evtgw (KindEventBasedGateway)
//	  → timer-catch (IntermediateCatchEvent timer "1h") → svc-timer → inner-end
//	  → signal-catch (IntermediateCatchEvent signal "approved") → svc-signal → inner-end2
func eventBasedGatewayInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-evtgw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("timer-catch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			event.NewIntermediateCatch("signal-catch", event.WithSignalName("approved")),
			activity.NewServiceTask("svc-timer", activity.WithTaskAction("timer-action")),
			activity.NewServiceTask("svc-signal", activity.WithTaskAction("signal-action")),
			event.NewEnd("inner-end"),
			event.NewEnd("inner-end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "evtgw"},
			{ID: "if2", Source: "evtgw", Target: "timer-catch"},
			{ID: "if3", Source: "evtgw", Target: "signal-catch"},
			{ID: "if4", Source: "timer-catch", Target: "svc-timer"},
			{ID: "if5", Source: "signal-catch", Target: "svc-signal"},
			{ID: "if6", Source: "svc-timer", Target: "inner-end"},
			{ID: "if7", Source: "svc-signal", Target: "inner-end2"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-evtgw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestEventBasedGatewayInsideSubProcess verifies that an event-based gateway
// nested inside a sub-process races its arms correctly within the child scope,
// and the sub-process exits cleanly when the winning branch completes.
//
// Scenario: signal wins over the timer.
//  1. Start → sub enters → event gateway arms (timer + signal) in child scope.
//  2. SignalReceived("approved") → first-event-wins: signal branch proceeds
//     (InvokeAction for signal-action); timer arm cancelled (CancelTimer emitted).
//  3. Complete signal-action → inner scope drains → outer-end → StatusCompleted.
func TestEventBasedGatewayInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := eventBasedGatewayInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → event gateway parks with arms ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "evtgw-sub-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// One token: the event gateway parked.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "evtgw", r1.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r1.State.Tokens[0].ScopeID,
		"gateway token must carry the sub-process scope ID")

	// Two armed events: timer arm + signal arm.
	assert.Len(t, r1.State.ArmedEvents, 2, "both gateway arms must be recorded in ArmedEvents")

	// ScheduleTimer for timer-catch arm.
	var timerID string
	for _, cmd := range r1.Commands {
		if st, ok := cmd.(engine.ScheduleTimer); ok {
			timerID = st.TimerID
		}
	}
	require.NotEmpty(t, timerID, "expected ScheduleTimer for timer-catch arm")

	// ---- Step 2: SignalReceived("approved") → signal wins; timer arm cancelled ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(30*time.Minute), "approved", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// InvokeAction for signal-action must be emitted.
	var signalCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "signal-action" {
			signalCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, signalCmdID, "expected InvokeAction for signal-action")

	// CancelTimer for the loser timer arm must be emitted.
	cancelFound := false
	for _, cmd := range r2.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == timerID {
			cancelFound = true
		}
	}
	assert.True(t, cancelFound, "expected CancelTimer for loser timer arm")

	// All armed events must be cleared (gateway resolved).
	assert.Empty(t, r2.State.ArmedEvents, "ArmedEvents must be empty after gateway resolves")

	// Token at svc-signal within the scope.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "svc-signal", r2.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r2.State.Tokens[0].ScopeID,
		"svc-signal token must remain in the sub-process scope")

	// ---- Step 3: Complete signal-action → inner scope drains → outer-end → StatusCompleted ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(time.Hour), signalCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"instance must complete after event-gateway signal branch drains sub-process scope")
	assert.Empty(t, r3.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r3.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r3.State.EndedAt)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CompleteInstance after event-gateway sub-process exits")
}

// inclusiveGatewayInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def (OR-fork + OR-join diamond):
//
//	inner-start → orsplit (KindInclusiveGateway) -{a>0}-> ta ; -{b>0}-> tb
//	ta, tb → orjoin (KindInclusiveGateway) → post (ServiceTask "post-action") → inner-end
func inclusiveGatewayInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-or", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewInclusive("orsplit"),
			activity.NewServiceTask("ta", activity.WithTaskAction("action-a")),
			activity.NewServiceTask("tb", activity.WithTaskAction("action-b")),
			gateway.NewInclusive("orjoin"),
			activity.NewServiceTask("post", activity.WithTaskAction("post-action")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "orsplit"},
			{ID: "if2", Source: "orsplit", Target: "ta", Condition: "a > 0"},
			{ID: "if3", Source: "orsplit", Target: "tb", Condition: "b > 0"},
			{ID: "if4", Source: "ta", Target: "orjoin"},
			{ID: "if5", Source: "tb", Target: "orjoin"},
			{ID: "if6", Source: "orjoin", Target: "post"},
			{ID: "if7", Source: "post", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-or", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestInclusiveGatewayInsideSubProcess verifies that an inclusive (OR) gateway
// fork+join nested inside a sub-process correctly activates multiple branches,
// joins them within the child scope, and the sub-process exits cleanly.
//
// Variables: {a:1, b:1} → both branches taken.
//  1. Start → sub enters → orsplit forks to ta AND tb (both conditions true).
//  2. Complete ta → OR-join waits (tb still reachable).
//  3. Complete tb → OR-join fires (both arrived) → post-action invoked.
//  4. Complete post-action → inner-end drains scope → outer-end → StatusCompleted.
func TestInclusiveGatewayInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := inclusiveGatewayInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → orsplit forks to ta AND tb ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "or-sub-i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// Two tokens: ta and tb, both in the sub-process scope.
	require.Len(t, r1.State.Tokens, 2, "OR-fork must produce two tokens for a>0 and b>0")
	nodeIDs := []string{r1.State.Tokens[0].NodeID, r1.State.Tokens[1].NodeID}
	assert.ElementsMatch(t, []string{"ta", "tb"}, nodeIDs, "forked tokens must land on ta and tb")
	for _, tok := range r1.State.Tokens {
		assert.Equal(t, scopeID, tok.ScopeID,
			"forked token at %q must carry sub-process scope ID", tok.NodeID)
	}

	// Two InvokeAction commands: action-a and action-b.
	require.Len(t, r1.Commands, 2, "expected two InvokeAction commands after OR-fork")
	cmdsByName := make(map[string]string)
	for _, cmd := range r1.Commands {
		ia, ok := cmd.(engine.InvokeAction)
		require.True(t, ok, "expected InvokeAction, got %T", cmd)
		cmdsByName[ia.Name] = ia.CommandID
	}
	assert.Contains(t, cmdsByName, "action-a")
	assert.Contains(t, cmdsByName, "action-b")

	// ---- Step 2: Complete action-a → OR-join must wait (tb still reachable) ----
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdsByName["action-a"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
	// OR-join must NOT fire yet (tb can still deliver).
	assert.Empty(t, r2.Commands, "OR-join must not fire while tb can still reach it")
	// Scope still open.
	require.Len(t, r2.State.Scopes, 1, "scope must still be open after first branch completes")

	// ---- Step 3: Complete action-b → OR-join fires → post-action invoked ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdsByName["action-b"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status, "instance still running (post-action pending)")

	// Exactly one InvokeAction for post-action (join fired once, not twice).
	require.Len(t, r3.Commands, 1, "OR-join must fire exactly once")
	postCmd, ok := r3.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for post-action")
	assert.Equal(t, "post-action", postCmd.Name)

	// ---- Step 4: Complete post-action → inner-end drains scope → outer-end → StatusCompleted ----
	r4, err := engine.Step(def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), postCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status,
		"instance must complete after inclusive gateway join drains sub-process scope")
	assert.Empty(t, r4.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r4.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r4.State.EndedAt)

	found := false
	for _, cmd := range r4.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CompleteInstance after inclusive-gateway sub-process exits")
}

// deadlineUserTaskInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-user (KindUserTask, DeadlineDuration "30m", DeadlineFlow "inner-escalate",
//	              DeadlineAction "notify-action") → inner-end
//	inner-user → (inner-escalate flow) → escalate-node (KindEndEvent)
func deadlineUserTaskInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-sla", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("inner-user", activity.WithEligibleRoles("reviewer"),
				activity.WithWaitDeadline(schedule.AfterExpr(`"30m"`), "inner-escalate"), activity.WithDeadlineAction("notify-action")),
			event.NewEnd("inner-end"),
			event.NewEnd("escalate-node"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-user"},
			{ID: "if2", Source: "inner-user", Target: "inner-end"},
			{ID: "inner-escalate", Source: "inner-user", Target: "escalate-node"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-sla", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestDeadlineUserTaskInsideSubProcess verifies that a deadline timer on a user task
// nested inside a sub-process:
//
//  1. Arms the deadline timer (ScheduleTimer) and parks the user task within the child scope.
//  2. When the deadline timer fires (task NOT completed), the escalation path runs within
//     the child scope (InvokeAction for "notify-action"), the task is cancelled
//     (UpdateTask), and the token moves to the escalation end node.
//  3. The escalation end drains the inner scope → outer-end → StatusCompleted
//     (sub-process exits cleanly to the parent).
func TestDeadlineUserTaskInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := deadlineUserTaskInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → user-task parks + deadline armed ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "sla-sub-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// AwaitHuman + ScheduleTimer(Deadline) emitted.
	var deadlineTimerID string
	var taskToken string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.AwaitHuman:
			taskToken = c.TaskToken
		case engine.ScheduleTimer:
			if c.Kind == engine.TimerDeadline {
				deadlineTimerID = c.TimerID
			}
		}
	}
	require.NotEmpty(t, taskToken, "expected AwaitHuman for inner-user task")
	require.NotEmpty(t, deadlineTimerID, "expected ScheduleTimer(Deadline) for inner-user task")

	// One token parked at inner-user, in the sub-process scope.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "inner-user", r1.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r1.State.Tokens[0].ScopeID,
		"inner-user token must carry the sub-process scope ID")

	// ---- Step 2: deadline fires (task NOT completed) → escalation path inside scope ----
	fireAt := at.Add(30 * time.Minute)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fireAt, deadlineTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// InvokeAction for notify-action, UpdateTask (cancelled), and CompleteInstance
	// (because escalate-node is an EndEvent, inner scope drains → outer completes).
	var foundNotify bool
	var foundUpdateTask bool
	var foundComplete bool
	for _, cmd := range r2.Commands {
		switch c := cmd.(type) {
		case engine.InvokeAction:
			if c.Name == "notify-action" {
				foundNotify = true
			}
		case engine.UpdateTask:
			if c.Task.TaskToken == taskToken {
				foundUpdateTask = true
			}
		case engine.CompleteInstance:
			foundComplete = true
		}
	}
	assert.True(t, foundNotify, "expected InvokeAction for notify-action on deadline breach")
	assert.True(t, foundUpdateTask, "expected UpdateTask (task cancelled) on deadline breach")
	assert.True(t, foundComplete,
		"expected CompleteInstance: escalation end drains inner scope → outer-end reached")

	// Instance must be completed (escalate-node is EndEvent → inner scope drains → outer-end).
	assert.Equal(t, engine.StatusCompleted, r2.State.Status,
		"instance must complete after deadline breach escalation path drains sub-process scope")
	assert.Empty(t, r2.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r2.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r2.State.EndedAt)
}
