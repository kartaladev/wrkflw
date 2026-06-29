package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// seedCallLinkRunning inserts a running call link via the Store write path.
func seedCallLinkRunning(
	t *testing.T,
	store *pg.Store,
	childID, parentID, commandID string,
) {
	t.Helper()
	step := callLinkBaseStep(childID)
	step.NewCallLink = &runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  commandID,
		ParentDefID:      "def-lineage",
		ParentDefVersion: 2,
		Depth:            1,
	}
	_, err := store.Create(t.Context(), step)
	require.NoError(t, err)
}

// seedChainLink inserts one predecessor→successor hop via ChainLinkStore.Record.
func seedChainLink(
	t *testing.T,
	cls *pg.ChainLinkStore,
	predID, outcome, succID string,
) {
	t.Helper()
	err := cls.Record(t.Context(), runtime.ChainLink{
		PredecessorID:            predID,
		PredecessorDefinitionRef: "def-pred:1",
		Outcome:                  runtime.Outcome(outcome),
		SuccessorID:              succID,
		SuccessorDefinitionRef:   "def-succ:1",
		CreatedAt:                time.Now().UTC(),
	})
	require.NoError(t, err)
}

// TestCallLinkStoreParentOf verifies ParentOf on the Postgres CallLinkStore.
func TestCallLinkStoreParentOf(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"returns the parent link for a known child": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				store := pg.NewStore(pool)
				cls := pg.NewCallLinkStore(pool)

				seedCallLinkRunning(t, store, "lin-child-1", "lin-parent-1", "cmd-lin-1")

				link, err := cls.ParentOf(t.Context(), "lin-child-1")
				require.NoError(t, err)
				require.NotNil(t, link)
				assert.Equal(t, "lin-child-1", link.ChildInstanceID)
				assert.Equal(t, "lin-parent-1", link.ParentInstanceID)
				assert.Equal(t, "cmd-lin-1", link.ParentCommandID)
				assert.Equal(t, "def-lineage", link.ParentDefID)
				assert.Equal(t, 2, link.ParentDefVersion)
				assert.Equal(t, 1, link.Depth)
			},
		},
		"returns nil, nil for unknown child instance (root)": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewCallLinkStore(pool)

				link, err := cls.ParentOf(t.Context(), "root-never-seeded")
				require.NoError(t, err)
				assert.Nil(t, link)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestCallLinkStoreChildrenOf verifies ChildrenOf on the Postgres CallLinkStore.
func TestCallLinkStoreChildrenOf(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"returns all children ordered by created_at then child_instance_id": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				store := pg.NewStore(pool)
				cls := pg.NewCallLinkStore(pool)

				// Two children of the same parent.
				seedCallLinkRunning(t, store, "lin-ch-aaa", "shared-parent", "cmd-a")
				seedCallLinkRunning(t, store, "lin-ch-zzz", "shared-parent", "cmd-z")
				// Unrelated child of a different parent — must not appear.
				seedCallLinkRunning(t, store, "other-child", "other-parent", "cmd-other")

				children, err := cls.ChildrenOf(t.Context(), "shared-parent")
				require.NoError(t, err)
				require.Len(t, children, 2)

				ids := []string{children[0].ChildInstanceID, children[1].ChildInstanceID}
				assert.Contains(t, ids, "lin-ch-aaa")
				assert.Contains(t, ids, "lin-ch-zzz")
				// All returned rows must reference the queried parent.
				for _, c := range children {
					assert.Equal(t, "shared-parent", c.ParentInstanceID)
				}
			},
		},
		"returns empty slice for a parent with no children": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewCallLinkStore(pool)

				children, err := cls.ChildrenOf(t.Context(), "nonexistent-parent")
				require.NoError(t, err)
				assert.Empty(t, children)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestChainLinkStorePredecessorOf verifies PredecessorOf on the Postgres ChainLinkStore.
func TestChainLinkStorePredecessorOf(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"returns the predecessor link for a known successor": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewChainLinkStore(pool)

				seedChainLink(t, cls, "pred-inst-1", "completed", "succ-inst-1")

				link, err := cls.PredecessorOf(t.Context(), "succ-inst-1")
				require.NoError(t, err)
				require.NotNil(t, link)
				assert.Equal(t, "pred-inst-1", link.PredecessorID)
				assert.Equal(t, "succ-inst-1", link.SuccessorID)
				assert.Equal(t, runtime.OutcomeCompleted, link.Outcome)
				assert.Equal(t, "def-pred:1", link.PredecessorDefinitionRef)
				assert.Equal(t, "def-succ:1", link.SuccessorDefinitionRef)
			},
		},
		"returns nil, nil for a successor with no predecessor (root chain)": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewChainLinkStore(pool)

				link, err := cls.PredecessorOf(t.Context(), "never-chained-inst")
				require.NoError(t, err)
				assert.Nil(t, link)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestChainLinkStoreSuccessorsOf verifies SuccessorsOf on the Postgres ChainLinkStore.
func TestChainLinkStoreSuccessorsOf(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"returns all successors ordered by outcome": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewChainLinkStore(pool)

				seedChainLink(t, cls, "fan-pred", "completed", "fan-succ-completed")
				seedChainLink(t, cls, "fan-pred", "terminated", "fan-succ-terminated")
				// Unrelated predecessor — must not appear.
				seedChainLink(t, cls, "other-pred", "completed", "other-succ")

				succs, err := cls.SuccessorsOf(t.Context(), "fan-pred")
				require.NoError(t, err)
				require.Len(t, succs, 2)
				// Ordered by outcome: "completed" < "terminated".
				assert.Equal(t, runtime.OutcomeCompleted, succs[0].Outcome)
				assert.Equal(t, "fan-succ-completed", succs[0].SuccessorID)
				assert.Equal(t, runtime.OutcomeTerminated, succs[1].Outcome)
				assert.Equal(t, "fan-succ-terminated", succs[1].SuccessorID)
			},
		},
		"returns empty slice for a predecessor with no successors": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewChainLinkStore(pool)

				succs, err := cls.SuccessorsOf(t.Context(), "lone-pred")
				require.NoError(t, err)
				assert.Empty(t, succs)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}
