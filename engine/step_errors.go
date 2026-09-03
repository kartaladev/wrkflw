package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
func findDirectBoundary(ctx context.Context, hostDef *model.ProcessDefinition, hostNodeID, errorCode string, vars map[string]any, cause error, eval ConditionEvaluator) (event.BoundaryEvent, bool) {
	for _, raw := range hostDef.Nodes {
		n, isBnd := raw.(event.BoundaryEvent)
		if !isBnd || n.AttachedTo != hostNodeID || !isErrorBoundary(n) {
			continue
		}
		matched, matchErr := boundaryErrorMatches(n, vars, cause, errorCode, eval)
		if matchErr != nil {
			// Treat as non-match; continue scanning remaining boundaries.
			slog.DebugContext(ctx, "boundary ErrorExpr eval error",
				"host_node_id", hostNodeID,
				"boundary_node_id", n.ID(),
				"error_code", errorCode,
				"error", matchErr,
			)
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
func findEnclosingBoundary(ctx context.Context, top *model.ProcessDefinition, s *InstanceState, scopeID, errorCode string, cause error, eval ConditionEvaluator) (boundary event.BoundaryEvent, errScopeID, targetScopeID string, lookupDef *model.ProcessDefinition, found bool, err error) {
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

		if handler, ok := findDirectBoundary(ctx, parentDef, activityNodeID, errorCode, s.Variables, cause, eval); ok {
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
func routeToBoundary(ctx context.Context, top *model.ProcessDefinition, s *InstanceState, lookupDef *model.ProcessDefinition, boundary event.BoundaryEvent, kind, targetScopeID string, at time.Time, pol stepPolicy, consume func([]Command) []Command) ([]Command, error) {
	cmds := emitFireOnceAction(s, boundary.Action)
	cmds = consume(cmds)

	outs := lookupDef.Outgoing(boundary.ID())
	if len(outs) == 0 {
		return cmds, fmt.Errorf("workflow-engine: propagateError: %s %q has no outgoing flow", kind, boundary.ID())
	}
	flowTarget := outs[0].Target

	s.placeTokenInScope(flowTarget, targetScopeID, at)

	driveCmds, err := drive(ctx, top, s, at, pol)
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
	// FailInstance (after a compensation walk if records exist).
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
//  2. A compensation walk is ALREADY in flight: it is neither restarted nor
//     terminated around — the error is recorded as its pending terminal outcome
//     (see deferFailureToInFlightCompensationWalk). Checked BEFORE the
//     record test below, because a walk over a sub-process scope's records leaves
//     both record lists empty.
//  3. Compensation records exist (RootCompensations or ArchivedCompensations):
//     run the compensation walk before terminating.
//  4. Otherwise: immediate s.Status = StatusFailed, cancel open tasks/timers/arms,
//     and emit FailInstance{Err: errorCode}.
func handleUnhandledError(ctx context.Context, top *model.ProcessDefinition, s *InstanceState, scopeID, originatingNodeID, failingTokenID, errorCode string, at time.Time, pol stepPolicy, policy unhandledErrorPolicy) ([]Command, error) {
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

	// A compensation walk already in flight IS the rollback: never start a second
	// one, and never terminate around it. Checked BEFORE the
	// compensation-records test below, mirroring handleCancelRequested's own
	// in-flight guard, which is likewise unconditional: a walk over a sub-process
	// scope's records leaves both RootCompensations and ArchivedCompensations
	// empty, so a nested guard falls through to endInstance and abandons the live
	// walk mid-flight — measured status=failed with the cursor cleared and the
	// dispatched compensation action orphaned.
	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
		return deferFailureToInFlightCompensationWalk(s, at, errorCode), nil
	}

	// The instance is dying, so harvest what its open scopes still hold BEFORE asking
	// whether there is anything to compensate. Without this the predicate below cannot
	// see a record for an activity that completed inside a still-open sub-process — it
	// lives in Scope.Compensations, which reaches the archive only on a NORMAL scope
	// exit — so the walk was skipped and the record was then unreachable forever.
	// Measured on `main`: a sub-process holding `undo-inner` failed with
	// FailInstance and no InvokeAction at all.
	//
	// Placed AFTER the in-flight-walk guard above, which must keep winning: a walk
	// already in flight IS the rollback.
	s.harvestOpenScopeCompensations()

	// Terminal unhandled error: run compensation walk before terminating.
	// Check both RootCompensations and ArchivedCompensations — consolidation
	// happens inside beginCompensation. The harvest above is what makes this predicate
	// correct for an open scope's records without changing its text.
	if len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0 {
		s.Status = StatusCompensating
		res, err := beginCompensation(ctx, top, s, at, pol, compensationOutcome{CloseKind: CloseKindErrored, FinalStatus: StatusFailed, FinalErr: errorCode})
		if err != nil {
			return nil, err
		}
		return res.Commands, nil
	}

	// No compensation records: immediate failure. endInstance reconciles the
	// human-task projection (a parallel branch parked at a UserTask must not be
	// left open in the TaskStore when the instance fails) and cancels
	// the scheduled work, in the same order as before. This branch does
	// NOT drop tokens, so the incidents whose token survives are deliberately kept.
	return s.endInstance(StatusFailed, at, FailInstance{Err: errorCode}), nil
}

// deferFailureToInFlightCompensationWalk records an unhandled error as the
// pending terminal outcome of a compensation walk that is already in flight,
// instead of starting a second walk. The caller has established both
// preconditions: s.Status == StatusCompensating and
// s.Compensating.ActiveCmdID != "".
//
// It does what beginCompensation's prologue does — cancel every remaining token's
// waits, then the leftover timers and arms — but leaves the CURSOR entirely
// untouched, so the outstanding InvokeAction keeps its awaiter and the records
// already consumed are not re-walked. A second walk would re-dispatch a
// compensation action that this repo's contract nowhere requires to be
// idempotent, and would orphan the in-flight command by renaming the cursor out
// from under it.
//
// The outcome is deferred rather than stamped on the cursor, reusing the
// PendingCancel protocol handleCancelRequested has long used for the same
// collision. Stamping the cursor was the shape this path originally shipped,
// and it converted the live walk into this error's rollback — inheriting that
// walk's own record source (its ArchiveKey, or a sub-process ScopeID). Every
// record OUTSIDE that source was then never compensated: measured, a targeted
// throw left the root record uncompensated AND erased it (a targeted cursor's
// ScopeID is empty, so once ResumeNode was cleared the finish took its walkAdmin
// branch and nil'd RootCompensations — root records 1 → 0), and a nested
// scope-wide throw stranded it. Deferring instead makes applyFinish re-enter
// beginCompensation over the REMAINDER once the walk drains, which is the whole
// point of the protocol being reused.
//
// PendingFinalStatus/PendingFinalErr are last-writer-wins: a second unhandled
// error arriving before the walk ends overwrites the first code. Deliberate,
// matching beginCompensation's own behaviour; a known imprecision.
//
// ⚠ The deferral is consumed only by applyFinish's consumePendingCancel plans —
// the walkThrowTargeted, walkThrowScopeWide and walkReverse finishes. The other
// two walks cannot be live here: walkPartial and walkAdmin are both begun by
// beginCompensation, whose prologue cancels every token (measured: tokens=0 for
// all of walkAdmin, walkPartial and walkReverse while in flight), and all three
// propagateError call sites — handleActionFailed's two and endEventStrategy's
// EndError — reach this function only from a live token. No token, no unhandled
// error, so those two modes never reach this guard.
func deferFailureToInFlightCompensationWalk(s *InstanceState, at time.Time, errorCode string) []Command {
	var cmds []Command
	// Snapshot before iterating: cancelTokenWaits consumes tokens from s.Tokens.
	tokensToCancel := make([]Token, len(s.Tokens))
	copy(tokensToCancel, s.Tokens)
	for _, tok := range tokensToCancel {
		cmds = append(cmds, cancelTokenWaits(s, &tok, at, CloseKindErrored)...)
	}
	cmds = append(cmds, s.cancelAllTimers()...)
	// cancelAllArmsAndBoundaries, NOT cancelAllScheduledWork — but not for the
	// reason that function's godoc gives. Its stated rationale ("the walk may still
	// resume the instance") is FALSE on this path: the deferral recorded below
	// makes the walk's finish terminate instead of resuming.
	//
	// The real reason is that this path is not terminating YET. The instance stays
	// StatusCompensating until the walk's own ActionCompleted arrives, and even
	// then a further walk over the remaining records may run first; only the last
	// finish applies FinalStatus through endInstance, which calls
	// cancelAllScheduledWork and sweeps the event-sub-process arms across all scopes
	// (source-verified: endInstance's last statement). Sweeping them here would
	// duplicate that and put CancelTimers on both sides of the terminal command,
	// breaking the [task cancels…, terminal, scheduled-work cancels…] ordering every
	// terminal path emits — the same constraint documented at
	// exitRootEventSubprocessScope.
	cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)

	s.PendingCancel = true
	s.PendingFinalStatus = StatusFailed
	s.PendingFinalErr = errorCode
	return cmds
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
func propagateError(ctx context.Context, top *model.ProcessDefinition, s *InstanceState, scopeID, originatingNodeID, failingTokenID, errorCode string, cause error, at time.Time, pol stepPolicy, policy unhandledErrorPolicy) ([]Command, error) {
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

		if handler, ok := findDirectBoundary(ctx, ownDef, originatingNodeID, errorCode, s.Variables, cause, pol.eval); ok {
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
					s.consumeTokenAs(failingTok, at, CloseKindBoundaryInterrupted)
				}
				return cmds
			}
			// Route in the SAME scope: only the failing activity's token is
			// consumed; the scope itself stays open.
			return routeToBoundary(ctx, top, s, ownDef, handler, "direct boundary", scopeID, at, pol, consume)
		}
	}

	// ── Step 2: Enclosing-scope walk ─────────────────────────────────────────
	handler, errScopeID, targetScopeID, lookupDef, found, err := findEnclosingBoundary(ctx, top, s, scopeID, errorCode, cause, pol.eval)
	if err != nil {
		return nil, err
	}
	if found {
		consume := func(cmds []Command) []Command {
			// Cancel every token in the erroring scope AND in all its descendant
			// scopes, retire their arms, archive their compensation records, then
			// close the whole subtree. closeScope already prunes descendants;
			// an earlier version did so while leaving their tokens
			// alive, so those tokens ended up naming a scope that no longer
			// existed and every subsequent Step failed in defForScope.
			cmds = append(cmds, cancelScopeSubtree(s, errScopeID, at, CloseKindBoundaryInterrupted)...)
			s.closeScope(errScopeID)
			return cmds
		}
		// Route in the PARENT scope: the erroring scope is fully torn down.
		return routeToBoundary(ctx, top, s, lookupDef, handler, "boundary error", targetScopeID, at, pol, consume)
	}

	// No handler found anywhere in the scope chain → unhandled error.
	return handleUnhandledError(ctx, top, s, scopeID, originatingNodeID, failingTokenID, errorCode, at, pol, policy)
}
