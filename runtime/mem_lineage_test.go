package runtime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// seedMemCallLink seeds one running call-link into a MemCallLinkStore using the
// existing SeedCallLink test-export helper.
func seedMemCallLink(s *runtime.MemCallLinkStore, childID, parentID string) {
	runtime.SeedCallLink(s, runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-lin",
		ParentDefVersion: 1,
		Depth:            1,
	})
}

// TestMemCallLinkStore_ParentOf tests the ParentOf lineage read on
// MemCallLinkStore.
func TestMemCallLinkStore_ParentOf(t *testing.T) {
	t.Parallel()

	t.Run("returns the call link when child has a parent", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemCallLinkStore()
		seedMemCallLink(cls, "child-1", "parent-1")

		got, err := cls.ParentOf(t.Context(), "child-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "child-1", got.ChildInstanceID)
		assert.Equal(t, "parent-1", got.ParentInstanceID)
	})

	t.Run("returns nil nil when child is a root instance", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemCallLinkStore()

		got, err := cls.ParentOf(t.Context(), "ghost")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

// TestMemCallLinkStore_ChildrenOf tests the ChildrenOf lineage read on
// MemCallLinkStore.
func TestMemCallLinkStore_ChildrenOf(t *testing.T) {
	t.Parallel()

	t.Run("returns all children ordered by child_instance_id", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemCallLinkStore()
		seedMemCallLink(cls, "child-zzz", "parent-A")
		seedMemCallLink(cls, "child-aaa", "parent-A")
		seedMemCallLink(cls, "child-other", "parent-B")

		children, err := cls.ChildrenOf(t.Context(), "parent-A")
		require.NoError(t, err)
		require.Len(t, children, 2)
		// Ordered by ChildInstanceID (stable sort in Mem).
		assert.Equal(t, "child-aaa", children[0].ChildInstanceID)
		assert.Equal(t, "child-zzz", children[1].ChildInstanceID)
		assert.Equal(t, "parent-A", children[0].ParentInstanceID)
		assert.Equal(t, "parent-A", children[1].ParentInstanceID)
	})

	t.Run("returns empty non-nil slice for unknown parent", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemCallLinkStore()

		children, err := cls.ChildrenOf(t.Context(), "no-such-parent")
		require.NoError(t, err)
		assert.NotNil(t, children)
		assert.Empty(t, children)
	})
}

// TestMemChainLinkStore_PredecessorOf tests the PredecessorOf lineage read on
// MemChainLinkStore.
func TestMemChainLinkStore_PredecessorOf(t *testing.T) {
	t.Parallel()

	t.Run("returns the chain link when successor was produced by chaining", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemChainLinkStore()
		ctx := t.Context()

		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID:            "pred-1",
			PredecessorDefinitionRef: "order:1",
			Outcome:                  runtime.OutcomeCompleted,
			SuccessorID:              "succ-1",
			SuccessorDefinitionRef:   "fulfillment:1",
			CreatedAt:                time.Now().UTC(),
		}))

		got, err := cls.PredecessorOf(ctx, "succ-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "pred-1", got.PredecessorID)
		assert.Equal(t, "succ-1", got.SuccessorID)
		assert.Equal(t, runtime.OutcomeCompleted, got.Outcome)
		assert.Equal(t, "order:1", got.PredecessorDefinitionRef)
		assert.Equal(t, "fulfillment:1", got.SuccessorDefinitionRef)
	})

	t.Run("returns nil nil when successor is a chain root", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemChainLinkStore()

		got, err := cls.PredecessorOf(t.Context(), "chain-root")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

// TestMemChainLinkStore_SuccessorsOf tests the SuccessorsOf lineage read on
// MemChainLinkStore.
func TestMemChainLinkStore_SuccessorsOf(t *testing.T) {
	t.Parallel()

	t.Run("returns all successors ordered by outcome", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemChainLinkStore()
		ctx := t.Context()
		now := time.Now().UTC()

		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "pred-fanout",
			Outcome:       runtime.OutcomeTerminated,
			SuccessorID:   "succ-terminated",
			CreatedAt:     now,
		}))
		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "pred-fanout",
			Outcome:       runtime.OutcomeCompleted,
			SuccessorID:   "succ-completed",
			CreatedAt:     now,
		}))
		// Different predecessor — must not appear.
		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "pred-other",
			Outcome:       runtime.OutcomeCompleted,
			SuccessorID:   "succ-other",
			CreatedAt:     now,
		}))

		succs, err := cls.SuccessorsOf(ctx, "pred-fanout")
		require.NoError(t, err)
		require.Len(t, succs, 2)
		// Ordered by outcome: "completed" < "terminated".
		assert.Equal(t, runtime.OutcomeCompleted, succs[0].Outcome)
		assert.Equal(t, "succ-completed", succs[0].SuccessorID)
		assert.Equal(t, runtime.OutcomeTerminated, succs[1].Outcome)
		assert.Equal(t, "succ-terminated", succs[1].SuccessorID)
	})

	t.Run("returns empty non-nil slice for unknown predecessor", func(t *testing.T) {
		t.Parallel()
		cls := runtime.NewMemChainLinkStore()

		succs, err := cls.SuccessorsOf(t.Context(), "ghost-pred")
		require.NoError(t, err)
		assert.NotNil(t, succs)
		assert.Empty(t, succs)
	})
}
