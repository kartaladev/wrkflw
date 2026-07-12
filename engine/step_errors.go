package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// boundaryErrorMatches decides whether error boundary n catches a thrown error.
//
// Precedence (highest to lowest):
//
//  1. ErrorCheck — Go closure (vars, cause) → bool. Highest; non-serializable.
//     When set, its return value is final: true = catch, false = no-catch
//     (does NOT fall through to Expr or Code on false).
//     vars is a SHALLOW CLONE so a misbehaving closure cannot mutate the live
//     instance variable map.
//  2. ErrorExpr — expr-lang predicate evaluated over vars + injected "_error"
//     (the thrown error code string). Truthy = catch. Serializable.
//     _error is injected into a CLONE of vars so it never leaks into instance state.
//     A runtime eval error (e.g. type mismatch) is returned to the caller so it
//     can decide whether to skip or abort (see propagateError for the skip/abort policy).
//  3. ErrorCode — exact match or catch-all: n.ErrorCode == "" || n.ErrorCode == errorCode.
//
// cause is the live thrown error: the original action error when available, or
// a synthesized errors.New(errorCode) for bare-code sources (an error-behavior
// end event, sub-instance failure). Callers guarantee cause is non-nil by the
// time boundaryErrorMatches is called.
func boundaryErrorMatches(n event.BoundaryEvent, vars map[string]any, cause error, errorCode string, eval ConditionEvaluator) (bool, error) {
	if n.ErrorCheck != nil {
		// Pass a shallow clone so a misbehaving closure cannot mutate the live
		// instance variable map (mutation trap prevention).
		cloned := make(map[string]any, len(vars))
		for k, v := range vars {
			cloned[k] = v
		}
		return n.ErrorCheck(cloned, cause), nil
	}
	if n.ErrorExpr != "" {
		// Clone vars + inject _error so the evaluator sees the code without
		// mutating the live instance variable map.
		env := make(map[string]any, len(vars)+1)
		for k, v := range vars {
			env[k] = v
		}
		env["_error"] = errorCode
		return eval.EvalBool(n.ErrorExpr, env)
	}
	return n.ErrorCode == "" || n.ErrorCode == errorCode, nil
}

// isErrorBoundary reports whether a KindBoundaryEvent node n represents a BPMN
// error boundary event, as opposed to a timer/signal/message boundary. An error
// boundary has none of the timer/signal/message trigger fields set; the presence
// of ErrorCode (specific or empty catch-all) is what actually selects it, but any
// trigger field being set means the node belongs to a different boundary flavor
// and must be skipped during error-boundary scans.
func isErrorBoundary(n event.BoundaryEvent) bool {
	return n.Timer.IsZero() && n.SignalName == "" && n.MessageName == ""
}

// findDirectBoundary scans hostDef for a KindBoundaryEvent error boundary attached
// to hostNodeID (bnd.AttachedTo == hostNodeID) that matches errorCode against vars
// and cause, using the three-tier boundaryErrorMatches precedence (Check → Expr →
// Code). Returns the matched boundary and true when found.
//
// A malformed ErrorExpr (a runtime eval error) is treated as non-match for that
// candidate — this is the error-recovery path, and one malformed predicate must
// not brick routing for all boundaries — so scanning continues to the next node.
//
// hostNodeID/hostDef are deliberately generic (not "the failing token's node"): a
// call-activity host and its own containing definition satisfy the same shape,
// which is what lets findDirectBoundary be reused outside a token-failure context.
func findDirectBoundary(hostDef *model.ProcessDefinition, hostNodeID, errorCode string, vars map[string]any, cause error, eval ConditionEvaluator) (event.BoundaryEvent, bool) {
	for _, raw := range hostDef.Nodes {
		n, isBnd := raw.(event.BoundaryEvent)
		if !isBnd || n.AttachedTo != hostNodeID || !isErrorBoundary(n) {
			continue
		}
		matched, matchErr := boundaryErrorMatches(n, vars, cause, errorCode, eval)
		if matchErr != nil {
			// Treat as non-match; continue scanning remaining boundaries.
			continue
		}
		if matched {
			return n, true
		}
	}
	return event.BoundaryEvent{}, false
}

// findEnclosingBoundary walks the scope chain from scopeID outward to root (""),
// looking for a matching error boundary attached to each ancestor scope's owning
// activity node in that ancestor's PARENT definition (scope.NodeID is the
// sub-process activity; the boundary itself is defined alongside it, one level up).
//
// Returns, when found: the matched boundary, errScopeID (the scope whose tokens
// must be cancelled — the level where the walk stopped), targetScopeID (the parent
// scope to route the recovery token into), and lookupDef (the parent definition,
// for resolving the boundary's outgoing flow). found is false, err is nil when no
// ancestor scope has a matching boundary (walk exhausted to root). err is non-nil
// only for a definition-resolution failure, mirroring the original inline walk's
// error contract (no partial commands — the caller has not started building any).
func findEnclosingBoundary(top *model.ProcessDefinition, s *InstanceState, scopeID, errorCode string, cause error, eval ConditionEvaluator) (boundary event.BoundaryEvent, errScopeID, targetScopeID string, lookupDef *model.ProcessDefinition, found bool, err error) {
	for currentScopeID := scopeID; currentScopeID != ""; {
		scope := s.scopeByID(currentScopeID)
		if scope == nil {
			// Scope is already closed (defensive). Stop walking.
			break
		}

		parentScopeID := scope.ParentID
		activityNodeID := scope.NodeID // the sub-process activity in the parent def

		parentDef, defErr := defForScope(top, s, parentScopeID)
		if defErr != nil {
			return event.BoundaryEvent{}, "", "", nil, false, fmt.Errorf("workflow-engine: propagateError: resolving parent def for scope %q: %w", currentScopeID, defErr)
		}

		if handler, ok := findDirectBoundary(parentDef, activityNodeID, errorCode, s.Variables, cause, eval); ok {
			return handler, currentScopeID, parentScopeID, parentDef, true, nil
		}

		currentScopeID = parentScopeID
	}
	return event.BoundaryEvent{}, "", "", nil, false, nil
}

// routeToBoundary is the shared route tail for a matched error boundary: fire its
// fire-once action, run the caller-supplied consume step (phase-specific token
// cleanup — a single failing token by ID, or a whole scope's tokens plus scope
// closure), resolve the boundary's outgoing flow within lookupDef, place a
// recovery token in targetScopeID, and drive forward.
//
// Command order matches the original inline implementations exactly: [fire-once
// action, consume's commands, drive's commands]. On any error, the commands
// accumulated so far are returned alongside the error (partial-command contract).
//
// kind labels the "no outgoing flow" error ("direct boundary" vs "boundary error")
// to preserve the exact original error text for each call site.
//
// lookupDef/boundary/targetScopeID/consume are deliberately generic (not
// hard-wired to "the failing token's" scope): a call-activity host routes into its
// OWN parent scope via the same shape, which is what lets routeToBoundary be
// reused outside a token-failure context.
func routeToBoundary(top *model.ProcessDefinition, s *InstanceState, lookupDef *model.ProcessDefinition, boundary event.BoundaryEvent, kind, targetScopeID string, at time.Time, mode StepMode, eval ConditionEvaluator, consume func([]Command) []Command) ([]Command, error) {
	cmds := emitFireOnceAction(s, boundary.Action)
	cmds = consume(cmds)

	outs := lookupDef.Outgoing(boundary.ID())
	if len(outs) == 0 {
		return cmds, fmt.Errorf("workflow-engine: propagateError: %s %q has no outgoing flow", kind, boundary.ID())
	}
	flowTarget := outs[0].Target

	s.placeTokenInScope(flowTarget, targetScopeID, at)

	driveCmds, err := drive(top, s, at, mode, eval)
	if err != nil {
		return cmds, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}

// unhandledErrorPolicy is propagateError's (and handleUnhandledError's)
// no-handler fallback policy: whether an error with no matching boundary
// handler fails the instance immediately or is parked as an admin-resumable
// incident. Named to make call sites self-documenting in place of a bare
// trailing bool (e.g. propagateError(..., raiseIncident) vs (..., failFast)).
type unhandledErrorPolicy bool

const (
	// failFast is the default no-handler outcome: StatusFailed via
	// FailInstance (after an ADR-0034 compensation walk if records exist).
	failFast unhandledErrorPolicy = false
	// raiseIncident parks the failing token as a [TokenIncident] and keeps
	// the instance running (admin-resumable) instead of failing it. Used by
	// the retry-exhaustion path when an effective policy exists but neither a
	// catch flow nor a boundary handled the terminal failure.
	raiseIncident unhandledErrorPolicy = true
)

// handleUnhandledError is the no-handler fallback for propagateError: neither a
// direct-attachment boundary nor an enclosing-scope boundary matched errorCode.
// Precedence:
//
//  1. policy == raiseIncident: park the failing token as a [TokenIncident] and
//     keep the instance running (admin-resumable), instead of failing it. Used by
//     the retry-exhaustion path when an effective policy exists but neither a
//     catch flow nor a boundary handled the terminal failure.
//  2. Compensation records exist (RootCompensations or ArchivedCompensations,
//     ADR-0039): run the compensation walk before terminating (ADR-0034).
//  3. Otherwise: immediate s.Status = StatusFailed, cancel open tasks/timers/arms,
//     and emit FailInstance{Err: errorCode}.
func handleUnhandledError(top *model.ProcessDefinition, s *InstanceState, scopeID, originatingNodeID, failingTokenID, errorCode string, at time.Time, mode StepMode, eval ConditionEvaluator, policy unhandledErrorPolicy) ([]Command, error) {
	if policy == raiseIncident {
		// Do NOT fail the instance. Raise an incident on the failing token and
		// keep the instance running (admin-resumable).
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
		res, err := beginCompensation(top, s, at, mode, eval, compensationOutcome{FinalStatus: StatusFailed, FinalErr: errorCode})
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
	for _, timerID := range s.removeAllEventTriggeredSubprocessArms() {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	return cmds, nil
}

// propagateError propagates a thrown errorCode to the nearest matching boundary error handler (BPMN-style error propagation).
//
// It performs two checks in order, stopping at the first match:
//
//  1. Direct-attachment check (only when originatingNodeID != ""), via
//     findDirectBoundary: Inspect the token's OWN scope definition
//     (defForScope(top, s, scopeID)) for a KindBoundaryEvent error event with
//     AttachedTo == originatingNodeID and (ErrorCode == errorCode || ErrorCode ==
//     ""). This covers the case where a boundary error event is attached directly
//     to the failing activity itself — e.g. a root-level ServiceTask "svc" with a
//     KindBoundaryEvent{AttachedTo:"svc"} in the root definition. When found:
//     consume ONLY the originating activity's token (already done by the caller),
//     cancel its arms, and route a token to the boundary's outgoing flow TARGET in
//     the SAME scope (scopeID). The scope is NOT closed — only the individual
//     activity's token is consumed (interrupting boundary on a single node,
//     contrast with enclosing-scope case below).
//
//  2. Enclosing-scope walk (unchanged from original behavior), via
//     findEnclosingBoundary: Walk outward from scopeID to root. At each level,
//     inspect the scope's activity node in the PARENT definition for a matching
//     boundary error event with AttachedTo == scope.NodeID (the sub-process that
//     owns the scope). When found: cancel ALL tokens in currentScopeID, close the
//     scope, and route a token to the boundary's outgoing flow in the PARENT
//     scope. This is the interrupting-boundary behavior for a sub-process error
//     escape.
//
// Both checks share the routeToBoundary tail (fire-once action, outgoing-flow
// resolve, placeToken, drive) and the isErrorBoundary marker predicate.
//
// Matching rule for a KindBoundaryEvent node bnd (both checks):
//   - bnd.AttachedTo == <activity-node-id>
//   - bnd.ErrorCode == errorCode (specific-code match) OR bnd.ErrorCode == "" (catch-all)
//   - No timer/signal/message fields set (it is an error boundary, not a timer/signal boundary)
//
// When NO handler is found (neither direct nor enclosing), see
// handleUnhandledError:
//   - Set s.Status = StatusFailed, s.EndedAt = &at.
//   - Emit FailInstance{Err: errorCode}.
//   - Emit CancelTimer for all outstanding timers, armed events, and boundaries.
//
// originatingNodeID should be set to the failing activity's NodeID (tok.NodeID) when
// called from ActionFailed. For an error-behavior end event, pass "" (an error end
// event is not an activity with a direct-attaching boundary).
//
// failingTokenID is the ID of the specific token that failed. When originatingNodeID
// is non-empty (ActionFailed path), the direct-attachment branch consumes THIS token
// by ID rather than by NodeID+ScopeID. This is correct when two active tokens occupy
// the same node in the same scope (e.g. in a parallel/loop topology) — consuming by
// ID ensures only the exact failing token is removed. For the error-behavior end
// event path (originatingNodeID == ""), the error-end token is already consumed by
// drive before propagateError is called, so failingTokenID is unused.
// policy controls the no-handler fallback (see unhandledErrorPolicy): when
// raiseIncident, an unhandled error parks the failing token as a
// [TokenIncident] and keeps the instance running (admin-resumable) instead of
// setting StatusFailed.
//
// cause is the original Go error from the live action invocation; pass nil for
// bare-code sources (an error-behavior end event, sub-instance failures). When
// nil, a synthesized errors.New(errorCode) is created so ErrorCheck closures
// always receive a non-nil error.
func propagateError(top *model.ProcessDefinition, s *InstanceState, scopeID, originatingNodeID, failingTokenID, errorCode string, cause error, at time.Time, mode StepMode, eval ConditionEvaluator, policy unhandledErrorPolicy) ([]Command, error) {
	// Guarantee that ErrorCheck closures always receive a non-nil error.
	// For bare-code sources (an error-behavior end event, sub-instance) the
	// caller passes nil; synthesize errors.New(errorCode) so the closure can
	// inspect the code via err.Error() without requiring a nil-check.
	if cause == nil {
		cause = errors.New(errorCode)
	}

	// ── Step 1: Direct-attachment check ──────────────────────────────────────
	// Only when the caller provides an originating node (ActionFailed path).
	if originatingNodeID != "" {
		ownDef, err := defForScope(top, s, scopeID)
		if err != nil {
			return nil, fmt.Errorf("workflow-engine: propagateError: resolving own scope def for direct-attachment check: %w", err)
		}

		if handler, ok := findDirectBoundary(ownDef, originatingNodeID, errorCode, s.Variables, cause, eval); ok {
			// Direct boundary found. The failing activity's token was already cleaned
			// up by the ActionFailed handler (preCmds cancelled its arms; the token
			// itself is still present but parked — we need to consume it now).
			consume := func(cmds []Command) []Command {
				// Consume the failing activity's token by its specific ID
				// (failingTokenID). Using the ID rather than NodeID+ScopeID
				// ensures correctness when two active tokens share the same
				// node in the same scope (e.g. a parallel or loop topology) —
				// we remove only the exact failing token, not the first one
				// found by position.
				var failingTok *Token
				if failingTokenID != "" {
					failingTok = s.tokenByID(failingTokenID)
				}
				if failingTok == nil {
					// Fallback: locate by NodeID+ScopeID (defensive; should not
					// occur when the caller passes a valid failingTokenID).
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
				return cmds
			}
			// Route in the SAME scope: only the failing activity's token is
			// consumed; the scope itself stays open.
			return routeToBoundary(top, s, ownDef, handler, "direct boundary", scopeID, at, mode, eval, consume)
		}
	}

	// ── Step 2: Enclosing-scope walk ─────────────────────────────────────────
	handler, errScopeID, targetScopeID, lookupDef, found, err := findEnclosingBoundary(top, s, scopeID, errorCode, cause, eval)
	if err != nil {
		return nil, err
	}
	if found {
		consume := func(cmds []Command) []Command {
			// Cancel all tokens in the erroring scope, then close it.
			tokensToCancel := make([]Token, 0, len(s.Tokens))
			for _, tok := range s.Tokens {
				if tok.ScopeID == errScopeID {
					tokensToCancel = append(tokensToCancel, tok)
				}
			}
			for _, tok := range tokensToCancel {
				// Cancel deadline/reminder timers, in-wait reminder, boundary arms,
				// and (for an event-based-gateway token) armed events, then consume
				// the token.
				cmds = append(cmds, cancelTokenWaits(s, &tok, at)...)
			}
			// Cancel ESP arms for the scope.
			for _, timerID := range s.removeEventTriggeredSubprocessArmsForScope(errScopeID) {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}
			s.closeScope(errScopeID)
			return cmds
		}
		// Route in the PARENT scope: the erroring scope is fully torn down.
		return routeToBoundary(top, s, lookupDef, handler, "boundary error", targetScopeID, at, mode, eval, consume)
	}

	// No handler found anywhere in the scope chain → unhandled error.
	return handleUnhandledError(top, s, scopeID, originatingNodeID, failingTokenID, errorCode, at, mode, eval, policy)
}
