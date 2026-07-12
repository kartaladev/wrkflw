package engine

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
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

// eventSubprocessNested reports whether raw acts as an event sub-process and,
// if so, returns its nested definition, its event-triggered inner start, and
// the non-interrupting flag. An event sub-process is an activity.SubProcess
// whose inner start is event-triggered (signal/timer/message); the
// non-interrupting flag is read from that start event (ADR-0122). A SubProcess
// with only a manual/none start is NOT an event sub-process (ok=false) — it
// stays token-driven inline. The returned inner start is the same one
// eventTriggeredStart selects, so callers need not re-scan.
func eventSubprocessNested(raw model.Node) (nested *model.ProcessDefinition, innerStart event.StartEvent, nonInterrupting bool, ok bool) {
	switch n := raw.(type) {
	case activity.SubProcess:
		if n.Subprocess == nil {
			return nil, event.StartEvent{}, false, false
		}
		se, has := eventTriggeredStart(n.Subprocess)
		if !has {
			return nil, event.StartEvent{}, false, false
		}
		return n.Subprocess, se, se.NonInterrupting, true
	}
	return nil, event.StartEvent{}, false, false
}

// armEventTriggeredSubprocesses scans the given definition for event
// sub-process nodes (see eventSubprocessNested) and records an
// eventTriggeredSubprocessArm for each. Called when a scope opens (via
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
func armEventTriggeredSubprocesses(def *model.ProcessDefinition, s *InstanceState, enclosingScopeID string, at time.Time, eval ConditionEvaluator) ([]Command, error) {
	var cmds []Command
	for _, raw := range def.Nodes {
		_, se, nonInterrupting, ok := eventSubprocessNested(raw)
		if !ok {
			continue
		}
		// se is the event-triggered inner start eventSubprocessNested already
		// resolved (StartNodes()[0] may be a manual start under multi-start nested
		// defs, ADR-0121) — no re-scan needed.

		arm := eventTriggeredSubprocessArm{
			EnclosingScopeID:    enclosingScopeID,
			EventSubprocessNode: raw.ID(),
			NonInterrupting:     nonInterrupting,
		}

		if se.SignalName != "" {
			arm.Signal = se.SignalName
		} else if !se.Timer.IsZero() {
			timerSpec, err := ResolveTrigger(eval, se.Timer, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("workflow-engine: event sub-process %q timer: %w", raw.ID(), err)
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
				return nil, fmt.Errorf("workflow-engine: event sub-process %q message correlation key: %w", raw.ID(), err)
			}
			arm.Message = se.MessageName
			arm.MessageKey = resolvedKey
		}

		s.EventTriggeredSubprocesses = append(s.EventTriggeredSubprocesses, arm)
	}
	return cmds, nil
}

// fireEventTriggeredSubprocessArm executes an event sub-process arm that has been triggered.
// Called from the SignalReceived, TimerFired, and MessageReceived handlers when the
// trigger matches an eventTriggeredSubprocessArm entry.
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
func fireEventTriggeredSubprocessArm(def *model.ProcessDefinition, s *InstanceState, ea eventTriggeredSubprocessArm, at time.Time, mode StepMode, eval ConditionEvaluator) ([]Command, error) {
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
	_, innerStart, _, isESP := eventSubprocessNested(espRaw)
	if !isESP {
		// Not an event sub-process (legacy or SubProcess-form): defensive no-op.
		// innerStart is the event-triggered start that armed this arm (the one
		// eventSubprocessNested resolved), not StartNodes()[0] which may be a
		// manual start under multi-start nested defs (ADR-0121).
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
		// (Fix 1: cancelTokenWaits also cancels armed events for an event-based-gateway-parked
		// token — its AwaitCommand starts with the "evtgw:" sentinel — so their timers do not
		// fire as stale orphans later.)
		for _, tok := range tokensToCancel {
			cmds = append(cmds, cancelTokenWaits(s, &tok, at)...)
		}

		// Cancel sibling event-subprocess arms for the same enclosing scope (all arms,
		// including this one). Emit CancelTimer for timer arms.
		// removeEventTriggeredSubprocessArmsForScope removes ALL arms for the scope including this one.
		for _, timerID := range s.removeEventTriggeredSubprocessArmsForScope(ea.EnclosingScopeID) {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}

		// Open a child scope for the event sub-process, parented to the ENCLOSING scope.
		// NodeID = the event sub-process node ID.
		// The drain code (KindEndEvent case) detects this as an event sub-process scope
		// (by checking the node kind in the parent definition) and handles completion:
		// when this child scope drains with no tokens left in the enclosing scope,
		// it closes the enclosing scope and resumes in the grandparent.
		childScopeID := s.openScope(ea.EventSubprocessNode, ea.EnclosingScopeID)
		s.placeTokenInScope(innerStart.ID(), childScopeID, at)
	} else {
		// Non-interrupting: leave enclosing scope running, spawn alongside. The arm
		// STAYS armed so it can fire again on the next delivery — BPMN
		// non-interrupting is repeatable (ADR-0124). Each fire opens its own child
		// scope; the arm is retired only when the enclosing scope closes
		// (removeEventTriggeredSubprocessArmsForScope) or the instance ends. A root
		// arm may therefore be present in a terminal snapshot; the runtime refuses
		// to hold correlation waiters for terminal instances, and this fire path is
		// status-guarded, so that is harmless.

		// Open a child scope for the event sub-process, parented to the enclosing scope.
		// NodeID = the event sub-process node ID.
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
