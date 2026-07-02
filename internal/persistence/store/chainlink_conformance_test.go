package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestChainLinkStore exercises the neutral ChainLinkStore across all three
// dialects (Postgres, MySQL, SQLite) via forEachDialect.
func TestChainLinkStore(t *testing.T) {
	// Compile-time assertions: *store.ChainLinkStore satisfies both
	// runtime.ChainLinkStore and runtime.ChainLineageReader.
	var _ runtime.ChainLinkStore = (*store.ChainLinkStore)(nil)
	var _ runtime.ChainLineageReader = (*store.ChainLinkStore)(nil)

	t.Run("record and lookup by successor", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			link := runtime.ChainLink{
				PredecessorID:            "pred-lookup-1",
				PredecessorDefinitionRef: "order:1",
				Outcome:                  runtime.OutcomeCompleted,
				SuccessorID:              "succ-lookup-1",
				SuccessorDefinitionRef:   "fulfillment:1",
				StartVars:                map[string]any{"orderID": "o-99"},
				CreatedAt:                time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
			}
			require.NoError(t, cls.Record(ctx, link), "%s: Record", b.name)

			got, ok, err := cls.LookupBySuccessor(ctx, "succ-lookup-1")
			require.NoError(t, err, "%s: LookupBySuccessor", b.name)
			require.True(t, ok, "%s: expected ok=true for known successor", b.name)

			assert.Equal(t, "pred-lookup-1", got.PredecessorID, "%s: PredecessorID", b.name)
			assert.Equal(t, "order:1", got.PredecessorDefinitionRef, "%s: PredecessorDefinitionRef", b.name)
			assert.Equal(t, "fulfillment:1", got.SuccessorDefinitionRef, "%s: SuccessorDefinitionRef", b.name)
			assert.Equal(t, runtime.OutcomeCompleted, got.Outcome, "%s: Outcome", b.name)
			assert.Equal(t, "succ-lookup-1", got.SuccessorID, "%s: SuccessorID", b.name)
			assert.Equal(t, map[string]any{"orderID": "o-99"}, got.StartVars, "%s: StartVars", b.name)
		})
	})

	t.Run("lookup unknown successor returns ok=false no error", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)

			_, ok, err := cls.LookupBySuccessor(t.Context(), "does-not-exist")
			require.NoError(t, err, "%s: LookupBySuccessor", b.name)
			assert.False(t, ok, "%s: expected ok=false for unknown successor", b.name)
		})
	})

	t.Run("duplicate predecessor+outcome returns ErrChainLinkExists first successor wins", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			first := runtime.ChainLink{
				PredecessorID: "dup-pred",
				Outcome:       runtime.OutcomeFailed,
				SuccessorID:   "dup-succ-first",
				CreatedAt:     time.Now().UTC(),
			}
			require.NoError(t, cls.Record(ctx, first), "%s: Record first", b.name)

			// Second insert for the same (PredecessorID, Outcome) must be rejected.
			err = cls.Record(ctx, runtime.ChainLink{
				PredecessorID: "dup-pred",
				Outcome:       runtime.OutcomeFailed,
				SuccessorID:   "dup-succ-second",
				CreatedAt:     time.Now().UTC(),
			})
			require.ErrorIs(t, err, runtime.ErrChainLinkExists, "%s: second Record must return ErrChainLinkExists", b.name)

			// The first successor must win — no overwrite.
			got, err := cls.ListByPredecessor(ctx, "dup-pred")
			require.NoError(t, err, "%s: ListByPredecessor after dup", b.name)
			require.Len(t, got, 1, "%s: exactly one hop after dup rejection", b.name)
			assert.Equal(t, "dup-succ-first", got[0].SuccessorID, "%s: first successor must win", b.name)
		})
	})

	t.Run("list by predecessor returns all hops ordered by outcome", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			require.NoError(t, cls.Record(ctx, runtime.ChainLink{
				PredecessorID: "list-pred",
				Outcome:       runtime.OutcomeCompleted,
				SuccessorID:   "list-pred-next-completed",
			}), "%s: Record completed", b.name)
			require.NoError(t, cls.Record(ctx, runtime.ChainLink{
				PredecessorID: "list-pred",
				Outcome:       runtime.OutcomeTerminated,
				SuccessorID:   "list-pred-next-terminated",
			}), "%s: Record terminated", b.name)
			// Unrelated predecessor — must not appear.
			require.NoError(t, cls.Record(ctx, runtime.ChainLink{
				PredecessorID: "other-pred",
				Outcome:       runtime.OutcomeCompleted,
				SuccessorID:   "other-pred-next",
			}), "%s: Record other-pred", b.name)

			hops, err := cls.ListByPredecessor(ctx, "list-pred")
			require.NoError(t, err, "%s: ListByPredecessor", b.name)
			require.Len(t, hops, 2, "%s: expected exactly 2 hops", b.name)
			assert.ElementsMatch(t,
				[]string{"list-pred-next-completed", "list-pred-next-terminated"},
				[]string{hops[0].SuccessorID, hops[1].SuccessorID},
				"%s: successor IDs mismatch", b.name,
			)
		})
	})

	t.Run("list by predecessor returns empty slice when no hops exist", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)

			hops, err := cls.ListByPredecessor(t.Context(), "ghost-pred")
			require.NoError(t, err, "%s: ListByPredecessor", b.name)
			assert.Empty(t, hops, "%s: hops must be empty", b.name)
		})
	})

	t.Run("created_at UTC round-trip", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			// Use a known UTC timestamp with sub-second precision to exercise the
			// time codec on all backends (TEXT on SQLite, native on PG/MySQL).
			at := time.Date(2026, 6, 28, 10, 30, 45, 123456789, time.UTC)
			link := runtime.ChainLink{
				PredecessorID: "ts-pred",
				Outcome:       runtime.OutcomeCompleted,
				SuccessorID:   "ts-succ",
				CreatedAt:     at,
			}
			require.NoError(t, cls.Record(ctx, link), "%s: Record", b.name)

			got, ok, err := cls.LookupBySuccessor(ctx, "ts-succ")
			require.NoError(t, err, "%s: LookupBySuccessor", b.name)
			require.True(t, ok, "%s: ok", b.name)
			// Normalised to UTC on every backend (ADR-0080).
			assert.Equal(t, time.UTC, got.CreatedAt.Location(), "%s: CreatedAt must be UTC", b.name)
			// Sub-second precision must survive on all native-time backends (PG/MySQL).
			// SQLite TEXT codec rounds to nanosecond via RFC3339Nano, so allow within 1ms.
			assert.WithinDuration(t, at, got.CreatedAt, time.Millisecond, "%s: CreatedAt round-trip", b.name)
		})
	})

	t.Run("nil start vars round-trip", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			link := runtime.ChainLink{
				PredecessorID: "nil-vars-pred",
				Outcome:       runtime.OutcomeTerminated,
				SuccessorID:   "nil-vars-succ",
				StartVars:     nil,
				CreatedAt:     time.Now().UTC(),
			}
			require.NoError(t, cls.Record(ctx, link), "%s: Record nil vars", b.name)

			got, ok, err := cls.LookupBySuccessor(ctx, "nil-vars-succ")
			require.NoError(t, err, "%s: LookupBySuccessor", b.name)
			require.True(t, ok, "%s: ok", b.name)
			assert.Nil(t, got.StartVars, "%s: nil StartVars must survive round-trip", b.name)
		})
	})

	// --- ChainLineageReader conformance ---

	t.Run("PredecessorOf returns the predecessor link", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			link := runtime.ChainLink{
				PredecessorID:            "lineage-pred-1",
				PredecessorDefinitionRef: "order:2",
				Outcome:                  runtime.OutcomeCompleted,
				SuccessorID:              "lineage-succ-1",
				SuccessorDefinitionRef:   "fulfillment:2",
				StartVars:                map[string]any{"k": "v"},
				CreatedAt:                time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
			}
			require.NoError(t, cls.Record(ctx, link), "%s: Record", b.name)

			got, err := cls.PredecessorOf(ctx, "lineage-succ-1")
			require.NoError(t, err, "%s: PredecessorOf", b.name)
			require.NotNil(t, got, "%s: expected non-nil result for known successor", b.name)

			assert.Equal(t, "lineage-pred-1", got.PredecessorID, "%s: PredecessorID", b.name)
			assert.Equal(t, "order:2", got.PredecessorDefinitionRef, "%s: PredecessorDefinitionRef", b.name)
			assert.Equal(t, runtime.OutcomeCompleted, got.Outcome, "%s: Outcome", b.name)
			assert.Equal(t, "lineage-succ-1", got.SuccessorID, "%s: SuccessorID", b.name)
			assert.Equal(t, "fulfillment:2", got.SuccessorDefinitionRef, "%s: SuccessorDefinitionRef", b.name)
			assert.Equal(t, map[string]any{"k": "v"}, got.StartVars, "%s: StartVars", b.name)
			assert.Equal(t, time.UTC, got.CreatedAt.Location(), "%s: CreatedAt must be UTC", b.name)
			assert.WithinDuration(t, link.CreatedAt, got.CreatedAt, time.Millisecond, "%s: CreatedAt round-trip", b.name)
		})
	})

	t.Run("PredecessorOf returns nil nil for chain root", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)

			got, err := cls.PredecessorOf(t.Context(), "not-a-successor")
			require.NoError(t, err, "%s: PredecessorOf", b.name)
			assert.Nil(t, got, "%s: expected nil for unknown successor", b.name)
		})
	})

	t.Run("SuccessorsOf returns successor links ordered by outcome", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			require.NoError(t, cls.Record(ctx, runtime.ChainLink{
				PredecessorID:            "sof-pred",
				PredecessorDefinitionRef: "proc:1",
				Outcome:                  runtime.OutcomeCompleted,
				SuccessorID:              "sof-succ-completed",
				SuccessorDefinitionRef:   "next:1",
				CreatedAt:                time.Now().UTC(),
			}), "%s: Record completed", b.name)
			require.NoError(t, cls.Record(ctx, runtime.ChainLink{
				PredecessorID:            "sof-pred",
				PredecessorDefinitionRef: "proc:1",
				Outcome:                  runtime.OutcomeTerminated,
				SuccessorID:              "sof-succ-terminated",
				SuccessorDefinitionRef:   "terminated-next:1",
				CreatedAt:                time.Now().UTC(),
			}), "%s: Record terminated", b.name)
			// Unrelated predecessor — must not appear.
			require.NoError(t, cls.Record(ctx, runtime.ChainLink{
				PredecessorID: "sof-other-pred",
				Outcome:       runtime.OutcomeCompleted,
				SuccessorID:   "sof-other-succ",
				CreatedAt:     time.Now().UTC(),
			}), "%s: Record other-pred", b.name)

			links, err := cls.SuccessorsOf(ctx, "sof-pred")
			require.NoError(t, err, "%s: SuccessorsOf", b.name)
			require.Len(t, links, 2, "%s: expected exactly 2 successors", b.name)
			// Results must be ordered by outcome (ascending lexicographic).
			assert.Equal(t, runtime.OutcomeCompleted, links[0].Outcome, "%s: links[0].Outcome", b.name)
			assert.Equal(t, "sof-succ-completed", links[0].SuccessorID, "%s: links[0].SuccessorID", b.name)
			assert.Equal(t, runtime.OutcomeTerminated, links[1].Outcome, "%s: links[1].Outcome", b.name)
			assert.Equal(t, "sof-succ-terminated", links[1].SuccessorID, "%s: links[1].SuccessorID", b.name)
			assert.Equal(t, "proc:1", links[0].PredecessorDefinitionRef, "%s: links[0].PredecessorDefinitionRef", b.name)
		})
	})

	t.Run("SuccessorsOf returns non-nil empty slice when no successors exist", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls, err := store.NewChainLinkStore(b.conn, b.dialect)
			require.NoError(t, err)

			links, err := cls.SuccessorsOf(t.Context(), "ghost-predecessor")
			require.NoError(t, err, "%s: SuccessorsOf", b.name)
			// Contract from ChainLineageReader: non-nil empty slice (never nil).
			assert.NotNil(t, links, "%s: SuccessorsOf must return non-nil empty slice", b.name)
			assert.Empty(t, links, "%s: SuccessorsOf must return empty slice when no successors", b.name)
		})
	})
}
