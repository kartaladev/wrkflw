package persistence_test

// chainlink_test.go exercises the public façade constructor
// persistence.NewChainLinkStore (ADR-0045), verifying the thin delegation wires
// through to the underlying Postgres ChainLinkStore.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestChainLinkStoreFacade(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	cls, err := persistence.NewChainLinkStore(pool)
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, cls.Record(ctx, runtime.ChainLink{
		PredecessorID: "p1",
		Outcome:       runtime.OutcomeCompleted,
		SuccessorID:   "p1-next-completed",
		StartVars:     map[string]any{"k": "v"},
	}))

	got, ok, err := cls.LookupBySuccessor(ctx, "p1-next-completed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "p1", got.PredecessorID)

	// Duplicate (predecessor, outcome) surfaces the re-exported sentinel through
	// the façade store.
	err = cls.Record(ctx, runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeCompleted, SuccessorID: "other"})
	require.ErrorIs(t, err, runtime.ErrChainLinkExists)
}
