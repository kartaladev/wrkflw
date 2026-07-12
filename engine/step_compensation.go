package engine

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
)

// compensationRecordsForScope returns a read-only slice of CompensationRecords for
// the given scope. scopeID "" means the root scope (s.RootCompensations).
func compensationRecordsForScope(s *InstanceState, scopeID string) []CompensationRecord {
	if scopeID == "" {
		return s.RootCompensations
	}
	sc := s.scopeByID(scopeID)
	if sc == nil {
		return nil
	}
	return sc.Compensations
}

// cursorRecords returns the CompensationRecord slice for the current compensation
// walk described by cur. When cur.ArchiveKey is non-empty, the records are drawn
// from s.ArchivedCompensations[cur.ArchiveKey] (a scope-targeted throw walk);
// otherwise compensationRecordsForScope is used (admin / cancel / error walks).
func cursorRecords(s *InstanceState, cur compensationCursor) []CompensationRecord {
	if cur.ArchiveKey != "" {
		return s.ArchivedCompensations[cur.ArchiveKey]
	}
	return compensationRecordsForScope(s, cur.ScopeID)
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
func stepCompensateRequested(def *model.ProcessDefinition, s *InstanceState, t CompensateRequested, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
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
	// Checked first, ahead of the state-dependent guards below, because it is
	// a pure trigger-shape validation independent of s.Status.
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
	// Reject a reverse trigger (ADR-0109 ReverseInstance) against an
	// already-terminal instance instead of silently resurrecting it. This is a
	// defense-in-depth guard: the runtime facade (ProcessDriver.ReverseInstance)
	// already rejects a terminal instance on its own pre-check Load, but a
	// concurrent completion between that Load and this Step call (TOCTOU) would
	// otherwise slip through undetected. Scoped STRICTLY to reverse intent
	// (t.ReverseNode != "") — a plain admin/partial CompensateRequested keeps
	// today's behaviour (e.g. cancel/error terminal paths that re-deliver
	// CompensateRequested on an already-terminal instance as a no-op-ish
	// full rollback).
	if t.ReverseNode != "" && s.Status.IsTerminal() {
		return StepResult{}, fmt.Errorf("workflow-engine: cannot reverse a terminal instance (status %v)", s.Status)
	}
	s.Status = StatusCompensating
	return beginCompensation(def, s, t.ToNode, 0, "", t.OccurredAt(), mode, eval, t.ReverseNode, t.ResetVars, t.RestoreTargetVars)
}

// beginCompensation is the shared initiator of a reverse-order compensation walk.
// It is called by stepCompensateRequested (admin path, finalStatus=0, finalErr="")
// and will be called by the error/cancel terminal paths (Task 2) with the
// appropriate outcome.
//
// s.Status must already be set to StatusCompensating by the caller.
//
// beginCompensation:
//  1. Cancels all in-flight tokens, timers, armed events, and boundaries (emitting
//     CancelTimer commands for each).
//  2. Looks up the root-scope (scopeID="") compensation records.
//  3. Validates toNode: if non-empty and not found in records, returns an error; if
//     toNode is the last (most-recently completed) record, calls stepCompensationFinish
//     immediately with the cancel commands prepended (nothing to compensate above it).
//  4. If there are no eligible records (empty records list), calls
//     stepCompensationFinish immediately, applying finalStatus/finalErr via the cursor.
//  5. Otherwise sets compensationCursor (stamping finalStatus and finalErr for the
//     terminal outcome) and emits the first compensation InvokeAction for the
//     most-recently completed record (reverse walk).
//
// The FinalStatus/FinalErr values are carried on the cursor across all advance steps
// so that stepCompensationFinish applies them when the walk completes.
// reverseNode/reverseResetVars carry the ReverseInstance full-reverse intent
// (ADR-0109): when reverseNode is non-empty, a FULL-rollback finish resumes at
// reverseNode (StatusRunning) — optionally resetting Variables to StartVariables —
// instead of terminating. All cancel/error/throw callers pass "", false so their
// terminate behaviour is unchanged.
// restoreTargetVars carries the FU#1 target-reverse intent (ADR-0116): when true
// (only the NewReverseToNode path sets it, always alongside a non-empty toNode),
// the PARTIAL-rollback finish restores Variables to toNode's start-of-visit
// snapshot. Cancel/error/throw/admin/full-reverse callers pass false so their
// variable handling is unchanged.
func beginCompensation(def *model.ProcessDefinition, s *InstanceState, toNode string, finalStatus Status, finalErr string, at time.Time, mode StepMode, eval ConditionEvaluator, reverseNode string, reverseResetVars bool, restoreTargetVars bool) (StepResult, error) {
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
		preCmds = append(preCmds, cancelTokenWaits(s, &tok, at)...)
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
	records := compensationRecordsForScope(s, scopeID)

	// Determine the starting index: the last record whose NodeID != toNode.
	// We walk from the end of records backward; the first record (from the right)
	// that is NOT the ToNode is the starting point.
	//
	// Because records are stored in completion order (oldest first), the reverse
	// walk is: len(records)-1 down to 0.
	//
	// Find how many records are eligible (those AFTER ToNode in completion order).
	// If ToNode == "", all records are eligible.
	// If ToNode != "", we exclude the record whose NodeID == ToNode (it is the
	// rollback TARGET — we do not compensate it). All records recorded AFTER ToNode
	// (i.e. later in the slice) are eligible.
	startIndex := len(records) - 1
	if toNode != "" {
		// Find the index of toNode in the records (it's recorded in completion order).
		toNodeIdx := -1
		for i, r := range records {
			if r.NodeID == toNode {
				toNodeIdx = i
			}
		}
		if toNodeIdx >= 0 {
			// Only records AFTER toNodeIdx (i.e. indices > toNodeIdx) are eligible.
			// The first to emit is the last eligible record.
			if toNodeIdx >= len(records)-1 {
				// ToNode was the last completed node — nothing to compensate above it.
				// Stamp the cursor so stepCompensationFinish applies the outcome; since
				// toNode != "", stepCompensationFinish takes the partial-rollback branch
				// (resume at toNode) regardless of FinalStatus/FinalErr — which is correct
				// for the admin path (outcome fields are zero).
				s.Compensating = compensationCursor{FinalStatus: finalStatus, FinalErr: finalErr, ReverseNode: reverseNode, ReverseResetVars: reverseResetVars, RestoreTargetVars: restoreTargetVars}
				finishRes, finishErr := stepCompensationFinish(def, s, toNode, at, mode, eval)
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
		finishRes, finishErr := stepCompensationFinish(def, s, toNode, at, mode, eval)
		if finishErr != nil {
			return StepResult{}, finishErr
		}
		finishRes.Commands = append(preCmds, finishRes.Commands...)
		return finishRes, nil
	}

	// Emit the first compensation InvokeAction (record at startIndex).
	rec := records[startIndex]
	cmdID := s.nextCommandID()
	s.Compensating = compensationCursor{
		ScopeID:           scopeID,
		ToNode:            toNode,
		NextIndex:         startIndex,
		ActiveCmdID:       cmdID,
		FinalStatus:       finalStatus,
		FinalErr:          finalErr,
		ReverseNode:       reverseNode,
		ReverseResetVars:  reverseResetVars,
		RestoreTargetVars: restoreTargetVars,
	}
	cmd := InvokeAction{
		CommandID: cmdID,
		Name:      rec.Action,
		Input:     copyVars(rec.Input),
	}
	cmds := append(preCmds, cmd)
	return StepResult{State: *s, Commands: cmds}, nil
}

// stepCompensationAdvance advances the compensation cursor after a compensation
// InvokeAction completes (ActionCompleted with cursor.ActiveCmdID). It emits the
// next InvokeAction in reverse order, or finalises compensation if the walk is done.
func stepCompensationAdvance(def *model.ProcessDefinition, s *InstanceState, at time.Time, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
	cur := s.Compensating
	// Use cursorRecords so that throw walks (ArchiveKey != "") read from the
	// archive and admin/cancel/error walks read from the live scope.
	records := cursorRecords(s, cur)

	// Advance: the record we just completed is at cur.NextIndex. Move to the
	// previous record (next in reverse order). nextIdx = cur.NextIndex - 1.
	nextIdx := cur.NextIndex - 1

	// Determine the stop boundary: the index of ToNode (exclusive, i.e. we stop
	// BEFORE emitting that index's compensation).
	toNodeIdx := -1
	if cur.ToNode != "" {
		for i, r := range records {
			if r.NodeID == cur.ToNode {
				toNodeIdx = i
			}
		}
	}

	// Check if next record is within the eligible range.
	// Eligible: nextIdx >= 0 AND nextIdx > toNodeIdx (i.e. the record is AFTER ToNode).
	if nextIdx < 0 || nextIdx <= toNodeIdx {
		// Walk complete: either exhausted all records, or reached ToNode boundary.
		return stepCompensationFinish(def, s, cur.ToNode, at, mode, eval)
	}

	// Emit the next compensation action. Preserve all cursor fields — including
	// ArchiveKey, ResumeNode, and ResumeScope — so that stepCompensationFinish
	// can use them when the walk eventually ends.
	rec := records[nextIdx]
	cmdID := s.nextCommandID()
	s.Compensating = compensationCursor{
		ScopeID:           cur.ScopeID,
		ArchiveKey:        cur.ArchiveKey,
		ResumeNode:        cur.ResumeNode,
		ResumeScope:       cur.ResumeScope,
		ToNode:            cur.ToNode,
		NextIndex:         nextIdx,
		StartRecordCount:  cur.StartRecordCount,
		ActiveCmdID:       cmdID,
		FinalStatus:       cur.FinalStatus,
		FinalErr:          cur.FinalErr,
		ReverseNode:       cur.ReverseNode,
		ReverseResetVars:  cur.ReverseResetVars,
		RestoreTargetVars: cur.RestoreTargetVars,
	}
	cmd := InvokeAction{
		CommandID: cmdID,
		Name:      rec.Action,
		Input:     copyVars(rec.Input),
	}
	return StepResult{State: *s, Commands: []Command{cmd}}, nil
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
func stepCompensationFinish(def *model.ProcessDefinition, s *InstanceState, toNode string, at time.Time, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
	// Save outcome fields AND cursor metadata BEFORE clearing the cursor.
	finalStatus := s.Compensating.FinalStatus
	finalErr := s.Compensating.FinalErr
	scopeID := s.Compensating.ScopeID
	archiveKey := s.Compensating.ArchiveKey
	resumeNode := s.Compensating.ResumeNode
	resumeScope := s.Compensating.ResumeScope
	startRecordCount := s.Compensating.StartRecordCount
	reverseNode := s.Compensating.ReverseNode
	reverseResetVars := s.Compensating.ReverseResetVars
	restoreTargetVars := s.Compensating.RestoreTargetVars
	// Clear the cursor — compensation walk is done.
	s.Compensating = compensationCursor{}

	// Build the finishPlan matching this walk's outcome. Branch order mirrors the
	// pre-refactor precedence: throw resume, then partial, then full-reverse, then
	// terminate. History is intentionally RETAINED across every resume outcome:
	// re-execution appends fresh visits on top, keeping the full run history intact.
	var plan finishPlan
	switch {
	case resumeNode != "":
		// Compensation throw resume: consume the archive entry (single ownership:
		// a second throw to the same ref finds len == 0 and no-ops; a later cancel
		// walk also won't re-run them), resume at the throw's successor in its own
		// scope, and re-activate one deferred throw. A cancel arriving mid-walk
		// preempts the resume and terminates (consumePendingCancel).
		plan = finishPlan{
			resume:               true,
			resumeAt:             resumeNode,
			resumeScope:          resumeScope,
			deleteArchive:        archiveKey,
			popDeferred:          true,
			consumePendingCancel: true,
		}
		if archiveKey == "" {
			// Scope-wide compensate throw (ADR-0120): the drained records came from
			// the throwing scope's LIVE list (RootCompensations or a sub-scope's
			// Compensations), not an archive entry. Clear them here (compensate-once)
			// so a second throw or a later cancel/rollback cannot re-run the
			// already-run compensations. A targeted throw (archiveKey != "") instead
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
		}
	case toNode != "":
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
	case reverseNode != "":
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
	default:
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
	return applyFinish(def, s, plan, at, mode, eval)
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
		return
	}
	clearRecords(s, plan.clearScope)
}

// applyFinish applies a finishPlan: the single site where a compensation walk
// either resumes (Status → Running, EndedAt cleared, token placed, drive) or
// terminates (FinalStatus applied, EndedAt stamped, records cleared). Collapsing
// the four former branches here means the resume/terminate invariants can no
// longer drift apart.
func applyFinish(def *model.ProcessDefinition, s *InstanceState, plan finishPlan, at time.Time, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
	// Throw-walk archive consume (single ownership). No-op for other plans
	// (deleteArchive == "").
	if plan.deleteArchive != "" && s.ArchivedCompensations != nil {
		delete(s.ArchivedCompensations, plan.deleteArchive)
	}

	// A cancel that arrived mid-walk preempts the resume: the walk's target is
	// already compensated (and, for a throw, removed from the archive above), so a
	// full cancel over the REMAINING records cannot double-run it. Terminate.
	if plan.resume && plan.consumePendingCancel && s.PendingCancel {
		s.PendingCancel = false
		// Clear THIS walk's own already-compensated records BEFORE the cancel walk
		// so it cannot re-run them (double-compensation). A TARGETED throw walk
		// (archiveKey != "") deleted its archive above (deleteArchive) and RETAINS
		// RootCompensations — those are genuinely-uncompensated outer records the
		// cancel walk must still compensate (doClearRecords == false, skipped here).
		// A SCOPE-WIDE throw walk (archiveKey == "", ADR-0120) instead compensated
		// the throwing scope's own live records, so — like the full-reverse walk —
		// it clears them here (doClearRecords == true). It clears ONLY the drained
		// prefix (scopeWideThrow, review A1): the re-issued beginCompensation then
		// compensates any sibling record appended mid-walk and terminates
		// (FailInstance{"cancelled"}, StatusTerminated) — the correct compensate-once
		// outcome that preserves the sibling record. A full-reverse walk
		// compensated ALL of RootCompensations and clears the whole list the same way.
		applyPlanRecordClearing(s, plan)
		s.Status = StatusCompensating
		return beginCompensation(def, s, "", StatusTerminated, "cancelled", at, mode, eval, "", false, false)
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
	s.placeTokenInScope(plan.resumeAt, plan.resumeScope, at)
	if plan.popDeferred {
		popOneDeferredThrow(s)
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
		for _, timerID := range s.removeEventTriggeredSubprocessArmsForScope("") {
			preDriveCmds = append(preDriveCmds, CancelTimer{TimerID: timerID})
		}
		espCmds, espErr := armEventTriggeredSubprocesses(def, s, "", at, eval)
		if espErr != nil {
			return StepResult{}, espErr
		}
		preDriveCmds = append(preDriveCmds, espCmds...)
	}
	driveCmds, err := drive(def, s, at, mode, eval)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: append(preDriveCmds, driveCmds...)}, nil
}

// applyTerminate reproduces the terminal-outcome finish: map an unset FinalStatus
// to StatusTerminated, stamp EndedAt, clear the walk's records, then reconcile the
// human-task projection (a parked UserTask on a sibling branch must not be left
// open once the instance terminates — ADR-0088/0089), emit FailInstance when a
// finalErr is set, and cancel outstanding timers/arms/boundaries. Command list
// and ordering are unchanged from the pre-refactor terminate branch.
func applyTerminate(s *InstanceState, plan finishPlan, at time.Time) StepResult {
	finalStatus := plan.finalStatus
	if finalStatus == 0 {
		finalStatus = StatusTerminated
	}
	s.Status = finalStatus
	ended := at
	s.EndedAt = &ended

	// Terminate plans never set scopeWideThrow (a scope-wide throw always resumes),
	// so this nils the whole scope list as before; routed through the shared helper
	// for a single clearing path.
	applyPlanRecordClearing(s, plan)

	var cmds []Command
	cmds = append(cmds, s.cancelOpenTasks()...)
	if plan.finalErr != "" {
		cmds = append(cmds, FailInstance{Err: plan.finalErr})
	}
	cmds = append(cmds, s.cancelAllTimers()...)
	cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
	for _, timerID := range s.removeAllEventTriggeredSubprocessArms() {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	return StepResult{State: *s, Commands: cmds}
}
