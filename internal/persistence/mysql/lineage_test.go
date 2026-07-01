package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newMySQLLineageStores returns freshly migrated CallLinkStore and
// ChainLinkStore for lineage read tests.
func newMySQLLineageStores(t *testing.T) (*mypkg.CallLinkStore, *mypkg.Store, *mypkg.ChainLinkStore) {
	t.Helper()
	db := dbtest.RunTestMySQL(t)
	return mypkg.NewCallLinkStore(db), mypkg.NewStore(db), mypkg.NewChainLinkStore(db)
}

// seedMySQLCallLinkForLineage seeds a child call-link under a given parent for
// lineage tests (running — not committed to terminal).
func seedMySQLCallLinkForLineage(t *testing.T, store *mypkg.Store, childID, parentID string) {
	t.Helper()
	step := callLinkMySQLBaseStep(childID)
	step.NewCallLink = &runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-lin",
		ParentDefVersion: 2,
		Depth:            1,
	}
	_, err := store.Create(t.Context(), step)
	require.NoError(t, err)
}

// TestCallLinkStore_ParentOf tests the ParentOf method added for
// CallLineageReader.
func TestCallLinkStore_ParentOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "returns the call link when the child has a parent",
			assert: func(t *testing.T) {
				cls, store, _ := newMySQLLineageStores(t)

				seedMySQLCallLinkForLineage(t, store, "lin-child-1", "lin-parent-1")

				got, err := cls.ParentOf(t.Context(), "lin-child-1")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "lin-child-1", got.ChildInstanceID)
				assert.Equal(t, "lin-parent-1", got.ParentInstanceID)
				assert.Equal(t, "def-lin", got.ParentDefID)
				assert.Equal(t, 2, got.ParentDefVersion)
				assert.Equal(t, 1, got.Depth)
			},
		},
		{
			name: "returns nil nil when child has no parent (root instance)",
			assert: func(t *testing.T) {
				cls, _, _ := newMySQLLineageStores(t)

				got, err := cls.ParentOf(t.Context(), "does-not-exist")
				require.NoError(t, err)
				assert.Nil(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}

// TestCallLinkStore_ChildrenOf tests the ChildrenOf method added for
// CallLineageReader.
func TestCallLinkStore_ChildrenOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "returns all children ordered by (created_at, child_instance_id)",
			assert: func(t *testing.T) {
				cls, store, _ := newMySQLLineageStores(t)

				// Seed two children under the same parent.
				seedMySQLCallLinkForLineage(t, store, "lin-child-aaa", "lin-parent-multi")
				seedMySQLCallLinkForLineage(t, store, "lin-child-bbb", "lin-parent-multi")
				// Different parent — must not appear.
				seedMySQLCallLinkForLineage(t, store, "lin-child-other", "lin-parent-other")

				children, err := cls.ChildrenOf(t.Context(), "lin-parent-multi")
				require.NoError(t, err)
				require.Len(t, children, 2)
				// Both children must have the correct parent.
				assert.Equal(t, "lin-parent-multi", children[0].ParentInstanceID)
				assert.Equal(t, "lin-parent-multi", children[1].ParentInstanceID)
				childIDs := []string{children[0].ChildInstanceID, children[1].ChildInstanceID}
				assert.ElementsMatch(t, []string{"lin-child-aaa", "lin-child-bbb"}, childIDs)
			},
		},
		{
			name: "returns empty non-nil slice for unknown parent",
			assert: func(t *testing.T) {
				cls, _, _ := newMySQLLineageStores(t)

				children, err := cls.ChildrenOf(t.Context(), "no-such-parent")
				require.NoError(t, err)
				assert.NotNil(t, children)
				assert.Empty(t, children)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}

// TestChainLinkStore_PredecessorOf tests the PredecessorOf method added for
// ChainLineageReader.
func TestChainLinkStore_PredecessorOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "returns the chain link when the successor was produced by chaining",
			assert: func(t *testing.T) {
				_, _, cls := newMySQLLineageStores(t)
				ctx := t.Context()

				require.NoError(t, cls.Record(ctx, runtime.ChainLink{
					PredecessorID:            "pred-lin-1",
					PredecessorDefinitionRef: "order:1",
					Outcome:                  runtime.OutcomeCompleted,
					SuccessorID:              "succ-lin-1",
					SuccessorDefinitionRef:   "fulfillment:1",
					CreatedAt:                time.Now().UTC(),
				}))

				got, err := cls.PredecessorOf(ctx, "succ-lin-1")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "pred-lin-1", got.PredecessorID)
				assert.Equal(t, "succ-lin-1", got.SuccessorID)
				assert.Equal(t, runtime.OutcomeCompleted, got.Outcome)
				assert.Equal(t, "order:1", got.PredecessorDefinitionRef)
				assert.Equal(t, "fulfillment:1", got.SuccessorDefinitionRef)
			},
		},
		{
			name: "returns nil nil when successor was not produced by chaining (chain root)",
			assert: func(t *testing.T) {
				_, _, cls := newMySQLLineageStores(t)

				got, err := cls.PredecessorOf(t.Context(), "chain-root-no-pred")
				require.NoError(t, err)
				assert.Nil(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}

// TestChainLinkStore_SuccessorsOf tests the SuccessorsOf method added for
// ChainLineageReader.
func TestChainLinkStore_SuccessorsOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "returns all successors ordered by outcome",
			assert: func(t *testing.T) {
				_, _, cls := newMySQLLineageStores(t)
				ctx := t.Context()
				now := time.Now().UTC()

				require.NoError(t, cls.Record(ctx, runtime.ChainLink{
					PredecessorID: "lin-pred-fanout",
					Outcome:       runtime.OutcomeTerminated,
					SuccessorID:   "lin-succ-terminated",
					CreatedAt:     now,
				}))
				require.NoError(t, cls.Record(ctx, runtime.ChainLink{
					PredecessorID: "lin-pred-fanout",
					Outcome:       runtime.OutcomeCompleted,
					SuccessorID:   "lin-succ-completed",
					CreatedAt:     now,
				}))
				// Different predecessor — must not appear.
				require.NoError(t, cls.Record(ctx, runtime.ChainLink{
					PredecessorID: "lin-pred-other",
					Outcome:       runtime.OutcomeCompleted,
					SuccessorID:   "lin-succ-other",
					CreatedAt:     now,
				}))

				succs, err := cls.SuccessorsOf(ctx, "lin-pred-fanout")
				require.NoError(t, err)
				require.Len(t, succs, 2)
				// Ordered by outcome (string sort: "completed" < "terminated").
				assert.Equal(t, runtime.OutcomeCompleted, succs[0].Outcome)
				assert.Equal(t, "lin-succ-completed", succs[0].SuccessorID)
				assert.Equal(t, runtime.OutcomeTerminated, succs[1].Outcome)
				assert.Equal(t, "lin-succ-terminated", succs[1].SuccessorID)
			},
		},
		{
			name: "returns empty non-nil slice for unknown predecessor",
			assert: func(t *testing.T) {
				_, _, cls := newMySQLLineageStores(t)

				succs, err := cls.SuccessorsOf(t.Context(), "ghost-pred")
				require.NoError(t, err)
				assert.NotNil(t, succs)
				assert.Empty(t, succs)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}
