package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
)

// compensationRecordsForScope returns a read-only slice of CompensationRecords for
// the given scope, and whether the scope was RESOLVABLE. scopeID "" means the
// root scope (s.RootCompensations), which is implicit and always resolvable.
//
// The second result exists because the two answers are otherwise identical at
// the call site: s.Scopes holds OPEN scopes only — three sites remove an entry
// (closeScope, closeScopeDescendants, endInstance's terminal nil) — so "this
// scope is open and holds no records" and "this scope is not open at all" both
// come back as an empty slice, and a caller keying on len(records) cannot tell
// them apart.
//
// ⚠ ok is ADVISORY. It does not close a defect: all four production callers
// discard it today, and each is safe to. Do not read the signature as a fix —
// read it as making the distinction EXPRESSIBLE (and pinned, by
// step_compensation_closed_scope_test.go) for the caller that one day needs it.
// The reasons, per caller — measured 2026-08-20 and recorded in
// docs/plans/sweep-evidence/fix-review-engine-ok-return.md:
//
//   - beginCompensation passes the untyped const "" — resolvable by construction.
//   - the scope-wide compensation throw (step_nodes.go) cannot be REACHED with a
//     closed scope: drive() resolves defForScope(def, s, tok.ScopeID) for every
//     active token before dispatching to any strategy, and that returns
//     `defForScope: unknown scope %q` for a scope absent from s.Scopes, so the
//     Step fails outright. Executed: the error, not an auto-advance.
//   - cursorRecords and retainedRecordPrefix are NOT gated by that call — they
//     take their scope id from the compensation CURSOR, not from a token — but
//     honouring ok there could not change an OUTCOME. Both answers hand back an
//     empty slice, and the four consumers of an empty cursorRecords result
//     (stepCompensationAdvance's bounds disjunct, retryStalledCompensation,
//     retryFailedCompensation, step_triggers.go's failedNodeID lookup) route to
//     the walk's FINISH or to an empty node id either way — which is what
//     ADR-0171 prescribes for a vanished source and is trivially right for an
//     open empty one. Executed: an unpinned cursor over an open-empty scope and
//     over an absent scope produce DeepEqual StepResults (state and commands)
//     through both the advance and the retry entry points.
//   - retainedRecordPrefix retains nothing for either answer, and setScopeRecords
//     writing it back is itself a no-op for a scope that is gone.
//
// If ok is ever wanted for visibility rather than control flow — a "this walk's
// record source vanished" signal — that is backlog 133, not this signature.
func compensationRecordsForScope(s *InstanceState, scopeID string) ([]CompensationRecord, bool) {
	if scopeID == "" {
		return s.RootCompensations, true
	}
	sc := s.scopeByID(scopeID)
	if sc == nil {
		return nil, false
	}
	return sc.Compensations, true
}

// cursorRecords returns the CompensationRecord slice for the current compensation
// walk described by cur.
//
// A pinned source (cur.Records, set by startCompensationWalk — ADR-0171) wins:
// it is a snapshot taken at walk start, so the walk keeps iterating the records
// it committed to even if a sibling branch destroys the live source underneath
// it. The live reads below remain for the walks that do not pin — the
// beginCompensation family — and for a throw cursor deserialized from a row
// written before ADR-0171, whose Records is nil.
func cursorRecords(s *InstanceState, cur compensationCursor) []CompensationRecord {
	if cur.Records != nil {
		return cur.Records
	}
	if cur.ArchiveKey != "" {
		return s.ArchivedCompensations[cur.ArchiveKey]
	}
	// The cursor's scope may already have closed under a live walk; the walk
	// iterates whatever remains, which for a closed scope is nothing.
	records, _ := compensationRecordsForScope(s, cur.ScopeID)
	return records
}

// eligibleRange computes the reverse-order compensation walk's index range over
// records for a given toNode boundary. start is the most-recently-completed
// record's index — the first to emit when walking backward — always
// len(records)-1 (start-1, start-2, ... 0 in a full rollback). stopExclusive is
// the index of toNode's LAST occurrence in records (matching the most-recent
// visit, mirroring beginCompensation's rollback-target rule), or -1 when
// toNode == "" or not found in records. An index is eligible for compensation
// iff it is > stopExclusive (i.e. AFTER toNode in completion order) — the
// walk proceeds start, start-1, ..., down to but excluding stopExclusive.
//
// Both beginCompensation (computing the walk's starting index) and
// stepCompensationAdvance (deciding whether the next index is still eligible)
// encode this same rule; this is the single shared derivation.
func eligibleRange(records []CompensationRecord, toNode string) (start, stopExclusive int) {
	start = len(records) - 1
	stopExclusive = -1
	if toNode != "" {
		for i, r := range records {
			if r.NodeID == toNode {
				stopExclusive = i
			}
		}
	}
	return start, stopExclusive
}

// hasCompensationRecordsToWalk reports whether a root-scope compensation walk
// started now would find anything to compensate.
//
// It deliberately mirrors beginCompensation's OWN decision rather than
// approximating it. That function calls consolidateArchiveIntoRoot before it
// counts, folding every ArchivedCompensations entry into RootCompensations, and
// only then asks eligibleRange for a start index — which, for the empty ToNode
// this predicate's caller has already established, is simply len(records)-1. So
// "records to walk" means root records OR archived ones, and a check written as
// len(s.RootCompensations) == 0 would be wrong rather than merely conservative:
// a sub-process whose scope closed before the instance terminated leaves its
// record archived, with RootCompensations empty and a real walk still pending.
// That state is reachable and is pinned by
// TestCompensateRequestedOnTerminalInstance's archived-records row.
//
// Empty archive slices are counted as absent, matching consolidateArchiveIntoRoot:
// appending an empty slice contributes no record.
//
// It does NOT consolidate: this runs on a path that may reject, and a predicate
// must not mutate the state it is asked about.
func hasCompensationRecordsToWalk(s *InstanceState) bool {
	if len(s.RootCompensations) > 0 {
		return true
	}
	for _, records := range s.ArchivedCompensations {
		if len(records) > 0 {
			return true
		}
	}
	return false
}

// stepCompensateRequested handles a CompensateRequested trigger. It sets the
// instance to StatusCompensating, then delegates to beginCompensation to cancel
// in-flight tokens, look up compensation records, and emit the first
// InvokeAction for the reverse-order walk (or finish immediately when there are
// no eligible records).
//
// The admin path always calls beginCompensation with zero finalStatus and empty
// finalErr, producing StatusTerminated with no FailInstance on a full rollback —
// identical to the prior behaviour.
func stepCompensateRequested(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t CompensateRequested, pol stepPolicy) (StepResult, error) {
	// Reject a malformed trigger that expresses reverse intent (ResetVars)
	// without naming a resume target (ReverseNode). CompensateRequested is a
	// public, directly-constructible struct — a caller who builds one by hand
	// (e.g. CompensateRequested{ResetVars: true}) instead of going through
	// NewReverseToStart can produce exactly this shape. Without this guard,
	// stepCompensationFinish's outcome switch falls through to the full-
	// rollback TERMINATE branch (ReverseNode == "" takes no reverse branch),
	// silently discarding ResetVars and terminating the instance instead of
	// resuming it — the engine-level twin of the WithTargetNode("") footgun
	// already guarded at the runtime facade (ADR-0109 hardening, finding #5).
	// Checked first, ahead of the two state-dependent guards below — the
	// in-flight-walk check and the terminal nothing-to-compensate check — because
	// it is a pure trigger-shape validation independent of s.Status. That
	// ordering is what makes the minimal malformed shapes keep reporting the
	// shape error even on a terminal instance (ADR-0165).
	if t.ResetVars && t.ReverseNode == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: ResetVars requires ReverseNode (use NewReverseToStart)")
	}
	// Sibling guard (F1.2, FU#1): reject a malformed trigger that expresses
	// target-reverse variable-restore intent (RestoreTargetVars) without a
	// target node (ToNode) to look the snapshot up on. RestoreTargetVars
	// restores Variables to ToNode's own start-of-visit snapshot (the Input
	// captured on ToNode's compensation record) — with ToNode == "" there is
	// no record to read the snapshot from. CompensateRequested is a public,
	// directly-constructible struct, so a caller who builds one by hand
	// (e.g. CompensateRequested{RestoreTargetVars: true}) instead of going
	// through NewReverseToNode can produce exactly this shape. Same
	// pure-trigger-shape rationale as the ResetVars guard above, and checked
	// alongside it for the same reason.
	if t.RestoreTargetVars && t.ToNode == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: RestoreTargetVars requires ToNode (use NewReverseToNode)")
	}
	// If a compensation walk is already in flight, ignore the redundant request:
	// restarting beginCompensation would re-walk records that are still
	// mid-consumption and re-emit the in-flight compensation (double-compensation).
	//
	// A facade-originated reverse trigger — full (t.ReverseNode != "") OR target
	// (t.RestoreTargetVars, set only by NewReverseToNode) — is the one exception:
	// the runtime facade (ProcessDriver.ReverseInstance) admits a Compensating
	// instance for both WithFullReverse and WithTargetNode, so a reverse arriving
	// mid-walk must not be silently discarded — the caller would otherwise believe
	// it succeeded when nothing happened. Reject it with an error instead. A plain
	// admin/partial CompensateRequested (both ReverseNode == "" and
	// RestoreTargetVars == false — a raw engine.NewCompensateRequested) keeps
	// today's silent no-op — that path is shared with admin/cancel/error callers
	// that may legitimately re-deliver a trigger mid-walk.
	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
		if t.ReverseNode != "" || t.RestoreTargetVars {
			return StepResult{}, fmt.Errorf("workflow-engine: cannot reverse instance while a compensation walk is in flight")
		}
		return StepResult{State: *s}, nil
	}
	// The one terminal condition that is NOT a property of the trigger, and so
	// cannot live in terminalPolicy: refuse a plain full rollback on a terminal
	// instance when there is nothing left to compensate (ADR-0165 Decision 5,
	// predicate corrected during implementation).
	//
	// dispatch has already refused every resume-shaped rollback by the time
	// control reaches here, so on a terminal instance this can only be the plain
	// full rollback allowOnTerminal waves through. ADR-0164 carve-out #1 keeps
	// working: with records to walk, compensating a finished instance is a
	// legitimate admin action and the walk proceeds untouched.
	//
	// Without them the walk is not merely pointless, it is destructive:
	// beginCompensation finds no eligible record and goes straight to
	// stepCompensationFinish, which re-runs the terminal transition — flipping
	// StatusFailed to StatusTerminated, dropping any token that survived the
	// original transition, and overwriting EndedAt so the recorded moment of
	// death moves. No compensation action is emitted in exchange, and the caller
	// is told nothing.
	//
	// ⚠ Decision 5 as written in ADR-0165 had this predicate INVERTED — it
	// refused the walk when records survive and admitted it when they do not,
	// on the assumption that "no records" meant "nothing happens". Measurement
	// showed the opposite on both halves. The decision's intent stands; only its
	// expression was wrong.
	//
	// The refusal is an error, not a silent drop, because the only caller that
	// can still reach this path is an explicit admin action: CancelRequested was
	// reclassified to rejectSilently and is now stopped at dispatch, so the
	// "internal machinery re-delivers this and must not see an error" rationale
	// no longer applies here.
	if s.Status.IsTerminal() && !hasCompensationRecordsToWalk(s) {
		// The SAME operator record dispatch leaves for the two refusals it can
		// make, under the same message: this is the third refusal of the same
		// class, and dispatch's own rule is that erroring is not a substitute for
		// the record — a caller's 422 is invisible to the operator reading logs.
		// The reason attribute is what tells the two apart, since an operator
		// grepping the message alone would otherwise see one line and conclude
		// the trigger never reached a handler.
		slog.WarnContext(ctx, "trigger rejected on terminal instance",
			"instance_id", s.InstanceID,
			"trigger", triggerTypeName(t),
			"status", s.Status.String(),
			"outcome", "errored",
			"reason", "nothing left to compensate",
		)
		return StepResult{}, fmt.Errorf("%w: nothing left to compensate (status %v)", ErrInstanceTerminal, s.Status)
	}
	s.Status = StatusCompensating
	// A rollback that resumes execution (full reverse, or a partial rollback to a
	// target node) is a reverse; a walk that just compensates and terminates is a
	// plain compensation (ADR-0145).
	closeKind := CloseKindCompensated
	if t.ReverseNode != "" || t.ToNode != "" || t.RestoreTargetVars {
		closeKind = CloseKindReversed
	}
	// A rollback that TERMINATES is a dying-instance transition, so it harvests what
	// the open scopes still hold — otherwise work completed inside a live sub-process
	// is invisible to the walk, and the operator's rollback dispatches nothing while
	// endInstance archives the record a moment later, so only a SECOND rollback
	// actually compensates it (ADR-0174, found by /code-review at the gate).
	//
	// ⚠ Gated on the walk terminating, and that gate is load-bearing. A full reverse
	// and a partial rollback both RESUME, leaving the sub-process scope alive; hoisting
	// their records both changes what the rollback compensates and re-exposes them to
	// the retained-records double-run. Measured on a resuming walk: partial rollback
	// [] → [undoInner], and a later cancel [undoRoot undoRoot] →
	// [undoInner undoRoot undoInner undoRoot].
	if t.ToNode == "" && t.ReverseNode == "" {
		s.harvestOpenScopeCompensations()
	}
	return beginCompensation(ctx, def, s, t.OccurredAt(), pol, compensationOutcome{
		CloseKind:         closeKind,
		ToNode:            t.ToNode,
		FinalStatus:       0,
		FinalErr:          "",
		ReverseNode:       t.ReverseNode,
		ReverseResetVars:  t.ResetVars,
		RestoreTargetVars: t.RestoreTargetVars,
	})
}

// compensationOutcome bundles beginCompensation's trailing arguments: the
// rollback target and terminal status/reason to apply when the walk
// completes, plus the optional ReverseInstance resume intent. Grouping these
// avoids an opaque positional tail (e.g. `"", false, false`) at call sites —
// every field below is a named replacement for one of beginCompensation's
// former bare positional params.
type compensationOutcome struct {
	// ToNode is the rollback target: "" compensates all root-scope records;
	// non-empty stops the reverse walk at (and excludes) this node's record.
	ToNode string
	// FinalStatus/FinalErr are carried on the cursor across all advance steps
	// so that stepCompensationFinish applies them when the walk completes (the
	// admin path passes the zero Status and "").
	FinalStatus Status
	FinalErr    string
	// ReverseNode/ReverseResetVars carry the ReverseInstance full-reverse
	// intent (ADR-0109): when ReverseNode is non-empty, a FULL-rollback finish
	// resumes at ReverseNode (StatusRunning) — optionally resetting Variables
	// to StartVariables via ReverseResetVars — instead of terminating. All
	// cancel/error/throw callers leave both zero so their terminate behaviour
	// is unchanged.
	ReverseNode      string
	ReverseResetVars bool
	// CloseKind is why the in-flight tokens are being torn down, stamped on
	// every visit this walk closes (ADR-0145): CloseKindInstanceCancelled for a
	// cancel, CloseKindErrored for a terminal error, CloseKindReversed for a
	// ReverseInstance rollback, CloseKindCompensated for a plain administrative
	// compensation walk.
	CloseKind CloseKind
	// RestoreTargetVars carries the FU#1 target-reverse intent (ADR-0116):
	// when true (only the NewReverseToNode path sets it, always alongside a
	// non-empty ToNode), the PARTIAL-rollback finish restores Variables to
	// ToNode's start-of-visit snapshot. Cancel/error/throw/admin/full-reverse
	// callers leave this false so their variable handling is unchanged.
	RestoreTargetVars bool
}

// beginCompensation is the shared initiator of a reverse-order compensation walk.
// It is called by stepCompensateRequested (admin path, outcome.FinalStatus=0,
// outcome.FinalErr="") and by the error/cancel terminal paths with the
// appropriate outcome.
//
// s.Status must already be set to StatusCompensating by the caller.
//
// beginCompensation:
//  1. Cancels all in-flight tokens, timers, armed events, and boundaries (emitting
//     CancelTimer commands for each).
//  2. Looks up the root-scope (scopeID="") compensation records.
//  3. Validates outcome.ToNode: if non-empty and not found in records, returns an
//     error; if it is the last (most-recently completed) record, calls
//     stepCompensationFinish immediately with the cancel commands prepended
//     (nothing to compensate above it).
//  4. If there are no eligible records (empty records list), calls
//     stepCompensationFinish immediately, applying outcome.FinalStatus/FinalErr
//     via the cursor.
//  5. Otherwise sets compensationCursor (stamping outcome.FinalStatus and
//     FinalErr for the terminal outcome) and emits the first compensation
//     InvokeAction for the most-recently completed record (reverse walk).
//
// See compensationOutcome for the meaning of each field.
func beginCompensation(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, at time.Time, pol stepPolicy, outcome compensationOutcome) (StepResult, error) {
	// Cancel all in-flight tokens (interrupting normal execution).
	// Also emit CancelTimer for any outstanding timers, armed events, and boundaries.
	var preCmds []Command

	// Snapshot tokens to cancel (avoid mutating while iterating).
	tokensToCancel := make([]Token, len(s.Tokens))
	copy(tokensToCancel, s.Tokens)
	for _, tok := range tokensToCancel {
		// Cancel deadline/reminder timers, in-wait reminder, boundary arms, and (for
		// an event-based-gateway token) armed events, then consume the token.
		// (cancelAllTimers below also sweeps any remaining timers, but emitting the
		// CancelTimer here keeps the per-token cleanup explicit and order-consistent
		// with the other interrupt sites.)
		preCmds = append(preCmds, cancelTokenWaits(s, &tok, at, outcome.CloseKind)...)
	}
	// Cancel any remaining timers and event-subprocess arms.
	preCmds = append(preCmds, s.cancelAllTimers()...)
	preCmds = append(preCmds, s.cancelAllArmsAndBoundaries()...)

	// Merge any archived sub-process compensation records into RootCompensations before
	// walking. consolidateArchiveIntoRoot is a no-op when ArchivedCompensations is nil.
	s.consolidateArchiveIntoRoot()

	// Compensate the root scope (top-level walk: root + all previously-archived records
	// are now in RootCompensations after consolidation above).
	const scopeID = ""
	// scopeID is the const root, which is always resolvable.
	records, _ := compensationRecordsForScope(s, scopeID)

	toNode := outcome.ToNode
	finalStatus := outcome.FinalStatus
	finalErr := outcome.FinalErr
	reverseNode := outcome.ReverseNode
	reverseResetVars := outcome.ReverseResetVars
	restoreTargetVars := outcome.RestoreTargetVars

	// Determine the starting index and the toNode boundary via eligibleRange
	// (shared with stepCompensationAdvance's re-derivation of the same rule).
	// Because records are stored in completion order (oldest first), the reverse
	// walk is: len(records)-1 down to 0.
	//
	// If ToNode == "", all records are eligible. If ToNode != "", we exclude the
	// record whose NodeID == ToNode (it is the rollback TARGET — we do not
	// compensate it): only records recorded AFTER ToNode (i.e. later in the
	// slice, index > toNodeIdx) are eligible.
	startIndex, toNodeIdx := eligibleRange(records, toNode)
	if toNode != "" {
		if toNodeIdx >= 0 {
			// Only records AFTER toNodeIdx (i.e. indices > toNodeIdx) are eligible.
			// The first to emit is the last eligible record.
			if toNodeIdx >= startIndex {
				// ToNode was the last completed node — nothing to compensate above it.
				// Stamp the cursor so stepCompensationFinish applies the outcome; since
				// toNode != "", stepCompensationFinish takes the partial-rollback branch
				// (resume at toNode) regardless of FinalStatus/FinalErr — which is correct
				// for the admin path (outcome fields are zero).
				s.Compensating = compensationCursor{FinalStatus: finalStatus, FinalErr: finalErr, ReverseNode: reverseNode, ReverseResetVars: reverseResetVars, RestoreTargetVars: restoreTargetVars}
				finishRes, finishErr := stepCompensationFinish(ctx, def, s, toNode, at, pol)
				if finishErr != nil {
					return StepResult{}, finishErr
				}
				finishRes.Commands = append(preCmds, finishRes.Commands...)
				return finishRes, nil
			}
			// startIndex = the last eligible record = len(records)-1
			// (all records after toNodeIdx — no change needed; startIndex already set).
		} else {
			// toNode was specified but not found in the compensation records.
			// Return a descriptive error so that an admin typo is surfaced rather
			// than silently rolling back everything.
			return StepResult{}, fmt.Errorf("workflow-engine: compensation target node %q not found in scope records", toNode)
		}
	}

	if startIndex < 0 {
		// No records at all — apply the terminal outcome (or, for a reverse walk,
		// resume at ReverseNode) immediately. Stamp the cursor so
		// stepCompensationFinish reads the outcome AND reverse fields — a
		// reverse-to-start with ZERO eligible records must still resume at start.
		s.Compensating = compensationCursor{FinalStatus: finalStatus, FinalErr: finalErr, ReverseNode: reverseNode, ReverseResetVars: reverseResetVars, RestoreTargetVars: restoreTargetVars}
		finishRes, finishErr := stepCompensationFinish(ctx, def, s, toNode, at, pol)
		if finishErr != nil {
			return StepResult{}, finishErr
		}
		finishRes.Commands = append(preCmds, finishRes.Commands...)
		return finishRes, nil
	}

	// Emit the first compensation InvokeAction (record at startIndex). s.Compensating
	// is the zero cursor here (stepCompensationFinish always resets it to
	// compensationCursor{} before any caller re-enters beginCompensation), so
	// copy-and-mutate is equivalent to a from-scratch literal.
	rec := records[startIndex]
	cmdID := s.nextCommandID()
	cur := s.Compensating
	cur.ScopeID = scopeID
	cur.StartedAt = at
	cur.ToNode = toNode
	cur.NextIndex = startIndex
	cur.ActiveCmdID = cmdID
	cur.FinalStatus = finalStatus
	cur.FinalErr = finalErr
	cur.ReverseNode = reverseNode
	cur.ReverseResetVars = reverseResetVars
	cur.RestoreTargetVars = restoreTargetVars
	s.Compensating = cur
	s.recordCompensationDispatch(cmdID)
	cmds := append(preCmds, compensationInvoke(rec, cmdID))
	// Arm the stall guard AFTER the s.cancelAllTimers() in the prologue above,
	// which nils s.Timers — an arm hoisted above it is silently discarded.
	cmds = append(cmds, armCompensationStallTimer(s, pol, rec.NodeID)...)
	return StepResult{State: *s, Commands: cmds}, nil
}

// cancelCompensationWalkTimers removes every outstanding WALK-SCOPED timer
// record — both TimerCompensationStall and TimerCompensationRetry — and returns
// a CancelTimer for each, so the scheduler is never left holding a timer for a
// walk that has moved on (ADR-0175, widened to the retry kind by ADR-0179).
//
// The membership question is [TimerKind.firesOnDyingInstance], which is where
// "this kind belongs to a compensation WALK rather than to the instance's
// forward work" is defined — so a future walk-scoped kind joins this sweep by
// being added there, not here. ⚠ It is deliberately NOT
// [TimerKind.detectionOnly]: that predicate answers whether a harness may fire
// the timer, and the retry kind answers it the other way (ADR-0179 Decision 4).
//
// Widening it is what stops a retry record outliving its walk. Measured before:
// a two-record RESUMING walk finished with `leakedTimerRecords=2` and emitted no
// CancelTimer, and stepCompensationFinish had already zeroed the cursor — so
// each orphan later fired against compensationCursor{}, the shape ADR-0171
// documents as having panicked in the pure core. (A TERMINATE finish hid this:
// endInstance's cancelAllTimers sweeps every record regardless of kind.)
//
// It sweeps by KIND rather than by key. The records carry Token: "" and an empty
// key names no record (ADR-0152), so neither cancelTimersForToken nor
// cancelTimersByTaskID can ever reach one — this is the only SELECTIVE sweep
// that does. ⚠ Not the only remover: cancelAllTimers reaches one by taking
// everything, but only on a terminal transition, and removeTimer reaches one
// only where the caller already holds its id (retryFailedCompensation, for the
// record that just fired).
// It returns WITHOUT touching s.Timers when there is nothing to cancel — the
// common case, and the one that keeps detection genuinely free when disabled.
// Rebuilding unconditionally would turn a nil Timers into an empty slice, and
// s.Timers is marshalled into the persisted snapshot: every stored row's
// `timers` would flip from null to [] on a walk that armed nothing (the
// stored-shape drift ADR-0174 hit with Scopes).
//
// ⚠ The early return is not a third filter to widen — it is DERIVED from the
// emit loop below it (cmds == nil iff that loop matched nothing), so widening
// the loop widens it. Leaving the loop narrow while widening only the rebuild
// would make the function return early and cancel nothing whenever detection is
// off, which is the default configuration.
func cancelCompensationWalkTimers(s *InstanceState) []Command {
	var cmds []Command
	for _, tr := range s.Timers {
		if tr.Kind.firesOnDyingInstance() {
			cmds = append(cmds, CancelTimer{TimerID: tr.TimerID})
		}
	}
	if cmds == nil {
		return nil
	}
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if !tr.Kind.firesOnDyingInstance() {
			out = append(out, tr)
		}
	}
	// nil, not an empty slice, when a walk-scoped record was the only one —
	// matching cancelAllTimers and the early return above. Otherwise every RESUME
	// finish of a walk that armed a stall guard or a retry backoff would persist
	// `timers: []` where it used to persist null.
	if len(out) == 0 {
		s.Timers = nil
		return cmds
	}
	s.Timers = out
	return cmds
}

// armCompensationStallTimer arms the stall timer guarding the compensation
// action just dispatched under cur.ActiveCmdID, and returns the commands to
// append. It is CANCEL-THEN-ARM: a walk re-arms on every advance and on retry,
// and leaving two live records would let a stale one raise an incident against a
// command that already completed.
//
// Its ARM half is a no-op when detection is disabled (pol.stallAfter == 0, the
// default), which is what keeps every existing command stream byte-identical.
// Its CANCEL half runs unconditionally, and since ADR-0179 that half also
// retires the retry backoff of the command this dispatch supersedes: every
// dispatch site calls this, so an advance out of a live backoff sweeps the
// orphan here rather than leaving it armed until the walk's finish.
//
// ⚠ At beginCompensation this MUST be called after s.cancelAllTimers(), which
// sets s.Timers to nil — an earlier arm is silently discarded.
func armCompensationStallTimer(s *InstanceState, pol stepPolicy, nodeID string) []Command {
	cmds := cancelCompensationWalkTimers(s)
	if pol.stallAfter <= 0 {
		return cmds
	}
	cur := s.Compensating
	timerID := s.nextTimerID()
	s.Timers = append(s.Timers, timerRecord{
		TimerID: timerID,
		Kind:    TimerCompensationStall,
		// Token is deliberately empty: the record guards the WALK, and
		// beginCompensation's prologue has already cancelled every token.
		NodeID: nodeID,
		// ScopeID mirrors the CURSOR's scope, which is what the walk is iterating.
		// ⚠ It is empty for a TARGETED compensation throw: that cursor is built as
		// {ArchiveKey: ref} and its emptiness is load-bearing for walkMode(), so the
		// records live in ArchivedCompensations[ref] rather than in a scope. Read
		// NodeID — always the compensated activity — rather than inferring location
		// from an empty ScopeID, which is ambiguous with the root scope.
		ScopeID:   cur.ScopeID,
		CommandID: cur.ActiveCmdID,
	})
	return append(cmds, ScheduleTimer{
		TimerID: timerID,
		Trigger: schedule.AfterDuration(pol.stallAfter),
		Kind:    TimerCompensationStall,
	})
}

// armCompensationRetryTimer decides whether the compensation record currently in
// flight is to be RE-DISPATCHED after a backoff rather than skipped, and when it
// is, arms the TimerCompensationRetry that will do it (ADR-0179 Decision 3).
//
// It returns (commands, true) when the retry branch is taken — the caller must
// then return WITHOUT advancing the walk — and (nil, false) when it is not, which
// is every case ADR-0034 Decision 4 already covered: no policy configured (the
// default), a non-retryable failure, an error the policy names non-retryable.
//
// nodeID is the failed record's node; errMsg and retryable come from the
// ActionFailed trigger, and are consulted the way the token retry path in
// handleActionFailed consults them.
func armCompensationRetryTimer(s *InstanceState, pol stepPolicy, nodeID, errMsg string, retryable bool) ([]Command, bool) {
	if pol.compensationRetry == nil {
		return nil, false
	}
	eff := pol.compensationRetry.Normalize()
	if !retryable || eff.IsNonRetryable(errMsg) {
		return nil, false
	}
	cur := s.Compensating
	// Budget, PER RECORD. Mirrors the token path's own exhaustion term in
	// handleActionFailed — `eff.MaxAttempts != 0 && attempt+1 >= eff.MaxAttempts`,
	// read here in its non-terminal form — including the MaxAttempts == 0 escape
	// that model.RetryPolicy documents as UNLIMITED and Normalize deliberately
	// preserves.
	//
	// ⚠ Exhaustion does NOT park the walk (ADR-0179 Decision 7): the caller falls
	// through to stepCompensationAdvance and the record is skipped, exactly as
	// ADR-0034 Decision 4 has always done. The incident is the durable record that
	// it happened.
	//
	// ⚠ MaxElapsed is not evaluated. A walk holds no token of its own, so there is
	// no per-attempt start timestamp to measure against — cursor.StartedAt is the
	// WALK's start and is deliberately never restamped (ADR-0175 decision 5).
	if eff.MaxAttempts != 0 && cur.RetryAttempts+1 >= eff.MaxAttempts {
		return nil, false
	}
	// Backoff takes a ZERO-BASED attempt number, and RetryAttempts is the count
	// already spent on this record — so the pre-increment value is the right
	// argument: the first retry of a record waits InitialInterval.
	//
	// ⚠ The compensation path applies NO jitter at all, unlike the token retry
	// path in handleActionFailed, which scales this same backoff by
	// ActionFailed.JitterFraction when the caller supplied one. A compensation
	// walk is serialized one-at-a-time per instance, so there is no retry storm
	// to spread and nothing to gain from spreading it.
	//
	// This comment previously read that the token path's formula was
	// `JitterFraction * Backoff(attempt)` and therefore collapsed to a zero
	// delay by default. That WAS true, and was the bug fixed in
	// handleActionFailed (see TestActionFailedRetryBackoffDefaultsToFullInterval);
	// the token path now defaults to the full interval too. What still separates
	// the two paths is the jitter, not the backoff.
	delay := eff.Backoff(cur.RetryAttempts)
	// CANCEL THE STALL GUARD FIRST, THEN ARM THE BACKOFF. The stall guard's job is
	// done: the action REPLIED, it did not go silent. Leaving it armed makes it
	// fire during a healthy backoff and tell the operator "compensation action
	// stalled" about an action that already answered — a visibility regression in
	// the ADR whose headline is visibility — and it opens a
	// CompensationEscape{Retry} race against the scheduled retry, because
	// handleResolveCompensationStall accepts the same still-active command id.
	//
	// ⚠ The two lines must not be swapped, and since cancelCompensationWalkTimers
	// was widened to every walk-scoped kind (ADR-0179 Decision 3, plan P1 step 10)
	// that is now LOAD-BEARING rather than merely prudent: an arm written ABOVE
	// the cancel is swept by it, so no retry record survives the call and the
	// retry never fires at all. Mutation-verified by swapping the two statements —
	// the whole package went red, where the same swap was observationally
	// equivalent while the sweep still filtered strictly on TimerCompensationStall.
	cmds := cancelCompensationWalkTimers(s)
	timerID := s.nextTimerID()
	s.Timers = append(s.Timers, timerRecord{
		TimerID: timerID,
		Kind:    TimerCompensationRetry,
		// Token is deliberately empty, as on the stall record: the timer guards
		// the WALK, which is not driven by a token of its own.
		NodeID: nodeID,
		// ScopeID mirrors the CURSOR's scope, the same convention
		// armCompensationStallTimer uses — empty for a targeted throw, whose
		// records live in ArchivedCompensations rather than in a scope.
		ScopeID: cur.ScopeID,
		// CommandID is the command that FAILED. retryFailedCompensation compares it
		// against ActiveCmdID to drop a late fire, exactly as
		// handleCompensationStallFired does.
		CommandID: cur.ActiveCmdID,
	})
	cur.RetryTimerID = timerID
	cur.RetryAttempts++
	s.Compensating = cur
	return append(cmds, ScheduleTimer{
		TimerID: timerID,
		Trigger: schedule.AfterDuration(delay),
		Kind:    TimerCompensationRetry,
	}), true
}

// compensationInvoke returns the InvokeAction that runs rec's compensation
// action under cmdID, with the record's captured Input copied so the command
// never aliases stored state.
func compensationInvoke(rec CompensationRecord, cmdID string) InvokeAction {
	return InvokeAction{
		CommandID: cmdID,
		Name:      rec.Action,
		Input:     copyVars(rec.Input),
	}
}

// stepCompensationAdvance advances the compensation cursor after a compensation
// InvokeAction completes (ActionCompleted with cursor.ActiveCmdID). It emits the
// next InvokeAction in reverse order, or finalises compensation if the walk is done.
func stepCompensationAdvance(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, at time.Time, pol stepPolicy) (StepResult, error) {
	cur := s.Compensating
	// Retire the stall incident for the command we are advancing PAST, before the
	// cursor is recomputed below — after it, cur.ActiveCmdID names the NEXT
	// command and nothing would match (ADR-0175).
	//
	// Every route by which a walk moves on funnels through here: a late
	// ActionCompleted, a late ActionFailed (best-effort skip, ADR-0034 Decision 4)
	// and the `skip` verb. Putting the sweep in handleActionCompleted instead
	// would silently miss the other two.
	s.retireCompensationStallIncidents(cur.ActiveCmdID)
	// Use cursorRecords so a pinned throw walk reads its snapshot, an unpinned
	// throw walk reads its archive, and admin/cancel/error walks read the live
	// scope.
	records := cursorRecords(s, cur)

	// Advance: the record we just completed is at cur.NextIndex. Move to the
	// previous record (next in reverse order). nextIdx = cur.NextIndex - 1.
	nextIdx := cur.NextIndex - 1

	// Determine the stop boundary via the SAME eligibleRange rule beginCompensation
	// uses to pick its starting index (start is unused here — nextIdx is already
	// computed above from the cursor).
	_, toNodeIdx := eligibleRange(records, cur.ToNode)

	// Check if next record is within the eligible range.
	// Eligible: nextIdx >= 0 AND nextIdx > toNodeIdx (i.e. the record is AFTER ToNode).
	//
	// nextIdx >= len(records) is the third disjunct (ADR-0171): a record source
	// that vanished or shrank under the cursor must route HERE, to the walk's
	// finish, and never to the index expression below. It is unreachable for a
	// walk this build started — startCompensationWalk pins the source, and
	// beginCompensation's own source is RootCompensations, which no scope
	// teardown can nil — but it IS reachable for a walk that was already in
	// flight when the process restarted: such a cursor was persisted without
	// Records, so cursorRecords falls back to a live read that now returns
	// nothing. That is the state
	// TestCompensationAdvanceFinishesWhenRecordSourceIsGone builds, and without
	// this disjunct it panics with an index out of range inside the pure engine
	// core.
	if nextIdx < 0 || nextIdx >= len(records) || nextIdx <= toNodeIdx {
		// Walk complete: exhausted all records, lost the record source, or
		// reached the ToNode boundary.
		return stepCompensationFinish(ctx, def, s, cur.ToNode, at, pol)
	}

	// Emit the next compensation action. cur already carries every field
	// unchanged from s.Compensating (ArchiveKey, ResumeNode, ResumeScope, etc.) —
	// only NextIndex and ActiveCmdID actually change on advance, so mutate just
	// those two and write cur back.
	rec := records[nextIdx]
	cmdID := s.nextCommandID()
	cur.NextIndex = nextIdx
	cur.ActiveCmdID = cmdID
	// The retry budget is PER RECORD (ADR-0179 Decision 3), and this is the only
	// site in the package where NextIndex ADVANCES — the other two writers,
	// beginCompensation and startCompensationWalk, start a walk from the cursor
	// stepCompensationFinish has already zeroed. Measured without this reset: the
	// second record was skipped outright with zero retries and the walk TERMINATED
	// (status terminated, FailInstance{cancelled}, no backoff armed), because the
	// first poison record had burned the whole budget. Resetting nowhere is the
	// mirror bug — unbounded retrying.
	//
	// RetryTimerID is cleared with it. Advancing with a stale non-empty value would
	// make handleActionFailed's idempotency guard swallow the NEXT record's first
	// genuine failure as though it were a redelivery.
	cur.RetryAttempts = 0
	cur.RetryTimerID = ""
	s.Compensating = cur
	// Hand ownership of this record over as it is dispatched (ADR-0173), so a walk
	// ABANDONED before its finish — a force-termination end event is the measured
	// route — leaves behind exactly the records it never ran. Written AFTER the
	// cursor assignment above because it updates the cursor's own window count.
	s.consumeDispatchedRecord(nextIdx)
	s.recordCompensationDispatch(cmdID)
	cmds := []Command{compensationInvoke(rec, cmdID)}
	// Re-arm the stall guard against the NEW ActiveCmdID. Written after the
	// cursor assignment above, which is what the helper reads.
	cmds = append(cmds, armCompensationStallTimer(s, pol, rec.NodeID)...)
	return StepResult{State: *s, Commands: cmds}, nil
}

// finishPlan is the parameterized description of ONE compensation-walk finish
// outcome. Every finish — throw-resume, partial-rollback, full-reverse, and
// terminate — is expressed as a finishPlan and applied by applyFinish, so the
// resume/terminate invariant-restoration lives in exactly one place.
type finishPlan struct {
	// resume is true for a resume outcome (Status → Running, place a token, drive)
	// and false for a terminate outcome (apply FinalStatus, stamp EndedAt).
	resume bool
	// resumeAt is the node to place the resume token at (resume outcomes only).
	resumeAt string
	// resumeScope is the ScopeID for the resume token ("" = root). Only the throw
	// resume uses a non-root scope (the throw token's scope); partial/reverse
	// resume at root.
	resumeScope string
	// clearScope is the scope whose compensation records are cleared when
	// doClearRecords is set ("" = root/RootCompensations).
	clearScope string
	// doClearRecords clears clearScope's records. Full-reverse and terminate clear;
	// targeted throw and partial RETAIN their records. A scope-wide throw also sets
	// it (see scopeWideThrow below for the prefix-only variant).
	doClearRecords bool
	// scopeWideThrow marks a SCOPE-WIDE compensation-throw finish (ADR-0120). When
	// set alongside doClearRecords, only the drainedCount leading records of
	// clearScope (the prefix the walk actually drained) are cleared instead of the
	// whole list — so a record a still-running sibling appended mid-walk survives
	// and stays compensable by a later cancel (review A1). Only the scope-wide
	// throw branch sets it; every other clearing plan nils the whole scope list.
	scopeWideThrow bool
	// drainedCount is the number of leading records a scope-wide throw walk drained
	// (its StartRecordCount). Used only when scopeWideThrow is set.
	drainedCount int
	// archiveWindowKey / Offset / Count carry the TEARDOWN WINDOW off the cursor
	// (ADR-0173): the records a mid-walk scope teardown parked in the archive on
	// this walk's behalf. consumeDispatchedRecord normally empties it as the walk
	// dispatches, so by the finish Count is 0; these exist for the residue a walk
	// that stopped short of its own records leaves — a ToNode boundary, or a
	// source that shrank. Set only on the scopeWideThrow branch.
	archiveWindowKey    string
	archiveWindowOffset int
	archiveWindowCount  int
	// resetVars resets Variables to StartVariables (full-reverse with reset only).
	resetVars bool
	// restoreVars, when non-nil, replaces Variables with a copy of this snapshot on
	// resume — the target node's start-of-visit Input for a target reverse (FU#1,
	// ADR-0116). nil (the default) leaves Variables untouched. Mutually exclusive
	// with resetVars by construction: a full-reverse (resetVars) never carries a
	// ToNode, and a target reverse (restoreVars) never carries a ReverseNode.
	restoreVars map[string]any
	// deleteArchive is the throw-walk ArchivedCompensations key to delete on finish
	// ("" = none) — single-ownership consume semantics.
	deleteArchive string
	// archiveConsumed marks a throw walk that handed each record's ownership over
	// AS IT DISPATCHED it (ADR-0173), i.e. one with a pinned record source. Its
	// deleteArchive slot therefore holds only records it never ran, and the finish
	// must delete the key only when the slot is empty. False for a cursor
	// persisted before ADR-0171, which pinned nothing, consumed nothing, and keeps
	// the original whole-key delete.
	archiveConsumed bool
	// popDeferred re-activates exactly one deferred compensation throw (throw walk
	// only — ADR-0071 serialization).
	popDeferred bool
	// consumePendingCancel makes a cancel that arrived mid-walk preempt the resume
	// and terminate instead. The throw walk (preserving the prior throw-walk
	// protocol) and the full-reverse walk (ADR-0109 hardening, finding #2) set it;
	// the partial-rollback resume keeps resuming.
	consumePendingCancel bool
	// rearmRootESP re-arms ROOT-scope event sub-processes (ADR-0109 hardening,
	// finding #1) via armEventTriggeredSubprocesses(def, s, "", at, eval), mirroring
	// handleStartInstance's own arm-then-drive sequence. Only the full-reverse
	// resume AT ROOT SCOPE sets this: beginCompensation does not sweep
	// s.EventTriggeredSubprocesses when a walk starts, so without a full reverse a
	// root-level event sub-process (armed at StartInstance, or left un-armed
	// after an earlier interrupting fire) is either stale or silently lost by
	// the time the instance resumes. Partial and throw resumes stay at their
	// own (possibly non-root) scope and must NOT re-arm.
	rearmRootESP bool
	// finalStatus / finalErr are the terminal outcome (terminate only).
	finalStatus Status
	finalErr    string
}

// validate asserts finishPlan's documented construction invariants (see the
// field comments above) hold, and panics with a workflow-engine:-prefixed
// message naming the violated invariant otherwise. A violated invariant here
// is a programming bug in this package — finishPlan is a transient, non-
// serialized value built entirely from in-package logic, never from
// persisted or external input — so it is deliberately NOT an error routed
// through the incident path.
//
// ⚠ That "never from persisted or external input" clause is load-bearing, and
// ADR-0173 nearly broke it: the teardown window it added is read off the
// PERSISTED cursor, so a corrupt row carrying a count with no key would have
// tripped the assertion below and panicked in the consumer's process.
// normalizeTeardownWindow sanitizes the window at the cursor read for exactly
// that reason — keep new plan fields sourced from persisted state going through
// it, or this function stops being a programming-bug detector and becomes an
// input validator that crashes. stepCompensationFinish calls this once the
// plan for the walk's outcome is fully built, before handing it to
// applyFinish.
func (p finishPlan) validate() {
	if p.resetVars && p.restoreVars != nil {
		panic("workflow-engine: finishPlan invariant violated: resetVars and restoreVars are mutually exclusive")
	}
	if p.scopeWideThrow && !p.doClearRecords {
		panic("workflow-engine: finishPlan invariant violated: scopeWideThrow must be set alongside doClearRecords")
	}
	if !p.resume && p.scopeWideThrow {
		panic("workflow-engine: finishPlan invariant violated: a terminate plan (resume=false) must never set scopeWideThrow")
	}
	// ADR-0173: a teardown window count without the key it indexes into would
	// silently address nothing.
	//
	// ⚠ The converse is NOT an invariant, and asserting it panicked the suite: the
	// USUAL finish carries a non-empty key with a ZERO count, because
	// consumeDispatchedRecord empties the window as the walk dispatches and only
	// the key survives to the finish. Key-set-implies-count-set was a plausible
	// symmetry that the built code refuted immediately.
	if p.archiveWindowCount > 0 && p.archiveWindowKey == "" {
		panic("workflow-engine: finishPlan invariant violated: archiveWindowCount without archiveWindowKey")
	}
	if p.archiveWindowKey != "" && !p.scopeWideThrow {
		panic("workflow-engine: finishPlan invariant violated: an archive window belongs only to a scope-wide throw")
	}
}

// clearRecords drops the compensation records for the given scope ("" = root).
// Shared by the full-reverse and terminate finish outcomes so a re-delivered
// terminal trigger cannot re-enter a walk and double-compensate.
func clearRecords(s *InstanceState, scopeID string) {
	if scopeID == "" {
		s.RootCompensations = nil
	} else if sc := s.scopeByID(scopeID); sc != nil {
		sc.Compensations = nil
	}
}

// clearRecordsPrefix drops only the first n compensation records for the given
// scope ("" = root), retaining records[n:]. A finished SCOPE-WIDE compensation
// throw drains exactly the prefix [0 .. n-1] that existed at walk start, so it
// clears only that prefix (compensate-once) while retaining any record a still-
// running sibling appended mid-walk at index >= n — that record is genuinely
// uncompensated and must stay compensable by a later cancel/rollback (ADR-0120
// review A1). n is clamped to the current slice length (defensive): if the list
// shrank, everything is cleared. A fresh backing slice is allocated for the
// retained tail so the drained records are released for GC and no stale element
// aliases the old array.
func clearRecordsPrefix(s *InstanceState, scopeID string, n int) {
	trim := func(records []CompensationRecord) []CompensationRecord {
		if n >= len(records) {
			return nil
		}
		if n <= 0 {
			return records
		}
		retained := make([]CompensationRecord, len(records)-n)
		copy(retained, records[n:])
		return retained
	}
	if scopeID == "" {
		s.RootCompensations = trim(s.RootCompensations)
		return
	}
	if sc := s.scopeByID(scopeID); sc != nil {
		sc.Compensations = trim(sc.Compensations)
	}
}

// retainedRecordPrefix returns a COPY of scopeID's records [0 .. n-1] — the ones
// a reverse-order walk never reached. It is abandon's record disposition
// (ADR-0175): the walk dispatches from the END of the slice downward, so index n
// is the record it stalled on and everything below n is untouched work.
//
// n <= 0 retains nothing; n >= len retains everything. It returns nil rather
// than an empty slice when nothing is retained, matching clearRecords.
func retainedRecordPrefix(s *InstanceState, scopeID string, n int) []CompensationRecord {
	// A closed scope retains nothing, which the len(records) == 0 guard below
	// already yields — the distinction does not change this answer.
	records, _ := compensationRecordsForScope(s, scopeID)
	if n <= 0 || len(records) == 0 {
		return nil
	}
	n = min(n, len(records))
	retained := make([]CompensationRecord, n)
	copy(retained, records[:n])
	return retained
}

// setScopeRecords replaces scopeID's compensation records outright ("" = root).
// A no-op for a scope that no longer exists.
func setScopeRecords(s *InstanceState, scopeID string, records []CompensationRecord) {
	if scopeID == "" {
		s.RootCompensations = records
		return
	}
	if sc := s.scopeByID(scopeID); sc != nil {
		sc.Compensations = records
	}
}

// lastCompensationRecordByNode returns a pointer to the LAST record in records
// whose NodeID equals nodeID (the most-recent visit of that node — matching
// beginCompensation's most-recent-match rule for a rollback target), or nil when
// no record names the node. Used by the target-reverse finish to read the
// resume target's start-of-visit variable snapshot (its Input). The returned
// pointer aliases the slice element; callers copyVars its Input before handing it
// to a resumed instance so the retained record's map is never mutated in place.
func lastCompensationRecordByNode(records []CompensationRecord, nodeID string) *CompensationRecord {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].NodeID == nodeID {
			return &records[i]
		}
	}
	return nil
}

// popOneDeferredThrow re-activates exactly ONE deferred compensation throw token
// (ADR-0071 serialization). The caller has already cleared the cursor, so the
// subsequent drive re-enters the throw handler for that token via the normal
// walk-start path. Popping one-per-finish keeps at most one walk in flight; any
// further deferred throws stay queued and drain as each walk completes. No-op
// when nothing is deferred.
func popOneDeferredThrow(s *InstanceState) {
	if len(s.DeferredCompensationThrows) == 0 {
		return
	}
	deferredTok := s.DeferredCompensationThrows[0]
	s.DeferredCompensationThrows = s.DeferredCompensationThrows[1:]
	if tok := s.tokenByID(deferredTok); tok != nil {
		tok.State = TokenActive
	}
}

// stepCompensationFinish finalises the compensation walk. It saves the cursor's
// outcome/metadata, clears the cursor, builds the finishPlan matching the walk's
// outcome, and delegates to applyFinish. The four outcomes are:
//   - ResumeNode != "" (compensation throw walk): delete the archive entry,
//     resume Running at ResumeNode in ResumeScope, pop one deferred throw, drive.
//   - toNode != "" (partial rollback via CompensateRequested): resume Running at
//     toNode (records RETAINED), drive.
//   - ReverseNode != "" (ReverseInstance full-reverse, ADR-0109): clear records,
//     optionally reset Variables to StartVariables, resume Running at ReverseNode,
//     drive.
//   - otherwise (full rollback): apply the cursor's terminal FinalStatus
//     (StatusTerminated / StatusFailed), stamp EndedAt, clear records.
func stepCompensationFinish(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, toNode string, at time.Time, pol stepPolicy) (StepResult, error) {
	// Snapshot the cursor BEFORE clearing it, with ToNode filled in from the
	// toNode parameter: the immediate-finish call sites in beginCompensation
	// (nothing to compensate / no records at all) stamp s.Compensating without
	// ToNode — there is no active walk to resume, so nothing needs it later —
	// and pass the real target via this parameter instead. The active-walk call
	// sites (beginCompensation's walking branch, stepCompensationAdvance) already
	// carry it on the cursor (cur.ToNode), so this assignment is a no-op there.
	// cur.walkMode() below is therefore always driven by the SAME field values
	// the pre-refactor inline switch inspected.
	cur := s.Compensating
	cur.ToNode = toNode

	// Save outcome fields AND cursor metadata BEFORE clearing the cursor.
	finalStatus := cur.FinalStatus
	finalErr := cur.FinalErr
	scopeID := cur.ScopeID
	archiveKey := cur.ArchiveKey
	resumeNode := cur.ResumeNode
	resumeScope := cur.ResumeScope
	startRecordCount := cur.StartRecordCount
	// A pinned record source is what makes this walk's incremental consuming
	// (ADR-0173) trustworthy; see finishPlan.archiveConsumed.
	archiveConsumed := len(cur.Records) > 0
	windowKey, windowOffset, windowCount := normalizeTeardownWindow(
		cur.TeardownArchiveKey, cur.TeardownArchiveOffset, cur.TeardownArchiveCount)
	reverseNode := cur.ReverseNode
	reverseResetVars := cur.ReverseResetVars
	restoreTargetVars := cur.RestoreTargetVars
	// Clear the cursor — compensation walk is done.
	s.Compensating = compensationCursor{}

	// Retire the walk's own timers here — the stall guard and, since ADR-0179, any
	// retry backoff still armed — so all five walk modes are covered by one line
	// (ADR-0175). Only a TERMINATING finish reaches cancelAllTimers, via
	// endInstance; a finish that RESUMES — the four modes throw-targeted,
	// throw-scope-wide, partial rollback and full reverse, less whichever of them
	// a deferred cancel flips to terminating (see [compensationCursor.walkTerminates])
	// — never touches s.Timers, so without this
	// the record leaks onto a Running instance and the scheduler keeps a timer
	// with nothing left to guard. Worse for the retry kind than for the stall
	// kind: the cursor is zeroed two lines above, so a leaked retry record fires
	// against compensationCursor{}.
	//
	// ⚠ The position relative to applyFinish is NOT load-bearing, and an earlier
	// revision of this comment claimed it was. Measured by moving the call below
	// applyFinish: the terminate mode still emits exactly ONE CancelTimer, because
	// whichever of the two sweeps runs first removes the record and the other then
	// finds nothing. Pinned by TestTerminalFinishCancelsStallTimerExactlyOnce.
	walkTimerCancels := cancelCompensationWalkTimers(s)

	// Build the finishPlan matching this walk's outcome. Branch order mirrors the
	// pre-refactor precedence: throw resume, then partial, then full-reverse, then
	// terminate. History is intentionally RETAINED across every resume outcome:
	// re-execution appends fresh visits on top, keeping the full run history intact.
	var plan finishPlan
	switch cur.walkMode() {
	case walkThrowTargeted, walkThrowScopeWide:
		// Compensation throw resume: consume the archive entry (single ownership,
		// so a later cancel walk won't re-run what this walk ran), resume at the
		// throw's successor in its own scope, and re-activate one deferred throw. A
		// cancel arriving mid-walk preempts the resume and terminates
		// (consumePendingCancel).
		//
		// ⚠ This said "a second throw to the same ref finds len == 0 and no-ops",
		// which ADR-0173 NARROWS: the walk now consumes only the records it
		// dispatched, so a sibling that re-entered the same sub-process node
		// mid-walk leaves its own record in the slot and the second throw
		// COMPENSATES it. That is the point — the whole-key delete was destroying a
		// genuinely uncompensated record (ADR-0120 review A1's rule, which the
		// scope-wide branch already honoured). The no-op still holds for the case
		// the sentence was written about: a second throw with nothing new archived.
		plan = finishPlan{
			resume:               true,
			resumeAt:             resumeNode,
			resumeScope:          resumeScope,
			deleteArchive:        archiveKey,
			archiveConsumed:      archiveConsumed,
			popDeferred:          true,
			consumePendingCancel: true,
		}
		if cur.walkMode() == walkThrowScopeWide {
			// Scope-wide compensate throw (ADR-0120): the drained records came from
			// the throwing scope's LIVE list (RootCompensations or a sub-scope's
			// Compensations), not an archive entry. Clear them here (compensate-once)
			// so a second throw or a later cancel/rollback cannot re-run the
			// already-run compensations. A targeted throw (walkThrowTargeted) instead
			// deletes only its archive entry above and RETAINS RootCompensations,
			// which hold unrelated outer records a later walk must still compensate.
			//
			// Clear ONLY the prefix the walk drained (StartRecordCount), not the whole
			// list: a compensable sibling running concurrently (throw-then-continue
			// leaves siblings live) can append a fresh record ABOVE that prefix during
			// the walk. That record is genuinely uncompensated and must survive for a
			// later cancel/rollback — nilling the whole list would silently lose it
			// (review A1).
			plan.doClearRecords = true
			plan.clearScope = scopeID
			plan.scopeWideThrow = true
			plan.drainedCount = startRecordCount
			plan.archiveWindowKey = windowKey
			plan.archiveWindowOffset = windowOffset
			plan.archiveWindowCount = windowCount
		}
	case walkPartial:
		// Partial rollback: resume at toNode. Records are RETAINED (not cleared):
		// the instance keeps running and a later full walk must still see them.
		// No double-compensation risk — consolidateArchiveIntoRoot already drained
		// the archive into RootCompensations (single ownership).
		//
		// FU#1 (ADR-0116): a target reverse (RestoreTargetVars, set only by
		// NewReverseToNode) additionally restores Variables to toNode's own
		// start-of-visit snapshot — the Input on toNode's most-recent compensation
		// record (records are RETAINED on a partial finish, so the record is still
		// present here). A raw admin CompensateRequested leaves RestoreTargetVars
		// false and keeps the current variables.
		plan = finishPlan{
			resume:   true,
			resumeAt: toNode,
		}
		if restoreTargetVars {
			if rec := lastCompensationRecordByNode(s.RootCompensations, toNode); rec != nil {
				plan.restoreVars = rec.Input
			}
		}
	case walkReverse:
		// Full reverse (ADR-0109): clear the scope's records (as full rollback
		// does), optionally reset Variables, resume at ReverseNode. Re-arm root
		// event sub-processes when the walk was rooted at scope "" (today the
		// only case: NewReverseToStart always targets the root scope) — see
		// finishPlan.rearmRootESP. A cancel arriving mid-walk preempts the resume
		// and terminates (consumePendingCancel), mirroring the throw walk — Fork B.
		plan = finishPlan{
			resume:               true,
			resumeAt:             reverseNode,
			doClearRecords:       true,
			clearScope:           scopeID,
			resetVars:            reverseResetVars,
			rearmRootESP:         scopeID == "",
			consumePendingCancel: true,
		}
	default: // walkAdmin
		// Full rollback / terminate. Zero FinalStatus (== StatusRunning, the iota-0
		// constant) means UNSET; applyTerminate maps it to StatusTerminated. Safe:
		// full-rollback finish is always terminal. The admin path (CompensateRequested)
		// leaves FinalStatus zero; error/cancel paths set it explicitly.
		plan = finishPlan{
			resume:         false,
			doClearRecords: true,
			clearScope:     scopeID,
			finalStatus:    finalStatus,
			finalErr:       finalErr,
		}
	}
	plan.validate()
	res, err := applyFinish(ctx, def, s, plan, at, pol)
	if err != nil {
		return StepResult{}, err
	}
	res.Commands = append(walkTimerCancels, res.Commands...)
	return res, nil
}

// applyPlanRecordClearing performs a finishPlan's record clearing: a scope-wide
// throw (scopeWideThrow) clears only the drainedCount-length prefix it consumed,
// retaining any sibling-appended record (review A1); every other clearing plan
// nils the whole scope list. No-op when doClearRecords is false.
func applyPlanRecordClearing(s *InstanceState, plan finishPlan) {
	if !plan.doClearRecords {
		return
	}
	if plan.scopeWideThrow {
		clearRecordsPrefix(s, plan.clearScope, plan.drainedCount)
		// Remove whatever survives of the teardown window (ADR-0173). Normally
		// nothing does: consumeDispatchedRecord empties it as the walk dispatches.
		// A walk that stops short of its own records — a ToNode boundary, or a
		// record source that shrank under it — leaves a residue, and it belongs to
		// this walk, not to the next one. Highest index first, so each removal
		// takes the window's last element and shifts nothing still inside it.
		for i := plan.archiveWindowCount - 1; plan.archiveWindowKey != "" && i >= 0; i-- {
			s.dropArchiveRecordAt(plan.archiveWindowKey, plan.archiveWindowOffset+i)
		}
		return
	}
	clearRecords(s, plan.clearScope)
}

// applyFinish applies a finishPlan: the single site where a compensation walk
// either resumes (Status → Running, EndedAt cleared, token placed, drive) or
// terminates (FinalStatus applied, EndedAt stamped, records cleared). Collapsing
// the four former branches here means the resume/terminate invariants can no
// longer drift apart.
func applyFinish(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, plan finishPlan, at time.Time, pol stepPolicy) (StepResult, error) {
	// Throw-walk archive consume (single ownership). No-op for other plans
	// (deleteArchive == "").
	if plan.deleteArchive != "" && s.ArchivedCompensations != nil {
		// A walk that consumed its slot as it dispatched (ADR-0173) has already
		// removed everything it drained, so whatever is left is a record it never
		// ran — a sibling's mid-walk re-entry of the same sub-process node, which
		// accumulates into this one slot. Deleting the key regardless was the
		// defect: measured, the second visit's record was destroyed uncompensated
		// and the deferred throw popped at finish found an empty slot.
		//
		// A cursor persisted before ADR-0171 pinned no snapshot, so it read the
		// slot LIVE and consumed nothing; the whole-key delete stays its correct
		// single-ownership consume.
		if !plan.archiveConsumed || len(s.ArchivedCompensations[plan.deleteArchive]) == 0 {
			s.deleteArchiveSlot(plan.deleteArchive)
		}
	}

	// A cancel — or an unhandled error (ADR-0170) — that arrived mid-walk preempts
	// the resume: the walk's target is already compensated (and, for a throw,
	// removed from the archive above), so a full walk over the REMAINING records
	// cannot double-run it. Terminate.
	if plan.resume && plan.consumePendingCancel && s.PendingCancel {
		s.PendingCancel = false
		// The deferred terminal outcome. BOTH deferral sites stamp it explicitly:
		// handleCancelRequested writes StatusTerminated + "cancelled",
		// deferFailureToInFlightCompensationWalk writes StatusFailed + the uncaught
		// error code. The ZERO PendingFinalStatus is therefore only a state
		// PERSISTED before that became true — PendingCancel set by a pre-fix
		// handleCancelRequested — and decodes as the cancel outcome it had then.
		// Deriving the cancel from the zero value alone was the defect: a cancel
		// arriving after a deferred error found the fields already stamped and
		// silently replayed the error's outcome.
		//
		// ⚠ pendingKind is pinned by NO test on EITHER path, and saying so is cheaper
		// than a reader assuming otherwise. CloseKind reaches nothing here but
		// beginCompensation's per-token cancelTokenWaits, and this re-entry runs with
		// no token left to close (measured: tokens=0 at the moment of a deferred
		// error). Both mutations were run: forcing the error case to
		// CloseKindInstanceCancelled, and forcing the cancel case to CloseKindErrored,
		// each left ./engine at EXIT=0. It is set for correctness of the value, not
		// for an observable.
		pendingStatus, pendingErr := s.PendingFinalStatus, s.PendingFinalErr
		if pendingStatus == 0 {
			pendingStatus, pendingErr = StatusTerminated, "cancelled"
		}
		pendingKind := CloseKindInstanceCancelled
		if pendingStatus == StatusFailed {
			pendingKind = CloseKindErrored
		}
		s.PendingFinalStatus, s.PendingFinalErr = 0, ""
		// Clear THIS walk's own already-compensated records BEFORE the cancel walk
		// so it cannot re-run them (double-compensation). A TARGETED throw walk
		// (archiveKey != "") deleted its archive above (deleteArchive) and RETAINS
		// RootCompensations — those are genuinely-uncompensated outer records the
		// cancel walk must still compensate (doClearRecords == false, skipped here).
		// A SCOPE-WIDE throw walk (archiveKey == "", ADR-0120) instead compensated
		// the snapshot it pinned of the throwing scope's own records, so — like the
		// full-reverse walk — it clears that scope's live list here
		// (doClearRecords == true). It clears ONLY the drained
		// prefix (scopeWideThrow, review A1): the re-issued beginCompensation then
		// compensates any sibling record appended mid-walk and terminates
		// (FailInstance{"cancelled"}, StatusTerminated) — the correct compensate-once
		// outcome that preserves the sibling record. A full-reverse walk
		// compensated ALL of RootCompensations and clears the whole list the same way.
		applyPlanRecordClearing(s, plan)
		s.Status = StatusCompensating
		// This re-entry is a TERMINAL walk (a deferred cancel, or a deferred unhandled
		// error — ADR-0170), so it is a dying-instance transition and harvests what the
		// open scopes still hold. Without this the deferred cancel terminated AROUND a
		// sibling scope's completed compensable work: measured invoked=[], status
		// terminated, with undoB merely archived afterwards by endInstance and therefore
		// unreachable until a second rollback (ADR-0174, found by /code-review).
		//
		// Placed AFTER applyPlanRecordClearing so the finishing walk's own drained prefix
		// is already gone: harvesting first would re-archive records this walk dispatched.
		// No terminality gate is needed here — unlike stepCompensateRequested's site, this
		// branch is reached only for a pending CANCEL or ERROR outcome, both terminal.
		s.harvestOpenScopeCompensations()
		return beginCompensation(ctx, def, s, at, pol, compensationOutcome{CloseKind: pendingKind, FinalStatus: pendingStatus, FinalErr: pendingErr})
	}

	if !plan.resume {
		return applyTerminate(s, plan, at), nil
	}

	// ── Resume outcome ────────────────────────────────────────────────────────
	applyPlanRecordClearing(s, plan)
	s.Status = StatusRunning
	// Every resume clears EndedAt: a Running instance must never carry an end
	// timestamp. Load-bearing after a reverse; defensive for throw/partial (a
	// non-terminal instance never has EndedAt set post-hardening) — one cheap
	// assignment that keeps the invariant true on all resume paths (finding #4).
	s.EndedAt = nil
	if plan.resetVars {
		s.Variables = copyVars(s.StartVariables)
	} else if plan.restoreVars != nil {
		// Target reverse (FU#1, ADR-0116): restore the resume target's
		// start-of-visit snapshot. copyVars protects the retained compensation
		// record's Input map from later mutation by the resumed instance.
		s.Variables = copyVars(plan.restoreVars)
	}
	// The resume target's scope can be GONE by the time the walk drains. Only a
	// compensation throw carries a non-empty resumeScope (walkPartial and
	// walkReverse always resume at the root scope), and a throw resumes past
	// itself, so sibling branches keep running and can destroy that scope
	// underneath the walk. exitSubprocessScope holds its own exit for exactly
	// this reason (ADR-0171), but it is not the only route: an error boundary on
	// the enclosing sub-process must fire, so its teardown cannot be deferred,
	// and exitNestedEventSubprocessScope closes the ENCLOSING scope when a nested
	// event sub-process is the last thing running inside it — neither consults
	// the hold. Both were measured to place the resume token in a pruned scope,
	// after which drive's defForScope failed and every subsequent Step returned
	// `workflow-engine: defForScope: unknown scope …`.
	//
	// Dropping the resume is the recovery: the branch the throw belonged to no
	// longer exists, and whatever destroyed its scope has already routed the
	// process onward (a boundary target, or the grandparent resume). Erroring
	// instead left the walk's own ActionCompleted permanently unappliable.
	resumeDropped := plan.resumeScope != "" && s.scopeByID(plan.resumeScope) == nil
	if resumeDropped {
		slog.WarnContext(ctx, "compensation walk resume scope no longer exists; dropping the resume",
			"instance_id", s.InstanceID,
			"resume_node", plan.resumeAt,
			"resume_scope", plan.resumeScope,
		)
	} else {
		s.placeTokenInScope(plan.resumeAt, plan.resumeScope, at)
	}
	if plan.popDeferred {
		popOneDeferredThrow(s)
	}
	// A dropped resume can leave NOTHING running — a deferred throw is the only
	// other thing that can put a token back, and there may be none. Completing
	// here is the same decision the scope exit that pruned the scope would have
	// taken had the cursor been clear: ADR-0168 only DEFERS that completion until
	// the walk drains, and this is where it drains. Without it the instance sits
	// Running with no token and no route to any terminal state.
	//
	// Guarded on resumeDropped, not on the token count alone: every other resume
	// has just placed its own token, so the count can only be zero here.
	if resumeDropped && len(s.Tokens) == 0 {
		return StepResult{State: *s, Commands: s.endInstance(StatusCompleted, at,
			CompleteInstance{Result: copyVars(s.Variables)})}, nil
	}
	var preDriveCmds []Command
	if plan.rearmRootESP {
		// beginCompensation never sweeps s.EventTriggeredSubprocesses when a walk
		// starts, so a root-scope arm from before the walk (or a leftover
		// one-shot removal from an earlier interrupting fire) may still be
		// present. Drop any stale root-scope arms first (emitting CancelTimer
		// for timer-triggered ones) so the re-arm below is idempotent instead
		// of appending a duplicate entry, then re-arm exactly as
		// handleStartInstance does for a fresh StartInstance.
		preDriveCmds = appendCancelTimers(preDriveCmds, s.removeEventTriggeredSubprocessArmsForScope(""))
		espCmds, espErr := armEventTriggeredSubprocesses(def, s, "", at, pol.eval)
		if espErr != nil {
			return StepResult{}, espErr
		}
		preDriveCmds = append(preDriveCmds, espCmds...)
	}
	driveCmds, err := drive(ctx, def, s, at, pol)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: append(preDriveCmds, driveCmds...)}, nil
}

// applyTerminate reproduces the terminal-outcome finish: map an unset FinalStatus
// to StatusTerminated, clear the walk's records, then hand the terminal
// transition to endInstance — which stamps the status and EndedAt, reconciles the
// human-task projection (a parked UserTask on a sibling branch must not be left
// open once the instance terminates — ADR-0088/0089), emits FailInstance when a
// finalErr is set, and cancels outstanding timers/arms/boundaries. Command list
// and ordering are unchanged from the pre-refactor terminate branch; only the
// record clearing moved ahead of the status assignment, which it never read.
func applyTerminate(s *InstanceState, plan finishPlan, at time.Time) StepResult {
	finalStatus := plan.finalStatus
	if finalStatus == 0 {
		finalStatus = StatusTerminated
	}

	// Terminate plans never set scopeWideThrow (a scope-wide throw always resumes),
	// so this nils the whole scope list as before; routed through the shared helper
	// for a single clearing path. It reads only finishPlan fields and never inspects
	// s.Status, so running it BEFORE the status assignment that moved into
	// endInstance is behaviour-preserving (ADR-0164).
	applyPlanRecordClearing(s, plan)

	// endInstance's cursor clear is REDUNDANT here — stepCompensationFinish already
	// zeroed s.Compensating one call up — and deliberately not special-cased: one
	// unconditional terminal path is the point (ADR-0164).
	var terminal Command
	if plan.finalErr != "" {
		terminal = FailInstance{Err: plan.finalErr}
	}
	cmds := s.endInstance(finalStatus, at, terminal)
	return StepResult{State: *s, Commands: cmds}
}

// handleResolveCompensationStall applies one of the three operator escapes from
// a stalled compensation walk (ADR-0175).
//
// Guards first, all three shared by every verb:
//
//   - no walk in flight                     -> ErrNoCompensationWalk
//   - CommandID != cursor's ActiveCmdID     -> ErrCompensationCommandMismatch
//   - non-empty IncidentID naming nothing   -> ErrStallIncidentNotFound
//
// Each refusal also logs a Warn: an operator escape that is refused is exactly
// the event an incident review needs to see, and mirroring dispatch's own
// refusal logging keeps the record in one style.
//
// A terminal instance never reaches here — dispatch's structural guard returns
// ErrInstanceTerminal first (ADR-0165), which is why the walk-in-flight check
// below can be about the CURSOR rather than about liveness.
func handleResolveCompensationStall(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t ResolveCompensationStall, pol stepPolicy) (StepResult, error) {
	cur := s.Compensating
	if s.Status != StatusCompensating || cur.ActiveCmdID == "" {
		slog.WarnContext(ctx, "compensation stall escape refused: no walk in flight",
			"instance_id", s.InstanceID,
			"command_id", t.CommandID,
			"disposition", t.Disposition.String(),
			"status", s.Status.String(),
		)
		return StepResult{}, ErrNoCompensationWalk
	}
	if t.CommandID != cur.ActiveCmdID {
		slog.WarnContext(ctx, "compensation stall escape refused: command id does not match the walk",
			"instance_id", s.InstanceID,
			"command_id", t.CommandID,
			"active_command_id", cur.ActiveCmdID,
			"disposition", t.Disposition.String(),
		)
		return StepResult{}, ErrCompensationCommandMismatch
	}
	if t.IncidentID != "" && !s.hasOpenStallIncident(t.IncidentID) {
		slog.WarnContext(ctx, "compensation stall escape refused: no such stall incident",
			"instance_id", s.InstanceID,
			"incident_id", t.IncidentID,
			"disposition", t.Disposition.String(),
		)
		return StepResult{}, ErrStallIncidentNotFound
	}

	switch t.Disposition {
	case CompensationRetry:
		return retryStalledCompensation(ctx, def, s, cur, t.OccurredAt(), pol)
	case CompensationSkip:
		// stepCompensationAdvance retires the stall incident itself, and takes the
		// byte-identical path a returned ActionFailed already takes.
		return stepCompensationAdvance(ctx, def, s, t.OccurredAt(), pol)
	case CompensationAbandon:
		return abandonCompensationWalk(ctx, def, s, cur, t.OccurredAt(), pol)
	default:
		return StepResult{}, fmt.Errorf("workflow-engine: unknown compensation disposition %d", t.Disposition)
	}
}

// retryStalledCompensation re-dispatches the record the walk stalled on, under a
// fresh command id, and re-arms the stall guard. The cursor's NextIndex does NOT
// move: this is the same record, tried again.
func retryStalledCompensation(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, cur compensationCursor, at time.Time, pol stepPolicy) (StepResult, error) {
	records := cursorRecords(s, cur)
	// Retry needs its OWN bounds check. ADR-0171's third disjunct in
	// stepCompensationAdvance guards NextIndex-1, not NextIndex, so it says
	// nothing about the index retry is about to read — and a naive
	// records[cur.NextIndex] PANICS inside the pure core (measured: len(records)=0
	// at NextIndex=1, on a pre-ADR-0171 cursor whose record source vanished under
	// it after a restart).
	if cur.NextIndex < 0 || cur.NextIndex >= len(records) {
		// The source shrank or vanished: there is nothing left to retry, so route
		// to the walk's finish exactly as a completed advance would. The finish
		// DRIVES (a resuming walk places a token and advances it), so it needs the
		// real ctx, def and timestamp — passing nil/zero here panicked in drive on
		// def.Node.
		s.retireCompensationStallIncidents(cur.ActiveCmdID)
		return stepCompensationFinish(ctx, def, s, cur.ToNode, at, pol)
	}
	rec := records[cur.NextIndex]
	// Retire the incidents against the OLD command id, BEFORE the cursor is
	// overwritten below — afterwards ActiveCmdID names the retry and matches
	// nothing. This is the same ordering constraint retryFailedCompensation
	// observes, for the same reason.
	//
	// BOTH walk-scoped kinds, not just the stall. This verb re-dispatches the same
	// record under a fresh command id, which is the identical "this attempt is
	// superseded" event retryFailedCompensation retires for — and ADR-0179
	// Decision 6 bounds the failure record at ONE PER EXHAUSTED RECORD, not one
	// per attempt. Since ADR-0175's verb has no cap, retiring only the stall kind
	// grows the count without bound: measured, three open
	// IncidentCompensationFailed records after two operator retries, one naming
	// each superseded command.
	//
	// ⚠ Scoped to cur.ActiveCmdID, never to the kind. The record raised by the
	// FINAL failure is the durable evidence of an unrecoverable compensation, and
	// no re-dispatch supersedes it — armCompensationRetryTimer declines once the
	// budget is spent and the walk skips and continues (Decision 7), so nothing
	// retires it. A kind-wide sweep here would delete the one outcome ADR-0179
	// exists to make visible.
	//
	// ⚠ Deliberately NOT mirrored into the bounds-check branch above. That branch
	// routes to the walk's FINISH because the record source vanished — no retry
	// goes out, so the attempt is not superseded by anything and its failure record
	// must survive. Only the stall retirement belongs there, where the guard is
	// simply no longer owed.
	s.retireCompensationStallIncidents(cur.ActiveCmdID)
	s.retireCompensationFailedIncidents(cur.ActiveCmdID)
	cmdID := s.nextCommandID()
	cur.ActiveCmdID = cmdID
	// Reset the ADR-0179 retry cursor alongside ActiveCmdID. This verb can arrive
	// DURING a live backoff — armCompensationRetryTimer arms one and leaves the
	// same command active, which is exactly the state this function's own guards
	// accept — and armCompensationStallTimer's cancel half below sweeps every
	// walk-scoped record, the retry backoff included. Leaving RetryTimerID naming
	// the swept record makes handleActionFailed's idempotency guard answer the
	// re-dispatch's NEXT genuine failure as a redelivery: no incident, no backoff,
	// no advance, and the instance stays StatusCompensating with nothing armed to
	// move it (measured: cmds=[] incidents unchanged timers=0).
	//
	// RetryAttempts goes with it. The operator's retry is a fresh attempt at this
	// record, not a continuation of the budget the superseded dispatch spent —
	// keeping the count would silently shorten the escape hatch's own budget, and
	// under MaxAttempts:1 would exhaust it before the first backoff.
	//
	// ⚠ retryFailedCompensation deliberately does NOT zero RetryAttempts: there
	// the budget is being CONSUMED by the engine, not restarted by an operator.
	// stepCompensationAdvance zeroes both because it moves to a new record.
	cur.RetryAttempts = 0
	cur.RetryTimerID = ""
	s.Compensating = cur
	// Deliberately NOT consumeDispatchedRecord: ownership of this record
	// transferred at the ORIGINAL dispatch (ADR-0173). Consuming it again would
	// shrink the walk's teardown window a second time for one record.
	s.recordCompensationDispatch(cmdID)
	cmds := []Command{compensationInvoke(rec, cmdID)}
	cmds = append(cmds, armCompensationStallTimer(s, pol, rec.NodeID)...)
	return StepResult{State: *s, Commands: cmds}, nil
}

// retryFailedCompensation is the TimerCompensationRetry fire handler (ADR-0179
// Decision 2): the backoff armed after an ActionFailed has elapsed, so the
// record the walk still has in flight is re-dispatched under a FRESH command id.
// The cursor's NextIndex does not move — this is the same record, tried again.
//
// ⚠ It is a NEW function and deliberately not a generalisation of
// retryStalledCompensation, which looks superficially identical. Source-verified,
// that one retires STALL incidents on both of its branches (the wrong incident
// here), arms armCompensationStallTimer as its only timer (the wrong kind), and
// has no policy, no attempt counter and no exhaustion branch. What the two do
// share — the bounds check and the recordCompensationDispatch call — is mirrored
// here rather than extracted, because the surrounding decisions differ.
//
// Guards, in order, mirroring handleCompensationStallFired's:
//   - not compensating any more: the walk finished under the backoff. Drop the
//     record, no-op.
//   - CommandID no longer matches ActiveCmdID: a LATE fire against a command the
//     walk has already moved past. Drop the record, no-op.
//   - NextIndex out of range for the record source: the source shrank or vanished
//     (a cursor persisted before ADR-0171, whose live-read fallback now returns
//     nothing). Route to the walk's finish, as retryStalledCompensation does —
//     records[NextIndex] on that shape PANICS inside the pure engine core, i.e.
//     in the consumer's process.
//
// RetryAttempts is deliberately left alone: it was incremented when the backoff
// was ARMED, and it counts attempts for THIS record until the walk advances past
// it.
func retryFailedCompensation(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, rec timerRecord, at time.Time, pol stepPolicy) (StepResult, error) {
	s.removeTimer(rec.TimerID)
	if s.Status != StatusCompensating || rec.CommandID != s.Compensating.ActiveCmdID {
		return StepResult{State: *s, Commands: nil}, nil
	}
	cur := s.Compensating
	records := cursorRecords(s, cur)
	if cur.NextIndex < 0 || cur.NextIndex >= len(records) {
		// The finish DRIVES (a resuming walk places a token and advances it), so it
		// needs the real ctx, def and timestamp — the same reason
		// retryStalledCompensation's own bounds branch passes them through.
		return stepCompensationFinish(ctx, def, s, cur.ToNode, at, pol)
	}
	r := records[cur.NextIndex]
	// Retire the failure incident for the OLD command id, BEFORE the cursor is
	// overwritten below — afterwards ActiveCmdID names the retry and matches
	// nothing. This is the same ordering constraint retryStalledCompensation
	// observes for the stall kind, for the same reason.
	//
	// The attempt this incident describes is superseded by the dispatch two lines
	// down, so keeping it would accumulate one incident PER ATTEMPT. What ADR-0179
	// Decision 6 promises is one per exhausted record: the LAST attempt's incident
	// is never retired here, because no further re-dispatch happens once the
	// budget is spent (armCompensationRetryTimer returns false and the walk skips
	// and continues instead).
	s.retireCompensationFailedIncidents(cur.ActiveCmdID)
	cmdID := s.nextCommandID()
	cur.ActiveCmdID = cmdID
	// The backoff is over. Clearing this re-opens the idempotency window in
	// handleActionFailed: the NEXT ActionFailed for this record is a real failure
	// to be counted, not a redelivery of the one already being retried.
	cur.RetryTimerID = ""
	s.Compensating = cur
	// Deliberately NOT consumeDispatchedRecord, for the reason retryStalledCompensation
	// gives: ownership of this record transferred at the ORIGINAL dispatch
	// (ADR-0173), and consuming it again would shrink the walk's teardown window a
	// second time for one record.
	s.recordCompensationDispatch(cmdID)
	cmds := []Command{compensationInvoke(r, cmdID)}
	cmds = append(cmds, armCompensationStallTimer(s, pol, r.NodeID)...)
	return StepResult{State: *s, Commands: cmds}, nil
}

// abandonCompensationWalk ends the walk and terminates the instance, retaining
// records [0 .. NextIndex-1] and DROPPING the record at NextIndex.
func abandonCompensationWalk(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, cur compensationCursor, at time.Time, pol stepPolicy) (StepResult, error) {
	// Accepted ONLY on a walk that finishes by terminating. stepCompensationFinish
	// picks its plan from walkMode(), so a resuming walk would take a RESUME plan
	// and the terminate intent would be silently discarded — measured destroying
	// un-run records on a throw walk.
	if cur.walkMode() != walkAdmin {
		slog.WarnContext(ctx, "compensation stall escape refused: this walk resumes rather than terminates",
			"instance_id", s.InstanceID,
			"command_id", cur.ActiveCmdID,
			"disposition", CompensationAbandon.String(),
		)
		return StepResult{}, ErrCompensationWalkResumes
	}
	// Retain exactly what the walk never dispatched. On a beginCompensation walk
	// the cursor is UNPINNED, so consumeDispatchedRecord early-returns and
	// RootCompensations still holds every record — run or not. Retaining the whole
	// list therefore re-dispatches an already-run compensation on the later admin
	// rollback (measured [undoB undoA] with undoB already run); retaining the
	// prefix keeps strictly the un-dispatched ones.
	//
	// The record AT NextIndex is dropped because its action may still be in flight
	// at a worker. Accepted cost: if it genuinely never ran, that undo work is
	// lost — retry is the verb for that case, and skip makes the same trade.
	retained := retainedRecordPrefix(s, cur.ScopeID, cur.NextIndex)
	// Retire the incident HERE, before delegating. This is LOAD-BEARING and must
	// not be mistaken for defence-in-depth: stepCompensationFinish clears
	// s.Compensating BEFORE it calls applyFinish, so endInstance's own remainder
	// sweep runs with ActiveCmdID == "" and early-returns. Nothing downstream will
	// retire this incident.
	//
	// ⚠ Deleting this line leaves the ENGINE SUITE GREEN unless
	// TestAbandonRetiresTheStallIncident exists — an earlier revision read that
	// green run as "redundant" and said so in this comment. It was untested, not
	// redundant. Without the call an abandoned walk terminates carrying a stale
	// "compensation action stalled" record, which incident_count, the service/
	// audit view and every reader of InstanceState.Incidents then report. (Before
	// ADR-0179 that record also reached runtime/outbox.go's terminalEventErr and
	// processdriver_action.go's terminalErr as the published cause of death; both
	// now go through the causeOfDeathIncident allow-list, which admits
	// IncidentAction only.)
	s.retireCompensationStallIncidents(cur.ActiveCmdID)
	// Delegate with the walk's own recorded outcome.
	//
	// ⚠ Abandon does NOT run applyFinish's consumePendingCancel path: walkAdmin's
	// finishPlan sets resume:false and leaves consumePendingCancel unset, and
	// applyFinish gates that branch on plan.resume. A PendingCancel is therefore
	// left set on the terminated instance. ADR-0175 claimed abandon discharges the
	// deferred-cancel deadlock; it cannot — PendingCancel is only ever stamped on
	// walks that RESUME, which abandon refuses. Skip is that verb.
	res, err := stepCompensationFinish(ctx, def, s, cur.ToNode, at, pol)
	if err != nil {
		return StepResult{}, err
	}
	// Re-install the retained prefix AFTER the finish, not before: walkAdmin's
	// finishPlan sets doClearRecords, so applyPlanRecordClearing nils the whole
	// scope list on its way out and any earlier trim is simply erased (measured —
	// root=[] where [step1 step2] was owed, and the later admin rollback then
	// refused with "nothing left to compensate").
	//
	// Written onto res.State rather than s: StepResult carries a shallow COPY of
	// the state, so mutating s here would not be visible to the caller.
	setScopeRecords(&res.State, cur.ScopeID, retained)
	return res, nil
}
