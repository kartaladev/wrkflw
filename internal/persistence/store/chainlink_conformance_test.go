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
	// Compile-time assertion: *store.ChainLinkStore satisfies runtime.ChainLinkStore.
	var _ runtime.ChainLinkStore = (*store.ChainLinkStore)(nil)

	t.Run("record and lookup by successor", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls := store.NewChainLinkStore(b.conn, b.dialect)
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
			cls := store.NewChainLinkStore(b.conn, b.dialect)

			_, ok, err := cls.LookupBySuccessor(t.Context(), "does-not-exist")
			require.NoError(t, err, "%s: LookupBySuccessor", b.name)
			assert.False(t, ok, "%s: expected ok=false for unknown successor", b.name)
		})
	})

	t.Run("duplicate predecessor+outcome returns ErrChainLinkExists first successor wins", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls := store.NewChainLinkStore(b.conn, b.dialect)
			ctx := t.Context()

			first := runtime.ChainLink{
				PredecessorID: "dup-pred",
				Outcome:       runtime.OutcomeFailed,
				SuccessorID:   "dup-succ-first",
				CreatedAt:     time.Now().UTC(),
			}
			require.NoError(t, cls.Record(ctx, first), "%s: Record first", b.name)

			// Second insert for the same (PredecessorID, Outcome) must be rejected.
			err := cls.Record(ctx, runtime.ChainLink{
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
			cls := store.NewChainLinkStore(b.conn, b.dialect)
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
			cls := store.NewChainLinkStore(b.conn, b.dialect)

			hops, err := cls.ListByPredecessor(t.Context(), "ghost-pred")
			require.NoError(t, err, "%s: ListByPredecessor", b.name)
			assert.Empty(t, hops, "%s: hops must be empty", b.name)
		})
	})

	t.Run("created_at UTC round-trip", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			cls := store.NewChainLinkStore(b.conn, b.dialect)
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
			cls := store.NewChainLinkStore(b.conn, b.dialect)
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
}
