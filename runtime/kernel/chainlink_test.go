package kernel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestMemChainLinkStoreRecordAndLookup(t *testing.T) {
	cls := kernel.NewMemChainLinkStore()
	ctx := t.Context()

	link := kernel.ChainLink{
		PredecessorID:            "approval-1",
		PredecessorDefinitionRef: model.Version("approval", 1),
		Outcome:                  kernel.OutcomeCompleted,
		SuccessorID:              "approval-1-next-completed",
		SuccessorDefinitionRef:   model.Version("fulfillment", 1),
		StartVars:                map[string]any{"orderID": "o-7"},
	}
	require.NoError(t, cls.Record(ctx, link))

	got, ok, err := cls.LookupBySuccessor(ctx, "approval-1-next-completed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "approval-1", got.PredecessorID)
	assert.Equal(t, kernel.OutcomeCompleted, got.Outcome)
	assert.Equal(t, map[string]any{"orderID": "o-7"}, got.StartVars)
}

func TestMemChainLinkStoreLookupUnknownSuccessor(t *testing.T) {
	cls := kernel.NewMemChainLinkStore()
	_, ok, err := cls.LookupBySuccessor(t.Context(), "nope")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestMemChainLinkStoreRecordDuplicateIsRejected(t *testing.T) {
	cls := kernel.NewMemChainLinkStore()
	ctx := t.Context()
	link := kernel.ChainLink{
		PredecessorID: "p1",
		Outcome:       kernel.OutcomeFailed,
		SuccessorID:   "p1-next-failed",
	}
	require.NoError(t, cls.Record(ctx, link))

	// A second Record for the same (PredecessorID, Outcome) is the exactly-once
	// backstop: it returns ErrChainLinkExists and does not overwrite.
	err := cls.Record(ctx, kernel.ChainLink{PredecessorID: "p1", Outcome: kernel.OutcomeFailed, SuccessorID: "other"})
	require.ErrorIs(t, err, kernel.ErrChainLinkExists)

	got, ok, err := cls.LookupBySuccessor(ctx, "p1-next-failed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "p1-next-failed", got.SuccessorID, "the original link must be intact")
}

func TestMemChainLinkStoreListByPredecessor(t *testing.T) {
	cls := kernel.NewMemChainLinkStore()
	ctx := t.Context()

	// Same predecessor can fan out to distinct outcomes (different hops).
	require.NoError(t, cls.Record(ctx, kernel.ChainLink{PredecessorID: "p1", Outcome: kernel.OutcomeCompleted, SuccessorID: "p1-next-completed"}))
	require.NoError(t, cls.Record(ctx, kernel.ChainLink{PredecessorID: "p1", Outcome: kernel.OutcomeFailed, SuccessorID: "p1-next-failed"}))
	require.NoError(t, cls.Record(ctx, kernel.ChainLink{PredecessorID: "p2", Outcome: kernel.OutcomeCompleted, SuccessorID: "p2-next-completed"}))

	hops, err := cls.ListByPredecessor(ctx, "p1")
	require.NoError(t, err)
	require.Len(t, hops, 2)

	successors := []string{hops[0].SuccessorID, hops[1].SuccessorID}
	assert.ElementsMatch(t, []string{"p1-next-completed", "p1-next-failed"}, successors)
}

func TestOutcomeConstants(t *testing.T) {
	assert.Equal(t, kernel.ChainOutcome("completed"), kernel.OutcomeCompleted)
	assert.Equal(t, kernel.ChainOutcome("failed"), kernel.OutcomeFailed)
	assert.Equal(t, kernel.ChainOutcome("terminated"), kernel.OutcomeTerminated)
}
