package persistence_test

// chainlink_test.go exercises the public façade constructor
// persistence.NewChainLinkStore (ADR-0045), verifying the thin delegation wires
// through to the underlying Postgres ChainLinkStore.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func TestChainLinkStoreFacade(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	cls, err := persistence.NewChainLinkStore(pool)
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, cls.Record(ctx, kernel.ChainLink{
		PredecessorID: "p1",
		Outcome:       kernel.OutcomeCompleted,
		SuccessorID:   "p1-next-completed",
		StartVars:     map[string]any{"k": "v"},
	}))

	got, ok, err := cls.LookupBySuccessor(ctx, "p1-next-completed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "p1", got.PredecessorID)

	// Duplicate (predecessor, outcome) surfaces the re-exported sentinel through
	// the façade store.
	err = cls.Record(ctx, kernel.ChainLink{PredecessorID: "p1", Outcome: kernel.OutcomeCompleted, SuccessorID: "other"})
	require.ErrorIs(t, err, kernel.ErrChainLinkExists)
}
