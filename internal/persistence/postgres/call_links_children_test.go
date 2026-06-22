package postgres_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// seedRunningLink inserts a child instance + running call link via Store.Create.
// The link stays in status='running' (no Commit call).
func seedRunningLink(t *testing.T, store *pg.Store, childID, parentID string) {
	t.Helper()

	step := callLinkBaseStep(childID)
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

// seedCompletedLinkForParent inserts a child instance + call link and commits it
// as completed under the given parentID (distinct from seedCompletedLink which
// uses "parent-<childID>" as the parent).
func seedCompletedLinkForParent(t *testing.T, store *pg.Store, pool *pgxpool.Pool, childID, parentID string) {
	t.Helper()

	step := callLinkBaseStep(childID)
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

	termStep := callLinkTerminalStep(childID)
	termStep.CallOutcome = &runtime.CallOutcome{Completed: true}
	_, err = store.Commit(t.Context(), tok, termStep)
	require.NoError(t, err)
}

// TestListRunningChildren verifies the ListRunningChildren method on the
// Postgres CallLinkStore.
//
// Setup:
//   - Parent "P": children p-child-aaa (running), p-child-bbb (running), p-child-zzz (completed)
//   - Parent "Q": child q-child-001 (running)
//
// Assertions:
//   - ListRunningChildren(ctx, "P") returns exactly p-child-aaa and p-child-bbb,
//     ordered by child_instance_id, excluding the completed child and Q's child.
//   - ListRunningChildren(ctx, "Q") returns exactly q-child-001.
//   - ListRunningChildren(ctx, "unknown") returns an empty (non-nil) slice.
func TestListRunningChildren(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"returns running children of P ordered by child_instance_id, excludes completed and Q's child": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				store := pg.NewStore(pool)
				cls := pg.NewCallLinkStore(pool)

				seedRunningLink(t, store, "p-child-aaa", "P")
				seedRunningLink(t, store, "p-child-bbb", "P")
				seedCompletedLinkForParent(t, store, pool, "p-child-zzz", "P")
				seedRunningLink(t, store, "q-child-001", "Q")

				children, err := cls.ListRunningChildren(t.Context(), "P")
				require.NoError(t, err)
				require.Len(t, children, 2)

				// ORDER BY child_instance_id: aaa < bbb.
				require.Equal(t, "p-child-aaa", children[0].ChildInstanceID)
				require.Equal(t, "P", children[0].ParentInstanceID)
				require.Equal(t, "p-child-bbb", children[1].ChildInstanceID)
				require.Equal(t, "P", children[1].ParentInstanceID)
			},
		},
		"returns single running child of Q": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				store := pg.NewStore(pool)
				cls := pg.NewCallLinkStore(pool)

				seedRunningLink(t, store, "p-child-aaa", "P")
				seedRunningLink(t, store, "p-child-bbb", "P")
				seedCompletedLinkForParent(t, store, pool, "p-child-zzz", "P")
				seedRunningLink(t, store, "q-child-001", "Q")

				children, err := cls.ListRunningChildren(t.Context(), "Q")
				require.NoError(t, err)
				require.Len(t, children, 1)
				require.Equal(t, "q-child-001", children[0].ChildInstanceID)
				require.Equal(t, "Q", children[0].ParentInstanceID)
			},
		},
		"returns empty slice for unknown parent": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewCallLinkStore(pool)

				children, err := cls.ListRunningChildren(t.Context(), "unknown-parent")
				require.NoError(t, err)
				require.NotNil(t, children)
				require.Empty(t, children)
			},
		},
		"call link fields are populated correctly": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				store := pg.NewStore(pool)
				cls := pg.NewCallLinkStore(pool)

				step := callLinkBaseStep("field-child-1")
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
				require.Equal(t, "field-child-1", got.ChildInstanceID)
				require.Equal(t, "field-parent", got.ParentInstanceID)
				require.Equal(t, "cmd-field", got.ParentCommandID)
				require.Equal(t, "def-field", got.ParentDefID)
				require.Equal(t, 7, got.ParentDefVersion)
				require.Equal(t, 3, got.Depth)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}
