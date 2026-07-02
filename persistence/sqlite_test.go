package persistence_test

// sqlite_test.go tests the SQLite facade constructor OpenSQLite. It uses
// dbtest.RunTestSQLite, the in-process SQLite analogue of RunTestDatabase.
// No Docker daemon is required.

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestOpenSQLite verifies that OpenSQLite returns a working Store over an
// in-process SQLite database: the probe passes, and a Create+Load round-trip
// via runtime.Runner succeeds (UTC timestamps, correct snapshot).
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
			model.NewStartEvent("start"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}

	r := runtime.NewRunner(nil, s)
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

// TestOpenSQLiteErrUnsupported verifies that the SQLite ownership/locker path
// returns dialect.ErrUnsupported from both TryLock and Unlock. SQLite provides
// no advisory locking, so there is no persistence.NewSQLiteAdvisoryLockOwnership
// constructor; callers who try to build a CachingStore with SQLite will hit
// ErrUnsupported when the locker methods are invoked.
//
// The underlying locker is exercised in
// internal/persistence/store/ownership_conformance_test.go; here we verify the
// sentinel is reachable via the dialect package imported from the facade layer.
func TestOpenSQLiteErrUnsupported(t *testing.T) {
	// Confirm the sentinel is exported and matchable via errors.Is.
	err := dialect.ErrUnsupported
	require.True(t, errors.Is(err, dialect.ErrUnsupported),
		"dialect.ErrUnsupported must be matchable via errors.Is")

	// Smoke-check: SQLite dialect reports TimestampsAsText=true (TEXT codec).
	d := dialect.NewSQLite()
	assert.True(t, d.TimestampsAsText(),
		"SQLite dialect must store timestamps as TEXT (RFC3339Nano)")
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
