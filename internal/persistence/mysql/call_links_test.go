package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newMySQLCallLinkStore returns a freshly migrated CallLinkStore + Store for tests.
func newMySQLCallLinkStore(t *testing.T) (*mypkg.CallLinkStore, *mypkg.Store) {
	t.Helper()
	db := database.RunTestMySQL(t)
	return mypkg.NewCallLinkStore(db), mypkg.NewStore(db)
}

// callLinkMySQLBaseStep returns a running-step for a child instance.
func callLinkMySQLBaseStep(instanceID string) runtime.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return runtime.AppliedStep{
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

// callLinkMySQLTerminalStep returns a completed-step for a child instance.
func callLinkMySQLTerminalStep(instanceID string) runtime.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return runtime.AppliedStep{
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

// seedMySQLCompletedLink seeds a child instance + call link, then commits it as
// terminal with the provided outcome.
func seedMySQLCompletedLink(t *testing.T, store *mypkg.Store, childID string, outcome runtime.CallOutcome) {
	t.Helper()

	createStep := callLinkMySQLBaseStep(childID)
	createStep.NewCallLink = &runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: "parent-" + childID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	tok, err := store.Create(t.Context(), createStep)
	require.NoError(t, err)

	termStep := callLinkMySQLTerminalStep(childID)
	termStep.CallOutcome = &outcome
	_, err = store.Commit(t.Context(), tok, termStep)
	require.NoError(t, err)
}

// TestCallLinkStore_ClaimPending_Plain verifies the plain (no-lease) ClaimPending path.
func TestCallLinkStore_ClaimPending_Plain(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "returns terminal unnotified rows in stable order",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				seedMySQLCompletedLink(t, store, "zzz-child", runtime.CallOutcome{Completed: true, Output: map[string]any{"a": float64(1)}})
				seedMySQLCompletedLink(t, store, "aaa-child", runtime.CallOutcome{Completed: false, Err: "boom"})

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 2)
				// Stable order: aaa-child < zzz-child.
				assert.Equal(t, "aaa-child", pending[0].Link.ChildInstanceID)
				assert.Equal(t, "zzz-child", pending[1].Link.ChildInstanceID)
			},
		},
		{
			name: "respects limit parameter",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				seedMySQLCompletedLink(t, store, "lim-child-1", runtime.CallOutcome{Completed: true})
				seedMySQLCompletedLink(t, store, "lim-child-2", runtime.CallOutcome{Completed: true})
				seedMySQLCompletedLink(t, store, "lim-child-3", runtime.CallOutcome{Completed: true})

				pending, err := cls.ClaimPending(t.Context(), 2)
				require.NoError(t, err)
				assert.Len(t, pending, 2)
			},
		},
		{
			name: "excludes running (non-terminal) rows",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				createStep := callLinkMySQLBaseStep("running-child")
				createStep.NewCallLink = &runtime.CallLink{
					ChildInstanceID:  "running-child",
					ParentInstanceID: "parent-x",
					ParentCommandID:  "cmd-x",
					ParentDefID:      "def-parent",
					ParentDefVersion: 1,
					Depth:            1,
				}
				_, err := store.Create(t.Context(), createStep)
				require.NoError(t, err)

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, pending)
			},
		},
		{
			name: "output JSON round-trips into Outcome.Output",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				want := map[string]any{"result": float64(99), "label": "ok"}
				seedMySQLCompletedLink(t, store, "json-child", runtime.CallOutcome{Completed: true, Output: want})

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 1)

				pn := pending[0]
				assert.True(t, pn.Outcome.Completed)
				assert.Equal(t, want, pn.Outcome.Output)
			},
		},
		{
			name: "failed outcome maps Completed=false with Err string",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				seedMySQLCompletedLink(t, store, "fail-child", runtime.CallOutcome{Completed: false, Err: "child timed out"})

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 1)

				pn := pending[0]
				assert.False(t, pn.Outcome.Completed)
				assert.Equal(t, "child timed out", pn.Outcome.Err)
				assert.Nil(t, pn.Outcome.Output)
			},
		},
		{
			name: "limit zero returns all rows",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				seedMySQLCompletedLink(t, store, "zero-lim-1", runtime.CallOutcome{Completed: true})
				seedMySQLCompletedLink(t, store, "zero-lim-2", runtime.CallOutcome{Completed: true})

				pending, err := cls.ClaimPending(t.Context(), 0)
				require.NoError(t, err)
				assert.Len(t, pending, 2)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestCallLinkStore_MarkNotified verifies the MarkNotified method.
func TestCallLinkStore_MarkNotified(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "notified row is excluded from subsequent ClaimPending",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				seedMySQLCompletedLink(t, store, "notif-child-1", runtime.CallOutcome{Completed: true})
				seedMySQLCompletedLink(t, store, "notif-child-2", runtime.CallOutcome{Completed: true})

				require.NoError(t, cls.MarkNotified(t.Context(), "notif-child-1"))

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 1)
				assert.Equal(t, "notif-child-2", pending[0].Link.ChildInstanceID)
			},
		},
		{
			name: "MarkNotified stamps notified_at and sets status to notified",
			assert: func(t *testing.T) {
				db := database.RunTestMySQL(t)
				cls := mypkg.NewCallLinkStore(db)
				store := mypkg.NewStore(db)

				seedMySQLCompletedLink(t, store, "stamp-child", runtime.CallOutcome{Completed: true})

				before := time.Now().UTC()
				require.NoError(t, cls.MarkNotified(t.Context(), "stamp-child"))
				after := time.Now().UTC()

				var status string
				var notifiedAt *time.Time
				err := db.QueryRowContext(t.Context(),
					`SELECT status, notified_at FROM wrkflw_call_links WHERE child_instance_id = ?`,
					"stamp-child",
				).Scan(&status, &notifiedAt)
				require.NoError(t, err)
				assert.Equal(t, "notified", status)
				require.NotNil(t, notifiedAt)
				assert.False(t, notifiedAt.Before(before.Add(-time.Second)), "notified_at should not predate the call")
				assert.False(t, notifiedAt.After(after.Add(time.Second)), "notified_at should not be in the future")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestCallLinkStore_LookupChild verifies the LookupChild method.
func TestCallLinkStore_LookupChild(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "returns the link for a known child instance",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				seedMySQLCompletedLink(t, store, "lookup-child", runtime.CallOutcome{Completed: true})

				link, ok, err := cls.LookupChild(t.Context(), "lookup-child")
				require.NoError(t, err)
				assert.True(t, ok)
				assert.Equal(t, "lookup-child", link.ChildInstanceID)
				assert.Equal(t, "parent-lookup-child", link.ParentInstanceID)
				assert.Equal(t, "cmd-lookup-child", link.ParentCommandID)
				assert.Equal(t, "def-parent", link.ParentDefID)
				assert.Equal(t, 1, link.ParentDefVersion)
				assert.Equal(t, 1, link.Depth)
			},
		},
		{
			name: "returns ok=false for unknown child instance ID",
			assert: func(t *testing.T) {
				cls, _ := newMySQLCallLinkStore(t)

				link, ok, err := cls.LookupChild(t.Context(), "nonexistent-child")
				require.NoError(t, err)
				assert.False(t, ok)
				assert.Equal(t, runtime.CallLink{}, link)
			},
		},
		{
			name: "lookup works on running (not yet terminal) link",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				callLink := &runtime.CallLink{
					ChildInstanceID:  "running-lookup",
					ParentInstanceID: "parent-running-lookup",
					ParentCommandID:  "cmd-rl",
					ParentDefID:      "def-parent",
					ParentDefVersion: 2,
					Depth:            3,
				}
				createStep := callLinkMySQLBaseStep("running-lookup")
				createStep.NewCallLink = callLink
				_, err := store.Create(t.Context(), createStep)
				require.NoError(t, err)

				link, ok, err := cls.LookupChild(t.Context(), "running-lookup")
				require.NoError(t, err)
				assert.True(t, ok)
				assert.Equal(t, "running-lookup", link.ChildInstanceID)
				assert.Equal(t, 2, link.ParentDefVersion)
				assert.Equal(t, 3, link.Depth)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestCallLinkStore_ListRunningChildren verifies the ListRunningChildren method.
func TestCallLinkStore_ListRunningChildren(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "returns running children ordered by child_instance_id",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				// Seed: P has two running children, one completed child; Q has one running child.
				seedMySQLRunningLink(t, store, "p-child-aaa", "P")
				seedMySQLRunningLink(t, store, "p-child-bbb", "P")
				seedMySQLCompletedLinkForParent(t, store, "p-child-zzz", "P")
				seedMySQLRunningLink(t, store, "q-child-001", "Q")

				children, err := cls.ListRunningChildren(t.Context(), "P")
				require.NoError(t, err)
				require.Len(t, children, 2)

				// ORDER BY child_instance_id: aaa < bbb.
				assert.Equal(t, "p-child-aaa", children[0].ChildInstanceID)
				assert.Equal(t, "P", children[0].ParentInstanceID)
				assert.Equal(t, "p-child-bbb", children[1].ChildInstanceID)
				assert.Equal(t, "P", children[1].ParentInstanceID)
			},
		},
		{
			name: "returns single running child",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				seedMySQLRunningLink(t, store, "p-child-aaa", "P")
				seedMySQLRunningLink(t, store, "q-child-001", "Q")

				children, err := cls.ListRunningChildren(t.Context(), "Q")
				require.NoError(t, err)
				require.Len(t, children, 1)
				assert.Equal(t, "q-child-001", children[0].ChildInstanceID)
				assert.Equal(t, "Q", children[0].ParentInstanceID)
			},
		},
		{
			name: "returns empty non-nil slice for unknown parent",
			assert: func(t *testing.T) {
				cls, _ := newMySQLCallLinkStore(t)

				children, err := cls.ListRunningChildren(t.Context(), "unknown-parent")
				require.NoError(t, err)
				assert.NotNil(t, children)
				assert.Empty(t, children)
			},
		},
		{
			name: "call link fields are populated correctly",
			assert: func(t *testing.T) {
				cls, store := newMySQLCallLinkStore(t)

				step := callLinkMySQLBaseStep("field-child-1")
				step.NewCallLink = &runtime.CallLink{
					ChildInstanceID:  "field-child-1",
					ParentInstanceID: "field-parent",
					ParentCommandID:  "cmd-field",
					ParentDefID:      "def-field",
					ParentDefVersion: 7,
					Depth:            3,
				}
				_, err := store.Create(t.Context(), step)
				require.NoError(t, err)

				children, err := cls.ListRunningChildren(t.Context(), "field-parent")
				require.NoError(t, err)
				require.Len(t, children, 1)

				got := children[0]
				assert.Equal(t, "field-child-1", got.ChildInstanceID)
				assert.Equal(t, "field-parent", got.ParentInstanceID)
				assert.Equal(t, "cmd-field", got.ParentCommandID)
				assert.Equal(t, "def-field", got.ParentDefID)
				assert.Equal(t, 7, got.ParentDefVersion)
				assert.Equal(t, 3, got.Depth)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// seedMySQLRunningLink seeds a running (not committed) call link.
func seedMySQLRunningLink(t *testing.T, store *mypkg.Store, childID, parentID string) {
	t.Helper()

	step := callLinkMySQLBaseStep(childID)
	step.NewCallLink = &runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	_, err := store.Create(t.Context(), step)
	require.NoError(t, err)
}

// seedMySQLCompletedLinkForParent seeds a completed call link under the given parentID.
func seedMySQLCompletedLinkForParent(t *testing.T, store *mypkg.Store, childID, parentID string) {
	t.Helper()

	step := callLinkMySQLBaseStep(childID)
	step.NewCallLink = &runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	tok, err := store.Create(t.Context(), step)
	require.NoError(t, err)

	termStep := callLinkMySQLTerminalStep(childID)
	termStep.CallOutcome = &runtime.CallOutcome{Completed: true}
	_, err = store.Commit(t.Context(), tok, termStep)
	require.NoError(t, err)
}
