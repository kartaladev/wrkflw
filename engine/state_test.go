package engine

// state_test.go — white-box tests for InstanceState and compensationCursor.
// Uses package engine (not engine_test) to access unexported types.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// cloneState: compensationCursor outcome fields
// ---------------------------------------------------------------------------

// TestCloneStateCarriesCompensatingOutcomeFields asserts that cloneState copies
// compensationCursor.FinalStatus and FinalErr (scalar fields; struct-value copy
// covers them) and that mutating the clone's cursor does not affect the original.
func TestCloneStateCarriesCompensatingOutcomeFields(t *testing.T) {
	st := InstanceState{
		InstanceID: "cs-outcome-1",
		Compensating: compensationCursor{
			ScopeID:     "",
			ToNode:      "",
			NextIndex:   2,
			ActiveCmdID: "cmd-1",
			FinalStatus: StatusFailed,
			FinalErr:    "x",
		},
	}

	clone := cloneState(st)

	// Clone must carry the new fields.
	assert.Equal(t, StatusFailed, clone.Compensating.FinalStatus,
		"cloneState must copy FinalStatus from the source cursor")
	assert.Equal(t, "x", clone.Compensating.FinalErr,
		"cloneState must copy FinalErr from the source cursor")

	// Mutating the clone's cursor must not affect the original (they are independent
	// value copies — no aliasing on scalar fields).
	clone.Compensating.FinalStatus = StatusTerminated
	clone.Compensating.FinalErr = "mutated"

	assert.Equal(t, StatusFailed, st.Compensating.FinalStatus,
		"mutating clone.Compensating.FinalStatus must not affect original")
	assert.Equal(t, "x", st.Compensating.FinalErr,
		"mutating clone.Compensating.FinalErr must not affect original")
}

// ---------------------------------------------------------------------------
// ArchivedCompensations: field existence + cloneState deep-copy isolation
// ---------------------------------------------------------------------------

// TestCloneStateArchivedCompensationsDeepCopy asserts that cloneState produces
// an independent copy of ArchivedCompensations. Mutating the clone's record's
// Input map must not affect the original (deep-copy semantics: same as
// RootCompensations deep-copy pattern).
func TestCloneStateArchivedCompensationsDeepCopy(t *testing.T) {
	t0 := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	st := InstanceState{
		InstanceID: "archive-clone-1",
		ArchivedCompensations: map[string][]CompensationRecord{
			"sub1": {
				{NodeID: "n1", Action: "a1", CompletedAt: t0, Input: map[string]any{"k": "v"}},
			},
		},
	}

	clone := cloneState(st)

	// Clone must have the same data.
	require.NotNil(t, clone.ArchivedCompensations, "clone must have ArchivedCompensations")
	require.Contains(t, clone.ArchivedCompensations, "sub1")
	require.Len(t, clone.ArchivedCompensations["sub1"], 1)
	assert.Equal(t, "n1", clone.ArchivedCompensations["sub1"][0].NodeID)
	assert.Equal(t, "a1", clone.ArchivedCompensations["sub1"][0].Action)
	assert.Equal(t, "v", clone.ArchivedCompensations["sub1"][0].Input["k"])

	// Mutating the clone's Input must NOT affect the original.
	clone.ArchivedCompensations["sub1"][0].Input["k"] = "mutated"
	assert.Equal(t, "v", st.ArchivedCompensations["sub1"][0].Input["k"],
		"mutating clone's ArchivedCompensations Input must not affect original")
}

// TestCloneStateArchivedCompensationsNilSource asserts that a nil
// ArchivedCompensations in the source produces nil in the clone.
func TestCloneStateArchivedCompensationsNilSource(t *testing.T) {
	st := InstanceState{
		InstanceID:            "archive-nil-1",
		ArchivedCompensations: nil,
	}
	clone := cloneState(st)
	assert.Nil(t, clone.ArchivedCompensations,
		"nil ArchivedCompensations in source must produce nil in clone")
}

// ---------------------------------------------------------------------------
// archiveCompensations: MOVE semantics
// ---------------------------------------------------------------------------

// TestArchiveCompensationsMoveSemantics asserts that archiveCompensations moves
// scope.Compensations into ArchivedCompensations keyed by scope.NodeID, clears
// the scope's slice, and does not touch RootCompensations (MOVE, not copy).
func TestArchiveCompensationsMoveSemantics(t *testing.T) {
	t0 := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	s := &InstanceState{
		InstanceID: "archive-move-1",
		Scopes: []Scope{
			{
				ID:     "s1",
				NodeID: "sub-proc-1",
				Compensations: []CompensationRecord{
					{NodeID: "inner-task", Action: "undo", CompletedAt: t0},
				},
			},
		},
	}

	s.archiveCompensations("s1")

	// Record must now be in the archive keyed by scope.NodeID.
	require.NotNil(t, s.ArchivedCompensations)
	require.Contains(t, s.ArchivedCompensations, "sub-proc-1")
	require.Len(t, s.ArchivedCompensations["sub-proc-1"], 1)
	assert.Equal(t, "inner-task", s.ArchivedCompensations["sub-proc-1"][0].NodeID)

	// Scope's own slice must be cleared (MOVE, not copy).
	scope := s.scopeByID("s1")
	require.NotNil(t, scope)
	assert.Nil(t, scope.Compensations, "scope.Compensations must be nil after archiveCompensations")

	// RootCompensations must be untouched.
	assert.Nil(t, s.RootCompensations, "RootCompensations must be untouched by archiveCompensations")
}

// TestArchiveCompensationsNoopOnEmpty asserts archiveCompensations is a no-op
// when the scope has no records.
func TestArchiveCompensationsNoopOnEmpty(t *testing.T) {
	s := &InstanceState{
		InstanceID: "archive-noop-1",
		Scopes:     []Scope{{ID: "s1", NodeID: "sub-proc-1", Compensations: nil}},
	}
	s.archiveCompensations("s1")
	assert.Nil(t, s.ArchivedCompensations, "no archive entry when scope has no records")
}

// TestArchiveCompensationsUnknownScope asserts archiveCompensations is a no-op
// when the scope ID is not found.
func TestArchiveCompensationsUnknownScope(t *testing.T) {
	s := &InstanceState{InstanceID: "archive-unknown-1"}
	s.archiveCompensations("nonexistent")
	assert.Nil(t, s.ArchivedCompensations)
}

// ---------------------------------------------------------------------------
// consolidateArchiveIntoRoot: ordering + nil-archive after
// ---------------------------------------------------------------------------

// TestConsolidateArchiveIntoRootOrdering asserts that consolidateArchiveIntoRoot
// merges ArchivedCompensations into RootCompensations, sorts by CompletedAt asc
// (NodeID tiebreak), sets ArchivedCompensations to nil, and produces no duplicates.
func TestConsolidateArchiveIntoRootOrdering(t *testing.T) {
	t0 := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Second)
	t2 := t0.Add(2 * time.Second)

	s := &InstanceState{
		InstanceID: "consolidate-1",
		RootCompensations: []CompensationRecord{
			{NodeID: "a", Action: "undo-a", CompletedAt: t1},
		},
		ArchivedCompensations: map[string][]CompensationRecord{
			"sub1": {{NodeID: "b", Action: "undo-b", CompletedAt: t2}},
			"sub2": {{NodeID: "c", Action: "undo-c", CompletedAt: t0}},
		},
	}

	s.consolidateArchiveIntoRoot()

	// ArchivedCompensations must be nil after consolidation.
	assert.Nil(t, s.ArchivedCompensations, "ArchivedCompensations must be nil after consolidation")

	// RootCompensations must contain all 3 records, sorted by CompletedAt ascending.
	require.Len(t, s.RootCompensations, 3, "must have exactly 3 records (no duplicates)")
	assert.Equal(t, "c", s.RootCompensations[0].NodeID, "T0 record must be first (c)")
	assert.Equal(t, "a", s.RootCompensations[1].NodeID, "T1 record must be second (a)")
	assert.Equal(t, "b", s.RootCompensations[2].NodeID, "T2 record must be third (b)")
}

// TestConsolidateArchiveIntoRootNoopOnEmptyArchive asserts that
// consolidateArchiveIntoRoot is a no-op when ArchivedCompensations is nil/empty.
func TestConsolidateArchiveIntoRootNoopOnEmptyArchive(t *testing.T) {
	t0 := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	s := &InstanceState{
		InstanceID:            "consolidate-noop-1",
		RootCompensations:     []CompensationRecord{{NodeID: "a", CompletedAt: t0}},
		ArchivedCompensations: nil,
	}
	s.consolidateArchiveIntoRoot()
	require.Len(t, s.RootCompensations, 1, "RootCompensations must be unchanged")
	assert.Equal(t, "a", s.RootCompensations[0].NodeID)
}
