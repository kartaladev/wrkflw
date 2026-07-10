package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// eventTriggeredStart returns the first event-triggered start event of def — a
// StartEvent carrying a signal, timer, or message trigger — or (zero,false) if
// none exists (e.g. every start is a manual/trigger-less start). An event
// sub-process is entered via this triggered start; StartNodes()[0] may be a
// manual start now that multi-start nested definitions are legal (ADR-0121).
func eventTriggeredStart(def *model.ProcessDefinition) (event.StartEvent, bool) {
	for _, raw := range def.StartNodes() {
		se, ok := raw.(event.StartEvent)
		if !ok {
			continue
		}
		if se.SignalName != "" || !se.Timer.IsZero() || se.MessageName != "" {
			return se, true
		}
	}
	return event.StartEvent{}, false
}

// armEventSubprocesses scans the given definition for KindEventSubProcess nodes
// and records an eventSubprocessArm for each. Called when a scope opens (via
// openScope in the KindSubProcess drive case) and at StartInstance for the root
// definition. enclosingScopeID is "" for root-level event sub-processes.
//
// The trigger is read from the nested definition's event-triggered start (see
// eventTriggeredStart), not StartNodes()[0], which may be a manual start under
// multi-start nested definitions (ADR-0121). Trigger encoding:
//   - Signal trigger: the event-triggered start's SignalName is non-empty.
//   - Timer trigger: the event-triggered start's Timer is set (non-zero TriggerSpec).
//   - Message trigger: the event-triggered start's MessageName is non-empty.
//
// Timer triggers emit a ScheduleTimer command. Signal/message triggers are recorded
// only (delivery arrives via SignalReceived/MessageReceived).
//
// Definition-scan order is deterministic; arms are appended in that order.
func armEventSubprocesses(def *model.ProcessDefinition, s *InstanceState, enclosingScopeID string, at time.Time, eval ConditionEvaluator) ([]Command, error) {
	var cmds []Command
	for _, raw := range def.Nodes {
		n, ok := raw.(event.EventSubProcess)
		if !ok {
			continue
		}
		if n.Subprocess == nil {
			continue // defensive: no nested def, skip
		}
		// Select the event-triggered start; StartNodes()[0] may be a manual start
		// once multi-start nested defs are legal (ADR-0121). An ESP with no
		// event-triggered start has no delivery to arm — skip it.
		se, ok := eventTriggeredStart(n.Subprocess)
		if !ok {
			continue
		}

		arm := eventSubprocessArm{
			EnclosingScopeID:    enclosingScopeID,
			EventSubprocessNode: n.ID(),
			NonInterrupting:     n.NonInterrupting,
		}

		if se.SignalName != "" {
			arm.Signal = se.SignalName
		} else if !se.Timer.IsZero() {
			timerSpec, err := ResolveTrigger(eval, se.Timer, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("workflow-engine: event sub-process %q timer: %w", n.ID(), err)
			}
			timerID := s.nextTimerID()
			arm.TimerID = timerID
			cmds = append(cmds, ScheduleTimer{
				TimerID: timerID,
				Token:   "", // no host token; keyed by enclosing scope
				Trigger: timerSpec,
				Kind:    TimerIntermediate,
			})
		} else if se.MessageName != "" {
			resolvedKey, err := eval.EvalString(se.CorrelationKey, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("workflow-engine: event sub-process %q message correlation key: %w", n.ID(), err)
			}
			arm.Message = se.MessageName
			arm.MessageKey = resolvedKey
		}

		s.EventSubprocesses = append(s.EventSubprocesses, arm)
	}
	return cmds, nil
}

// fireEventSubprocessArm executes an event sub-process arm that has been triggered.
// Called from the SignalReceived, TimerFired, and MessageReceived handlers when the
// trigger matches an eventSubprocessArm entry.
//
// Dispatch order (relative to gateway/boundary/deadline/standalone):
//  1. Event-gateway arm (first-event-wins routing).
//  2. Boundary event arm (interrupting/non-interrupting on host activity).
//  3. Event sub-process arm (interrupting: cancel scope; non-interrupting: spawn alongside).
//  4. deadline/in-wait timer record.
//  5. Standalone parked token.
//
// For interrupting (!ea.NonInterrupting):
//  1. Verify the enclosing scope is still active (if not, clean no-op).
//  2. Cancel ALL tokens in the enclosing scope (consuming them + closing visits).
//  3. Cancel all other event-subprocess arms for the same scope (emit CancelTimer for timer arms).
//  4. Cancel all boundary arms for tokens that were in the scope.
//  5. Open a NEW child scope for the event sub-process (parent = enclosing scope).
//  6. Place a token on the event sub-process's start node in that child scope.
//  7. Drive forward. When this child scope drains (KindEndEvent path), it exits via the
//     ENCLOSING scope's parent (since the enclosing scope is now "completed" by the
//     event sub-process completion).
//
// For non-interrupting (ea.NonInterrupting):
//  1. Verify the enclosing scope is still active.
//  2. Do NOT cancel the enclosing scope's tokens.
//  3. Remove ONLY this arm (one-shot).
//  4. Open a child scope and place a start token — runs alongside.
//  5. Drive forward.
func fireEventSubprocessArm(def *model.ProcessDefinition, s *InstanceState, ea eventSubprocessArm, at time.Time, mode StepMode, eval ConditionEvaluator) ([]Command, error) {
	// Verify the enclosing scope is still active. For root scope (empty enclosingScopeID),
	// the scope is always "active" as long as the instance is running.
	if ea.EnclosingScopeID != "" {
		scope := s.scopeByID(ea.EnclosingScopeID)
		if scope == nil {
			// Enclosing scope is gone (completed or cancelled): stale trigger, clean no-op.
			return nil, nil
		}
	} else {
		// Root scope: active if instance is running.
		if s.Status != StatusRunning {
			return nil, nil
		}
	}

	// Resolve the enclosing scope's definition so we can find the event sub-process node.
	enclosingDef, err := defForScope(def, s, ea.EnclosingScopeID)
	if err != nil {
		return nil, err
	}

	// Resolve the event sub-process node in the enclosing definition.
	espRaw, ok := enclosingDef.Node(ea.EventSubprocessNode)
	if !ok {
		// Node missing: defensive no-op.
		return nil, nil
	}
	espNode, isESP := espRaw.(event.EventSubProcess)
	if !isESP || espNode.Subprocess == nil {
		// Not an EventSubProcess or has no nested def: defensive no-op.
		return nil, nil
	}
	// Enter the ESP at its event-triggered start (the one that armed this arm),
	// not StartNodes()[0] which may be a manual start under multi-start nested
	// defs (ADR-0121). If no event-triggered start exists, the arm should never
	// have been recorded: defensive no-op.
	innerStart, ok := eventTriggeredStart(espNode.Subprocess)
	if !ok {
		return nil, nil
	}

	var cmds []Command

	if !ea.NonInterrupting {
		// Interrupting: cancel all tokens in the enclosing scope, keep the enclosing
		// scope itself open (so the drain code can detect its children), then open a
		// child scope for the event sub-process.

		// Collect all tokens in the enclosing scope (snapshot to avoid mutating while iterating).
		var tokensToCancel []Token
		for _, tok := range s.Tokens {
			if tok.ScopeID == ea.EnclosingScopeID {
				tokensToCancel = append(tokensToCancel, tok)
			}
		}
		// Cancel deadline/reminder timers and boundary arms for each token in scope, then consume.
		for _, tok := range tokensToCancel {
			// Cancel deadline/reminder timers (UserTask case).
			taskTok := tok.AwaitCommand
			for _, timerID := range s.cancelTimersByTaskToken(taskTok, "") {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}
			// Cancel any token-keyed in-wait reminder (ReceiveTask / catch): its
			// parked token is being consumed, so the recurring reminder must go.
			for _, timerID := range s.cancelTimersForToken(tok.ID, "") {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}
			// Cancel boundary arms for this host token.
			for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}
			// Fix 1: if the token is an event-based-gateway-parked token (its
			// AwaitCommand starts with the "evtgw:" sentinel), cancel all of its
			// armed events so their timers do not fire as stale orphans later.
			// Deterministic: removeArmedEventsForGateway returns timer IDs in
			// ArmedEvents slice order; we emit CancelTimer for each.
			if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
				for _, timerID := range s.removeArmedEventsForGateway(tok.ID) {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
			}
			// Consume the token (close visit).
			tokPtr := s.tokenByID(tok.ID)
			if tokPtr != nil {
				s.consumeToken(tokPtr, at)
			}
		}

		// Cancel sibling event-subprocess arms for the same enclosing scope (all arms,
		// including this one). Emit CancelTimer for timer arms.
		// removeEventSubprocessArmsForScope removes ALL arms for the scope including this one.
		for _, timerID := range s.removeEventSubprocessArmsForScope(ea.EnclosingScopeID) {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}

		// Open a child scope for the event sub-process, parented to the ENCLOSING scope.
		// NodeID = the event sub-process node ID (KindEventSubProcess).
		// The drain code (KindEndEvent case) detects this as an event sub-process scope
		// (by checking the node kind in the parent definition) and handles completion:
		// when this child scope drains with no tokens left in the enclosing scope,
		// it closes the enclosing scope and resumes in the grandparent.
		childScopeID := s.openScope(ea.EventSubprocessNode, ea.EnclosingScopeID)
		s.placeTokenInScope(innerStart.ID(), childScopeID, at)
	} else {
		// Non-interrupting: leave enclosing scope running, spawn alongside.

		// Remove only THIS arm (one-shot).
		s.removeEventSubprocessArm(ea.EnclosingScopeID, ea.EventSubprocessNode)

		// Open a child scope for the event sub-process, parented to the enclosing scope.
		// NodeID = the event sub-process node ID (KindEventSubProcess).
		// This child scope runs alongside; when it drains, it is closed without affecting
		// the enclosing scope (tokensInScope for the enclosing scope is unaffected).
		childScopeID := s.openScope(ea.EventSubprocessNode, ea.EnclosingScopeID)
		s.placeTokenInScope(innerStart.ID(), childScopeID, at)
	}

	// Drive forward.
	driveCmds, err := drive(def, s, at, mode, eval)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}
