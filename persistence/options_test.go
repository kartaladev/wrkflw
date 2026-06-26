package persistence_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// minimalDef returns the simplest process definition: start → end.
// Used in option tests that need a real runner round-trip.
func minimalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "opt-test",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
}

// TestWithHistoryCapReturnsOption verifies WithHistoryCap returns a non-nil Option
// and that passing it to OpenPostgres succeeds end-to-end.
func TestWithHistoryCapReturnsOption(t *testing.T) {
	t.Parallel()

	opt := persistence.WithHistoryCap(50)
	assert.NotNil(t, opt, "WithHistoryCap must return a non-nil Option")

	pool := database.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	store, err := persistence.OpenPostgres(t.Context(), pool, opt)
	require.NoError(t, err)
	require.NotNil(t, store)

	// Drive a minimal process through the store to confirm the option is wired.
	r := runtime.NewRunner(nil, store)
	st, err := r.Run(t.Context(), minimalDef(), "hist-cap-1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}

// TestWithOutboxNotifyReturnsOption verifies WithOutboxNotify returns a non-nil
// Option, creates a store with it, and drives a minimal process to confirm an
// outbox row is created.
func TestWithOutboxNotifyReturnsOption(t *testing.T) {
	t.Parallel()

	opt := persistence.WithOutboxNotify()
	assert.NotNil(t, opt, "WithOutboxNotify must return a non-nil Option")

	pool := database.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	store, err := persistence.OpenPostgres(t.Context(), pool, opt)
	require.NoError(t, err)
	require.NotNil(t, store)

	r := runtime.NewRunner(nil, store)
	st, err := r.Run(t.Context(), minimalDef(), "notify-opt-1", map[string]any{"x": 1})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)

	// Confirm the outbox row exists.
	var n int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM wrkflw_outbox WHERE instance_id = $1`, "notify-opt-1",
	).Scan(&n))
	assert.Greater(t, n, 0, "at least one outbox row must exist after running with WithOutboxNotify")
}

// TestWithListenNotifyReturnsOption verifies WithListenNotify returns a non-nil
// RelayOption and that a Relay constructed with it is non-nil.
func TestWithListenNotifyReturnsOption(t *testing.T) {
	t.Parallel()

	opt := persistence.WithListenNotify()
	assert.NotNil(t, opt, "WithListenNotify must return a non-nil RelayOption")

	pool := database.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	pub := &capturingPublisher{}
	relay := persistence.NewRelay(pool, pub,
		opt,
		persistence.WithPollInterval(50*time.Millisecond),
	)
	require.NotNil(t, relay, "NewRelay with WithListenNotify must return a non-nil Relay")
}

// TestNewAdvisoryLockOwnershipFacade verifies the full advisory-lock lifecycle
// through the persistence façade: construct, acquire, release, close.
func TestNewAdvisoryLockOwnershipFacade(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	owner, closer, err := persistence.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)
	require.NotNil(t, owner)
	require.NotNil(t, closer)

	// Acquire a lock.
	ok, err := owner.Acquire(t.Context(), "test-instance-1")
	require.NoError(t, err)
	assert.True(t, ok, "Acquire on a free instance must return true")

	// Release the lock.
	err = owner.Release(t.Context(), "test-instance-1")
	require.NoError(t, err, "Release must not error")

	// Close the ownership connection.
	err = closer.Close()
	require.NoError(t, err, "Close must not error")
}

// TestNewAdvisoryLockOwnershipFacadeClosedPool verifies that
// NewAdvisoryLockOwnership propagates the error and returns (nil, nil, err)
// when the pool cannot provide a session connection (closed pool).
func TestNewAdvisoryLockOwnershipFacadeClosedPool(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	// Close the pool so Acquire of a session connection fails.
	pool.Close()

	owner, closer, err := persistence.NewAdvisoryLockOwnership(t.Context(), pool)
	require.Error(t, err, "must return an error when pool is closed")
	assert.Nil(t, owner)
	assert.Nil(t, closer)
}
