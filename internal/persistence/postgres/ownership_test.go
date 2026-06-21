package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestAdvisoryLockOwnershipContention simulates two processes (each with its
// own dedicated session connection) competing for the same instance id.
//
// Sequence:
//   - A acquires the free id  → true
//   - A re-acquires (sticky)  → true (no DB round-trip)
//   - B acquires same id      → false (A holds it)
//   - A releases              → B acquires → true
func TestAdvisoryLockOwnershipContention(t *testing.T) {
	pool := database.RunTestDatabase(t)

	// Two independent "processes", each with its own dedicated session connection.
	procA, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procA.Close() //nolint:errcheck

	procB, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procB.Close() //nolint:errcheck

	id := "owned-instance"

	ownedA, err := procA.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, ownedA, "A acquires the free instance")

	// Sticky: A re-acquiring is true with no contention.
	again, err := procA.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, again, "A sticky re-acquire returns true")

	// B cannot acquire while A holds the lock.
	ownedB, err := procB.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.False(t, ownedB, "B is blocked while A owns")

	// After A releases, B can acquire.
	require.NoError(t, procA.Release(t.Context(), id))
	ownedB2, err := procB.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, ownedB2, "B acquires after A releases")
}
