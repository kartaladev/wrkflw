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

// pendingMarkedState is a minimal running instance carrying a pending-command
// mark of every kind that has a non-scalar field, so the round trip below is
// exercising maps and nested structs and not just strings.
func pendingMarkedState(id string, markedAt time.Time) engine.InstanceState {
	return engine.InstanceState{
		InstanceID: id,
		DefID:      "d-pending",
		DefVersion: 1,
		Status:     engine.StatusRunning,
		StartedAt:  markedAt,
		PendingCommands: []engine.PendingCommand{
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
		},
		PendingCommandsAt: markedAt,
	}
}

// TestPendingCommandMarkRoundTripsOnEveryDialect pins the claim that made option
// (a) proportionate in the first place: the crash-recovery mark rides the
// existing `snapshot` column and therefore needs NO migration and no
// dialect-parity work — Postgres JSONB, MySQL JSON and SQLite TEXT all carry it.
//
// The mark is compared field by field against what was written, because a
// silently dropped Input map would leave a mark that survives the round trip and
// re-drives the wrong command.
func TestPendingCommandMarkRoundTripsOnEveryDialect(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ctx := t.Context()
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)

		markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		want := pendingMarkedState("i-pending-"+b.name, markedAt)
		_, err = s.Create(ctx, kernel.AppliedStep{
			State:   want,
			Trigger: engine.NewStartInstance(markedAt, nil),
		})
		require.NoError(t, err)

		got, _, err := s.Load(ctx, want.InstanceID)
		require.NoError(t, err)

		require.Len(t, got.PendingCommands, 2, "dialect %s must carry both marks", b.name)
		assert.Equal(t, want.PendingCommands, got.PendingCommands,
			"dialect %s must round-trip every pending-command field", b.name)
		assert.True(t, got.PendingCommandsAt.Equal(markedAt),
			"dialect %s must round-trip the lease stamp", b.name)
	})
}

// TestClearPendingCommandsOnEveryDialect pins the three properties that make
// clearing the mark a bookkeeping write rather than a step: the version does not
// move, the journal does not grow, and a stale expected is a silent no-op.
//
// Each of those is load-bearing. A version bump would hand the driver a spurious
// ErrConcurrentUpdate on its very next commit; a journal row would corrupt
// replay; and treating a stale version as a conflict would push a retry loop at
// work that is already done.
func TestClearPendingCommandsOnEveryDialect(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ctx := t.Context()
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)

		markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		seeded := pendingMarkedState("i-clear-"+b.name, markedAt)
		version, err := s.Create(ctx, kernel.AppliedStep{
			State:   seeded,
			Trigger: engine.NewStartInstance(markedAt, nil),
		})
		require.NoError(t, err)

		journalBefore, err := s.Entries(ctx, seeded.InstanceID)
		require.NoError(t, err)

		require.NoError(t, s.ClearPendingCommands(ctx, seeded.InstanceID, version))

		got, gotVersion, err := s.Load(ctx, seeded.InstanceID)
		require.NoError(t, err)
		assert.Empty(t, got.PendingCommands, "dialect %s: the mark must be gone", b.name)
		assert.True(t, got.PendingCommandsAt.IsZero(),
			"dialect %s: the lease stamp must be cleared with the mark", b.name)
		assert.Equal(t, version, gotVersion,
			"dialect %s: clearing the mark must NOT advance the concurrency token", b.name)
		assert.Equal(t, seeded.Status, got.Status,
			"dialect %s: no other snapshot field may change", b.name)

		journalAfter, err := s.Entries(ctx, seeded.InstanceID)
		require.NoError(t, err)
		assert.Len(t, journalAfter, len(journalBefore),
			"dialect %s: clearing the mark applies no trigger, so it must write no journal row", b.name)

		// A stale expected is success, not a conflict.
		require.NoError(t, s.ClearPendingCommands(ctx, seeded.InstanceID, version+7),
			"dialect %s: a stale version must be a silent no-op", b.name)
		// So is an instance that does not exist at all.
		require.NoError(t, s.ClearPendingCommands(ctx, "no-such-instance", version),
			"dialect %s: an unknown instance must be a silent no-op", b.name)
	})
}
