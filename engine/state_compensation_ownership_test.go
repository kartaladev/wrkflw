package engine

// White-box, deliberately: the record-ownership helpers are
// unexported, and the behaviour that matters — which record leaves the archive
// slot, and whether the map is left non-nil but empty — is not observable from
// the public Step API in isolation. The end-to-end routes are covered
// black-box in step_compensation_scope_drain_test.go.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ownershipRecords builds n compensation records named undo0..undo(n-1).
func ownershipRecords(names ...string) []CompensationRecord {
	out := make([]CompensationRecord, 0, len(names))
	for i, n := range names {
		out = append(out, CompensationRecord{
			NodeID:      "svc" + n,
			Action:      "undo" + n,
			CompletedAt: time.Date(2026, 8, 11, 9, i, 0, 0, time.UTC),
		})
	}
	return out
}

// archiveActions returns the Action of every record under key, in slot order.
func archiveActions(s *InstanceState, key string) []string {
	var out []string
	for _, r := range s.ArchivedCompensations[key] {
		out = append(out, r.Action)
	}
	return out
}

// TestDropArchiveRecordAt covers the single helper every archive deletion routes
// through, including the map-nilling required on BOTH paths: a
// non-nil empty ArchivedCompensations is a gratuitous difference in a persisted,
// JSON-projected shape, and it already occurs on main via applyFinish's
// pre-existing whole-key delete.
func TestDropArchiveRecordAt(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		slots  map[string][]CompensationRecord
		key    string
		index  int
		assert func(t *testing.T, s *InstanceState)
	}

	cases := []testCase{
		{
			name:  "drops the named index and retains the rest in order",
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("A", "B", "C")},
			key:   "outer", index: 1,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undoA", "undoC"}, archiveActions(s, "outer"))
			},
		},
		{
			name:  "dropping the last remaining record nils the whole map",
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("A")},
			key:   "outer", index: 0,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Nil(t, s.ArchivedCompensations,
					"an emptied archive must be nil, not a non-nil empty map")
			},
		},
		{
			name: "emptying one slot keeps the map when another survives",
			slots: map[string][]CompensationRecord{
				"outer": ownershipRecords("A"),
				"inner": ownershipRecords("B"),
			},
			key: "outer", index: 0,
			assert: func(t *testing.T, s *InstanceState) {
				require.NotNil(t, s.ArchivedCompensations)
				_, stillThere := s.ArchivedCompensations["outer"]
				assert.False(t, stillThere, "the emptied key is removed, not left as an empty slice")
				assert.Equal(t, []string{"undoB"}, archiveActions(s, "inner"))
			},
		},
		{
			name:  "an out-of-range index is a no-op",
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("A", "B")},
			key:   "outer", index: 7,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undoA", "undoB"}, archiveActions(s, "outer"))
			},
		},
		{
			name:  "an unknown key is a no-op and does not create the slot",
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("A")},
			key:   "nope", index: 0,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undoA"}, archiveActions(s, "outer"))
				assert.Len(t, s.ArchivedCompensations, 1)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{InstanceID: "own", ArchivedCompensations: tc.slots}
			s.dropArchiveRecordAt(tc.key, tc.index)
			tc.assert(t, s)
		})
	}
}

// TestConsumeDispatchedRecord pins which slot a dispatch consumes from, and the
// two cursors that must be left ENTIRELY alone: one with no pinned snapshot
// (a cursor written by an older version) and one with no teardown window.
func TestConsumeDispatchedRecord(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		cursor compensationCursor
		slots  map[string][]CompensationRecord
		idx    int
		assert func(t *testing.T, s *InstanceState)
	}

	pinned := ownershipRecords("A", "B")

	cases := []testCase{
		{
			name: "a targeted walk consumes its own archive slot at the dispatched index",
			cursor: compensationCursor{
				ActiveCmdID: "c1", ArchiveKey: "sub", Records: pinned, ResumeNode: "n",
			},
			slots: map[string][]CompensationRecord{"sub": ownershipRecords("A", "B", "tail")},
			idx:   1,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undoA", "undotail"}, archiveActions(s, "sub"),
					"the dispatched record leaves; the sibling-appended tail stays")
			},
		},
		{
			name: "a scope-wide walk consumes its teardown window and shrinks the count",
			cursor: compensationCursor{
				ActiveCmdID: "c1", ScopeID: "s1", Records: pinned, ResumeNode: "n",
				TeardownArchiveKey: "outer", TeardownArchiveOffset: 1, TeardownArchiveCount: 2,
			},
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("head0", "A", "B", "tail")},
			idx:   1,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undohead0", "undoA", "undotail"}, archiveActions(s, "outer"),
					"offset+idx addresses the window's last element")
				assert.Equal(t, 1, s.Compensating.TeardownArchiveCount,
					"the window shrinks so a later abandonment leaves exactly the remainder")
			},
		},
		{
			name: "a cursor with NO pinned snapshot is left alone (legacy shape)",
			cursor: compensationCursor{
				ActiveCmdID: "c1", ScopeID: "s1", Records: nil, ResumeNode: "n",
				TeardownArchiveKey: "outer", TeardownArchiveOffset: 0, TeardownArchiveCount: 2,
			},
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("A", "B")},
			idx:   1,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undoA", "undoB"}, archiveActions(s, "outer"),
					"a walk with no snapshot never dispatches these; deleting them loses them")
			},
		},
		{
			name: "a scope-wide walk with no teardown window touches nothing",
			cursor: compensationCursor{
				ActiveCmdID: "c1", ScopeID: "s1", Records: pinned, ResumeNode: "n",
			},
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("A", "B")},
			idx:   1,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undoA", "undoB"}, archiveActions(s, "outer"))
			},
		},
		{
			name: "an index outside the window is left in the archive",
			cursor: compensationCursor{
				ActiveCmdID: "c1", ScopeID: "s1", Records: pinned, ResumeNode: "n",
				TeardownArchiveKey: "outer", TeardownArchiveOffset: 0, TeardownArchiveCount: 1,
			},
			slots: map[string][]CompensationRecord{"outer": ownershipRecords("A")},
			idx:   5,
			assert: func(t *testing.T, s *InstanceState) {
				assert.Equal(t, []string{"undoA"}, archiveActions(s, "outer"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{InstanceID: "own", ArchivedCompensations: tc.slots, Compensating: tc.cursor}
			s.consumeDispatchedRecord(tc.idx)
			tc.assert(t, s)
		})
	}
}

// TestArchiveCompensationsPartitionRequiresALiveWalk covers the ONE conjunct of
// scopeWideWalkDraining that no end-to-end route can currently falsify: the
// liveness test. Measured — dropping `ActiveCmdID != ""` leaves the whole engine
// suite at EXIT=0, because ActiveCmdID is never cleared on its own (the only two
// writers reset the entire cursor, engine/step_compensation.go and engine/state.go).
//
// It is kept rather than deleted because it is the same liveness idiom
// compensationWalkHoldsScope uses, and because a future change that cleared just
// the command id would otherwise silently make a DEAD walk partition live records.
// This test is what stops that from being a silent regression.
func TestArchiveCompensationsPartitionRequiresALiveWalk(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		InstanceID: "live",
		Scopes: []Scope{{
			ID: "s1", NodeID: "outer",
			Compensations: ownershipRecords("A", "B"),
		}},
		Compensating: compensationCursor{
			// Everything a partitioning walk needs EXCEPT a live command.
			ScopeID:          "s1",
			Records:          ownershipRecords("A", "B"),
			ResumeNode:       "n",
			StartRecordCount: 2,
			NextIndex:        1,
		},
	}
	s.archiveCompensations("s1")

	assert.Equal(t, []string{"undoA", "undoB"}, archiveActions(s, "outer"),
		"a cursor with no in-flight command is not a live walk: archive the whole list")
	assert.Empty(t, s.Compensating.TeardownArchiveKey,
		"and stamp no window, since no walk will consume it")
}

// TestApplyPlanRecordClearingRemovesAResidualWindow covers the finish's
// residual-window removal, which no public-API route reaches today: a pinned
// scope-wide walk always advances down to index 0, so consumeDispatchedRecord has
// emptied the window by the time the finish runs (measured — mutating the removal
// leaves the whole engine suite at EXIT=0).
//
// The removal is kept as the counterpart to the ownership rule rather than deleted
// on the strength of a green suite: undemonstrated is not unreachable, and a walk
// that ever stops short of its own records — a shrunken source, a future
// boundary — would otherwise leave records it owns to the next walk. This test
// exercises it directly so the code is not merely asserted to work.
func TestApplyPlanRecordClearingRemovesAResidualWindow(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		InstanceID: "resid",
		ArchivedCompensations: map[string][]CompensationRecord{
			"outer": ownershipRecords("head0", "head1", "tail"),
		},
	}
	applyPlanRecordClearing(s, finishPlan{
		resume: true, scopeWideThrow: true, doClearRecords: true,
		clearScope:          "gone",
		archiveWindowKey:    "outer",
		archiveWindowOffset: 0,
		archiveWindowCount:  2,
	})

	assert.Equal(t, []string{"undotail"}, archiveActions(s, "outer"),
		"the window's records belong to the finished walk; the sibling tail does not")
}

// TestArchiveCompensationsPartitionsAScopeAtMostOnce pins the caller convention
// the teardown-window stamp depends on: archiveCompensations nils a scope's
// records, so a second call on the same scope returns at its len == 0 guard and
// cannot restamp the cursor.
//
// Without that, the stamp's unconditional overwrite would be a silent
// two-directional data loss — the first window's un-consumed records orphaned in
// the archive (a double-run), and the newly-archived records misclassified as this
// walk's window and deleted (a lost compensation). No route on this tree breaks
// the convention; this test is what makes that a checked claim rather than a
// comment.
func TestArchiveCompensationsPartitionsAScopeAtMostOnce(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		InstanceID: "once",
		Scopes:     []Scope{{ID: "s1", NodeID: "outer", Compensations: ownershipRecords("A", "B")}},
		Compensating: compensationCursor{
			ActiveCmdID: "c1", ScopeID: "s1", ResumeNode: "n",
			Records:          ownershipRecords("A", "B"),
			StartRecordCount: 2, NextIndex: 1,
		},
	}
	s.archiveCompensations("s1")
	require.Equal(t, "outer", s.Compensating.TeardownArchiveKey)
	require.Equal(t, 1, s.Compensating.TeardownArchiveCount, "the un-dispatched head is windowed")
	require.Equal(t, []string{"undoA"}, archiveActions(s, "outer"))

	// THE CONVENTION: archiveCompensations nil'd the scope's records, so a real
	// second call returns at its len == 0 guard and restamps nothing. This is the
	// assertion that matters.
	require.Empty(t, s.Scopes[0].Compensations, "control: the first call nil'd the records")
	before := s.Compensating
	s.archiveCompensations("s1")
	assert.Equal(t, before, s.Compensating, "a scope with no records restamps nothing")

	// THE HAZARD, shown rather than asserted away: if the convention ever broke and
	// a scope were partitioned twice with records present, the stamp's overwrite
	// moves the OFFSET past the first window, orphaning its records in the archive.
	//
	// ⚠ Note what does NOT discriminate here: TeardownArchiveCount recomputes to
	// the SAME value, so asserting on the count alone would pass while the window
	// silently addressed the wrong records. The first version of this test did
	// exactly that.
	hazard := &InstanceState{
		InstanceID:            "hazard",
		Scopes:                []Scope{{ID: "s1", NodeID: "outer", Compensations: ownershipRecords("late")}},
		ArchivedCompensations: map[string][]CompensationRecord{"outer": ownershipRecords("A")},
		Compensating:          s.Compensating,
	}
	hazard.archiveCompensations("s1")
	assert.Equal(t, 1, hazard.Compensating.TeardownArchiveOffset,
		"the restamp moves the window off undoA — which is why the at-most-once "+
			"convention above is load-bearing and not merely tidy")
	assert.Equal(t, 1, hazard.Compensating.TeardownArchiveCount,
		"and the count is UNCHANGED, so it cannot be what a guard keys on")
}

// TestOwnershipToleratesACorruptPersistedCursor pins that a cursor arriving from
// a persisted row cannot drive the pure engine core into a panic.
//
// This is not a hardening nicety — it is the invariant already stated on
// compensationCursor.Records ("nil on a cursor deserialized from a row written
// by an older version — that is the case cursorRecords' live-read fallback and
// stepCompensationAdvance's bounds check exist for") and that clearRecordsPrefix
// enforces for this very field with an explicit "(defensive)" clamp. InstanceState
// is persisted as whole-struct JSON by internal/persistence/store, `Compensating`
// is an exported field, and a panic here lands in the CONSUMER's process because
// this ships as a library.
//
// Both cases below were measured panicking before the clamps, and measured NOT
// panicking on `main` — so each was a regression this delivery introduced:
//   - negative StartRecordCount → `slice bounds out of range [:-1]`
//   - a window count with no key → the finishPlan invariant panic
func TestOwnershipToleratesACorruptPersistedCursor(t *testing.T) {
	t.Parallel()

	t.Run("a negative StartRecordCount does not panic the partition", func(t *testing.T) {
		t.Parallel()
		s := &InstanceState{
			InstanceID: "corrupt",
			Scopes:     []Scope{{ID: "s1", NodeID: "outer", Compensations: ownershipRecords("A", "B")}},
			Compensating: compensationCursor{
				ActiveCmdID: "c1", ScopeID: "s1", ResumeNode: "n",
				Records:          ownershipRecords("A", "B"),
				StartRecordCount: -1, NextIndex: 1,
			},
		}
		require.NotPanics(t, func() { s.archiveCompensations("s1") })
		assert.Equal(t, []string{"undoA", "undoB"}, archiveActions(s, "outer"),
			"a nonsense drained count archives everything rather than losing records")
	})

	t.Run("a window count with no key is ignored, not asserted", func(t *testing.T) {
		t.Parallel()
		// The plan builder must normalize this away, so validate() keeps guarding
		// only in-package construction — which is what licenses it to panic at all.
		require.NotPanics(t, func() {
			normalizeTeardownWindow("", 3, 2)
		})
		key, off, count := normalizeTeardownWindow("", 3, 2)
		assert.Empty(t, key)
		assert.Zero(t, off)
		assert.Zero(t, count, "a count with no key addresses nothing and must be dropped")

		key, off, count = normalizeTeardownWindow("outer", 1, 2)
		assert.Equal(t, "outer", key)
		assert.Equal(t, 1, off)
		assert.Equal(t, 2, count, "a well-formed window is passed through untouched")

		_, off, count = normalizeTeardownWindow("outer", -5, -3)
		assert.Zero(t, off, "a negative offset cannot address a slot")
		assert.Zero(t, count)
	})
}
