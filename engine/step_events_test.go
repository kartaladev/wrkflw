package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
)

// signalCatchDef returns a linear definition:
//
//	Start → SignalCatch("approved") → ServiceTask(complete) → End
func signalCatchDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-signal", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewIntermediateCatchEvent("catch-approved", definition.WithSignalName("approved")),
			definition.NewServiceTask("complete", definition.WithActionName("complete-action")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "catch-approved"},
			{ID: "f2", Source: "catch-approved", Target: "complete"},
			{ID: "f3", Source: "complete", Target: "end"},
		},
	}
}

// messageCatchDef returns a linear definition:
//
//	Start → MessageCatch("order", correlationKey="orderId") → ServiceTask(process) → End
func messageCatchDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-message", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewIntermediateCatchEvent("catch-order", definition.WithMessageNameAndKey("order", `orderId`)),
			definition.NewServiceTask("process", definition.WithActionName("process-order")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "catch-order"},
			{ID: "f2", Source: "catch-order", Target: "process"},
			{ID: "f3", Source: "process", Target: "end"},
		},
	}
}

// signalThrowDef returns a definition:
//
//	Start → ServiceTask(setup) → SignalThrow("done") → ServiceTask(after) → End
func signalThrowDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-throw", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("setup", definition.WithActionName("setup-action")),
			definition.NewIntermediateThrowEvent("throw-done", definition.WithThrowSignal("done")),
			definition.NewServiceTask("after", definition.WithActionName("after-action")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
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
func twoSignalTokensDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-2-signal", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewParallelGateway("fork"),
			definition.NewIntermediateCatchEvent("catch1", definition.WithSignalName("wake")),
			definition.NewIntermediateCatchEvent("catch2", definition.WithSignalName("wake")),
			definition.NewEndEvent("end1"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
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
	def := &definition.ProcessDefinition{
		ID: "p-msg-nokey", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			// No CorrelationKey: match on name only
			definition.NewIntermediateCatchEvent("catch-msg", definition.WithMessageNameAndKey("ping", "")),
			definition.NewServiceTask("svc", definition.WithActionName("pong")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
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
func eventGatewayDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-evtgw", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewEventBasedGateway("evtgw"),
			definition.NewIntermediateCatchEvent("timer-catch", definition.WithTimerDuration(`"1h"`)),
			definition.NewIntermediateCatchEvent("signal-catch", definition.WithSignalName("approved")),
			definition.NewServiceTask("timer-branch", definition.WithActionName("timer-action")),
			definition.NewServiceTask("signal-branch", definition.WithActionName("signal-action")),
			definition.NewEndEvent("end1"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
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

// eventGatewayMessageDef returns a definition modeling an event-based gateway
// that races a timer catch event against a message catch event:
//
//	Start → EventGateway → TimerCatch("1h") → ServiceTask(timer-branch) → End1
//	                     → MessageCatch("order") → ServiceTask(msg-branch) → End2
func eventGatewayMessageDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-evtgw-msg", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewEventBasedGateway("evtgw"),
			definition.NewIntermediateCatchEvent("timer-catch", definition.WithTimerDuration(`"1h"`)),
			definition.NewIntermediateCatchEvent("msg-catch", definition.WithMessageNameAndKey("order", "")),
			definition.NewServiceTask("timer-branch", definition.WithActionName("timer-action")),
			definition.NewServiceTask("msg-branch", definition.WithActionName("msg-action")),
			definition.NewEndEvent("end1"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "evtgw"},
			{ID: "f-gw-timer", Source: "evtgw", Target: "timer-catch"},
			{ID: "f-gw-msg", Source: "evtgw", Target: "msg-catch"},
			{ID: "f-timer-branch", Source: "timer-catch", Target: "timer-branch"},
			{ID: "f-msg-branch", Source: "msg-catch", Target: "msg-branch"},
			{ID: "f-timer-end", Source: "timer-branch", Target: "end1"},
			{ID: "f-msg-end", Source: "msg-branch", Target: "end2"},
		},
	}
}

// TestEventGatewayFirstMessageWins: gateway races a timer arm vs a message arm.
// Firing the message first causes:
//   - The message branch proceeds (InvokeAction for msg-action).
//   - A CancelTimer is emitted for the loser timer arm.
//   - A late TimerFired is a clean no-op.
func TestEventGatewayFirstMessageWins(t *testing.T) {
	def := eventGatewayMessageDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → EventGateway arms both catch events.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Capture the timer ID for later assertions.
	var schedTimer *engine.ScheduleTimer
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			vv := st
			schedTimer = &vv
		}
	}
	require.NotNil(t, schedTimer, "expected ScheduleTimer for timer arm")
	require.Len(t, r1.State.ArmedEvents, 2, "both arms must be recorded")

	// Step 2: MessageReceived("order","") → message branch proceeds.
	r2, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(t0, "order", "", nil), engine.StepOptions{})
	require.NoError(t, err)

	var msgBranchIA *engine.InvokeAction
	var cancelCmd *engine.CancelTimer
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			vv := v
			msgBranchIA = &vv
		case engine.CancelTimer:
			vv := v
			cancelCmd = &vv
		}
	}
	require.NotNil(t, msgBranchIA, "expected InvokeAction for msg-action")
	assert.Equal(t, "msg-action", msgBranchIA.Name)
	require.NotNil(t, cancelCmd, "expected CancelTimer for loser timer arm")
	assert.Equal(t, schedTimer.TimerID, cancelCmd.TimerID)
	assert.Empty(t, r2.State.ArmedEvents, "all armed events must be cleared after win")

	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "msg-branch", r2.State.Tokens[0].NodeID)

	// Step 3: Late TimerFired for the cancelled timer arm is a clean no-op.
	tLate := t0.Add(time.Hour)
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(tLate, schedTimer.TimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Nil(t, r3.Commands, "late TimerFired after gateway resolved must be no-op")
	assert.Len(t, r3.State.Tokens, 1)
	assert.Equal(t, "msg-branch", r3.State.Tokens[0].NodeID)
}

// ---- Boundary event tests ----

// interruptingBoundaryTimerDef returns a definition:
//
//	Start → UserTask("approve") → End
//	                ↑ interrupting timer boundary "3h" → escalate → End2
func interruptingBoundaryTimerDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-bnd-timer", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewUserTask("approve", nil),
			definition.NewBoundaryEvent("bnd-timer", "approve", definition.WithBoundaryTimer(`"3h"`)),
			definition.NewServiceTask("escalate", definition.WithActionName("escalate-action")),
			definition.NewEndEvent("end"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "approve"},
			{ID: "f-approve-end", Source: "approve", Target: "end"},
			{ID: "f-bnd-escalate", Source: "bnd-timer", Target: "escalate"},
			{ID: "f-escalate-end", Source: "escalate", Target: "end2"},
		},
	}
}

// TestInterruptingBoundaryTimerCancelsHost verifies:
//  1. On entering UserTask("approve"), an AwaitHuman is emitted AND a ScheduleTimer
//     is emitted for the interrupting boundary timer (3h).
//  2. Firing the boundary timer (WITHOUT completing the task) cancels the host token,
//     places a new token on "escalate" (InvokeAction for escalate-action), emits
//     a CancelTimer for any deadline/reminder timers on the task (none here).
//  3. A late HumanCompleted for the now-consumed host token is a clean no-op
//     (ErrTokenNotFound).
func TestInterruptingBoundaryTimerCancelsHost(t *testing.T) {
	def := interruptingBoundaryTimerDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → UserTask parked; boundary timer armed.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var awaitHuman *engine.AwaitHuman
	var boundaryTimer *engine.ScheduleTimer
	for _, c := range r1.Commands {
		switch v := c.(type) {
		case engine.AwaitHuman:
			vv := v
			awaitHuman = &vv
		case engine.ScheduleTimer:
			vv := v
			boundaryTimer = &vv
		}
	}
	require.NotNil(t, awaitHuman, "expected AwaitHuman for approve task")
	require.NotNil(t, boundaryTimer, "expected ScheduleTimer for boundary timer")
	assert.Equal(t, t0.Add(3*time.Hour), boundaryTimer.FireAt, "boundary timer must fire at t0+3h")

	// One token: parked at "approve".
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "approve", r1.State.Tokens[0].NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, r1.State.Tokens[0].State)
	// Boundary arm recorded.
	require.Len(t, r1.State.Boundaries, 1, "boundary arm must be recorded")

	// Step 2: Boundary timer fires → host cancelled, escalate path runs.
	tFired := t0.Add(3 * time.Hour)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(tFired, boundaryTimer.TimerID), engine.StepOptions{})
	require.NoError(t, err)

	// InvokeAction for escalate-action must be emitted.
	var escalateIA *engine.InvokeAction
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			escalateIA = &vv
		}
	}
	require.NotNil(t, escalateIA, "expected InvokeAction for escalate-action")
	assert.Equal(t, "escalate-action", escalateIA.Name)

	// Host token is gone; a new token is on "escalate".
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "escalate", r2.State.Tokens[0].NodeID)
	// Boundary arms cleared.
	assert.Empty(t, r2.State.Boundaries, "boundary arms must be cleared after interrupting fire")

	// Step 3: Late HumanCompleted for the now-consumed host token is a no-op (error).
	// The token is gone, so the engine returns ErrTokenNotFound.
	_, err = engine.Step(def, r2.State,
		engine.NewHumanCompleted(tFired, awaitHuman.TaskToken, nil, authz.Actor{ID: "user1"}), engine.StepOptions{})
	assert.Error(t, err, "late HumanCompleted for consumed host must return error (token gone)")
}

// nonInterruptingBoundaryDef returns a definition:
//
//	Start → UserTask("work") → End
//	               ↑ non-interrupting signal boundary "notify" → notify-svc → End2
func nonInterruptingBoundaryDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-bnd-nonint", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewUserTask("work", nil),
			definition.NewBoundaryEvent("bnd-signal", "work", definition.WithBoundarySignal("notify"), definition.BoundaryNonInterrupting()),
			definition.NewServiceTask("notify-svc", definition.WithActionName("notify-action")),
			definition.NewEndEvent("end"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-notify", Source: "bnd-signal", Target: "notify-svc"},
			{ID: "f-notify-end", Source: "notify-svc", Target: "end2"},
		},
	}
}

// TestNonInterruptingBoundarySpawnsParallelToken verifies:
//  1. On entering UserTask("work"), AwaitHuman is emitted and the signal boundary arm
//     is recorded (no ScheduleTimer for signal boundaries).
//  2. Firing the boundary signal → an ADDITIONAL token appears on "notify-svc"
//     (InvokeAction for notify-action), while the host "work" token is still parked.
//  3. The host can still be completed normally after the boundary fires.
func TestNonInterruptingBoundarySpawnsParallelToken(t *testing.T) {
	def := nonInterruptingBoundaryDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → UserTask parked; signal boundary arm recorded.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var awaitHuman *engine.AwaitHuman
	for _, c := range r1.Commands {
		if ah, ok := c.(engine.AwaitHuman); ok {
			vv := ah
			awaitHuman = &vv
		}
	}
	require.NotNil(t, awaitHuman, "expected AwaitHuman for work task")

	// No ScheduleTimer for a signal boundary.
	for _, c := range r1.Commands {
		_, isTimer := c.(engine.ScheduleTimer)
		assert.False(t, isTimer, "signal boundary must not emit ScheduleTimer")
	}

	// One token at "work"; boundary arm recorded.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "work", r1.State.Tokens[0].NodeID)
	require.Len(t, r1.State.Boundaries, 1, "signal boundary arm must be recorded")

	// Step 2: Signal fires → additional token on "notify-svc"; host still parked.
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(t0, "notify", nil), engine.StepOptions{})
	require.NoError(t, err)

	var notifyIA *engine.InvokeAction
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			notifyIA = &vv
		}
	}
	require.NotNil(t, notifyIA, "expected InvokeAction for notify-action")
	assert.Equal(t, "notify-action", notifyIA.Name)

	// Two tokens: one still at "work" (parked), one at "notify-svc".
	require.Len(t, r2.State.Tokens, 2, "non-interrupting: host + new boundary token")
	nodeIDs := make(map[string]bool)
	for _, tok := range r2.State.Tokens {
		nodeIDs[tok.NodeID] = true
	}
	assert.True(t, nodeIDs["work"], "host token must still be at work")
	assert.True(t, nodeIDs["notify-svc"], "new token must be at notify-svc")

	// The fired boundary arm is removed (one-shot).
	assert.Empty(t, r2.State.Boundaries, "fired non-interrupting arm removed")

	// Step 3: Complete the host normally — the host token advances, the instance
	// keeps running because the notify-svc token (from the non-interrupting boundary)
	// is still pending.
	r3, err := engine.Step(def, r2.State,
		engine.NewHumanCompleted(t0, awaitHuman.TaskToken, nil, authz.Actor{ID: "user1"}), engine.StepOptions{})
	require.NoError(t, err)
	// Instance still running: notify-svc token is pending its action.
	assert.Equal(t, engine.StatusRunning, r3.State.Status)
	// No "work" token remains: host advanced past end and was consumed.
	for _, tok := range r3.State.Tokens {
		assert.NotEqual(t, "work", tok.NodeID, "host token must have advanced past work")
	}
}

// hostCompletionCancelsBoundaryDef returns a definition:
//
//	Start → ServiceTask("work") → End
//	               ↑ interrupting timer boundary "1h" → alert → End2
func hostCompletionCancelsBoundaryDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-bnd-hostfirst", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("work", definition.WithActionName("work-action")),
			definition.NewBoundaryEvent("bnd-timer", "work", definition.WithBoundaryTimer(`"1h"`)),
			definition.NewServiceTask("alert", definition.WithActionName("alert-action")),
			definition.NewEndEvent("end"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-alert", Source: "bnd-timer", Target: "alert"},
			{ID: "f-alert-end", Source: "alert", Target: "end2"},
		},
	}
}

// TestHostCompletionCancelsArmedBoundary verifies:
//  1. On entering ServiceTask("work"), a boundary timer arm is scheduled and recorded.
//  2. Completing the host FIRST emits a CancelTimer for the boundary timer.
//  3. A late boundary TimerFired is a clean no-op.
func TestHostCompletionCancelsArmedBoundary(t *testing.T) {
	def := hostCompletionCancelsBoundaryDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → ServiceTask parked; boundary timer armed.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var invokeWork *engine.InvokeAction
	var boundaryTimer *engine.ScheduleTimer
	for _, c := range r1.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			vv := v
			invokeWork = &vv
		case engine.ScheduleTimer:
			vv := v
			boundaryTimer = &vv
		}
	}
	require.NotNil(t, invokeWork, "expected InvokeAction for work-action")
	require.NotNil(t, boundaryTimer, "expected ScheduleTimer for boundary timer")
	require.Len(t, r1.State.Boundaries, 1, "boundary arm must be recorded")

	// Step 2: Complete the host FIRST → CancelTimer for boundary emitted.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(t0, invokeWork.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	var cancelCmd *engine.CancelTimer
	for _, c := range r2.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			vv := ct
			cancelCmd = &vv
		}
	}
	require.NotNil(t, cancelCmd, "expected CancelTimer for boundary timer on host completion")
	assert.Equal(t, boundaryTimer.TimerID, cancelCmd.TimerID)

	// Boundary arms cleared.
	assert.Empty(t, r2.State.Boundaries, "boundary arms cleared after host completion")

	// Step 3: Late boundary TimerFired is a clean no-op.
	tLate := t0.Add(time.Hour)
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(tLate, boundaryTimer.TimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Nil(t, r3.Commands, "late boundary TimerFired after host completion must be no-op")
}

// badBoundaryDurationDef returns a definition:
//
//	Start → ServiceTask("work") → End
//	               ↑ interrupting timer boundary with malformed TimerDuration → alert → End2
//
// The boundary TimerDuration is intentionally invalid so that EvalDuration fails.
func badBoundaryDurationDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-bad-bnd-dur", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("work", definition.WithActionName("work-action")),
			definition.NewBoundaryEvent("bnd-bad", "work", definition.WithBoundaryTimer(`"not a duration"`)),
			definition.NewServiceTask("alert", definition.WithActionName("alert-action")),
			definition.NewEndEvent("end"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-alert", Source: "bnd-bad", Target: "alert"},
			{ID: "f-alert-end", Source: "alert", Target: "end2"},
		},
	}
}

// TestBoundaryBadDurationErrors verifies that entering a host activity whose
// boundary TimerDuration is malformed causes Step to return a non-nil error
// wrapping the eval failure. Previously the error was silently dropped (no-arm);
// after the fix, it must propagate out of Step.
func TestBoundaryBadDurationErrors(t *testing.T) {
	def := badBoundaryDurationDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Driving into the host (StartInstance → ServiceTask with bad boundary) must return
	// a non-nil error mentioning the boundary node and/or the eval failure.
	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.Error(t, err, "bad boundary TimerDuration must cause Step to return an error")
	assert.Contains(t, err.Error(), "bnd-bad",
		"error message must reference the boundary node ID")
}

// actionFailedCancelsArmsAndBoundariesDef returns a definition:
//
//	Start → ServiceTask("work") → End
//	               ↑ interrupting timer boundary "2h" → alert → End2
//
// Used to verify that ActionFailed cancels armed boundary timer IDs (Fix 1).
func actionFailedCancelsArmsAndBoundariesDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-af-cancel", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("work", definition.WithActionName("work-action")),
			definition.NewBoundaryEvent("bnd-timer", "work", definition.WithBoundaryTimer(`"2h"`)),
			definition.NewServiceTask("alert", definition.WithActionName("alert-action")),
			definition.NewEndEvent("end"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-alert", Source: "bnd-timer", Target: "alert"},
			{ID: "f-alert-end", Source: "alert", Target: "end2"},
		},
	}
}

// TestActionFailedCancelsArmsAndBoundaries verifies Fix 1:
// When ActionFailed is received for the host token, the engine must emit
// CancelTimer commands for ALL pending boundary timer arms (s.Boundaries) and
// event-gateway timer arms (s.ArmedEvents), and clear both slices. Previously
// only s.Timers (deadline/reminder records) were drained, leaving boundary and
// gateway timer arms orphaned in the scheduler.
func TestActionFailedCancelsArmsAndBoundaries(t *testing.T) {
	def := actionFailedCancelsArmsAndBoundariesDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → ServiceTask("work") parked with boundary timer arm.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Capture the InvokeAction commandID and the boundary ScheduleTimer.
	var workIA *engine.InvokeAction
	var boundaryTimer *engine.ScheduleTimer
	for _, c := range r1.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			vv := v
			workIA = &vv
		case engine.ScheduleTimer:
			vv := v
			boundaryTimer = &vv
		}
	}
	require.NotNil(t, workIA, "expected InvokeAction for work-action")
	require.NotNil(t, boundaryTimer, "expected ScheduleTimer for boundary timer")

	// The boundary arm must be recorded in s.Boundaries.
	require.Len(t, r1.State.Boundaries, 1, "boundary arm must be recorded")
	boundaryTimerID := boundaryTimer.TimerID

	// Step 2: ActionFailed for the work command.
	// EXPECTED (after fix): StatusFailed, CancelTimer{boundaryTimerID} emitted,
	//                        s.Boundaries empty.
	// ACTUAL (before fix):  StatusFailed, NO CancelTimer, s.Boundaries still has the arm.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(t0, workIA.CommandID, "simulated failure", false), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusFailed, r2.State.Status, "instance must be StatusFailed")

	// After fix: a CancelTimer for the boundary timer must be emitted.
	var cancelCmd *engine.CancelTimer
	for _, c := range r2.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			vv := ct
			cancelCmd = &vv
		}
	}
	require.NotNil(t, cancelCmd, "ActionFailed must emit CancelTimer for boundary timer arm")
	assert.Equal(t, boundaryTimerID, cancelCmd.TimerID, "CancelTimer must reference the boundary timer ID")

	// After fix: s.Boundaries must be empty.
	assert.Empty(t, r2.State.Boundaries, "ActionFailed must clear s.Boundaries")
	// s.ArmedEvents must also be empty (none were armed in this topology, but still).
	assert.Empty(t, r2.State.ArmedEvents, "ActionFailed must clear s.ArmedEvents")
}

// nonInterruptingBoundarySignalSelfCascadeDef returns a definition:
//
//	Start → UserTask("work") → End
//	               ↑ non-interrupting signal boundary "pulse" → SignalCatch("pulse") → End2
//
// When SignalReceived{"pulse"} is delivered:
//   - Step 2: The non-interrupting boundary fires → spawns a new token on "inner-catch"
//     (which itself parks awaiting "pulse").
//   - Step 3: The standalone broadcast loop must NOT re-consume the newly-spawned
//     token because it was NOT awaiting "pulse" at the delivery instant.
//
// BPMN semantics: signal delivery is a point-in-time event; tokens spawned during
// the same Step are not in scope for the current delivery.
func nonInterruptingBoundarySignalSelfCascadeDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-nonint-selfcascade", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewUserTask("work", nil),
			definition.NewBoundaryEvent("bnd-pulse", "work", definition.WithBoundarySignal("pulse"), definition.BoundaryNonInterrupting()),
			// The boundary's outgoing path leads to a signal catch for the same signal.
			definition.NewIntermediateCatchEvent("inner-catch", definition.WithSignalName("pulse")),
			definition.NewEndEvent("end"),
			definition.NewEndEvent("end2"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-catch", Source: "bnd-pulse", Target: "inner-catch"},
			{ID: "f-catch-end", Source: "inner-catch", Target: "end2"},
		},
	}
}

// TestNonInterruptingBoundarySignalNoSelfCascade verifies Fix 2:
// A non-interrupting signal boundary fires when SignalReceived{"pulse"} is delivered.
// The boundary's outgoing path leads to a signal-catch also awaiting "pulse".
// The newly-spawned token (parked at inner-catch) must NOT be re-consumed by
// the same delivery — it is not in the snapshot of tokens awaiting "pulse"
// at the delivery instant.
func TestNonInterruptingBoundarySignalNoSelfCascade(t *testing.T) {
	def := nonInterruptingBoundarySignalSelfCascadeDef()
	t0 := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → UserTask("work") parked; signal boundary arm recorded.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "work", r1.State.Tokens[0].NodeID)
	require.Len(t, r1.State.Boundaries, 1, "signal boundary arm must be recorded")

	// Step 2: Deliver SignalReceived{"pulse"}.
	//   - The boundary arm fires (non-interrupting): spawns a token at "inner-catch"
	//     which parks awaiting "pulse".
	//   - The standalone broadcast loop (step 3 in SignalReceived dispatch) must NOT
	//     re-consume this newly-spawned token — it was NOT in the snapshot at delivery.
	//   - Expected: "work" token still parked, "inner-catch" token parked awaiting "pulse".
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(t0, "pulse", nil), engine.StepOptions{})
	require.NoError(t, err)

	// Two tokens must exist: the host "work" and the newly-spawned "inner-catch".
	require.Len(t, r2.State.Tokens, 2, "host + inner-catch token must exist")

	nodeIDs := make(map[string]string) // nodeID → AwaitSignal
	for _, tok := range r2.State.Tokens {
		nodeIDs[tok.NodeID] = tok.AwaitSignal
	}
	assert.Equal(t, "", nodeIDs["work"],
		"host work token must still be parked (AwaitCommand, not AwaitSignal)")
	assert.Equal(t, "pulse", nodeIDs["inner-catch"],
		"inner-catch token must remain parked awaiting 'pulse' (not re-consumed by this delivery)")

	// The inner-catch token must be parked (not consumed/advanced), confirming no self-cascade.
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "inner-catch" {
			assert.Equal(t, engine.TokenWaitingCommand, tok.State,
				"inner-catch token must be parked (AwaitSignal), not active/consumed")
			assert.Equal(t, "pulse", tok.AwaitSignal,
				"inner-catch token must still be awaiting 'pulse'")
		}
	}
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
