package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newMySQLChainLinkStore returns a freshly migrated ChainLinkStore backed by a
// MySQL testcontainer database (auto-migrated via database.RunTestMySQL).
func newMySQLChainLinkStore(t *testing.T) *mypkg.ChainLinkStore {
	t.Helper()
	db := database.RunTestMySQL(t)
	return mypkg.NewChainLinkStore(db)
}

// TestChainLinkStore_RecordLookupList is the primary table-driven test covering
// the full ChainLinkStore port: Record, LookupBySuccessor, and ListByPredecessor.
func TestChainLinkStore_RecordLookupList(t *testing.T) {
	t.Parallel()

	t.Run("record and lookup by successor", func(t *testing.T) {
		t.Parallel()
		cls := newMySQLChainLinkStore(t)
		ctx := t.Context()

		link := runtime.ChainLink{
			PredecessorID:            "pred-1",
			PredecessorDefinitionRef: "order:1",
			Outcome:                  runtime.OutcomeCompleted,
			SuccessorID:              "succ-1",
			SuccessorDefinitionRef:   "fulfillment:1",
			StartVars:                map[string]any{"orderID": "o-99"},
			CreatedAt:                time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
		}
		require.NoError(t, cls.Record(ctx, link))

		got, ok, err := cls.LookupBySuccessor(ctx, "succ-1")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "pred-1", got.PredecessorID)
		assert.Equal(t, "order:1", got.PredecessorDefinitionRef)
		assert.Equal(t, "fulfillment:1", got.SuccessorDefinitionRef)
		assert.Equal(t, runtime.OutcomeCompleted, got.Outcome)
		assert.Equal(t, "succ-1", got.SuccessorID)
		assert.Equal(t, map[string]any{"orderID": "o-99"}, got.StartVars)
	})

	t.Run("lookup unknown successor returns ok=false no error", func(t *testing.T) {
		t.Parallel()
		cls := newMySQLChainLinkStore(t)

		_, ok, err := cls.LookupBySuccessor(t.Context(), "does-not-exist")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("duplicate (predecessor+outcome) returns ErrChainLinkExists", func(t *testing.T) {
		t.Parallel()
		cls := newMySQLChainLinkStore(t)
		ctx := t.Context()

		link := runtime.ChainLink{
			PredecessorID: "dup-pred",
			Outcome:       runtime.OutcomeFailed,
			SuccessorID:   "dup-succ-1",
			CreatedAt:     time.Now().UTC(),
		}
		require.NoError(t, cls.Record(ctx, link))

		err := cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "dup-pred",
			Outcome:       runtime.OutcomeFailed,
			SuccessorID:   "dup-succ-2",
			CreatedAt:     time.Now().UTC(),
		})
		require.ErrorIs(t, err, runtime.ErrChainLinkExists)
	})

	t.Run("list by predecessor returns all hops ordered by outcome", func(t *testing.T) {
		t.Parallel()
		cls := newMySQLChainLinkStore(t)
		ctx := t.Context()

		now := time.Now().UTC()
		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "multi-pred",
			Outcome:       runtime.OutcomeCompleted,
			SuccessorID:   "multi-succ-completed",
			CreatedAt:     now,
		}))
		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "multi-pred",
			Outcome:       runtime.OutcomeTerminated,
			SuccessorID:   "multi-succ-terminated",
			CreatedAt:     now,
		}))
		// Different predecessor — must not appear.
		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "other-pred",
			Outcome:       runtime.OutcomeCompleted,
			SuccessorID:   "other-succ",
			CreatedAt:     now,
		}))

		hops, err := cls.ListByPredecessor(ctx, "multi-pred")
		require.NoError(t, err)
		require.Len(t, hops, 2)
		assert.ElementsMatch(t,
			[]string{"multi-succ-completed", "multi-succ-terminated"},
			[]string{hops[0].SuccessorID, hops[1].SuccessorID},
		)
		// Ordered by outcome (string sort: "completed" < "terminated").
		assert.Equal(t, runtime.OutcomeCompleted, hops[0].Outcome)
		assert.Equal(t, runtime.OutcomeTerminated, hops[1].Outcome)
	})

	t.Run("list by predecessor returns nil slice for unknown predecessor", func(t *testing.T) {
		t.Parallel()
		cls := newMySQLChainLinkStore(t)

		hops, err := cls.ListByPredecessor(t.Context(), "ghost")
		require.NoError(t, err)
		assert.Empty(t, hops)
	})

	t.Run("nil start vars round-trips as nil", func(t *testing.T) {
		t.Parallel()
		cls := newMySQLChainLinkStore(t)
		ctx := t.Context()

		require.NoError(t, cls.Record(ctx, runtime.ChainLink{
			PredecessorID: "nil-vars-pred",
			Outcome:       runtime.OutcomeCompleted,
			SuccessorID:   "nil-vars-succ",
			StartVars:     nil,
			CreatedAt:     time.Now().UTC(),
		}))

		got, ok, err := cls.LookupBySuccessor(ctx, "nil-vars-succ")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Nil(t, got.StartVars)
	})
}
