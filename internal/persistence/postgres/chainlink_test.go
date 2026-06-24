package postgres_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newChainLinkStore returns a freshly migrated Postgres ChainLinkStore + pool.
func newChainLinkStore(t *testing.T) (runtime.ChainLinkStore, *pgxpool.Pool) {
	t.Helper()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	return pg.NewChainLinkStore(pool), pool
}

func TestPostgresChainLinkStoreRecordAndLookup(t *testing.T) {
	cls, _ := newChainLinkStore(t)
	ctx := t.Context()

	link := runtime.ChainLink{
		PredecessorID:            "approval-1",
		PredecessorDefinitionRef: "approval:1",
		Outcome:                  runtime.OutcomeCompleted,
		SuccessorID:              "approval-1-next-completed",
		SuccessorDefinitionRef:   "fulfillment:1",
		StartVars:                map[string]any{"orderID": "o-7"},
	}
	require.NoError(t, cls.Record(ctx, link))

	got, ok, err := cls.LookupBySuccessor(ctx, "approval-1-next-completed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "approval-1", got.PredecessorID)
	assert.Equal(t, "approval:1", got.PredecessorDefinitionRef)
	assert.Equal(t, "fulfillment:1", got.SuccessorDefinitionRef)
	assert.Equal(t, runtime.OutcomeCompleted, got.Outcome)
	assert.Equal(t, map[string]any{"orderID": "o-7"}, got.StartVars)
}

func TestPostgresChainLinkStoreLookupUnknown(t *testing.T) {
	cls, _ := newChainLinkStore(t)
	_, ok, err := cls.LookupBySuccessor(t.Context(), "nope")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPostgresChainLinkStoreDuplicateRejected(t *testing.T) {
	cls, _ := newChainLinkStore(t)
	ctx := t.Context()
	link := runtime.ChainLink{
		PredecessorID: "p1",
		Outcome:       runtime.OutcomeFailed,
		SuccessorID:   "p1-next-failed",
	}
	require.NoError(t, cls.Record(ctx, link))

	// 23505 unique-violation on (predecessor, outcome) maps to ErrChainLinkExists.
	err := cls.Record(ctx, runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeFailed, SuccessorID: "other"})
	require.ErrorIs(t, err, runtime.ErrChainLinkExists)
}

func TestPostgresChainLinkStoreListByPredecessor(t *testing.T) {
	cls, _ := newChainLinkStore(t)
	ctx := t.Context()

	require.NoError(t, cls.Record(ctx, runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeCompleted, SuccessorID: "p1-next-completed"}))
	require.NoError(t, cls.Record(ctx, runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeTerminated, SuccessorID: "p1-next-terminated"}))
	require.NoError(t, cls.Record(ctx, runtime.ChainLink{PredecessorID: "p2", Outcome: runtime.OutcomeCompleted, SuccessorID: "p2-next-completed"}))

	hops, err := cls.ListByPredecessor(ctx, "p1")
	require.NoError(t, err)
	require.Len(t, hops, 2)
	assert.ElementsMatch(t,
		[]string{"p1-next-completed", "p1-next-terminated"},
		[]string{hops[0].SuccessorID, hops[1].SuccessorID},
	)
}

// TestStoreCreateDuplicateInstance asserts the Postgres Store.Create maps a
// primary-key violation on the instance insert to runtime.ErrInstanceExists,
// aligning it with MemStore (ADR-0045) so the Chainer can no-op a duplicate start.
func TestStoreCreateDuplicateInstance(t *testing.T) {
	store, _ := newCallLinkStore(t)
	ctx := t.Context()

	_, err := store.Create(ctx, callLinkBaseStep("dup-inst"))
	require.NoError(t, err)

	_, err = store.Create(ctx, callLinkBaseStep("dup-inst"))
	require.ErrorIs(t, err, runtime.ErrInstanceExists)
}
