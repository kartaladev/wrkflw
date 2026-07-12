package engine

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// armBoundaries finds all KindBoundaryEvent nodes with AttachedTo == hostNode,
// records a boundaryArm for each, and returns ScheduleTimer commands for timer
// boundaries. Called from the ServiceTask and UserTask park points in drive.
//
// Definition-scan order is deterministic (Nodes slice order); boundary arms are
// appended in the same order so s.Boundaries is deterministic.
//
// A bad Timer trigger expression is returned as a wrapped error — consistent with
// the intermediate-timer and deadline paths — so callers can fail fast rather than
// silently no-arming the boundary. The resolved trigger (including native
// recurring/calendar forms) is emitted verbatim on the ScheduleTimer command; the
// scheduler owns the firing math and any native recurrence.
func armBoundaries(def *model.ProcessDefinition, s *InstanceState, hostTokenID, hostNode string, at time.Time, eval ConditionEvaluator) ([]Command, error) {
	var cmds []Command
	for _, raw := range def.Nodes {
		n, ok := raw.(event.BoundaryEvent)
		if !ok || n.AttachedTo != hostNode {
			continue
		}
		// Find the boundary's single outgoing flow.
		outs := def.Outgoing(n.ID())
		if len(outs) == 0 {
			continue // unreachable if model.Validate passes
		}
		flowID := outs[0].ID

		arm := boundaryArm{
			HostToken:       hostTokenID,
			HostNode:        hostNode,
			BoundaryNode:    n.ID(),
			Flow:            flowID,
			NonInterrupting: n.NonInterrupting,
			Action:          n.Action,
		}

		timerSpec, err := ResolveTrigger(eval, n.Timer, s.Variables)
		if err != nil {
			return nil, fmt.Errorf("workflow-engine: boundary %q on %q: %w", n.ID(), hostNode, err)
		}
		if !timerSpec.IsZero() {
			timerID := s.nextTimerID()
			arm.TimerID = timerID
			cmds = append(cmds, ScheduleTimer{
				TimerID: timerID,
				Token:   hostTokenID,
				Trigger: timerSpec,
				Kind:    TimerIntermediate,
			})
		} else if n.SignalName != "" {
			arm.Signal = n.SignalName
		} else if n.MessageName != "" {
			resolvedKey, err := eval.EvalString(n.CorrelationKey, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("workflow-engine: boundary %q on %q message correlation key: %w", n.ID(), hostNode, err)
			}
			arm.Message = n.MessageName
			arm.MessageKey = resolvedKey
		}
		s.Boundaries = append(s.Boundaries, arm)
	}
	return cmds, nil
}

// fireBoundaryArm executes a boundary event arm that has fired. It is called
// from the TimerFired and SignalReceived handlers.
//
// For interrupting boundaries (!ba.NonInterrupting):
//  1. Verify the host token is still parked. If not, it's a late/stale fire → no-op.
//  2. cancelTokenWaits sweeps the host: cancel deadline/reminder timers (UserTask,
//     via cancelTimersByTaskToken using the host's taskToken), cancel its in-wait
//     reminder, remove ALL boundary arms for this host (emit CancelTimer for timer
//     siblings), and consume the host token (close its visit, remove from slice).
//     A boundary host is never an event-based-gateway token, so the sweep's
//     "evtgw:"-prefixed armed-event removal is always a no-op here.
//  3. Place a new Active token at the boundary's outgoing flow target.
//  4. Drive forward.
//
// For non-interrupting boundaries (ba.NonInterrupting):
//  1. Verify the host token is still parked. If not, no-op.
//  2. Leave the host parked.
//  3. Remove ONLY this boundary arm (fired once; do not re-arm — repeating out of scope).
//  4. Place an additional Active token at the boundary's outgoing flow target.
//  5. Drive forward (the new token).
func fireBoundaryArm(def *model.ProcessDefinition, s *InstanceState, ba boundaryArm, at time.Time, mode StepMode, eval ConditionEvaluator) ([]Command, error) {
	// Find the host token by ID (not by AwaitCommand — the host token parks on
	// taskToken/cmdID, not on the boundary timer). If the token is gone (already
	// consumed by another path), this is a late fire — clean no-op.
	hostTok := s.tokenByID(ba.HostToken)
	if hostTok == nil {
		// Also clean up stale boundary arms for this host (defensive).
		s.removeBoundaryArmsForHost(ba.HostToken)
		return nil, nil
	}

	// Resolve the effective definition for the boundary's scope. A boundary event
	// inside a sub-process must look up its outgoing flow in the INNER definition,
	// not the top-level one. defForScope returns the inner def for a scoped token.
	tdef, err := defForScope(def, s, hostTok.ScopeID)
	if err != nil {
		return nil, err
	}

	// Resolve the boundary's outgoing flow target.
	var flowTarget string
	for _, f := range tdef.Flows {
		if f.ID == ba.Flow {
			flowTarget = f.Target
			break
		}
	}
	if flowTarget == "" {
		// No target: unreachable if model.Validate passes (boundary must have outgoing flow).
		return nil, fmt.Errorf("workflow-engine: boundary %q: outgoing flow %q not found", ba.BoundaryNode, ba.Flow)
	}

	hostScopeID := hostTok.ScopeID
	var cmds []Command

	// Emit the fire-once boundary action before routing (mirrors the
	// deadline-breach action path in handleDeadlineFired). FireAndForget
	// means a catalog action failure does not block routing.
	cmds = append(cmds, emitFireOnceAction(s, ba.Action)...)

	if !ba.NonInterrupting {
		// Interrupting: consume the host, cancel its task timers and boundary siblings.
		//
		// cancelTokenWaits cancels deadline/reminder timers for the host (UserTask
		// case: AwaitCommand == taskToken; for a ServiceTask host, AwaitCommand is a
		// cmdID, not a taskToken, so cancelTimersByTaskToken finds no records — which
		// is correct), the host's in-wait reminder, and ALL boundary arms for this
		// host (emit CancelTimer for timer siblings — the fired arm's timerID, if
		// any, is included; it already fired so the runtime's cancel is idempotent —
		// no special handling needed), then consumes the host token (close its
		// visit, remove from slice).
		cmds = append(cmds, cancelTokenWaits(s, hostTok, at)...)

		// Place a new Active token at the boundary's outgoing flow target, keeping
		// the host token's scope so boundary-routed tokens stay in the same scope.
		s.placeTokenInScope(flowTarget, hostScopeID, at)
	} else {
		// Non-interrupting: leave host parked, spawn an additional token. The arm
		// STAYS armed so it can fire again on the next delivery — BPMN
		// non-interrupting is repeatable (ADR-0124). The arm is retired only when
		// the host token completes/advances (removeBoundaryArmsForHost) or the
		// instance ends (terminal sweep). Keeping the arm also lets a recurring
		// timer boundary's job be cancelled at host end (the arm still holds its
		// TimerID), fixing a latent gocron-job leak.

		// Spawn a new Active token at the boundary's outgoing flow target, keeping
		// the host token's scope.
		s.placeTokenInScope(flowTarget, hostScopeID, at)
	}

	// Drive forward (the newly placed token(s)).
	driveCmds, err := drive(def, s, at, mode, eval)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}
