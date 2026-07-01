package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestAdvisoryLockOwnershipClosedPoolError verifies that NewAdvisoryLockOwnership
// propagates the error when the pool cannot acquire a session connection.
func TestAdvisoryLockOwnershipClosedPoolError(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	pool.Close() // closed pool → Acquire fails

	_, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.Error(t, err, "NewAdvisoryLockOwnership must return an error on a closed pool")
	require.Contains(t, err.Error(), "acquire session conn",
		"error must reference the session connection acquisition failure")
}

// TestAdvisoryLockReleaseNotHeldIsNoop verifies that Release on an instance ID
// that was never acquired is a no-op (returns nil). This covers the
// !o.held[instanceID] early-return branch in Release.
func TestAdvisoryLockReleaseNotHeldIsNoop(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	owner, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer owner.Close() //nolint:errcheck

	// Release an instance that was never acquired — must be a no-op.
	err = owner.Release(t.Context(), "never-held-instance")
	assert.NoError(t, err, "Release on a never-held instance must return nil")
}

// TestAdvisoryLockCloseUnlocksHeld verifies that Close releases every held
// advisory lock, so another ownership can immediately acquire the same instance.
func TestAdvisoryLockCloseUnlocksHeld(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	// Process A acquires the lock.
	procA, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)

	ok, err := procA.Acquire(t.Context(), "close-test-1")
	require.NoError(t, err)
	require.True(t, ok, "procA must acquire the free instance")

	// Close procA — this must release all held locks.
	require.NoError(t, procA.Close())

	// Process B can now acquire the same instance (A's lock was released by Close).
	procB, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procB.Close() //nolint:errcheck

	ok, err = procB.Acquire(t.Context(), "close-test-1")
	require.NoError(t, err)
	assert.True(t, ok, "procB must acquire the instance after procA.Close released it")
}
