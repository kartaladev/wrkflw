package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// propagateError propagates a thrown errorCode to the nearest matching boundary error handler (BPMN-style error propagation).
//
// It performs two checks in order, stopping at the first match:
//
//  1. Direct-attachment check (only when originatingNodeID != ""):
//     Inspect the token's OWN scope definition (defForScope(top, s, scopeID)) for
//     a KindBoundaryEvent error event with AttachedTo == originatingNodeID and
//     (ErrorCode == errorCode || ErrorCode == ""). This covers the case where a
//     boundary error event is attached directly to the failing activity itself —
//     e.g. a root-level ServiceTask "svc" with a KindBoundaryEvent{AttachedTo:"svc"}
//     in the root definition. When found: consume ONLY the originating activity's
//     token (already done by the caller), cancel its arms, and route a token to the
//     boundary's outgoing flow TARGET in the SAME scope (scopeID). The scope is NOT
//     closed — only the individual activity's token is consumed (interrupting
//     boundary on a single node, contrast with enclosing-scope case below).
//
//  2. Enclosing-scope walk (unchanged from original behavior):
//     Walk outward from scopeID to root. At each level, inspect the scope's activity
//     node in the PARENT definition for a matching boundary error event with
//     AttachedTo == scope.NodeID (the sub-process that owns the scope). When found:
//     cancel ALL tokens in currentScopeID, close the scope, and route a token to the
//     boundary's outgoing flow in the PARENT scope. This is the interrupting-boundary
//     behavior for a sub-process error escape.
//
// Matching rule for a KindBoundaryEvent node bnd (both checks):
//   - bnd.AttachedTo == <activity-node-id>
//   - bnd.ErrorCode == errorCode (specific-code match) OR bnd.ErrorCode == "" (catch-all)
//   - No timer/signal/message fields set (it is an error boundary, not a timer/signal boundary)
//
// When NO handler is found (neither direct nor enclosing):
//   - Set s.Status = StatusFailed, s.EndedAt = &at.
//   - Emit FailInstance{Err: errorCode}.
//   - Emit CancelTimer for all outstanding timers, armed events, and boundaries.
//
// originatingNodeID should be set to the failing activity's NodeID (tok.NodeID) when
// called from ActionFailed. For KindErrorEndEvent, pass "" (an error end event is not
// an activity with a direct-attaching boundary).
//
// failingTokenID is the ID of the specific token that failed. When originatingNodeID
// is non-empty (ActionFailed path), the direct-attachment branch consumes THIS token
// by ID rather than by NodeID+ScopeID. This is correct when two active tokens occupy
// the same node in the same scope (e.g. in a parallel/loop topology) — consuming by
// ID ensures only the exact failing token is removed. For the KindErrorEndEvent path
// (originatingNodeID == ""), the error-end token is already consumed by drive before
// propagateError is called, so failingTokenID is unused.
// raiseIncidentOnUnhandled controls the no-handler fallback: when true, an
// unhandled error parks the failing token as a [TokenIncident] and keeps the
// instance running (admin-resumable) instead of setting StatusFailed.
func propagateError(top *model.ProcessDefinition, s *InstanceState, scopeID, originatingNodeID, failingTokenID, errorCode string, at time.Time, mode StepMode, eval ConditionEvaluator, raiseIncidentOnUnhandled bool) ([]Command, error) {
	// ── Step 1: Direct-attachment check ──────────────────────────────────────
	// Only when the caller provides an originating node (ActionFailed path).
	// Inspect the failing token's OWN scope definition for a boundary error event
	// with AttachedTo == originatingNodeID. If found, consume only the failing
	// activity's token and route a recovery token in the SAME scope.
	if originatingNodeID != "" {
		ownDef, err := defForScope(top, s, scopeID)
		if err != nil {
			return nil, fmt.Errorf("workflow-engine: propagateError: resolving own scope def for direct-attachment check: %w", err)
		}

		var directHandler *event.BoundaryEvent
		for _, raw := range ownDef.Nodes {
			n, isBnd := raw.(event.BoundaryEvent)
			if !isBnd {
				continue
			}
			if n.AttachedTo != originatingNodeID {
				continue
			}
			// Must be an error boundary (no timer/signal/message fields).
			if n.TimerDuration != "" || n.SignalName != "" || n.MessageName != "" {
				continue
			}
			// Match: catch-all or specific code.
			if n.ErrorCode == "" || n.ErrorCode == errorCode {
				bnd := n
				directHandler = &bnd
				break
			}
		}

		if directHandler != nil {
			// Direct boundary found. The failing activity's token was already cleaned
			// up by the ActionFailed handler (preCmds cancelled its arms; the token
			// itself is still present but parked — we need to consume it now).
			var cmds []Command

			// Consume the failing activity's token by its specific ID (failingTokenID).
			// Using the ID rather than NodeID+ScopeID ensures correctness when two
			// active tokens share the same node in the same scope (e.g. a parallel or
			// loop topology) — we remove only the exact failing token, not the first
			// one found by position.
			var failingTok *Token
			if failingTokenID != "" {
				failingTok = s.tokenByID(failingTokenID)
			}
			if failingTok == nil {
				// Fallback: locate by NodeID+ScopeID (defensive; should not occur when
				// the caller passes a valid failingTokenID).
				for i := range s.Tokens {
					if s.Tokens[i].NodeID == originatingNodeID && s.Tokens[i].ScopeID == scopeID {
						failingTok = &s.Tokens[i]
						break
					}
				}
			}
			if failingTok != nil {
				s.consumeToken(failingTok, at)
			}

			// Find the boundary's outgoing flow target in the own scope definition.
			outs := ownDef.Outgoing(directHandler.ID())
			if len(outs) == 0 {
				return cmds, fmt.Errorf("workflow-engine: propagateError: direct boundary %q has no outgoing flow", directHandler.ID())
			}
			flowTarget := outs[0].Target

			// Place a recovery token in the SAME scope (the failing token's scope).
			// This is the key distinction from the enclosing-scope case: only the
			// failing activity's token is consumed; the scope itself stays open.
			s.placeTokenInScope(flowTarget, scopeID, at)

			// Drive forward from the recovery token.
			driveCmds, err := drive(top, s, at, mode, eval)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, driveCmds...)
			return cmds, nil
		}
	}

	// ── Step 2: Enclosing-scope walk ─────────────────────────────────────────
	// Walk the scope chain from scopeID outward to root ("").
	// At each step, inspect the scope's activity node in the PARENT definition
	// for a matching boundary error event. The loop terminates when currentScopeID
	// reaches "" (root — no scope to inspect) or when a handler is found (early return).
	for currentScopeID := scopeID; currentScopeID != ""; {
		scope := s.scopeByID(currentScopeID)
		if scope == nil {
			// Scope is already closed (defensive). Stop walking.
			break
		}

		parentScopeID := scope.ParentID
		activityNodeID := scope.NodeID // the sub-process activity in the parent def

		// Resolve the parent definition.
		parentDef, err := defForScope(top, s, parentScopeID)
		if err != nil {
			return nil, fmt.Errorf("workflow-engine: propagateError: resolving parent def for scope %q: %w", currentScopeID, err)
		}

		// Scan the parent def for a boundary error event attached to activityNodeID
		// that matches errorCode (specific or catch-all).
		var handler *event.BoundaryEvent
		for _, raw := range parentDef.Nodes {
			n, isBnd := raw.(event.BoundaryEvent)
			if !isBnd {
				continue
			}
			if n.AttachedTo != activityNodeID {
				continue
			}
			// Only boundary error events have a non-zero ErrorCode or catch-all
			// behavior. We identify a "boundary error event" as a KindBoundaryEvent
			// that has NO TimerDuration and NO SignalName and NO MessageName set
			// (i.e. it is not a timer/signal/message boundary but an error boundary).
			// The presence of ErrorCode (specific or empty catch-all) is the marker.
			//
			// Design note: we check !n.SignalName && !n.TimerDuration && !n.MessageName
			// so timer/signal boundary events on the same host are skipped. A boundary
			// event with no trigger fields at all defaults to error boundary semantics.
			if n.TimerDuration != "" || n.SignalName != "" || n.MessageName != "" {
				continue // not an error boundary
			}
			// Match: catch-all (n.ErrorCode=="") or specific code match.
			if n.ErrorCode == "" || n.ErrorCode == errorCode {
				bnd := n
				handler = &bnd
				break
			}
		}

		if handler != nil {
			// Handler found in the PARENT scope. Cancel all tokens in currentScopeID,
			// close the scope, then route a token along the boundary's outgoing flow
			// in the parent scope.

			// 1. Cancel all tokens in the erroring scope.
			var cmds []Command
			tokensToCancel := make([]Token, 0, len(s.Tokens))
			for _, tok := range s.Tokens {
				if tok.ScopeID == currentScopeID {
					tokensToCancel = append(tokensToCancel, tok)
				}
			}
			for _, tok := range tokensToCancel {
				// Cancel deadline/reminder timers (UserTask case).
				for _, timerID := range s.cancelTimersByTaskToken(tok.AwaitCommand, "") {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
				// Cancel boundary arms for this host token.
				for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
				// Cancel any event-gateway arms.
				if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
					for _, timerID := range s.removeArmedEventsForGateway(tok.ID) {
						cmds = append(cmds, CancelTimer{TimerID: timerID})
					}
				}
				tokPtr := s.tokenByID(tok.ID)
				if tokPtr != nil {
					s.consumeToken(tokPtr, at)
				}
			}
			// Cancel ESP arms for the scope.
			for _, timerID := range s.removeEventSubprocessArmsForScope(currentScopeID) {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}

			// 2. Close the erroring scope.
			s.closeScope(currentScopeID)

			// 3. Find the boundary's outgoing flow target in the parent definition.
			outs := parentDef.Outgoing(handler.ID())
			if len(outs) == 0 {
				return cmds, fmt.Errorf("workflow-engine: propagateError: boundary error %q has no outgoing flow", handler.ID())
			}
			flowTarget := outs[0].Target

			// 4. Place a token on the recovery path in the parent scope.
			s.placeTokenInScope(flowTarget, parentScopeID, at)

			// 5. Drive forward from the recovery token.
			driveCmds, err := drive(top, s, at, mode, eval)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, driveCmds...)
			return cmds, nil
		}

		// No handler at this scope level — walk up to the parent.
		currentScopeID = parentScopeID
	}

	// No handler found anywhere in the scope chain → unhandled error.
	if raiseIncidentOnUnhandled {
		// Do NOT fail the instance. Raise an incident on the failing token and
		// keep the instance running (admin-resumable). Used by the retry-
		// exhaustion path when an effective policy exists but neither a catch
		// flow nor a boundary handled the terminal failure.
		failingTok := s.tokenByID(failingTokenID)
		attempts, cmdID := 1, ""
		if failingTok != nil {
			// Attempts is the total executions: the initial attempt plus all
			// retries (RetryAttempts counts retries only).
			attempts = failingTok.RetryAttempts + 1
			cmdID = failingTok.AwaitCommand
			failingTok.State = TokenIncident
		}
		s.Incidents = append(s.Incidents, Incident{
			ID:        s.nextIncidentID(),
			TokenID:   failingTokenID,
			NodeID:    originatingNodeID,
			ScopeID:   scopeID,
			CommandID: cmdID,
			Error:     errorCode,
			Attempts:  attempts,
			CreatedAt: at,
		})
		return nil, nil
	}

	// Terminal unhandled error: run compensation walk before terminating (ADR-0034).
	// Check both RootCompensations and ArchivedCompensations (ADR-0039) — consolidation
	// happens inside beginCompensation.
	if len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0 {
		s.Status = StatusCompensating
		res, err := beginCompensation(top, s, "", StatusFailed, errorCode, at, mode, eval)
		if err != nil {
			return nil, err
		}
		return res.Commands, nil
	}

	// No compensation records: immediate failure (unchanged behaviour).
	s.Status = StatusFailed
	ended := at
	s.EndedAt = &ended
	var cmds []Command
	// Reconcile the human-task projection: a parallel branch parked at a UserTask
	// must not be left open in the TaskStore when the instance fails (ADR-0089).
	cmds = append(cmds, s.cancelOpenTasks()...)
	cmds = append(cmds, FailInstance{Err: errorCode})
	cmds = append(cmds, s.cancelAllTimers()...)
	cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
	return cmds, nil
}
