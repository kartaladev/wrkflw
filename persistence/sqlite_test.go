package persistence_test

// sqlite_test.go tests the SQLite facade constructor OpenSQLite. It uses
// dbtest.RunTestSQLite, the in-process SQLite analogue of RunTestDatabase.
// No Docker daemon is required.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestOpenSQLite verifies that OpenSQLite returns a working Store over an
// in-process SQLite database: the probe passes, and a Create+Load round-trip
// via runtime.ProcessDriver succeeds (UTC timestamps, correct snapshot).
func TestOpenSQLite(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	s, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err, "OpenSQLite must succeed on a migrated SQLite db")
	require.NotNil(t, s)

	// Drive a minimal start→end process through the Runner so Create+Commit+Load
	// are all exercised in a realistic path (mirrors TestOpenPostgresEndToEnd).
	def := &model.ProcessDefinition{
		ID:      "sqlite-e2e",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}

	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), s)
	require.NoError(t, err)
	st, err := r.Run(t.Context(), def, "i-sqlite-e2e", map[string]any{"backend": "sqlite"})
	require.NoError(t, err, "Runner.Run must succeed on SQLite store")
	require.Equal(t, engine.StatusCompleted, st.Status)

	// Load confirms the snapshot round-trips through SQLite's TEXT codec.
	reloaded, _, err := s.Load(t.Context(), "i-sqlite-e2e")
	require.NoError(t, err, "Load must find the completed instance")
	assert.Equal(t, engine.StatusCompleted, reloaded.Status)
	assert.Equal(t, "i-sqlite-e2e", reloaded.InstanceID)
	assert.Equal(t, "sqlite", reloaded.Variables["backend"])
}

// TestOpenSQLiteErrUnsupported verifies the end-to-end fail-loud contract for
// SQLite advisory locking: NewSQLiteAdvisoryLockOwnership returns a valid
// kernel.Ownership whose Acquire method returns dialect.ErrUnsupported. This
// proves the full wiring from facade → store.AdvisoryLockOwnership →
// store.sqliteLocker → dialect.ErrUnsupported, not just that the sentinel
// equals itself.
//
// Release is a no-op (returns nil) when the lock was never acquired — that is
// the correct AdvisoryLockOwnership contract: it guards via the held-map so
// it never calls Unlock on a lock it doesn't hold. The unsupported-locking
// failure surfaces on Acquire, which is the guarding point for ownership flows.
func TestOpenSQLiteErrUnsupported(t *testing.T) {
	ctx := t.Context()

	owner, closer, err := persistence.NewSQLiteAdvisoryLockOwnership()
	require.NoError(t, err, "NewSQLiteAdvisoryLockOwnership must not fail (no connection needed)")
	require.NotNil(t, owner)
	require.NotNil(t, closer)
	t.Cleanup(func() { _ = closer.Close() })

	// Acquire must return (false, dialect.ErrUnsupported): proves that the full
	// wiring from facade → store.AdvisoryLockOwnership → sqliteLocker is live.
	ok, acquireErr := owner.Acquire(ctx, "some-instance-id")
	assert.False(t, ok, "Acquire on SQLite must not report ownership")
	require.Error(t, acquireErr)
	assert.ErrorIs(t, acquireErr, dialect.ErrUnsupported,
		"Acquire on SQLite must wrap dialect.ErrUnsupported through the full facade chain")

	// Release returns nil for an un-held instance (held-map guard fires first).
	// This is correct: there is nothing to unlock when Acquire never succeeded.
	releaseErr := owner.Release(ctx, "some-instance-id")
	assert.NoError(t, releaseErr,
		"Release on an un-held SQLite instance must be a no-op (nil), not an error")
}

// TestOpenSQLiteNotFoundSentinel verifies that ErrInstanceNotFound is returned
// via errors.Is when a non-existent instance is loaded from a SQLite Store —
// same contract as the Postgres and MySQL sentinels.
func TestOpenSQLiteNotFoundSentinel(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	s, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err)

	_, _, err = s.Load(t.Context(), "no-such-instance")
	require.Error(t, err)
	assert.ErrorIs(t, err, persistence.ErrInstanceNotFound,
		"ErrInstanceNotFound must be returned for a missing SQLite instance")
}
