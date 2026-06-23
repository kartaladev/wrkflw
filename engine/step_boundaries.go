package engine

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/model"
)

// armBoundaries finds all KindBoundaryEvent nodes with AttachedTo == hostNode,
// records a boundaryArm for each, and returns ScheduleTimer commands for timer
// boundaries. Called from the ServiceTask and UserTask park points in drive.
//
// Definition-scan order is deterministic (Nodes slice order); boundary arms are
// appended in the same order so s.Boundaries is deterministic.
//
// A bad TimerDuration expression is returned as a wrapped error — consistent with
// the intermediate-timer and SLA paths — so callers can fail fast rather than
// silently no-arming the boundary.
func armBoundaries(def *model.ProcessDefinition, s *InstanceState, hostTokenID, hostNode string, at time.Time) ([]Command, error) {
	var cmds []Command
	for _, raw := range def.Nodes {
		n, ok := raw.(model.BoundaryEvent)
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
		}

		if n.TimerDuration != "" {
			dur, err := conditions.EvalDuration(n.TimerDuration, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("workflow-engine: boundary %q on %q: %w", n.ID(), hostNode, err)
			}
			timerID := s.nextTimerID()
			arm.TimerID = timerID
			cmds = append(cmds, ScheduleTimer{
				TimerID: timerID,
				Token:   hostTokenID,
				FireAt:  at.Add(dur),
				Kind:    TimerIntermediate,
			})
		} else if n.SignalName != "" {
			arm.Signal = n.SignalName
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
//  2. Cancel any SLA/reminder timers on the host (UserTask) via cancelTimersByTaskToken
//     using the host's taskToken (AwaitCommand == taskToken for UserTask hosts).
//  3. Consume the host token (close its visit, remove from slice).
//  4. Remove ALL boundary arms for this host (emit CancelTimer for timer siblings).
//  5. Place a new Active token at the boundary's outgoing flow target.
//  6. Drive forward.
//
// For non-interrupting boundaries (ba.NonInterrupting):
//  1. Verify the host token is still parked. If not, no-op.
//  2. Leave the host parked.
//  3. Remove ONLY this boundary arm (fired once; do not re-arm — repeating out of scope).
//  4. Place an additional Active token at the boundary's outgoing flow target.
//  5. Drive forward (the new token).
func fireBoundaryArm(def *model.ProcessDefinition, s *InstanceState, ba boundaryArm, at time.Time, mode StepMode) ([]Command, error) {
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

	if !ba.NonInterrupting {
		// Interrupting: consume the host, cancel its task timers and boundary siblings.

		// Cancel SLA/reminder timers for the host (UserTask case: AwaitCommand == taskToken).
		// For a ServiceTask host, AwaitCommand is a cmdID (not a taskToken), so
		// cancelTimersByTaskToken will find no records — which is correct.
		hostTaskToken := hostTok.AwaitCommand
		for _, timerID := range s.cancelTimersByTaskToken(hostTaskToken, "") {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}

		// Consume the host token (close its visit, remove from slice).
		s.consumeToken(hostTok, at)

		// Remove ALL boundary arms for this host and emit CancelTimer for timer siblings.
		// The fired arm's timerID (if any) is included; it already fired so the
		// runtime's cancel is idempotent — no special handling needed.
		for _, timerID := range s.removeBoundaryArmsForHost(ba.HostToken) {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}

		// Place a new Active token at the boundary's outgoing flow target, keeping
		// the host token's scope so boundary-routed tokens stay in the same scope.
		s.placeTokenInScope(flowTarget, hostScopeID, at)
	} else {
		// Non-interrupting: leave host parked, spawn an additional token.

		// Remove only THIS boundary arm (it fired once; no re-arm in scope).
		s.removeBoundaryArm(ba.HostToken, ba.BoundaryNode)

		// Spawn a new Active token at the boundary's outgoing flow target, keeping
		// the host token's scope.
		s.placeTokenInScope(flowTarget, hostScopeID, at)
	}

	// Drive forward (the newly placed token(s)).
	driveCmds, err := drive(def, s, at, mode)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}
