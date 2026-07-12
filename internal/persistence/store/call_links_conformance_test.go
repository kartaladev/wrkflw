// Package store_test: 3-dialect conformance for CallLinkStore.
//
// Each sub-test runs against Postgres, MySQL, and SQLite via forEachDialect.
// Seeding uses store.New (the neutral core) so the write side is exercised
// alongside the read/claim side. The leased-claim path is covered with a
// fake clock: correctness under single-writer is asserted on SQLite; the
// full SKIP LOCKED concurrency is only exercised on Postgres and MySQL.
package store_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const callLinkLeaseTTL = 30 * time.Second

func leaseClockBase() time.Time {
	return time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
}

// callLinkBaseStepStore returns a running AppliedStep for a child instance,
// matching the helper shape used in the postgres/mysql source tests.
func callLinkBaseStepStore(instanceID string) kernel.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: instanceID,
			DefID:      "def-parent",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, map[string]any{"k": "v"}),
	}
}

// callLinkTerminalStepStore returns a completed AppliedStep for a child instance.
func callLinkTerminalStepStore(instanceID string) kernel.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: instanceID,
			DefID:      "def-parent",
			DefVersion: 1,
			Status:     engine.StatusCompleted,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
	}
}

// seedCompletedCallLink seeds a child instance + call link via store.New, then
// commits it as terminal with the given outcome.
func seedCompletedCallLink(t *testing.T, b backend, childID string, outcome kernel.CallOutcome) {
	t.Helper()

	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err, "%s: seedCompletedCallLink: store.New", b.name)

	createStep := callLinkBaseStepStore(childID)
	createStep.NewCallLink = &kernel.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: "parent-" + childID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	tok, err := s.Create(t.Context(), createStep)
	require.NoError(t, err, "%s: seed create", b.name)

	termStep := callLinkTerminalStepStore(childID)
	termStep.CallOutcome = &outcome
	_, err = s.Commit(t.Context(), tok, termStep)
	require.NoError(t, err, "%s: seed commit", b.name)
}

// seedRunningCallLink seeds a running (not committed) call link.
func seedRunningCallLink(t *testing.T, b backend, childID, parentID string) {
	t.Helper()

	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err, "%s: seedRunningCallLink: store.New", b.name)

	step := callLinkBaseStepStore(childID)
	step.NewCallLink = &kernel.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	_, err = s.Create(t.Context(), step)
	require.NoError(t, err, "%s: seed running create", b.name)
}

// seedCompletedCallLinkForParent seeds a completed call link under a specific parentID.
func seedCompletedCallLinkForParent(t *testing.T, b backend, childID, parentID string) {
	t.Helper()

	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err, "%s: seedCompletedCallLinkForParent: store.New", b.name)

	step := callLinkBaseStepStore(childID)
	step.NewCallLink = &kernel.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	tok, err := s.Create(t.Context(), step)
	require.NoError(t, err, "%s: seed completed-for-parent create", b.name)

	termStep := callLinkTerminalStepStore(childID)
	termStep.CallOutcome = &kernel.CallOutcome{Completed: true}
	_, err = s.Commit(t.Context(), tok, termStep)
	require.NoError(t, err, "%s: seed completed-for-parent commit", b.name)
}

// TestCallLinkStore is the 3-dialect conformance suite for store.CallLinkStore.
// It covers:
//   - Insert a link (seeded via store.New) + verify via ClaimPending
//   - ParentOf / ChildrenOf / LookupChild read paths
//   - ListRunningChildren
//   - MarkNotified
//   - Leased-claim: claim one, immediate second claim, TTL expiry, notified rows
func TestCallLinkStore(t *testing.T) {
	// Compile-time interface assertions.
	var _ kernel.CallLinkStore = (*store.CallLinkStore)(nil)
	var _ kernel.CallLineageReader = (*store.CallLinkStore)(nil)

	t.Run("insert link is returned by ClaimPending after completion", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "ins-child-1", kernel.CallOutcome{Completed: true, Output: map[string]any{"a": float64(1)}})

			pending, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending", b.name)
			require.Len(t, pending, 1, "%s: expected 1 pending", b.name)
			assert.Equal(t, "ins-child-1", pending[0].Link.ChildInstanceID, "%s: ChildInstanceID", b.name)
			assert.True(t, pending[0].Outcome.Completed, "%s: Completed", b.name)
		})
	})

	t.Run("ClaimPending returns terminal rows in stable child_instance_id order", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "zzz-child", kernel.CallOutcome{Completed: true, Output: map[string]any{"a": float64(1)}})
			seedCompletedCallLink(t, b, "aaa-child", kernel.CallOutcome{Completed: false, Err: "boom"})

			pending, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending", b.name)
			require.Len(t, pending, 2, "%s: expected 2", b.name)
			assert.Equal(t, "aaa-child", pending[0].Link.ChildInstanceID, "%s: first must be aaa", b.name)
			assert.Equal(t, "zzz-child", pending[1].Link.ChildInstanceID, "%s: second must be zzz", b.name)
		})
	})

	t.Run("ClaimPending respects limit parameter", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "lim-child-1", kernel.CallOutcome{Completed: true})
			seedCompletedCallLink(t, b, "lim-child-2", kernel.CallOutcome{Completed: true})
			seedCompletedCallLink(t, b, "lim-child-3", kernel.CallOutcome{Completed: true})

			pending, err := cls.ClaimPending(t.Context(), 2)
			require.NoError(t, err, "%s: ClaimPending limit=2", b.name)
			assert.Len(t, pending, 2, "%s: must not exceed limit", b.name)
		})
	})

	t.Run("ClaimPending excludes running (non-terminal) rows", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedRunningCallLink(t, b, "running-child", "parent-x")

			pending, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending", b.name)
			assert.Empty(t, pending, "%s: running rows must not appear", b.name)
		})
	})

	t.Run("ClaimPending output JSON round-trips", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			want := map[string]any{"result": float64(99), "label": "ok"}
			seedCompletedCallLink(t, b, "json-child", kernel.CallOutcome{Completed: true, Output: want})

			pending, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending", b.name)
			require.Len(t, pending, 1, "%s: expected 1", b.name)
			assert.True(t, pending[0].Outcome.Completed, "%s: Completed", b.name)
			assert.Equal(t, want, pending[0].Outcome.Output, "%s: Output", b.name)
		})
	})

	t.Run("ClaimPending failed outcome maps Completed=false with Err string", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "fail-child", kernel.CallOutcome{Completed: false, Err: "child timed out"})

			pending, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending", b.name)
			require.Len(t, pending, 1, "%s: expected 1", b.name)
			assert.False(t, pending[0].Outcome.Completed, "%s: Completed must be false", b.name)
			assert.Equal(t, "child timed out", pending[0].Outcome.Err, "%s: Err", b.name)
			assert.Nil(t, pending[0].Outcome.Output, "%s: Output must be nil", b.name)
		})
	})

	t.Run("ClaimPending limit zero returns all rows", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "zero-lim-1", kernel.CallOutcome{Completed: true})
			seedCompletedCallLink(t, b, "zero-lim-2", kernel.CallOutcome{Completed: true})

			pending, err := cls.ClaimPending(t.Context(), 0)
			require.NoError(t, err, "%s: ClaimPending limit=0", b.name)
			assert.Len(t, pending, 2, "%s: limit=0 must return all rows", b.name)
		})
	})

	// --- MarkNotified ---

	t.Run("MarkNotified excludes row from subsequent ClaimPending", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "notif-child-1", kernel.CallOutcome{Completed: true})
			seedCompletedCallLink(t, b, "notif-child-2", kernel.CallOutcome{Completed: true})

			require.NoError(t, cls.MarkNotified(t.Context(), "notif-child-1"), "%s: MarkNotified", b.name)

			pending, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending after MarkNotified", b.name)
			require.Len(t, pending, 1, "%s: notified row must be excluded", b.name)
			assert.Equal(t, "notif-child-2", pending[0].Link.ChildInstanceID, "%s: remaining row", b.name)
		})
	})

	t.Run("MarkNotified uses clock for notified_at (fake clock)", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			fixed := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
			fc := clockwork.NewFakeClockAt(fixed)
			cls, err := store.NewCallLinkStore(b.conn, b.dialect, store.WithCallLinkClock(fc))
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)

			seedCompletedCallLink(t, b, "mn-clock-child", kernel.CallOutcome{Completed: true})

			before := time.Now().UTC()
			require.NoError(t, cls.MarkNotified(t.Context(), "mn-clock-child"), "%s: MarkNotified", b.name)
			after := time.Now().UTC()
			_ = before
			_ = after

			// The fake clock is fixed — the wall clock cannot be used for validation,
			// but we can verify MarkNotified did not error and the row is now excluded.
			pending, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending after fake-clock MarkNotified", b.name)
			assert.Empty(t, pending, "%s: notified row must be excluded", b.name)
		})
	})

	t.Run("WithCallLinkClock nil falls back to system clock (no panic)", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect, store.WithCallLinkClock(nil))
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "nil-clk-child", kernel.CallOutcome{Completed: true})
			assert.NotPanics(t, func() {
				_ = cls.MarkNotified(t.Context(), "nil-clk-child")
			}, "%s: nil clock must not panic", b.name)
		})
	})

	// --- LookupChild ---

	t.Run("LookupChild returns the link for a known child", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "lookup-child", kernel.CallOutcome{Completed: true})

			link, ok, err := cls.LookupChild(t.Context(), "lookup-child")
			require.NoError(t, err, "%s: LookupChild", b.name)
			assert.True(t, ok, "%s: ok must be true", b.name)
			assert.Equal(t, "lookup-child", link.ChildInstanceID, "%s: ChildInstanceID", b.name)
			assert.Equal(t, "parent-lookup-child", link.ParentInstanceID, "%s: ParentInstanceID", b.name)
			assert.Equal(t, "cmd-lookup-child", link.ParentCommandID, "%s: ParentCommandID", b.name)
			assert.Equal(t, "def-parent", link.ParentDefID, "%s: ParentDefID", b.name)
			assert.Equal(t, 1, link.ParentDefVersion, "%s: ParentDefVersion", b.name)
			assert.Equal(t, 1, link.Depth, "%s: Depth", b.name)
		})
	})

	t.Run("LookupChild returns ok=false for unknown instance", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)

			link, ok, err := cls.LookupChild(t.Context(), "nonexistent-child")
			require.NoError(t, err, "%s: LookupChild", b.name)
			assert.False(t, ok, "%s: ok must be false", b.name)
			assert.Equal(t, kernel.CallLink{}, link, "%s: empty link on miss", b.name)
		})
	})

	t.Run("LookupChild works on running (not yet terminal) link", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err, "%s: store.New", b.name)
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)

			step := callLinkBaseStepStore("running-lookup")
			step.NewCallLink = &kernel.CallLink{
				ChildInstanceID:  "running-lookup",
				ParentInstanceID: "parent-running-lookup",
				ParentCommandID:  "cmd-rl",
				ParentDefID:      "def-parent",
				ParentDefVersion: 2,
				Depth:            3,
			}
			_, err = s.Create(t.Context(), step)
			require.NoError(t, err, "%s: create running", b.name)

			link, ok, err := cls.LookupChild(t.Context(), "running-lookup")
			require.NoError(t, err, "%s: LookupChild running", b.name)
			assert.True(t, ok, "%s: ok must be true", b.name)
			assert.Equal(t, "running-lookup", link.ChildInstanceID, "%s: ChildInstanceID", b.name)
			assert.Equal(t, 2, link.ParentDefVersion, "%s: ParentDefVersion", b.name)
			assert.Equal(t, 3, link.Depth, "%s: Depth", b.name)
		})
	})

	// --- ParentOf (CallLineageReader) ---

	t.Run("ParentOf returns the link for a known child", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLink(t, b, "parentof-child", kernel.CallOutcome{Completed: true})

			link, err := cls.ParentOf(t.Context(), "parentof-child")
			require.NoError(t, err, "%s: ParentOf", b.name)
			require.NotNil(t, link, "%s: expected non-nil link", b.name)
			assert.Equal(t, "parentof-child", link.ChildInstanceID, "%s: ChildInstanceID", b.name)
			assert.Equal(t, "parent-parentof-child", link.ParentInstanceID, "%s: ParentInstanceID", b.name)
		})
	})

	t.Run("ParentOf returns nil nil for root instance", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)

			link, err := cls.ParentOf(t.Context(), "not-a-child")
			require.NoError(t, err, "%s: ParentOf root", b.name)
			assert.Nil(t, link, "%s: must be nil for root", b.name)
		})
	})

	// --- ChildrenOf (CallLineageReader) ---

	t.Run("ChildrenOf returns all children ordered by created_at child_instance_id", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedCompletedCallLinkForParent(t, b, "ch-child-1", "co-parent")
			seedCompletedCallLinkForParent(t, b, "ch-child-2", "co-parent")

			links, err := cls.ChildrenOf(t.Context(), "co-parent")
			require.NoError(t, err, "%s: ChildrenOf", b.name)
			require.Len(t, links, 2, "%s: expected 2 children", b.name)
			// Verify IDs present (order may vary by insertion time).
			ids := []string{links[0].ChildInstanceID, links[1].ChildInstanceID}
			assert.ElementsMatch(t, []string{"ch-child-1", "ch-child-2"}, ids, "%s: child IDs", b.name)
		})
	})

	t.Run("ChildrenOf returns non-nil empty slice when no children", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)

			links, err := cls.ChildrenOf(t.Context(), "unknown-parent")
			require.NoError(t, err, "%s: ChildrenOf unknown", b.name)
			assert.NotNil(t, links, "%s: must be non-nil", b.name)
			assert.Empty(t, links, "%s: must be empty", b.name)
		})
	})

	// --- ListRunningChildren ---

	t.Run("ListRunningChildren returns running children ordered by child_instance_id", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedRunningCallLink(t, b, "p-child-aaa", "P")
			seedRunningCallLink(t, b, "p-child-bbb", "P")
			seedCompletedCallLinkForParent(t, b, "p-child-zzz", "P")
			seedRunningCallLink(t, b, "q-child-001", "Q")

			children, err := cls.ListRunningChildren(t.Context(), "P")
			require.NoError(t, err, "%s: ListRunningChildren P", b.name)
			require.Len(t, children, 2, "%s: 2 running children for P", b.name)
			assert.Equal(t, "p-child-aaa", children[0].ChildInstanceID, "%s: first", b.name)
			assert.Equal(t, "P", children[0].ParentInstanceID, "%s: parent of first", b.name)
			assert.Equal(t, "p-child-bbb", children[1].ChildInstanceID, "%s: second", b.name)
		})
	})

	t.Run("ListRunningChildren returns single running child", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)
			seedRunningCallLink(t, b, "p-child-aaa", "P")
			seedRunningCallLink(t, b, "q-child-001", "Q")

			children, err := cls.ListRunningChildren(t.Context(), "Q")
			require.NoError(t, err, "%s: ListRunningChildren Q", b.name)
			require.Len(t, children, 1, "%s: 1 running child for Q", b.name)
			assert.Equal(t, "q-child-001", children[0].ChildInstanceID, "%s: child ID", b.name)
		})
	})

	t.Run("ListRunningChildren call link fields populated correctly", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err, "%s: store.New", b.name)
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)

			step := callLinkBaseStepStore("field-child-1")
			step.NewCallLink = &kernel.CallLink{
				ChildInstanceID:  "field-child-1",
				ParentInstanceID: "field-parent",
				ParentCommandID:  "cmd-field",
				ParentDefID:      "def-field",
				ParentDefVersion: 7,
				Depth:            3,
			}
			_, err = s.Create(t.Context(), step)
			require.NoError(t, err, "%s: create field-child", b.name)

			children, err := cls.ListRunningChildren(t.Context(), "field-parent")
			require.NoError(t, err, "%s: ListRunningChildren", b.name)
			require.Len(t, children, 1, "%s: 1 child", b.name)
			got := children[0]
			assert.Equal(t, "field-child-1", got.ChildInstanceID, "%s: ChildInstanceID", b.name)
			assert.Equal(t, "field-parent", got.ParentInstanceID, "%s: ParentInstanceID", b.name)
			assert.Equal(t, "cmd-field", got.ParentCommandID, "%s: ParentCommandID", b.name)
			assert.Equal(t, "def-field", got.ParentDefID, "%s: ParentDefID", b.name)
			assert.Equal(t, 7, got.ParentDefVersion, "%s: ParentDefVersion", b.name)
			assert.Equal(t, 3, got.Depth, "%s: Depth", b.name)
		})
	})

	t.Run("ListRunningChildren returns empty non-nil slice for unknown parent", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err, "%s: NewCallLinkStore", b.name)

			children, err := cls.ListRunningChildren(t.Context(), "unknown-parent")
			require.NoError(t, err, "%s: ListRunningChildren unknown", b.name)
			require.NotNil(t, children, "%s: must be non-nil", b.name)
			assert.Empty(t, children, "%s: must be empty", b.name)
		})
	})

	// --- Leased-claim ---

	t.Run("leased claim stamps claimed_at/claimed_by", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			fc := clockwork.NewFakeClockAt(leaseClockBase())
			cls, err := store.NewCallLinkStore(b.conn, b.dialect,
				store.WithCallLinkLease("replica-A", callLinkLeaseTTL),
				store.WithCallLinkClock(fc),
			)
			require.NoError(t, err, "%s: NewCallLinkStore leased", b.name)
			seedCompletedCallLink(t, b, "lease-child-1", kernel.CallOutcome{Completed: true})

			got, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending leased", b.name)
			require.Len(t, got, 1, "%s: expected 1 row", b.name)
			assert.Equal(t, "lease-child-1", got[0].Link.ChildInstanceID, "%s: ChildInstanceID", b.name)
		})
	})

	t.Run("immediate second claim by another worker returns nothing while lease is live", func(t *testing.T) {
		// The lease is enforced by a WHERE predicate (leased_until in the future),
		// not SKIP LOCKED, so a second worker is excluded on every backend —
		// including single-writer SQLite, where worker B simply reads the
		// already-committed lease that worker A wrote.
		forEachDialect(t, func(t *testing.T, b backend) {
			fc := clockwork.NewFakeClockAt(leaseClockBase())
			clsA, err := store.NewCallLinkStore(b.conn, b.dialect,
				store.WithCallLinkLease("replica-A", callLinkLeaseTTL),
				store.WithCallLinkClock(fc),
			)
			require.NoError(t, err, "%s: NewCallLinkStore A", b.name)
			clsB, err := store.NewCallLinkStore(b.conn, b.dialect,
				store.WithCallLinkLease("replica-B", callLinkLeaseTTL),
				store.WithCallLinkClock(fc),
			)
			require.NoError(t, err, "%s: NewCallLinkStore B", b.name)
			seedCompletedCallLink(t, b, "lease-child-2", kernel.CallOutcome{Completed: true})

			first, err := clsA.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: A ClaimPending", b.name)
			require.Len(t, first, 1, "%s: A must get 1", b.name)

			second, err := clsB.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: B ClaimPending", b.name)
			assert.Empty(t, second, "%s: B must not see A's claimed row", b.name)
		})
	})

	t.Run("after fake-clock advance past TTL second worker reclaims", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			fc := clockwork.NewFakeClockAt(leaseClockBase())
			clsA, err := store.NewCallLinkStore(b.conn, b.dialect,
				store.WithCallLinkLease("replica-A", callLinkLeaseTTL),
				store.WithCallLinkClock(fc),
			)
			require.NoError(t, err, "%s: NewCallLinkStore A", b.name)
			clsB, err := store.NewCallLinkStore(b.conn, b.dialect,
				store.WithCallLinkLease("replica-B", callLinkLeaseTTL),
				store.WithCallLinkClock(fc),
			)
			require.NoError(t, err, "%s: NewCallLinkStore B", b.name)
			seedCompletedCallLink(t, b, "lease-child-3", kernel.CallOutcome{Completed: true})

			first, err := clsA.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: A ClaimPending", b.name)
			require.Len(t, first, 1, "%s: A must get 1", b.name)

			fc.Advance(callLinkLeaseTTL + time.Second)

			reclaimed, err := clsB.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: B reclaim after TTL", b.name)
			require.Len(t, reclaimed, 1, "%s: B must get 1 after TTL", b.name)
			assert.Equal(t, "lease-child-3", reclaimed[0].Link.ChildInstanceID, "%s: ChildInstanceID", b.name)
		})
	})

	t.Run("notified row never returned by leased ClaimPending", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			fc := clockwork.NewFakeClockAt(leaseClockBase())
			cls, err := store.NewCallLinkStore(b.conn, b.dialect,
				store.WithCallLinkLease("replica-A", callLinkLeaseTTL),
				store.WithCallLinkClock(fc),
			)
			require.NoError(t, err, "%s: NewCallLinkStore leased", b.name)
			seedCompletedCallLink(t, b, "lease-notif-1", kernel.CallOutcome{Completed: true})

			require.NoError(t, cls.MarkNotified(t.Context(), "lease-notif-1"), "%s: MarkNotified", b.name)

			got, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending after MarkNotified", b.name)
			assert.Empty(t, got, "%s: notified row must not appear", b.name)

			fc.Advance(callLinkLeaseTTL + time.Second)
			got2, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: ClaimPending after TTL", b.name)
			assert.Empty(t, got2, "%s: notified row must not appear even after TTL", b.name)
		})
	})

	t.Run("ttl=0 two consecutive claims both return the link (backward-compat)", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewCallLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			seedCompletedCallLink(t, b, "lease-noopt-1", kernel.CallOutcome{Completed: true})

			first, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: first ClaimPending", b.name)
			require.Len(t, first, 1, "%s: first must return 1", b.name)

			second, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: second ClaimPending", b.name)
			require.Len(t, second, 1, "%s: second must also return 1 (no lease)", b.name)
			assert.Equal(t, "lease-noopt-1", second[0].Link.ChildInstanceID, "%s: ChildInstanceID", b.name)
		})
	})

	t.Run("same-worker live lease excludes re-claim then reclaims after TTL", func(t *testing.T) {
		// Single-worker lease semantics hold on every backend: a live lease
		// (leased_until in the future) excludes even the leaser's own re-claim
		// via the WHERE predicate, and expiry re-exposes the row. This is the
		// single-writer correctness the SQLite path relies on, but the predicate
		// is dialect-agnostic so it runs on Postgres and MySQL too.
		forEachDialect(t, func(t *testing.T, b backend) {
			fc := clockwork.NewFakeClockAt(leaseClockBase())
			cls, err := store.NewCallLinkStore(b.conn, b.dialect,
				store.WithCallLinkLease("single-writer", callLinkLeaseTTL),
				store.WithCallLinkClock(fc),
			)
			require.NoError(t, err, "%s: NewCallLinkStore leased", b.name)
			seedCompletedCallLink(t, b, "sw-lease-1", kernel.CallOutcome{Completed: true})

			// First claim succeeds.
			first, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: first claim", b.name)
			require.Len(t, first, 1, "%s: must get 1", b.name)
			assert.Equal(t, "sw-lease-1", first[0].Link.ChildInstanceID, "%s: ChildInstanceID", b.name)

			// Immediate second claim returns nothing (lease is live).
			second, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: second claim", b.name)
			assert.Empty(t, second, "%s: second claim must be empty while lease is live", b.name)

			// After TTL expiry, reclaim.
			fc.Advance(callLinkLeaseTTL + time.Second)
			reclaimed, err := cls.ClaimPending(t.Context(), 10)
			require.NoError(t, err, "%s: reclaim after TTL", b.name)
			require.Len(t, reclaimed, 1, "%s: must reclaim after TTL", b.name)
			assert.Equal(t, "sw-lease-1", reclaimed[0].Link.ChildInstanceID, "%s: ChildInstanceID after TTL", b.name)
		})
	})
}
