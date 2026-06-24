package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestMemChainLinkStoreRecordAndLookup(t *testing.T) {
	cls := runtime.NewMemChainLinkStore()
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
	assert.Equal(t, runtime.OutcomeCompleted, got.Outcome)
	assert.Equal(t, map[string]any{"orderID": "o-7"}, got.StartVars)
}

func TestMemChainLinkStoreLookupUnknownSuccessor(t *testing.T) {
	cls := runtime.NewMemChainLinkStore()
	_, ok, err := cls.LookupBySuccessor(t.Context(), "nope")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestMemChainLinkStoreRecordDuplicateIsRejected(t *testing.T) {
	cls := runtime.NewMemChainLinkStore()
	ctx := t.Context()
	link := runtime.ChainLink{
		PredecessorID: "p1",
		Outcome:       runtime.OutcomeFailed,
		SuccessorID:   "p1-next-failed",
	}
	require.NoError(t, cls.Record(ctx, link))

	// A second Record for the same (PredecessorID, Outcome) is the exactly-once
	// backstop: it returns ErrChainLinkExists and does not overwrite.
	err := cls.Record(ctx, runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeFailed, SuccessorID: "other"})
	require.ErrorIs(t, err, runtime.ErrChainLinkExists)

	got, ok, err := cls.LookupBySuccessor(ctx, "p1-next-failed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "p1-next-failed", got.SuccessorID, "the original link must be intact")
}

func TestMemChainLinkStoreListByPredecessor(t *testing.T) {
	cls := runtime.NewMemChainLinkStore()
	ctx := t.Context()

	// Same predecessor can fan out to distinct outcomes (different hops).
	require.NoError(t, cls.Record(ctx, runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeCompleted, SuccessorID: "p1-next-completed"}))
	require.NoError(t, cls.Record(ctx, runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeFailed, SuccessorID: "p1-next-failed"}))
	require.NoError(t, cls.Record(ctx, runtime.ChainLink{PredecessorID: "p2", Outcome: runtime.OutcomeCompleted, SuccessorID: "p2-next-completed"}))

	hops, err := cls.ListByPredecessor(ctx, "p1")
	require.NoError(t, err)
	require.Len(t, hops, 2)

	successors := []string{hops[0].SuccessorID, hops[1].SuccessorID}
	assert.ElementsMatch(t, []string{"p1-next-completed", "p1-next-failed"}, successors)
}

func TestOutcomeConstants(t *testing.T) {
	assert.Equal(t, runtime.Outcome("completed"), runtime.OutcomeCompleted)
	assert.Equal(t, runtime.Outcome("failed"), runtime.OutcomeFailed)
	assert.Equal(t, runtime.Outcome("terminated"), runtime.OutcomeTerminated)
}
