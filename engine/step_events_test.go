package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// signalCatchDef returns a linear definition:
//
//	Start → SignalCatch("approved") → ServiceTask(complete) → End
func signalCatchDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-signal", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "catch-approved", Kind: model.KindIntermediateCatchEvent, SignalName: "approved"},
			{ID: "complete", Kind: model.KindServiceTask, Action: "complete-action"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "catch-approved"},
			{ID: "f2", Source: "catch-approved", Target: "complete"},
			{ID: "f3", Source: "complete", Target: "end"},
		},
	}
}

// messageCatchDef returns a linear definition:
//
//	Start → MessageCatch("order", correlationKey="orderId") → ServiceTask(process) → End
func messageCatchDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-message", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "catch-order", Kind: model.KindIntermediateCatchEvent, MessageName: "order", CorrelationKey: `orderId`},
			{ID: "process", Kind: model.KindServiceTask, Action: "process-order"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "catch-order"},
			{ID: "f2", Source: "catch-order", Target: "process"},
			{ID: "f3", Source: "process", Target: "end"},
		},
	}
}

// signalThrowDef returns a definition:
//
//	Start → ServiceTask(setup) → SignalThrow("done") → ServiceTask(after) → End
func signalThrowDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-throw", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "setup", Kind: model.KindServiceTask, Action: "setup-action"},
			{ID: "throw-done", Kind: model.KindIntermediateThrowEvent, SignalName: "done"},
			{ID: "after", Kind: model.KindServiceTask, Action: "after-action"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "setup"},
			{ID: "f2", Source: "setup", Target: "throw-done"},
			{ID: "f3", Source: "throw-done", Target: "after"},
			{ID: "f4", Source: "after", Target: "end"},
		},
	}
}

// twoSignalTokensDef returns a definition with a parallel split into two signal-catch branches:
//
//	Start → ParallelFork → SignalCatch("wake") → End1
//	                     → SignalCatch("wake") → End2
func twoSignalTokensDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-2-signal", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "fork", Kind: model.KindParallelGateway},
			{ID: "catch1", Kind: model.KindIntermediateCatchEvent, SignalName: "wake"},
			{ID: "catch2", Kind: model.KindIntermediateCatchEvent, SignalName: "wake"},
			{ID: "end1", Kind: model.KindEndEvent},
			{ID: "end2", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "catch1"},
			{ID: "f3", Source: "fork", Target: "catch2"},
			{ID: "f4", Source: "catch1", Target: "end1"},
			{ID: "f5", Source: "catch2", Target: "end2"},
		},
	}
}

// TestSignalCatchResumesOnSignal verifies:
//  1. StartInstance drives into the signal-catch node; the token is parked with AwaitSignal=="approved".
//  2. A SignalReceived("approved") resumes the token and advances into the service task (InvokeAction).
//  3. A SignalReceived("other") is a clean no-op (no commands, no error).
func TestSignalCatchResumesOnSignal(t *testing.T) {
	def := signalCatchDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: StartInstance → parks at signal-catch
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 0, "signal-catch emits no commands on entry")
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "catch-approved", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "approved", tok.AwaitSignal)
	assert.Equal(t, "", tok.AwaitCommand)

	// Step 2: A non-matching signal is a clean no-op
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at, "other", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Nil(t, r2.Commands, "unmatched signal: no commands")
	// state is otherwise unchanged (same token, same node)
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "catch-approved", r2.State.Tokens[0].NodeID)
	assert.Equal(t, "approved", r2.State.Tokens[0].AwaitSignal)

	// Step 3: The matching signal resumes the token → InvokeAction on "complete-action"
	r3, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at, "approved", map[string]any{"result": "ok"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.Commands, 1)
	ia, ok := r3.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "complete-action", ia.Name)
	// Token has moved past the catch node into "complete"
	require.Len(t, r3.State.Tokens, 1)
	assert.Equal(t, "complete", r3.State.Tokens[0].NodeID)
	assert.Equal(t, "", r3.State.Tokens[0].AwaitSignal)
}

// TestMessageCatchCorrelates verifies:
//  1. A MessageReceived with matching name+key resumes the token.
//  2. A MessageReceived with non-matching key is a clean no-op.
func TestMessageCatchCorrelates(t *testing.T) {
	def := messageCatchDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Start the instance with orderId set in variables for correlation-key evaluation.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"orderId": "ORD-42"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 0)
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "catch-order", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "order", tok.AwaitMessage)
	// The correlation key was evaluated against variables: orderId="ORD-42"
	assert.Equal(t, "ORD-42", tok.AwaitMessageKey)

	// Step 2: Non-matching correlation key is a clean no-op
	r2, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(at, "order", "WRONG-KEY", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Nil(t, r2.Commands)
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "catch-order", r2.State.Tokens[0].NodeID)

	// Step 3: Matching name+key resumes the token → InvokeAction on "process-order"
	r3, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(at, "order", "ORD-42", map[string]any{"payload": "x"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.Commands, 1)
	ia, ok := r3.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "process-order", ia.Name)
	require.Len(t, r3.State.Tokens, 1)
	assert.Equal(t, "process", r3.State.Tokens[0].NodeID)
	assert.Equal(t, "", r3.State.Tokens[0].AwaitMessage)
	assert.Equal(t, "", r3.State.Tokens[0].AwaitMessageKey)
}

// TestMessageCatchNoCorrelationKeyMatchesOnNameOnly verifies that when
// CorrelationKey is empty on the node, MessageReceived matches on name alone
// (the empty string "" matches any MessageReceived whose CorrelationKey is also "").
func TestMessageCatchNoCorrelationKeyMatchesOnNameOnly(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "p-msg-nokey", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			// No CorrelationKey: match on name only
			{ID: "catch-msg", Kind: model.KindIntermediateCatchEvent, MessageName: "ping"},
			{ID: "svc", Kind: model.KindServiceTask, Action: "pong"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "catch-msg"},
			{ID: "f2", Source: "catch-msg", Target: "svc"},
			{ID: "f3", Source: "svc", Target: "end"},
		},
	}

	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "ping", tok.AwaitMessage)
	assert.Equal(t, "", tok.AwaitMessageKey)

	// MessageReceived with empty CorrelationKey matches
	r2, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(at, "ping", "", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	ia, ok := r2.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "pong", ia.Name)
}

// TestSignalThrowEmitsCommand verifies that a KindIntermediateThrowEvent with
// SignalName emits a ThrowSignal command and continues to the next node.
func TestSignalThrowEmitsCommand(t *testing.T) {
	def := signalThrowDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Start → parks at "setup" service task
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	setupIA, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for setup-action")
	assert.Equal(t, "setup-action", setupIA.Name)

	// Complete the setup service task → drives through throw → parks at "after"
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at, setupIA.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Must contain ThrowSignal{"done"} and InvokeAction{"after-action"}
	var throwCmd *engine.ThrowSignal
	var invokeCmd *engine.InvokeAction
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.ThrowSignal:
			vv := v
			throwCmd = &vv
		case engine.InvokeAction:
			vv := v
			invokeCmd = &vv
		}
	}
	require.NotNil(t, throwCmd, "expected ThrowSignal command")
	assert.Equal(t, "done", throwCmd.Name)
	require.NotNil(t, invokeCmd, "expected InvokeAction for after-action")
	assert.Equal(t, "after-action", invokeCmd.Name)
}

// TestBroadcastSignalResumesAllTokens verifies that a SignalReceived with a given
// name resumes ALL tokens awaiting that signal, not just the first.
func TestBroadcastSignalResumesAllTokens(t *testing.T) {
	def := twoSignalTokensDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// StartInstance → parallel fork → two signal-catch tokens
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 0)
	require.Len(t, r1.State.Tokens, 2)
	for _, tok := range r1.State.Tokens {
		assert.Equal(t, engine.TokenWaitingCommand, tok.State)
		assert.Equal(t, "wake", tok.AwaitSignal)
	}

	// SignalReceived("wake") resumes both tokens → both end events consumed
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at, "wake", nil), engine.StepOptions{})
	require.NoError(t, err)
	// Both end events should fire → instance completed
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	// CompleteInstance command must be present
	var hasComplete bool
	for _, c := range r2.Commands {
		if _, ok := c.(engine.CompleteInstance); ok {
			hasComplete = true
		}
	}
	assert.True(t, hasComplete, "expected CompleteInstance after both branches resume")
}

// Compile-time interface assertions for new triggers and commands.
var (
	_ engine.Trigger = engine.SignalReceived{}
	_ engine.Trigger = engine.MessageReceived{}
	_ engine.Command = engine.ThrowSignal{}
)

// TestSignalReceivedFields asserts NewSignalReceived stores all fields.
func TestSignalReceivedFields(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	payload := map[string]any{"x": 1}
	sr := engine.NewSignalReceived(at, "my-signal", payload)
	assert.Equal(t, at, sr.OccurredAt())
	assert.Equal(t, "my-signal", sr.Name)
	assert.Equal(t, payload, sr.Payload)
}

// TestMessageReceivedFields asserts NewMessageReceived stores all fields.
func TestMessageReceivedFields(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	payload := map[string]any{"y": 2}
	mr := engine.NewMessageReceived(at, "my-message", "key-99", payload)
	assert.Equal(t, at, mr.OccurredAt())
	assert.Equal(t, "my-message", mr.Name)
	assert.Equal(t, "key-99", mr.CorrelationKey)
	assert.Equal(t, payload, mr.Payload)
}

// TestThrowSignalFields asserts ThrowSignal stores all fields.
func TestThrowSignalFields(t *testing.T) {
	ts := engine.ThrowSignal{Name: "evt", Payload: map[string]any{"a": 1}}
	assert.Equal(t, "evt", ts.Name)
	assert.Equal(t, map[string]any{"a": 1}, ts.Payload)
}

// eventGatewayDef returns a definition modeling an event-based gateway that
// races a timer catch event against a signal catch event:
//
//	Start → EventGateway → TimerCatch("1h") → ServiceTask(timer-branch) → End1
//	                     → SignalCatch("approved") → ServiceTask(signal-branch) → End2
//
// TimerDuration uses the expr-evaluable format `"1h"` (quoted Go duration string).
func eventGatewayDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-evtgw", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "evtgw", Kind: model.KindEventBasedGateway},
			{ID: "timer-catch", Kind: model.KindIntermediateCatchEvent, TimerDuration: `"1h"`},
			{ID: "signal-catch", Kind: model.KindIntermediateCatchEvent, SignalName: "approved"},
			{ID: "timer-branch", Kind: model.KindServiceTask, Action: "timer-action"},
			{ID: "signal-branch", Kind: model.KindServiceTask, Action: "signal-action"},
			{ID: "end1", Kind: model.KindEndEvent},
			{ID: "end2", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "evtgw"},
			{ID: "f-gw-timer", Source: "evtgw", Target: "timer-catch"},
			{ID: "f-gw-signal", Source: "evtgw", Target: "signal-catch"},
			{ID: "f-timer-branch", Source: "timer-catch", Target: "timer-branch"},
			{ID: "f-signal-branch", Source: "signal-catch", Target: "signal-branch"},
			{ID: "f-timer-end", Source: "timer-branch", Target: "end1"},
			{ID: "f-signal-end", Source: "signal-branch", Target: "end2"},
		},
	}
}

// TestEventGatewayFirstTimerWins: gateway races a timer-catch vs a signal-catch.
// Firing the timer first causes:
//   - The timer branch proceeds (InvokeAction for timer-action).
//   - The signal arm is dropped (no CancelTimer needed for signal arms, just removed).
//   - A late SignalReceived("approved") is a clean no-op.
func TestEventGatewayFirstTimerWins(t *testing.T) {
	def := eventGatewayDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → EventGateway arms both catch events.
	// The timer arm must emit ScheduleTimer; the signal arm is just recorded.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Must have exactly one ScheduleTimer (for the timer-catch arm).
	var schedTimer *engine.ScheduleTimer
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			vv := st
			schedTimer = &vv
		}
	}
	require.NotNil(t, schedTimer, "expected ScheduleTimer for timer-catch arm")
	// FireAt = t0 + PT1H = 1 hour later.
	assert.Equal(t, t0.Add(time.Hour), schedTimer.FireAt)

	// The gateway token must be parked (no active tokens, no regular catch-event tokens).
	require.Len(t, r1.State.Tokens, 1, "gateway token should be parked")
	assert.Equal(t, engine.TokenWaitingCommand, r1.State.Tokens[0].State)
	assert.Equal(t, "evtgw", r1.State.Tokens[0].NodeID)

	// ArmedEvents must have two entries: one timer arm, one signal arm.
	assert.Len(t, r1.State.ArmedEvents, 2, "both arms must be recorded in ArmedEvents")

	// Step 2: TimerFired for the timer arm → timer branch proceeds.
	tFired := t0.Add(time.Hour)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(tFired, schedTimer.TimerID), engine.StepOptions{})
	require.NoError(t, err)

	// Timer branch must have invoked "timer-action".
	var timerBranchIA *engine.InvokeAction
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			timerBranchIA = &vv
		}
	}
	require.NotNil(t, timerBranchIA, "expected InvokeAction for timer-action")
	assert.Equal(t, "timer-action", timerBranchIA.Name)

	// The signal arm must be gone (no armedEvents remain for this gateway).
	assert.Empty(t, r2.State.ArmedEvents, "all armed events must be cleared after win")

	// Exactly one token exists: parked at timer-branch waiting for action.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "timer-branch", r2.State.Tokens[0].NodeID)

	// Step 3: Late SignalReceived("approved") for the now-cancelled signal arm must be a clean no-op.
	r3, err := engine.Step(def, r2.State,
		engine.NewSignalReceived(tFired, "approved", map[string]any{"x": 1}), engine.StepOptions{})
	require.NoError(t, err)
	// No commands: the signal arm was cancelled so no token is awaiting it.
	assert.Nil(t, r3.Commands, "late signal after gateway resolved must be no-op")
	// State unchanged relative to r2.
	assert.Len(t, r3.State.Tokens, 1)
	assert.Equal(t, "timer-branch", r3.State.Tokens[0].NodeID)
	// Variables must NOT have been mutated by the no-op signal (mergeVars fix).
	assert.Equal(t, r2.State.Variables, r3.State.Variables,
		"no-match signal must not mutate instance variables (mergeVars fix)")
}

// TestEventGatewayFirstSignalWins: same gateway; firing the signal first causes:
//   - The signal branch proceeds (InvokeAction for signal-action).
//   - A CancelTimer is emitted for the loser timer arm.
//   - A late TimerFired for that timer is a clean no-op.
func TestEventGatewayFirstSignalWins(t *testing.T) {
	def := eventGatewayDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → EventGateway arms both catch events.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Capture the scheduled timer ID so we can check CancelTimer later.
	var schedTimer *engine.ScheduleTimer
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			vv := st
			schedTimer = &vv
		}
	}
	require.NotNil(t, schedTimer, "expected ScheduleTimer for timer-catch arm")
	require.Len(t, r1.State.ArmedEvents, 2)

	// Step 2: SignalReceived("approved") → signal branch proceeds.
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(t0, "approved", nil), engine.StepOptions{})
	require.NoError(t, err)

	// Signal branch must have invoked "signal-action".
	var signalBranchIA *engine.InvokeAction
	var cancelCmd *engine.CancelTimer
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			vv := v
			signalBranchIA = &vv
		case engine.CancelTimer:
			vv := v
			cancelCmd = &vv
		}
	}
	require.NotNil(t, signalBranchIA, "expected InvokeAction for signal-action")
	assert.Equal(t, "signal-action", signalBranchIA.Name)

	// A CancelTimer must be emitted for the loser timer arm.
	require.NotNil(t, cancelCmd, "expected CancelTimer for loser timer arm")
	assert.Equal(t, schedTimer.TimerID, cancelCmd.TimerID)

	// All armed events cleared.
	assert.Empty(t, r2.State.ArmedEvents)

	// Exactly one token: parked at signal-branch waiting for action.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "signal-branch", r2.State.Tokens[0].NodeID)

	// Step 3: Late TimerFired for the cancelled timer arm must be a clean no-op.
	tLate := t0.Add(time.Hour)
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(tLate, schedTimer.TimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Nil(t, r3.Commands, "late TimerFired after gateway resolved must be no-op")
	assert.Len(t, r3.State.Tokens, 1)
	assert.Equal(t, "signal-branch", r3.State.Tokens[0].NodeID)
}

// TestEventGatewayMergeVarsFix verifies the mergeVars-on-no-match fix:
// A SignalReceived that matches no token (standalone or gateway arm) must NOT
// mutate instance variables.
func TestEventGatewayMergeVarsFix(t *testing.T) {
	def := signalCatchDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"existing": "value"}), engine.StepOptions{})
	require.NoError(t, err)

	// A non-matching signal must not mutate variables.
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at, "no-match", map[string]any{"injected": "bad"}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Nil(t, r2.Commands)
	assert.NotContains(t, r2.State.Variables, "injected",
		"non-matching signal must not inject variables into instance state")
	assert.Equal(t, "value", r2.State.Variables["existing"])
}
