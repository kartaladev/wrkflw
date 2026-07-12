package engine

import (
	"fmt"
	"sort"
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
// completes; cloneState copies it by value (all fields are plain scalars).
type compensationCursor struct {
	// ScopeID identifies the scope being compensated ("" = root).
	// Ignored when ArchiveKey is non-empty (archive walk).
	ScopeID string
	// ArchiveKey is the ArchivedCompensations map key for a throw-walk
	// (non-empty). When set, cursorRecords reads from ArchivedCompensations[ArchiveKey]
	// instead of the live scope or RootCompensations. Empty for all
	// beginCompensation (admin / cancel / error) walks.
	ArchiveKey string
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
	s.ScopeSeq++
	id := fmt.Sprintf("%s-s%d", s.InstanceID, s.ScopeSeq)
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
	if s.ArchivedCompensations == nil {
		s.ArchivedCompensations = make(map[string][]CompensationRecord)
	}
	s.ArchivedCompensations[scope.NodeID] = append(s.ArchivedCompensations[scope.NodeID], scope.Compensations...)
	scope.Compensations = nil
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
	// Deterministic iteration: sort archive keys.
	keys := make([]string, 0, len(s.ArchivedCompensations))
	for k := range s.ArchivedCompensations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.RootCompensations = append(s.RootCompensations, s.ArchivedCompensations[k]...)
	}
	s.ArchivedCompensations = nil
	// Stable-sort combined slice by CompletedAt asc, NodeID asc tiebreak.
	sort.SliceStable(s.RootCompensations, func(i, j int) bool {
		if s.RootCompensations[i].CompletedAt.Equal(s.RootCompensations[j].CompletedAt) {
			return s.RootCompensations[i].NodeID < s.RootCompensations[j].NodeID
		}
		return s.RootCompensations[i].CompletedAt.Before(s.RootCompensations[j].CompletedAt)
	})
}

// closeScope removes the Scope with the given scopeID from s.Scopes. It is a
// no-op if no scope with that ID exists. Child scopes (those whose ParentID
// equals scopeID) are NOT automatically removed — callers are responsible for
// closing or reparenting children before closing a parent. This is intentionally
// minimal; Plan 8 (compensation/rollback) will add the richer cascading logic.
func (s *InstanceState) closeScope(scopeID string) {
	out := make([]Scope, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		if sc.ID != scopeID {
			out = append(out, sc)
		}
	}
	s.Scopes = out
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
