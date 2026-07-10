package engine_test

// step_subprocess_eventstart_test.go — ADR-0122 Task 4 parity tests: the same
// event-sub-process BEHAVIORS already covered by the legacy event.EventSubProcess
// kind (see state_esp_test.go, step_eventsubprocess_multistart_test.go, and the
// ESP cases in step_subprocess_test.go / reverse_instance_test.go), re-authored
// using the NEW form — an activity.SubProcess whose nested definition has an
// event-triggered inner start (event.NewStart with WithSignalName /
// WithMessageCorrelator / WithStartTimer; WithNonInterrupting for the
// non-interrupting flavor). The event-sub SubProcess node carries NO incoming
// sequence flow, exactly like the legacy ESP nodes it mirrors.
//
// These scenarios are NOT converted to the table-test form: each is a distinct,
// multi-step Step()/assert narrative over a structurally different definition
// (different trigger kind, different nesting depth, different reverse/compensation
// setup) — the "structurally different setup" exception the table-test skill
// carves out. Where a file already contains two tests that call the exact same
// SUT shape with only the input varying, that file uses tables (see
// step_events_test.go); these do not fit that shape.
//
// Every test here is expected to PASS immediately: T2 (engine) and T3
// (validation) already recognize the SubProcess-with-event-start form. A
// failure here is a real parity gap, not a test bug.

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

// ---------------------------------------------------------------------------
// Scenario 1: root-level INTERRUPTING, MESSAGE-triggered event-sub cancels
// enclosing (root) tokens and completes.
// Mirrors: TestRootLevelEventSubprocessCompletes (Fix 2), step_subprocess_test.go,
// but with a message trigger (worked example in task-4-brief.md) instead of signal.
// ---------------------------------------------------------------------------

// rootMessageEventStartDef builds:
//
//	root-start → root-svc (ServiceTask "normal-action") → root-end
//	[event-sub "root-esp"] triggered by message "cancel" correlated on orderId (interrupting)
//	  esp-start(message "cancel", key "orderId") → esp-svc("esp-action") → esp-end
//
// "root-esp" is an activity.SubProcess with NO incoming sequence flow — it is
// latent until the message fires (ADR-0122 form of a root-level event-sub).
func rootMessageEventStartDef() *model.ProcessDefinition {
	espInner := &model.ProcessDefinition{
		ID: "root-esp-msg-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithMessageCorrelator("cancel", "orderId")),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "re1", Source: "esp-start", Target: "esp-svc"},
			{ID: "re2", Source: "esp-svc", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "root-esp-msg-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("root-start"),
			activity.NewServiceTask("root-svc", activity.WithTaskAction("normal-action")),
			event.NewEnd("root-end"),
			activity.NewSubProcess("root-esp", espInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "rf1", Source: "root-start", Target: "root-svc"},
			{ID: "rf2", Source: "root-svc", Target: "root-end"},
		},
	}
}

// TestEventStartSubprocess_RootInterrupting_Message mirrors
// TestRootLevelEventSubprocessCompletes: a root-level interrupting event-sub
// fires, cancels the root-svc token, runs its own path, and the instance
// completes cleanly with no outer scope to resume into.
func TestEventStartSubprocess_RootInterrupting_Message(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := rootMessageEventStartDef()

	// ---- Step 1: StartInstance (with orderId var) → root-svc parks ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-root-msg-esp"},
		engine.NewStartInstance(at, map[string]any{"orderId": "ORD-1"}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	normalCmdID := findInvokeActionID(t, r1.Commands, "normal-action")

	// Root-level event-sub arm must be recorded (EnclosingScopeID == ""),
	// with the message correlation key resolved from vars.
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1, "root-level event-sub arm must be recorded")
	arm := r1.State.EventTriggeredSubprocesses[0]
	assert.Equal(t, "", arm.EnclosingScopeID, "root-level arm must have empty EnclosingScopeID")
	assert.Equal(t, "cancel", arm.Message)
	assert.Equal(t, "ORD-1", arm.MessageKey, "correlation key must be resolved from vars")

	// ---- Step 2: MessageReceived{"cancel","ORD-1"} → interrupting event-sub fires ----
	r2, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(at.Add(time.Second), "cancel", "ORD-1", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// root-svc token must be gone (cancelled by the interrupting event-sub).
	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "root-svc", tok.NodeID, "root-svc token must be cancelled")
	}

	// A child scope must be open for the event-sub, parented to root ("").
	var espScope *engine.Scope
	for i := range r2.State.Scopes {
		if r2.State.Scopes[i].NodeID == "root-esp" {
			espScope = &r2.State.Scopes[i]
			break
		}
	}
	require.NotNil(t, espScope, "expected a child scope for the root-level event-sub")
	assert.Equal(t, "", espScope.ParentID)

	espCmdID := findInvokeActionID(t, r2.Commands, "esp-action")

	// ---- Step 3: complete esp-action → event-sub scope drains → instance completes ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), espCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Empty(t, r3.State.Tokens)
	assert.Empty(t, r3.State.Scopes)
	require.NotNil(t, r3.State.EndedAt)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
		}
	}
	assert.True(t, found, "expected CompleteInstance after root-level event-sub completes")
	require.NotEmpty(t, normalCmdID, "sanity: normal-action was invoked before being cancelled")
}

// ---------------------------------------------------------------------------
// Scenario 2: root-level NON-INTERRUPTING, SIGNAL-triggered event-sub runs
// alongside; both drain to completion.
// Combines TestRootLevelEventSubprocessCompletes's root-level shape with
// TestNonInterruptingEventSubprocessRunsAlongside's non-interrupting behavior.
// ---------------------------------------------------------------------------

// rootNonInterruptingSignalEventStartDef builds:
//
//	root-start → root-svc (ServiceTask "normal-action") → root-end
//	[event-sub "root-esp", non-interrupting] triggered by signal "notify"
//	  esp-start(signal "notify", non-interrupting) → esp-svc("notify-action") → esp-end
func rootNonInterruptingSignalEventStartDef() *model.ProcessDefinition {
	espInner := &model.ProcessDefinition{
		ID: "root-esp-nonintr-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithSignalName("notify"), event.WithNonInterrupting()),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("notify-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "re1", Source: "esp-start", Target: "esp-svc"},
			{ID: "re2", Source: "esp-svc", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "root-esp-nonintr-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("root-start"),
			activity.NewServiceTask("root-svc", activity.WithTaskAction("normal-action")),
			event.NewEnd("root-end"),
			activity.NewSubProcess("root-esp", espInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "rf1", Source: "root-start", Target: "root-svc"},
			{ID: "rf2", Source: "root-svc", Target: "root-end"},
		},
	}
}

// TestEventStartSubprocess_RootNonInterrupting_Signal verifies that a
// root-level non-interrupting event-sub spawns alongside the root path
// without cancelling it, and that the instance only completes once BOTH
// paths finish.
func TestEventStartSubprocess_RootNonInterrupting_Signal(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := rootNonInterruptingSignalEventStartDef()

	// ---- Step 1: StartInstance → root-svc parks; event-sub arm recorded ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-root-nonintr"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	normalCmdID := findInvokeActionID(t, r1.Commands, "normal-action")

	require.Len(t, r1.State.EventTriggeredSubprocesses, 1)
	assert.True(t, r1.State.EventTriggeredSubprocesses[0].NonInterrupting)
	assert.Equal(t, "", r1.State.EventTriggeredSubprocesses[0].EnclosingScopeID)

	// ---- Step 2: SignalReceived{"notify"} → non-interrupting: spawn alongside ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "notify", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// root-svc token must STILL be present (non-interrupting: host not cancelled).
	rootSvcPresent := false
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "root-svc" {
			rootSvcPresent = true
		}
	}
	assert.True(t, rootSvcPresent, "root-svc must still be pending (non-interrupting)")

	// The arm is one-shot: removed after firing.
	assert.Empty(t, r2.State.EventTriggeredSubprocesses, "one-shot arm must be removed after firing")

	notifyCmdID := findInvokeActionID(t, r2.Commands, "notify-action")

	// ---- Step 3: complete notify-action → event-sub scope drains, root-svc still pending ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), notifyCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status, "instance still running: root-svc still pending")
	assert.Empty(t, r3.State.Scopes, "event-sub child scope must be closed after it drains")

	// ---- Step 4: complete normal-action → root drains → instance completes ----
	r4, err := engine.Step(def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), normalCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status)
	assert.Empty(t, r4.State.Tokens)
	assert.Empty(t, r4.State.Scopes)

	found := false
	for _, cmd := range r4.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
		}
	}
	assert.True(t, found, "expected CompleteInstance once both paths drain")
}

// ---------------------------------------------------------------------------
// Scenario 3: NESTED event-sub (declared inside a SubProcess scope) arms on
// scope open, fires, drains correctly (interrupting flavor).
// Mirrors: TestInterruptingEventSubprocessCancelsParentScope, step_subprocess_test.go.
// ---------------------------------------------------------------------------

// nestedEventStartDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-user (KindUserTask) → inner-end
//	[event-sub "evtsub"] triggered by signal "cancel"
//	  evtsub-start(signal "cancel"[, non-interrupting]) → evtsub-svc("cancel-action") → evtsub-end
func nestedEventStartDef(nonInterrupting bool) *model.ProcessDefinition {
	var evtsubStart model.Node
	if nonInterrupting {
		evtsubStart = event.NewStart("evtsub-start", event.WithSignalName("cancel"), event.WithNonInterrupting())
	} else {
		evtsubStart = event.NewStart("evtsub-start", event.WithSignalName("cancel"))
	}

	evtsubInner := &model.ProcessDefinition{
		ID: "evtsub-inner", Version: 1,
		Nodes: []model.Node{
			evtsubStart,
			activity.NewServiceTask("evtsub-svc", activity.WithTaskAction("cancel-action")),
			event.NewEnd("evtsub-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "evtsub-start", Target: "evtsub-svc"},
			{ID: "ef2", Source: "evtsub-svc", Target: "evtsub-end"},
		},
	}

	inner := &model.ProcessDefinition{
		ID: "inner-evtsub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("inner-user"),
			event.NewEnd("inner-end"),
			activity.NewSubProcess("evtsub", evtsubInner),
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

// TestEventStartSubprocess_Nested_Interrupting mirrors
// TestInterruptingEventSubprocessCancelsParentScope: a nested (inside "sub")
// interrupting event-sub cancels the user-task token in the enclosing (inner)
// scope, runs its own path, and completing it drains the enclosing scope and
// completes the instance. A late HumanCompleted on the cancelled task must
// error with ErrTokenNotFound.
func TestEventStartSubprocess_Nested_Interrupting(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := nestedEventStartDef(false) // interrupting

	// ---- Step 1: StartInstance — outer-start → sub → inner-start → inner-user (parks) ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-nested-esp"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	require.Len(t, r1.State.Tokens, 1, "expected one parked token at inner-user")
	assert.Equal(t, "inner-user", r1.State.Tokens[0].NodeID)
	taskToken := r1.State.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken)

	require.Len(t, r1.State.Scopes, 1, "expected one scope open for sub")
	innerScopeID := r1.State.Scopes[0].ID

	// ---- Step 2: SignalReceived{"cancel"} — interrupting event-sub fires ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "inner-user", tok.NodeID,
			"inner-user token must be cancelled by interrupting event-sub")
	}

	var espScope *engine.Scope
	for i := range r2.State.Scopes {
		sc := &r2.State.Scopes[i]
		if sc.ParentID == innerScopeID {
			espScope = sc
			break
		}
	}
	require.NotNil(t, espScope, "expected a child scope for the event-sub")

	cancelCmdID := findInvokeActionID(t, r2.Commands, "cancel-action")

	// ---- Step 3: complete cancel-action — event-sub scope drains → outer completes ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cancelCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Empty(t, r3.State.Tokens)
	assert.Empty(t, r3.State.Scopes)
	require.NotNil(t, r3.State.EndedAt)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
		}
	}
	assert.True(t, found)

	// ---- Step 4: late HumanCompleted must error with ErrTokenNotFound ----
	_, err = engine.Step(def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Second), taskToken, nil, authz.Actor{ID: "alice"}), engine.StepOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// TestEventStartSubprocess_Nested_NonInterrupting mirrors
// TestNonInterruptingEventSubprocessRunsAlongside: a nested non-interrupting
// event-sub runs alongside the enclosing scope's user task; the enclosing
// scope only drains once both the event-sub and inner-user complete.
func TestEventStartSubprocess_Nested_NonInterrupting(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := nestedEventStartDef(true) // non-interrupting

	// ---- Step 1: StartInstance ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-nested-nonintr"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "inner-user", r1.State.Tokens[0].NodeID)

	// ---- Step 2: SignalReceived{"cancel"} — non-interrupting: spawn alongside ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	userTaskPresent := false
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "inner-user" {
			userTaskPresent = true
		}
	}
	assert.True(t, userTaskPresent, "inner-user must still be parked (non-interrupting)")

	cancelCmdID := findInvokeActionID(t, r2.Commands, "cancel-action")

	// ---- Step 3: complete cancel-action — event-sub scope drains, instance still running ----
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cancelCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status)

	userTaskStillPresent := false
	for _, tok := range r3.State.Tokens {
		if tok.NodeID == "inner-user" {
			userTaskStillPresent = true
		}
	}
	assert.True(t, userTaskStillPresent, "inner-user must still be parked after event-sub completes")

	// ---- Step 4: complete inner-user → inner scope drains → outer completes ----
	task := r3.State.Tasks[0]
	r4, err := engine.Step(def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Second), task.TaskToken, nil, authz.Actor{ID: "alice"}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status)
	assert.Empty(t, r4.State.Tokens)
	assert.Empty(t, r4.State.Scopes)

	found := false
	for _, cmd := range r4.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
		}
	}
	assert.True(t, found)
}

// ---------------------------------------------------------------------------
// Scenario 4: TIMER-triggered event-sub emits ScheduleTimer on arm, fires on
// TimerFired.
// Mirrors: TestTimerEventSubprocessArmsOnScopeOpen, step_subprocess_test.go.
// ---------------------------------------------------------------------------

// timerNestedEventStartDef builds:
//
// outer:  outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-svc (ServiceTask "inner-action") → inner-end
//	[event-sub "evtsub"] triggered by timer "1h" (interrupting)
//	  evtsub-start(timer "1h") → evtsub-svc("timeout-action") → evtsub-end
func timerNestedEventStartDef() *model.ProcessDefinition {
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
			activity.NewSubProcess("evtsub", evtsubInner),
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

// TestEventStartSubprocess_Timer_ArmsOnScopeOpen mirrors
// TestTimerEventSubprocessArmsOnScopeOpen: a timer-triggered event-sub arms
// (emits ScheduleTimer) when its enclosing scope opens; firing the timer
// cancels the normal path and runs the timeout path instead.
func TestEventStartSubprocess_Timer_ArmsOnScopeOpen(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := timerNestedEventStartDef()

	// ---- Step 1: StartInstance → sub enters → InvokeAction + ScheduleTimer (arm) ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-timer-esp"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	var schedTimer engine.ScheduleTimer
	innerCmdID := findInvokeActionID(t, r1.Commands, "inner-action")
	for _, cmd := range r1.Commands {
		if c, ok := cmd.(engine.ScheduleTimer); ok {
			schedTimer = c
		}
	}
	require.NotEmpty(t, schedTimer.TimerID, "expected ScheduleTimer for the event-sub timer arm")

	assert.Len(t, r1.State.EventTriggeredSubprocesses, 1, "expected one event-sub arm recorded")

	// ---- Step 2: timer fires (interrupting) → inner-svc cancelled, evtsub-svc fires ----
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(at.Add(time.Hour), schedTimer.TimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "inner-svc", tok.NodeID, "inner-svc must be cancelled")
	}
	timeoutCmdID := findInvokeActionID(t, r2.Commands, "timeout-action")

	// ---- Step 3: complete timeout-action → event-sub scope drains → outer completes ----
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
	assert.True(t, found)
	require.NotEmpty(t, innerCmdID, "sanity: inner-action was invoked before being cancelled")
}

// TestEventStartSubprocess_NormalCloseCancelsArm mirrors
// TestEventSubprocessArmCancelledOnNormalScopeClose (M2): when the enclosing
// scope drains WITHOUT the event-sub's timer ever firing, the orphaned arm
// must be cancelled (CancelTimer emitted) and removed from state.
func TestEventStartSubprocess_NormalCloseCancelsArm(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := timerNestedEventStartDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-esp-cancel-scope"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	innerCmdID := findInvokeActionID(t, r1.Commands, "inner-action")
	var espTimerID string
	for _, cmd := range r1.Commands {
		if c, ok := cmd.(engine.ScheduleTimer); ok {
			espTimerID = c.TimerID
		}
	}
	require.NotEmpty(t, espTimerID)
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1)

	// Complete inner-svc normally (the event-sub timer never fires) → scope drains.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Minute), innerCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)

	cancelFound := false
	for _, cmd := range r2.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == espTimerID {
			cancelFound = true
		}
	}
	assert.True(t, cancelFound, "CancelTimer for the orphaned event-sub arm must be emitted")
	assert.Empty(t, r2.State.EventTriggeredSubprocesses)
}

// ---------------------------------------------------------------------------
// Scenario 5: MESSAGE-triggered event-sub correlates by key.
// New scenario (no direct legacy ESP test used a message trigger); mirrors the
// correlation-matching pattern of TestMessageCatchCorrelates (step_events_test.go)
// applied to the event-sub arm/fire path (step_eventsubprocess.go arm.Message /
// arm.MessageKey, resolved via se.CorrelationKey against start vars).
// ---------------------------------------------------------------------------

func messageNestedEventStartDef() *model.ProcessDefinition {
	evtsubInner := &model.ProcessDefinition{
		ID: "evtsub-msg-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("evtsub-start", event.WithMessageCorrelator("cancel", "orderId")),
			activity.NewServiceTask("evtsub-svc", activity.WithTaskAction("cancel-action")),
			event.NewEnd("evtsub-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "evtsub-start", Target: "evtsub-svc"},
			{ID: "ef2", Source: "evtsub-svc", Target: "evtsub-end"},
		},
	}
	inner := &model.ProcessDefinition{
		ID: "inner-msg-evtsub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewEnd("inner-end"),
			activity.NewSubProcess("evtsub", evtsubInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-msg-evtsub", Version: 1,
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

// TestEventStartSubprocess_Message_CorrelatesByKey verifies that a
// message-triggered event-sub's arm carries the resolved correlation key, a
// non-matching key is a clean no-op, and a matching key fires the event-sub
// (cancelling the enclosing scope's token, interrupting flavor).
func TestEventStartSubprocess_Message_CorrelatesByKey(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := messageNestedEventStartDef()

	// ---- Step 1: StartInstance with orderId var → arm carries resolved key ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-msg-esp"},
		engine.NewStartInstance(at, map[string]any{"orderId": "ORD-42"}), engine.StepOptions{})
	require.NoError(t, err)
	innerCmdID := findInvokeActionID(t, r1.Commands, "inner-action")

	require.Len(t, r1.State.EventTriggeredSubprocesses, 1)
	arm := r1.State.EventTriggeredSubprocesses[0]
	assert.Equal(t, "cancel", arm.Message)
	assert.Equal(t, "ORD-42", arm.MessageKey)

	// ---- Step 2: non-matching correlation key is a clean no-op ----
	r2, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(at.Add(time.Second), "cancel", "WRONG-KEY", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r2.Commands, "non-matching correlation key must be a clean no-op")
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "inner-svc", r2.State.Tokens[0].NodeID, "inner-svc token must be undisturbed")

	// ---- Step 3: matching name+key fires the interrupting event-sub ----
	r3, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(at.Add(2*time.Second), "cancel", "ORD-42", map[string]any{"x": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status)

	for _, tok := range r3.State.Tokens {
		assert.NotEqual(t, "inner-svc", tok.NodeID, "inner-svc must be cancelled by the matching message")
	}
	cancelCmdID := findInvokeActionID(t, r3.Commands, "cancel-action")

	// ---- Step 4: complete cancel-action → event-sub drains → outer completes ----
	r4, err := engine.Step(def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), cancelCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status)

	found := false
	for _, cmd := range r4.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
		}
	}
	assert.True(t, found)
	require.NotEmpty(t, innerCmdID, "sanity: inner-action was invoked before being cancelled")
}

// ---------------------------------------------------------------------------
// Scenario 6a: interrupting event-sub with a sibling non-interrupting
// event-sub child — firing the interrupting one must also cancel the
// sibling's arm (emitting CancelTimer for a timer-armed sibling), per the
// documented dispatch order in step_eventsubprocess.go
// (fireEventTriggeredSubprocessArm step 3: "Cancel all other event-subprocess
// arms for the same scope").
// ---------------------------------------------------------------------------

// siblingEventStartDef builds a ROOT-level scope with two event-subs:
//
//	root-start → root-svc ("normal-action") → root-end
//	[event-sub "esp-signal", interrupting] signal "cancel" → esp-svc("cancel-action") → esp-end
//	[event-sub "esp-timer", non-interrupting] timer "1h"    → timer-svc("timer-action") → timer-end
func siblingEventStartDef() *model.ProcessDefinition {
	signalInner := &model.ProcessDefinition{
		ID: "esp-signal-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-signal-start", event.WithSignalName("cancel")),
			activity.NewServiceTask("esp-signal-svc", activity.WithTaskAction("cancel-action")),
			event.NewEnd("esp-signal-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "se1", Source: "esp-signal-start", Target: "esp-signal-svc"},
			{ID: "se2", Source: "esp-signal-svc", Target: "esp-signal-end"},
		},
	}
	timerInner := &model.ProcessDefinition{
		ID: "esp-timer-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-timer-start", event.WithStartTimer(schedule.AfterExpr(`"1h"`)), event.WithNonInterrupting()),
			activity.NewServiceTask("esp-timer-svc", activity.WithTaskAction("timer-action")),
			event.NewEnd("esp-timer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "te1", Source: "esp-timer-start", Target: "esp-timer-svc"},
			{ID: "te2", Source: "esp-timer-svc", Target: "esp-timer-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "root-sibling-esp-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("root-start"),
			activity.NewServiceTask("root-svc", activity.WithTaskAction("normal-action")),
			event.NewEnd("root-end"),
			activity.NewSubProcess("esp-signal", signalInner),
			activity.NewSubProcess("esp-timer", timerInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "rf1", Source: "root-start", Target: "root-svc"},
			{ID: "rf2", Source: "root-svc", Target: "root-end"},
		},
	}
}

// TestEventStartSubprocess_InterruptingCancelsSiblingArm verifies that firing
// an interrupting event-sub cancels a sibling non-interrupting event-sub's
// still-pending arm in the same (root) scope, emitting CancelTimer for its
// scheduled timer.
func TestEventStartSubprocess_InterruptingCancelsSiblingArm(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := siblingEventStartDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-sibling-esp"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r1.State.EventTriggeredSubprocesses, 2, "both sibling arms must be recorded")
	var siblingTimerID string
	for _, cmd := range r1.Commands {
		if c, ok := cmd.(engine.ScheduleTimer); ok {
			siblingTimerID = c.TimerID
		}
	}
	require.NotEmpty(t, siblingTimerID, "expected ScheduleTimer for the non-interrupting sibling's timer arm")

	// Fire the interrupting signal-triggered sibling.
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// The non-interrupting sibling's arm must be cancelled too (CancelTimer emitted).
	cancelFound := false
	for _, cmd := range r2.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == siblingTimerID {
			cancelFound = true
		}
	}
	assert.True(t, cancelFound, "expected CancelTimer for the sibling's timer arm")
	assert.Empty(t, r2.State.EventTriggeredSubprocesses, "all root-scope arms must be cleared on interrupting fire")

	// root-svc must be cancelled (interrupting cancels the whole enclosing scope).
	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "root-svc", tok.NodeID)
	}
}

// ---------------------------------------------------------------------------
// Scenario 6b: Fix 1 — interrupting event-sub cancels an event-based
// gateway's armed events (ArmedEvents cleanup) in the enclosing scope.
// Mirrors: TestInterruptingEventSubprocessCancelsGatewayArms, step_subprocess_test.go.
// ---------------------------------------------------------------------------

// espWithEventGatewayEventStartDef mirrors espWithEventGatewayDef
// (step_subprocess_test.go) with the event-sub authored as an
// activity.SubProcess-with-event-start:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → evtgw (KindEventBasedGateway)
//	  → timer-catch (IntermediateCatchEvent timer "2h") → normal-end
//	  → signal-catch (IntermediateCatchEvent signal "done") → normal-end
//	[event-sub "evtsub"] triggered by signal "cancel" (interrupting)
//	  evtsub-start(signal "cancel") → evtsub-svc("cancel-action") → evtsub-end
func espWithEventGatewayEventStartDef() *model.ProcessDefinition {
	evtsubInner := &model.ProcessDefinition{
		ID: "esp-gw-evtsub-inner-es", Version: 1,
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
		ID: "inner-esp-gw-es", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("timer-catch", event.WithCatchTimer(schedule.AfterExpr(`"2h"`))),
			event.NewIntermediateCatch("signal-catch", event.WithSignalName("done")),
			event.NewEnd("normal-end"),
			activity.NewSubProcess("evtsub", evtsubInner),
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
		ID: "outer-esp-gw-es", Version: 1,
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

// TestEventStartSubprocess_InterruptingCancelsGatewayArms mirrors
// TestInterruptingEventSubprocessCancelsGatewayArms (Fix 1).
func TestEventStartSubprocess_InterruptingCancelsGatewayArms(t *testing.T) {
	at := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	def := espWithEventGatewayEventStartDef()

	// ---- Step 1: StartInstance → inner starts → event gateway parks with timer arm ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-esp-gw-es"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	require.NotEmpty(t, r1.State.ArmedEvents, "event gateway must have armed events")

	var gwTimerID string
	for _, cmd := range r1.Commands {
		if st, ok := cmd.(engine.ScheduleTimer); ok {
			gwTimerID = st.TimerID
		}
	}
	require.NotEmpty(t, gwTimerID)

	require.NotEmpty(t, r1.State.EventTriggeredSubprocesses, "event-sub arm must be recorded")

	// ---- Step 2: SignalReceived{"cancel"} — interrupting event-sub fires ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	assert.Empty(t, r2.State.ArmedEvents,
		"ArmedEvents must be empty after interrupting event-sub cancels the enclosing scope")

	cancelTimerFound := false
	for _, cmd := range r2.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == gwTimerID {
			cancelTimerFound = true
		}
	}
	assert.True(t, cancelTimerFound, "expected CancelTimer for the gateway timer arm")

	// A late TimerFired for the cancelled gateway timer must be a clean no-op.
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(at.Add(2*time.Hour), gwTimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r3.Commands)
	assert.Equal(t, engine.StatusRunning, r3.State.Status)
}

// ---------------------------------------------------------------------------
// Scenario 7: reverse-instance over an armed (root-level, timer) event-sub.
// Mirrors: TestReverseToStart_RearmsRootEventSubprocess, reverse_instance_test.go.
// ---------------------------------------------------------------------------

// reverseWithRootEventStartDef mirrors reverseWithRootESPDef but with the
// event-sub authored as an activity.SubProcess with an event-triggered start.
func reverseWithRootEventStartDef() *model.ProcessDefinition {
	espInner := &model.ProcessDefinition{
		ID: "resp-es-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithStartTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "re1", Source: "esp-start", Target: "esp-svc"},
			{ID: "re2", Source: "esp-svc", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-rev-esp-es", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			activity.NewServiceTask("park", activity.WithTaskAction("park")),
			event.NewEnd("end"),
			activity.NewSubProcess("root-esp", espInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "park"},
			{ID: "f3", Source: "park", Target: "end"},
		},
	}
}

// TestEventStartSubprocess_ReverseToStart_RearmsRootEventSubprocess mirrors
// TestReverseToStart_RearmsRootEventSubprocess: a full reverse-to-start must
// re-arm the root-level event-sub (re-emitting ScheduleTimer with a fresh
// TimerID) and cancel the stale original timer.
func TestEventStartSubprocess_ReverseToStart_RearmsRootEventSubprocess(t *testing.T) {
	def := reverseWithRootEventStartDef()
	t0 := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)

	// ---- Step 1: StartInstance → root event-sub arms (EnclosingScopeID == "") ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-rev-esp-es"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1, "root event-sub must arm on StartInstance")
	assert.Equal(t, "", r1.State.EventTriggeredSubprocesses[0].EnclosingScopeID)
	originalTimerID := r1.State.EventTriggeredSubprocesses[0].TimerID
	require.NotEmpty(t, originalTimerID)

	// ---- Step 2: complete "do" → compensation recorded, token parks on "park" ----
	cmdDo := findInvokeActionID(t, r1.Commands, "do")
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdDo, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status)
	require.Len(t, r2.State.RootCompensations, 1)

	// ---- Step 3: NewReverseToStart → "undo" fires ----
	r3, err := engine.Step(def, r2.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	undoID := findInvokeActionID(t, r3.Commands, "undo")

	// ---- Step 4: complete "undo" → resume at start (re-arm must happen HERE) ----
	r4, err := engine.Step(def, r3.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r4.State.Status)

	require.Len(t, r4.State.EventTriggeredSubprocesses, 1, "root event-sub must be re-armed after full reverse")
	assert.Equal(t, "", r4.State.EventTriggeredSubprocesses[0].EnclosingScopeID)

	var schedTimer engine.ScheduleTimer
	foundTimer := false
	for _, cmd := range r4.Commands {
		if st, ok := cmd.(engine.ScheduleTimer); ok {
			schedTimer = st
			foundTimer = true
		}
	}
	assert.True(t, foundTimer, "resume must re-schedule the root event-sub's timer")
	assert.NotEmpty(t, schedTimer.TimerID)
	assert.Equal(t, r4.State.EventTriggeredSubprocesses[0].TimerID, schedTimer.TimerID)

	assert.Contains(t, r4.Commands, engine.CancelTimer{TimerID: originalTimerID},
		"full-reverse re-arm must cancel the stale original root event-sub timer")
	assert.NotEqual(t, originalTimerID, r4.State.EventTriggeredSubprocesses[0].TimerID,
		"re-armed arm must carry a freshly minted TimerID")
}
