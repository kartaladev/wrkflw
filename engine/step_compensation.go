package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
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
	// If a compensation walk is already in flight, ignore the redundant request:
	// restarting beginCompensation would re-walk records that are still
	// mid-consumption and re-emit the in-flight compensation (double-compensation).
	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
		return StepResult{State: *s}, nil
	}
	s.Status = StatusCompensating
	return beginCompensation(def, s, t.ToNode, 0, "", t.OccurredAt(), mode, eval)
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
func beginCompensation(def *model.ProcessDefinition, s *InstanceState, toNode string, finalStatus Status, finalErr string, at time.Time, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
	// Cancel all in-flight tokens (interrupting normal execution).
	// Also emit CancelTimer for any outstanding timers, armed events, and boundaries.
	var preCmds []Command

	// Snapshot tokens to cancel (avoid mutating while iterating).
	tokensToCancel := make([]Token, len(s.Tokens))
	copy(tokensToCancel, s.Tokens)
	for _, tok := range tokensToCancel {
		// Cancel deadline/reminder timers for this token.
		for _, timerID := range s.cancelTimersByTaskToken(tok.AwaitCommand, "") {
			preCmds = append(preCmds, CancelTimer{TimerID: timerID})
		}
		// Cancel boundary arms.
		for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
			preCmds = append(preCmds, CancelTimer{TimerID: timerID})
		}
		// Cancel event-gateway arms.
		if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
			for _, timerID := range s.removeArmedEventsForGateway(tok.ID) {
				preCmds = append(preCmds, CancelTimer{TimerID: timerID})
			}
		}
		tokPtr := s.tokenByID(tok.ID)
		if tokPtr != nil {
			s.consumeToken(tokPtr, at)
		}
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
				s.Compensating = compensationCursor{FinalStatus: finalStatus, FinalErr: finalErr}
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
		// No records at all — apply the terminal outcome immediately.
		// Stamp the cursor so stepCompensationFinish reads the outcome fields.
		s.Compensating = compensationCursor{FinalStatus: finalStatus, FinalErr: finalErr}
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
		ScopeID:     scopeID,
		ToNode:      toNode,
		NextIndex:   startIndex,
		ActiveCmdID: cmdID,
		FinalStatus: finalStatus,
		FinalErr:    finalErr,
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
	// the Phase 3 fields (ArchiveKey, ResumeNode, ResumeScope) — so that
	// stepCompensationFinish can use them when the walk eventually ends.
	rec := records[nextIdx]
	cmdID := s.nextCommandID()
	s.Compensating = compensationCursor{
		ScopeID:     cur.ScopeID,
		ArchiveKey:  cur.ArchiveKey,
		ResumeNode:  cur.ResumeNode,
		ResumeScope: cur.ResumeScope,
		ToNode:      cur.ToNode,
		NextIndex:   nextIdx,
		ActiveCmdID: cmdID,
		FinalStatus: cur.FinalStatus,
		FinalErr:    cur.FinalErr,
	}
	cmd := InvokeAction{
		CommandID: cmdID,
		Name:      rec.Action,
		Input:     copyVars(rec.Input),
	}
	return StepResult{State: *s, Commands: []Command{cmd}}, nil
}

// stepCompensationFinish finalises the compensation walk:
//   - If ResumeNode != "" (compensation throw walk): delete the archive entry,
//     set Status = StatusRunning, place a token at ResumeNode in ResumeScope,
//     and drive forward (resuming execution past the throw event).
//   - If toNode != "" (partial rollback via CompensateRequested): place a token
//     at toNode, set Status = StatusRunning, and drive forward.
//   - If toNode == "" and ResumeNode == "": full rollback — apply the cursor's
//     terminal FinalStatus (StatusTerminated / StatusFailed).
func stepCompensationFinish(def *model.ProcessDefinition, s *InstanceState, toNode string, at time.Time, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
	// Save outcome fields AND cursor metadata BEFORE clearing the cursor.
	finalStatus := s.Compensating.FinalStatus
	finalErr := s.Compensating.FinalErr
	scopeID := s.Compensating.ScopeID
	archiveKey := s.Compensating.ArchiveKey
	resumeNode := s.Compensating.ResumeNode
	resumeScope := s.Compensating.ResumeScope
	// Clear the cursor — compensation walk is done.
	s.Compensating = compensationCursor{}

	// ── Phase 3: compensation throw resume branch ─────────────────────────────
	// A throw walk always has a non-empty ResumeNode. When the walk finishes,
	// we delete the archive entry (single ownership: a second throw to the same
	// ref finds len == 0 and becomes a no-op; a later cancel walk also won't
	// re-run them because the archive key is already gone), resume status to
	// Running, and place a token at the throw's successor.
	if resumeNode != "" {
		// Remove the archive entry (consume semantics — single ownership).
		if archiveKey != "" && s.ArchivedCompensations != nil {
			delete(s.ArchivedCompensations, archiveKey)
		}
		if s.PendingCancel {
			// A cancel arrived mid-throw-walk. The throw's target is now compensated and
			// removed from the archive, so a full cancel over the REMAINING records
			// (root + other archives) cannot double-run it. Run it and terminate.
			s.PendingCancel = false
			s.Status = StatusCompensating
			return beginCompensation(def, s, "", StatusTerminated, "cancelled", at, mode, eval)
		}
		s.Status = StatusRunning
		// Place the resume token in the correct scope (the throw token's scope).
		s.placeTokenInScope(resumeNode, resumeScope, at)
		// SERIALIZE (ADR-0071): if compensation throws were deferred while this walk
		// was in flight, re-activate exactly ONE now. The cursor was just cleared
		// (ActiveCmdID == ""), so the subsequent drive re-enters the throw handler for
		// that token and starts its walk through the normal walk-start path — no logic
		// duplication. Popping one-per-finish keeps at most one walk in flight; any
		// further deferred throws stay queued and drain as each walk completes.
		if len(s.DeferredCompensationThrows) > 0 {
			deferredTok := s.DeferredCompensationThrows[0]
			s.DeferredCompensationThrows = s.DeferredCompensationThrows[1:]
			if tok := s.tokenByID(deferredTok); tok != nil {
				tok.State = TokenActive
			}
		}
		driveCmds, err := drive(def, s, at, mode, eval)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: *s, Commands: driveCmds}, nil
	}

	// ── Partial rollback (CompensateRequested with non-empty ToNode) ──────────
	if toNode != "" {
		// Records are intentionally RETAINED here (not cleared): the instance
		// keeps running and a later full walk must still see them. There is no
		// double-compensation risk — consolidateArchiveIntoRoot already drained
		// the archive into RootCompensations (single ownership: the records now
		// live only in root, with ArchivedCompensations nil).
		s.Status = StatusRunning
		// Place a new token at toNode and drive forward.
		s.placeToken(toNode, at)
		driveCmds, err := drive(def, s, at, mode, eval)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: *s, Commands: driveCmds}, nil
	}

	// ── Full rollback (toNode == "" and ResumeNode == "") ─────────────────────
	// Apply the cursor's terminal outcome.
	// Zero FinalStatus (== StatusRunning, the iota-0 constant) means UNSET;
	// map it to StatusTerminated. Safe: full-rollback finish is always
	// terminal, so no caller of beginCompensation ever wants a non-terminal
	// outcome here. The admin path (CompensateRequested) leaves FinalStatus
	// zero; error/cancel paths set it explicitly (StatusFailed / StatusTerminated).
	if finalStatus == 0 {
		finalStatus = StatusTerminated
	}
	s.Status = finalStatus
	ended := at
	s.EndedAt = &ended

	// Clear the compensation records for the walk's scope so that a re-delivered
	// CancelRequested (or any other terminal trigger) on an already-terminal instance
	// cannot re-enter the walk and double-compensate money-moving actions.
	// beginCompensation today always uses scopeID="" (root scope); future scope-targeted
	// walks (ADR-0035) will set a non-empty scopeID, which the else branch handles.
	if scopeID == "" {
		s.RootCompensations = nil
	} else {
		if sc := s.scopeByID(scopeID); sc != nil {
			sc.Compensations = nil
		}
	}

	var cmds []Command
	// Reconcile the human-task projection at the terminal of every compensation
	// walk: a parked UserTask on a sibling branch must not be left open in the
	// TaskStore once the instance is terminated/failed (ADR-0088/0089). Idempotent
	// for cancel-with-compensation, whose tasks were already cancelled at trigger.
	cmds = append(cmds, s.cancelOpenTasks()...)
	if finalErr != "" {
		cmds = append(cmds, FailInstance{Err: finalErr})
	}
	cmds = append(cmds, s.cancelAllTimers()...)
	cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
	return StepResult{State: *s, Commands: cmds}, nil
}
