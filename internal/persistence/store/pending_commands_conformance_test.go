package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// pendingMarkedStep is newTestInstance — the package's richer seed, carrying
// tokens, history and sequence counters — plus a crash-recovery mark of every
// kind that has a non-scalar field, so the round trip below exercises maps and
// nested structs and not just strings.
func pendingMarkedStep(t *testing.T, id string, markedAt time.Time) kernel.AppliedStep {
	t.Helper()
	step := newTestInstance(t, id)
	step.State.PendingCommands = []engine.PendingCommand{
		{
			Kind:      engine.PendingInvokeAction,
			CommandID: "cmd-1",
			Name:      "charge",
			Input:     map[string]any{"amount": "12.50"},
		},
		{
			Kind:      engine.PendingStartSubInstance,
			CommandID: "cmd-2",
			DefRef:    model.Version("child", 3),
			Input:     map[string]any{"seed": "v"},
		},
	}
	step.State.PendingCommandsAt = markedAt
	return step
}

// TestPendingCommandMarkRoundTripsAndClearsOnEveryDialect covers the two claims
// that made mechanism (a) proportionate, in one 3-dialect pass because the
// second's setup is the first's assertion.
//
// FIRST: the mark rides the existing `snapshot` column and therefore needs NO
// migration and no dialect-parity work — Postgres JSONB, MySQL JSON and SQLite
// TEXT all carry it, field for field. A silently dropped Input map would leave a
// mark that survives the round trip and re-drives the wrong command, so the
// comparison is field-level and not merely "non-empty".
//
// SECOND: clearing the mark is a BOOKKEEPING write, not a step. The version does
// not move, the journal does not grow, and a stale expected is a silent no-op.
// Each is load-bearing: a version bump would hand the driver a spurious
// ErrConcurrentUpdate on its very next commit, a journal row would corrupt
// replay, and treating a stale version as a conflict would push a retry loop at
// work that is already done.
func TestPendingCommandMarkRoundTripsAndClearsOnEveryDialect(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ctx := t.Context()
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)

		markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		id := "i-pending-" + b.name
		seeded := pendingMarkedStep(t, id, markedAt)

		version, err := s.Create(ctx, seeded)
		require.NoError(t, err)

		// ── the mark round-trips ─────────────────────────────────────────────
		got, gotVersion, err := s.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, got.PendingCommands, 2, "dialect %s must carry both marks", b.name)
		assert.Equal(t, seeded.State.PendingCommands, got.PendingCommands,
			"dialect %s must round-trip every pending-command field", b.name)
		assert.True(t, got.PendingCommandsAt.Equal(markedAt),
			"dialect %s must round-trip the lease stamp", b.name)
		require.Equal(t, version, gotVersion)

		journalBefore, err := s.Entries(ctx, id)
		require.NoError(t, err)

		// ── and clearing it is bookkeeping, not a step ───────────────────────
		require.NoError(t, s.ClearPendingCommands(ctx, id, version))

		cleared, clearedVersion, err := s.Load(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, cleared.PendingCommands, "dialect %s: the mark must be gone", b.name)
		assert.True(t, cleared.PendingCommandsAt.IsZero(),
			"dialect %s: the lease stamp must be cleared with the mark", b.name)
		assert.Equal(t, version, clearedVersion,
			"dialect %s: clearing the mark must NOT advance the concurrency token", b.name)
		assert.Equal(t, got.Status, cleared.Status,
			"dialect %s: no other snapshot field may change", b.name)
		assert.Equal(t, got.Tokens, cleared.Tokens,
			"dialect %s: no other snapshot field may change", b.name)

		journalAfter, err := s.Entries(ctx, id)
		require.NoError(t, err)
		assert.Len(t, journalAfter, len(journalBefore),
			"dialect %s: clearing the mark applies no trigger, so it must write no journal row", b.name)

		// A stale expected is success, not a conflict.
		require.NoError(t, s.ClearPendingCommands(ctx, id, version+7),
			"dialect %s: a stale version must be a silent no-op", b.name)
		// So is an instance that does not exist at all.
		require.NoError(t, s.ClearPendingCommands(ctx, "no-such-instance", version),
			"dialect %s: an unknown instance must be a silent no-op", b.name)
	})
}
