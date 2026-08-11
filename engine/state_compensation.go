package engine

import (
	"cmp"
	"maps"
	"slices"
	"time"
)

// CompensationRecord is a record of a completed compensable activity within a
// scope. Plan 8 (compensation/rollback) populates these records when an activity
// with a non-empty CompensateAction completes; it walks them in reverse
// completion order when rolling back the scope.
//
// Fields:
//   - NodeID: the BPMN node ID of the completed compensable activity.
//   - Action: the name of the service action to invoke as compensation.
//   - CompletedAt: the time the activity completed (from the trigger's OccurredAt;
//     never time.Now() — deterministic and clock-free).
//   - Input: a snapshot of the instance variables at the moment the activity was
//     invoked (i.e. the same map passed to InvokeAction). Taken before merging the
//     activity's output so the compensation action receives the original inputs.
//     Deep-copied in cloneState so mutations to the clone do not affect the original.
type CompensationRecord struct {
	// NodeID is the BPMN node ID of the completed compensable activity.
	NodeID string
	// Action is the name of the compensating service action (registered in the
	// service-action catalog) to run when this activity is rolled back.
	Action string
	// CompletedAt is the time the activity completed (from the trigger's OccurredAt).
	CompletedAt time.Time
	// Input is a snapshot of the instance variables at invocation time.
	Input map[string]any
}

// Scope represents an active execution scope within a process instance. Scopes
// are created when a sub-process node is entered and removed when it exits.
// They form a tree via ParentID: the root scope has an empty ParentID.
//
// Fields:
//   - ID: unique scope identifier, assigned deterministically as
//     "<instanceID>-s<ScopeSeq>" (e.g. "inst-1-s1").
//   - NodeID: the BPMN node (typically a sub-process) that opened this scope.
//   - ParentID: the ID of the enclosing scope, or "" if this is a root scope.
//   - Compensations: ordered list of completed compensable activities inside
//     this scope, accumulated as activities finish. Plan 8 reads this list in
//     reverse order when rolling back the scope.
type Scope struct {
	// ID is the unique scope identifier (deterministic, no clock/random).
	ID string
	// NodeID is the BPMN node that opened this scope.
	NodeID string
	// ParentID is the enclosing scope's ID, or "" for a root scope.
	ParentID string
	// Compensations records completed compensable activities in entry order.
	// Plan 8 (compensation) populates and consumes this list.
	Compensations []CompensationRecord
}

// compensationCursor tracks progress through an in-flight reverse-order
// compensation walk (ADR-0039, ADR-0071, ADR-0109, ADR-0116, ADR-0120). It is
// set when a CompensateRequested trigger arrives and cleared when the walk
// completes.
//
// Every field except Records is a plain scalar. Records is a slice whose
// elements carry an Input map, so cloneState deep-copies it explicitly
// (ADR-0171) — a plain struct copy would share the backing array and the
// records' maps with the original.
type compensationCursor struct {
	// ScopeID identifies the scope being compensated ("" = root).
	// Ignored when ArchiveKey is non-empty (archive walk).
	ScopeID string
	// ArchiveKey is the ArchivedCompensations map key for a throw-walk
	// (non-empty). Empty for all beginCompensation (admin / cancel / error)
	// walks, which is how walkMode tells a targeted throw from a scope-wide one.
	//
	// It selects cursorRecords' source — ArchivedCompensations[ArchiveKey]
	// rather than the live scope or RootCompensations — only when Records is
	// nil, i.e. for a cursor persisted before ADR-0171. On every cursor this
	// build writes, the pinned Records wins.
	ArchiveKey string
	// Records pins the walk's record source at walk start (ADR-0171). When
	// non-nil it is what cursorRecords iterates, in place of the live scope /
	// archive read; the walk's indices are then immune to anything that mutates
	// or destroys that source mid-flight.
	//
	// It is set by startCompensationWalk — i.e. for COMPENSATION THROW walks
	// only, which are the only walks that leave sibling tokens running
	// (throw-then-continue). A sibling reaching its scope's end event calls
	// archiveCompensations + closeScope, which nils the very slice a scope-wide
	// throw cursor's ScopeID names; stepCompensationAdvance then indexed an empty
	// slice and PANICKED inside the pure engine core — i.e. in the consumer's
	// process, since this ships as a library.
	//
	// beginCompensation deliberately does NOT pin: its prologue cancels every
	// token, so no concurrent branch survives to mutate its source, and that
	// source is always RootCompensations (its scopeID is the const "") which no
	// scope teardown can nil.
	//
	// nil on a cursor deserialized from a row written before ADR-0171 — that is
	// the case cursorRecords' live-read fallback and stepCompensationAdvance's
	// bounds check exist for.
	Records []CompensationRecord
	// ResumeNode is the node to place a token at when the compensation throw
	// walk finishes (the throw event's single successor). When non-empty,
	// stepCompensationFinish sets Status = StatusRunning and places a token
	// here instead of applying FinalStatus. Empty for admin / cancel / error
	// walks (which always terminate).
	ResumeNode string
	// ResumeScope is the ScopeID of the scope in which the token should be
	// placed at ResumeNode after the throw walk finishes. Empty = root scope.
	// Populated by the compensation throw producer from the throw token's ScopeID.
	ResumeScope string
	// ToNode is the rollback target node ID (exclusive). Empty = full rollback.
	ToNode string
	// ReverseNode, when non-empty, makes the FULL-rollback finish resume at this
	// node (StatusRunning) instead of terminating — the ReverseInstance full-reverse
	// form (ADR-0109). Kept DISTINCT from ResumeNode (the compensation-throw resume)
	// so the throw-walk branch is never triggered by a reverse-to-start walk.
	ReverseNode string
	// ReverseResetVars, when true, resets Variables to StartVariables when the
	// full-rollback finish resumes at ReverseNode.
	ReverseResetVars bool
	// RestoreTargetVars, when true, makes the PARTIAL-rollback finish (ToNode
	// non-empty) restore Variables to ToNode's own start-of-visit snapshot — the
	// Input captured on ToNode's most-recent compensation record — instead of
	// leaving the current variables untouched (FU#1, ADR-0116). Carried on the
	// cursor (all-scalar) from the CompensateRequested trigger; the snapshot map
	// itself is resolved at finish time and lives on the transient finishPlan, so
	// the cursor stays value-copyable by cloneState with no map to deep-copy.
	// Zero (false) for every other walk — admin partial rollback keeps current
	// vars, matching ADR-0109's original WithTargetNode contract.
	RestoreTargetVars bool
	// StartRecordCount is the number of records present in the throwing scope
	// when a SCOPE-WIDE compensation throw walk started (ADR-0120). The walk
	// drains exactly the prefix [0 .. StartRecordCount-1] in reverse; on finish,
	// only that prefix is cleared (compensate-once), retaining any record a still-
	// running sibling appended mid-walk at index >= StartRecordCount (review A1).
	// Zero for every other walk (targeted throw, admin/cancel/error), which clear
	// their whole record source instead. Carried on the cursor (scalar) so
	// cloneState stays a plain value copy.
	StartRecordCount int
	// TeardownArchiveKey, TeardownArchiveOffset and TeardownArchiveCount locate
	// the TEARDOWN WINDOW: the records a mid-walk scope teardown parked in
	// ArchivedCompensations on this walk's behalf (ADR-0173).
	//
	// When a teardown that cannot be deferred destroys the scope a scope-wide
	// throw walk is draining, archiveCompensations keeps the records the walk has
	// NOT yet dispatched — it must, or an abandoned walk loses them — and records
	// where they landed here: the slot key (the closed scope's NodeID), the slot
	// length BEFORE the append, and how many records were parked.
	//
	// consumeDispatchedRecord then removes one as each is dispatched, so the
	// window always holds exactly the un-dispatched remainder, and
	// applyPlanRecordClearing removes whatever survives to the finish (normally
	// nothing). Removing them only at the finish was the shape ADR-0173 first
	// designed, and it halved the abandoned-walk double-run instead of closing it.
	//
	// TeardownArchiveKey == "" means NO TEARDOWN HAPPENED. That is the value on
	// every untorn-down walk and on every cursor persisted before ADR-0173, which
	// is why this needs no data migration in that direction.
	//
	// All three are plain scalars, keeping this struct value-copyable by
	// cloneState (see the Records comment above).
	TeardownArchiveKey    string
	TeardownArchiveOffset int
	TeardownArchiveCount  int
	// NextIndex is the index of the CompensationRecord currently in-flight
	// (most recently emitted). Counts DOWN from len(records)-1 to 0 as
	// compensation actions complete; the next record to emit is NextIndex-1.
	NextIndex int
	// ActiveCmdID is the CommandID of the compensation InvokeAction currently
	// in flight. Cleared when the step completes.
	ActiveCmdID string
	// FinalStatus is the Status the instance must enter when the full-rollback
	// branch of stepCompensationFinish fires (toNode == "" and ResumeNode == "").
	// The zero value (StatusRunning == 0) means UNSET: stepCompensationFinish
	// maps it to StatusTerminated (back-compat; admin full-rollback path and
	// pre-migration in-flight compensations deserialized from JSONB retain the
	// prior Terminated behaviour). Error/cancel paths that trigger compensation
	// set this explicitly: StatusFailed for unhandled errors, StatusTerminated
	// for cancel. This is always a terminal value at finish time — no caller of
	// beginCompensation ever wants a non-terminal final status here.
	FinalStatus Status
	// FinalErr is the error string passed to a FailInstance command when the
	// full-rollback branch completes. When non-empty, stepCompensationFinish
	// appends FailInstance{Err: FinalErr} to the result commands before clearing
	// the cursor. The admin path leaves this empty (no FailInstance emitted).
	FinalErr string
}

// walkMode is the finish outcome a compensationCursor describes, derived from
// which of its fields are non-zero. It replaces the "which-field-is-zero"
// inference that was previously repeated at each call site that needed to
// know what kind of walk was in flight. walkMode is a value computed on
// demand — compensationCursor gains no stored field, so the persisted
// (JSON-projected) shape is unchanged.
type walkMode int

const (
	// walkAdmin is the full-rollback / terminate outcome: none of ResumeNode,
	// ToNode, or ReverseNode is set. Covers the admin CompensateRequested full
	// rollback and the cancel/error terminal walks.
	walkAdmin walkMode = iota
	// walkThrowTargeted is a scope-targeted compensation-throw resume
	// (ResumeNode set, ArchiveKey non-empty): finish consumes exactly that
	// archive entry and RETAINS RootCompensations.
	walkThrowTargeted
	// walkThrowScopeWide is a scope-wide compensation-throw resume (ResumeNode
	// set, ArchiveKey empty, ADR-0120): finish clears the StartRecordCount
	// prefix of the throwing scope's own live records.
	walkThrowScopeWide
	// walkPartial is a partial-rollback resume to ToNode (ResumeNode and
	// ReverseNode both empty): finish resumes at ToNode with records RETAINED.
	walkPartial
	// walkReverse is a ReverseInstance full-reverse resume (ReverseNode set,
	// ResumeNode and ToNode both empty, ADR-0109): finish clears records and
	// optionally resets Variables before resuming at ReverseNode.
	walkReverse
)

// walkMode derives which finish outcome c describes, from the SAME
// field-emptiness precedence stepCompensationFinish's switch used to encode
// inline: a throw resume (ResumeNode) takes precedence over a partial
// rollback (ToNode), which takes precedence over a full reverse (ReverseNode);
// none of the three set means the admin/terminal full rollback. Pure function
// of c's fields — no external state, nothing stored.
func (c compensationCursor) walkMode() walkMode {
	switch {
	case c.ResumeNode != "":
		if c.ArchiveKey == "" {
			return walkThrowScopeWide
		}
		return walkThrowTargeted
	case c.ToNode != "":
		return walkPartial
	case c.ReverseNode != "":
		return walkReverse
	default:
		return walkAdmin
	}
}

// walkTerminates reports whether the walk this cursor describes will END the
// instance rather than resume it. pendingCancel is InstanceState.PendingCancel,
// which can flip a resuming walk to terminating mid-flight.
//
// ⚠ It MIRRORS stepCompensationFinish's finishPlan construction and must be kept
// in step with it. Read that switch, not this one, if they ever disagree:
//
//   - walkAdmin           → finishPlan.resume = false                → terminates
//   - walkThrowTargeted   → resume + consumePendingCancel            → pendingCancel
//   - walkThrowScopeWide  → resume + consumePendingCancel            → pendingCancel
//   - walkReverse         → resume + consumePendingCancel            → see below
//   - walkPartial         → resume, consumePendingCancel NOT SET     → resumes
//
// ⚠ walkPartial is the case that refuted the obvious predicate
// (`walkMode() == walkAdmin || pendingCancel`): a partial rollback does NOT
// consume a deferred cancel, so it resumes with PendingCancel still true, and
// treating it as dying silences arms on a LIVE instance.
//
// ⚠ walkReverse RESUMES, yet is reported as terminating — deliberately, per
// ADR-0172 Decision 1a. A reverse resume sets ResetVars and re-arms every root
// event sub-process (finishPlan.rearmRootESP); letting an arm fire into it was
// measured producing two concurrent tokens, the event sub-process body's
// variables wiped underneath it, and an INTERRUPTING one-shot arm resurrected
// while its body still runs. Widening this is rearmRootESP's problem, not the
// arm-fire path's.
func (c compensationCursor) walkTerminates(pendingCancel bool) bool {
	switch c.walkMode() {
	case walkAdmin, walkReverse:
		return true
	case walkPartial:
		return false
	default: // walkThrowTargeted, walkThrowScopeWide
		return pendingCancel
	}
}

// recordCompensation appends a CompensationRecord to the scope identified by
// scopeID. If scopeID is "" (root-level token), the record is appended to
// s.RootCompensations — the root-scope compensation list that is stored directly
// on the InstanceState rather than in a Scope entry. This keeps s.Scopes clean
// (containing only currently-open sub-process scopes) so that existing tests that
// assert on len(s.Scopes) are unaffected.
//
// If scopeID is non-empty and the scope is not found (defensive: should not occur
// in a well-formed state), the call is a no-op.
func (s *InstanceState) recordCompensation(scopeID, nodeID, action string, completedAt time.Time, input map[string]any) {
	rec := CompensationRecord{
		NodeID:      nodeID,
		Action:      action,
		CompletedAt: completedAt,
		Input:       input,
	}
	if scopeID == "" {
		s.RootCompensations = append(s.RootCompensations, rec)
		return
	}
	scope := s.scopeByID(scopeID)
	if scope == nil {
		return // defensive no-op
	}
	scope.Compensations = append(scope.Compensations, rec)
}

// openScope creates a new Scope for the given nodeID nested inside
// parentScopeID (empty string for a root scope). The scope is assigned a
// deterministic ID of the form "<instanceID>-s<ScopeSeq>" using s.ScopeSeq,
// which is incremented before use. The new scope is appended to s.Scopes.
// Returns the new scope's ID.
func (s *InstanceState) openScope(nodeID, parentScopeID string) string {
	id := s.nextID("s", &s.ScopeSeq)
	s.Scopes = append(s.Scopes, Scope{
		ID:       id,
		NodeID:   nodeID,
		ParentID: parentScopeID,
	})
	return id
}

// tokensInScope counts the number of tokens in s.Tokens whose ScopeID equals
// scopeID. Returns 0 if no tokens match.
func (s *InstanceState) tokensInScope(scopeID string) int {
	count := 0
	for i := range s.Tokens {
		if s.Tokens[i].ScopeID == scopeID {
			count++
		}
	}
	return count
}

// archiveCompensations moves the Compensations of the scope identified by scopeID
// into ArchivedCompensations keyed by that scope's NodeID (the sub-process node
// that opened the scope). On normal sub-process exit (ADR-0039) scope identity is
// preserved for scope-targeted compensation, and records are still reachable by
// the root/instance walk via consolidateArchiveIntoRoot. A sub-process entered more than once accumulates
// records in the same archive slot. No-op if the scope has no records or is not
// found.
func (s *InstanceState) archiveCompensations(scopeID string) {
	scope := s.scopeByID(scopeID)
	if scope == nil || len(scope.Compensations) == 0 {
		return
	}
	toArchive, windowCount, windowed := s.partitionForLiveWalk(scopeID, scope.Compensations)
	scope.Compensations = nil
	if windowed {
		cur := s.Compensating
		cur.TeardownArchiveKey = scope.NodeID
		cur.TeardownArchiveOffset = len(s.ArchivedCompensations[scope.NodeID])
		cur.TeardownArchiveCount = windowCount
		s.Compensating = cur
	}
	if len(toArchive) == 0 {
		return
	}
	if s.ArchivedCompensations == nil {
		s.ArchivedCompensations = make(map[string][]CompensationRecord)
	}
	s.ArchivedCompensations[scope.NodeID] = append(s.ArchivedCompensations[scope.NodeID], toArchive...)
}

// partitionForLiveWalk splits a scope's records at the indices of a scope-wide
// compensation throw walk that is draining that very scope, so a teardown which
// CANNOT be deferred hands each record to exactly one owner (ADR-0173).
//
// Three ranges, where drained = min(StartRecordCount, len(records)) and NextIndex
// is the record currently in flight (the walk counts DOWN):
//
//   - [NextIndex .. drained-1] — already dispatched by this walk. DROPPED: they
//     are compensated, and archiving them is what let a later walk re-run them.
//   - [0 .. NextIndex-1] — committed to but not yet dispatched. ARCHIVED and
//     WINDOWED, so an abandoned walk leaves them recoverable; consumeDispatchedRecord
//     removes each as the walk reaches it.
//   - [drained ..] — appended by a live sibling mid-walk. ARCHIVED unwindowed:
//     genuinely uncompensated, and ADR-0120 review A1's rule that they survive now
//     holds across a teardown too.
//
// Returns (records to archive, window length, whether a window was established).
// windowed is returned separately from windowCount > 0 because a walk that has
// dispatched everything still needs its cursor stamped — the key records that this
// scope's records were partitioned, not merely that some were parked.
//
// When no such walk exists the whole list is archived, unchanged from before.
func (s *InstanceState) partitionForLiveWalk(scopeID string, records []CompensationRecord) ([]CompensationRecord, int, bool) {
	if !s.scopeWideWalkDraining(scopeID) {
		return records, 0, false
	}
	// Both counts are clamped into [0, len(records)] because both arrive from a
	// PERSISTED cursor: InstanceState round-trips as whole-struct JSON and
	// `Compensating` is an exported field, so a corrupt or hostile row can carry a
	// negative StartRecordCount. Unclamped, `records[:undispatched]` panicked with
	// `slice bounds out of range [:-1]` inside the pure engine core — i.e. in the
	// CONSUMER's process, since this ships as a library. `main` absorbed the same
	// input, so the missing clamp was a regression, not an inherited gap.
	//
	// clearRecordsPrefix clamps the SAME field for the same reason (its "(defensive)"
	// note), and ADR-0171's bounds check in stepCompensationAdvance exists for this
	// exact threat. This is that convention, not a new one.
	drained := min(max(s.Compensating.StartRecordCount, 0), len(records))
	undispatched := min(max(s.Compensating.NextIndex, 0), drained)
	head, tail := records[:undispatched], records[drained:]
	merged := make([]CompensationRecord, 0, len(head)+len(tail))
	merged = append(merged, head...)
	merged = append(merged, tail...)
	return merged, undispatched, true
}

// scopeWideWalkDraining reports whether a live scope-wide compensation throw walk
// owns the records of the scope identified by scopeID (ADR-0173).
//
// Three conjuncts, and no more. Two obvious-looking others were measured
// NON-DISCRIMINATING by two independent auditors and deliberately left out:
//
//   - walkMode() == walkThrowScopeWide is implied. compensationCursor.ScopeID is
//     non-empty ONLY for a scope-wide throw — beginCompensation walks the root
//     scope through the const "", and the targeted throw producer passes
//     {ArchiveKey: ref} — so ScopeID == scopeID with a real scopeID already
//     selects that mode.
//   - scopeID != "" is unreachable. The implicit root scope has no Scope entry, so
//     archiveCompensations' scope == nil early return fires first and this is never
//     asked about it.
//
// len(Records) > 0 is the one that is NOT obvious and IS load-bearing: it selects
// a walk with a PINNED record source (ADR-0171). Without a snapshot a walk cannot
// dispatch anything once the teardown nils the live source — cursorRecords falls
// back to a live read that returns nothing and the walk finishes on
// stepCompensationAdvance's bounds check — so partitioning on its behalf would
// delete records nobody ever runs. Such a cursor (persisted before ADR-0171,
// reachable after a process restart) is therefore left entirely alone, keeping its
// prior behaviour including the double-run this delivery closes for every other
// case. Deliberate: losing the record outright is worse.
func (s *InstanceState) scopeWideWalkDraining(scopeID string) bool {
	cur := s.Compensating
	return cur.ActiveCmdID != "" && len(cur.Records) > 0 && cur.ScopeID == scopeID
}

// consolidateArchiveIntoRoot moves all ArchivedCompensations into RootCompensations,
// then stable-sorts the combined slice by (CompletedAt ascending, NodeID ascending
// tiebreak) for determinism, then sets ArchivedCompensations to nil. This is called
// by beginCompensation before the root/instance walk so the walk sees root + all
// archived sub-process records as one reverse-ordered sequence. Records are MOVED
// (single ownership: once consolidated, the archive is empty; stepCompensationFinish
// clears RootCompensations on walk finish, covering everything).
func (s *InstanceState) consolidateArchiveIntoRoot() {
	if len(s.ArchivedCompensations) == 0 {
		return
	}
	// Deterministic iteration: drain the archive in sorted key order.
	for _, k := range slices.Sorted(maps.Keys(s.ArchivedCompensations)) {
		s.RootCompensations = append(s.RootCompensations, s.ArchivedCompensations[k]...)
	}
	s.ArchivedCompensations = nil
	// Stable-sort combined slice by CompletedAt asc, NodeID asc tiebreak.
	slices.SortStableFunc(s.RootCompensations, func(a, b CompensationRecord) int {
		return cmp.Or(a.CompletedAt.Compare(b.CompletedAt), cmp.Compare(a.NodeID, b.NodeID))
	})
}

// dropArchiveRecordAt removes the record at index i from the ArchivedCompensations
// slot named key, retaining the rest in order. Out-of-range indices and unknown
// keys are no-ops.
//
// It is the SINGLE path by which a compensation record leaves the archive, which
// is what makes the map-nilling below uniform: an emptied slot is deleted, and an
// emptied map becomes nil rather than a non-nil empty map. That normalization is
// not cosmetic — InstanceState is persisted as JSON, so `{}` and `null` are a
// gratuitous difference in a stored shape — and it fixes a wart that PRE-DATES
// ADR-0173: applyFinish's whole-key delete already produced it (ADR-0173 spec §5.7).
func (s *InstanceState) dropArchiveRecordAt(key string, i int) {
	recs := s.ArchivedCompensations[key]
	if i < 0 || i >= len(recs) {
		return
	}
	retained := make([]CompensationRecord, 0, len(recs)-1)
	retained = append(retained, recs[:i]...)
	retained = append(retained, recs[i+1:]...)
	if len(retained) > 0 {
		s.ArchivedCompensations[key] = retained
		return
	}
	s.deleteArchiveSlot(key)
}

// deleteArchiveSlot removes an ArchivedCompensations key entirely, nilling the
// map once its last key is gone. Shared with applyFinish's targeted whole-key
// delete so both deletion paths normalize the same way.
func (s *InstanceState) deleteArchiveSlot(key string) {
	delete(s.ArchivedCompensations, key)
	if len(s.ArchivedCompensations) == 0 {
		s.ArchivedCompensations = nil
	}
}

// consumeDispatchedRecord drops the record a compensation walk is dispatching at
// index idx from whichever archive slot still holds it, so that a walk ABANDONED
// mid-flight leaves behind exactly the records it never dispatched (ADR-0173).
//
// Two sources can hold it:
//
//   - a TARGETED walk's own slot, ArchivedCompensations[ArchiveKey], which is its
//     record source; and
//   - a scope-wide walk's TEARDOWN WINDOW, when a teardown destroyed its scope
//     mid-walk and parked the un-dispatched head in the archive.
//
// In both cases the walk counts DOWN, so the record being dispatched is the LAST
// element still inside the pinned prefix / window. Removing it therefore shifts
// nothing that remains to be dispatched, which is what keeps these absolute
// indices valid without any re-basing.
//
// len(Records) == 0 — a cursor persisted before ADR-0171, which pinned no
// snapshot — returns early and is the load-bearing guard, not a defensive one.
// Such a walk cannot dispatch from a source a teardown has nilled: cursorRecords
// falls back to a live read that returns nothing, and stepCompensationAdvance's
// bounds check routes it straight to its finish. Consuming on its behalf would
// delete records nobody ever runs — the exact loss ADR-0173 rejects its simpler
// alternative for.
func (s *InstanceState) consumeDispatchedRecord(idx int) {
	cur := s.Compensating
	if len(cur.Records) == 0 {
		return
	}
	if cur.ArchiveKey != "" {
		s.dropArchiveRecordAt(cur.ArchiveKey, idx)
		return
	}
	if cur.TeardownArchiveKey == "" || idx >= cur.TeardownArchiveCount {
		return
	}
	s.dropArchiveRecordAt(cur.TeardownArchiveKey, cur.TeardownArchiveOffset+idx)
	cur.TeardownArchiveCount = idx
	s.Compensating = cur
}

// descendantScopeIDs returns scopeID plus every scope transitively nested inside
// it. A single forward pass over s.Scopes suffices because openScope always
// appends a child after its parent (ScopeSeq is monotonically increasing and a
// scope's ParentID must already exist when it is opened), so by the time a scope
// is visited its parent's membership is already known.
//
// It deliberately has NO existence guard, unlike closeScope. scopeByID("") is
// ALWAYS nil because the root scope is implicit — no Scope entry exists for it —
// so guarding here would make every root-level teardown a silent no-op. The
// returned set therefore contains scopeID itself even when no such Scope exists,
// which is exactly what a root-level teardown needs (ADR-0162).
func (s *InstanceState) descendantScopeIDs(scopeID string) map[string]bool {
	ids := map[string]bool{scopeID: true}
	for _, sc := range s.Scopes {
		if ids[sc.ID] || ids[sc.ParentID] {
			ids[sc.ID] = true
		}
	}
	return ids
}

// closeScope removes the Scope with the given scopeID from s.Scopes, along with
// every descendant scope reachable via the ParentID chain. It is a no-op if no
// scope with that ID exists (also covering the case where scopeID was already
// closed). Callers remain responsible for any per-scope cleanup outside s.Scopes
// (cancelling tokens, arms, timers, archiving compensation records) before
// invoking closeScope; this only prunes the scope tree itself (ADR-0130).
//
// The existence guard is load-bearing and must NOT be pushed into
// descendantScopeIDs: removing it here would turn closeScope("") — the implicit
// root — into an instance-wide scope wipe.
func (s *InstanceState) closeScope(scopeID string) {
	if s.scopeByID(scopeID) == nil {
		return
	}
	doomed := s.descendantScopeIDs(scopeID)
	out := make([]Scope, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		if doomed[sc.ID] {
			continue
		}
		out = append(out, sc)
	}
	s.Scopes = out
}

// closeScopeDescendants prunes every scope nested inside scopeID from s.Scopes,
// KEEPING scopeID itself. The interrupting event-sub-process teardown needs
// exactly this shape: the enclosing scope stays open so the drain code can
// detect its children, while its descendants must not survive the interrupt
// (ADR-0162). No existence guard, for the same reason descendantScopeIDs has
// none — the root scope is implicit, and its descendants are real.
func (s *InstanceState) closeScopeDescendants(scopeID string) {
	doomed := s.descendantScopeIDs(scopeID)
	out := make([]Scope, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		if sc.ID != scopeID && doomed[sc.ID] {
			continue
		}
		out = append(out, sc)
	}
	s.Scopes = out
}

// tokensInScopeSubtree counts tokens in scopeID and in every scope nested inside
// it. The sub-process drain checks need this rather than tokensInScope: a
// grandchild scope holding the live token must keep the subtree from being
// declared drained, or closeScope prunes it and leaves a token naming a scope
// that no longer exists — which wedges the instance permanently (ADR-0162).
func (s *InstanceState) tokensInScopeSubtree(scopeID string) int {
	ids := s.descendantScopeIDs(scopeID)
	count := 0
	for i := range s.Tokens {
		if ids[s.Tokens[i].ScopeID] {
			count++
		}
	}
	return count
}

// hasChildScopeWithTokens reports whether any child scope of parentID — other
// than exceptID — still holds a token anywhere in its own subtree. It is the
// single form of the "can this scope exit yet?" question that all four scope
// exits in engine/step_nodes.go ask — exitEventSubprocessScope,
// exitRootEventSubprocessScope, exitNestedEventSubprocessScope and
// exitRegularSubprocessScope. Before ADR-0162 three of them hand-rolled it over
// tokensInScope, which sees only DIRECT children, and the fourth had no check at
// all, which is how the fourth stayed broken.
//
// exceptID == "" means "no exception": the root scope is implicit and has no
// Scope entry, so no real scope ever has an empty ID.
func (s *InstanceState) hasChildScopeWithTokens(parentID, exceptID string) bool {
	for _, sc := range s.Scopes {
		if sc.ParentID == parentID && sc.ID != exceptID && s.tokensInScopeSubtree(sc.ID) > 0 {
			return true
		}
	}
	return false
}

// compensationWalkHoldsScope reports whether an in-flight compensation walk
// still needs the scope identified by scopeID to exist (ADR-0171).
//
// A compensation THROW resumes past itself, so sibling branches keep running
// while its walk is outstanding. A sibling reaching the scope's end event would
// otherwise archive and close that scope mid-walk, after which the walk's own
// resume placed a token in a scope defForScope can no longer resolve — measured
// as a PERMANENT wedge, every subsequent Step returning
// `workflow-engine: defForScope: unknown scope`.
//
// The predicate is ResumeScope alone, deliberately. It is the scope the resume
// token lands in, and BOTH throw forms set it to the throwing token's own scope
// — so a scope-wide throw's ScopeID is either equal to it or empty, and adding
// `ScopeID == scopeID` as a second disjunct changes no outcome (measured: the
// engine suite is green with that disjunct removed, and red with ResumeScope
// removed). The walk's RECORD SOURCE needs no protection from this function:
// startCompensationWalk pins it onto the cursor.
//
// scopeID is always a real (non-empty) scope at the call site: the root scope is
// implicit and is never closed. The empty-scopeID guard keeps a root walk
// (ResumeScope "") from matching it regardless.
func (s *InstanceState) compensationWalkHoldsScope(scopeID string) bool {
	if s.Compensating.ActiveCmdID == "" || scopeID == "" {
		return false
	}
	return s.Compensating.ResumeScope == scopeID
}

// scopeByID returns a pointer to the Scope with the given id, or nil if no
// such scope exists in s.Scopes. The pointer is into the slice element; callers
// must not hold it across mutations to s.Scopes.
func (s *InstanceState) scopeByID(id string) *Scope {
	for i := range s.Scopes {
		if s.Scopes[i].ID == id {
			return &s.Scopes[i]
		}
	}
	return nil
}

// normalizeTeardownWindow sanitizes a teardown window read off a PERSISTED cursor
// before it becomes a finishPlan field.
//
// finishPlan.validate() panics on a violated invariant, and its licence to do so
// is that a finishPlan is "built entirely from in-package logic, never from
// persisted or external input". Copying the window straight off the cursor would
// have falsified that: a row carrying a count with no key is unreachable
// in-package (archiveCompensations always stamps both together) but trivially
// representable in JSON, and it turned a corrupt row into a deliberate panic in
// the consumer's process.
//
// Normalizing here keeps validate() guarding only what in-package code builds,
// which is what makes panicking there the right call.
func normalizeTeardownWindow(key string, offset, count int) (string, int, int) {
	if key == "" || count <= 0 {
		return "", 0, 0
	}
	return key, max(offset, 0), count
}
