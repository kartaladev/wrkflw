package postgres_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestAdvisoryLockOwnershipCloseGuard verifies that after Close is called:
//   - Acquire returns (false, ErrOwnershipClosed).
//   - Release returns ErrOwnershipClosed.
//   - A second Close call is idempotent (returns nil).
func TestAdvisoryLockOwnershipCloseGuard(t *testing.T) {
	pool := database.RunTestDatabase(t)

	o, err := pg.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)

	// Close the ownership once.
	require.NoError(t, o.Close())

	// Acquire after Close must return false + ErrOwnershipClosed.
	owned, acquireErr := o.Acquire(t.Context(), "post-close-id")
	assert.False(t, owned, "Acquire after Close must return owned=false")
	require.Error(t, acquireErr)
	assert.True(t, errors.Is(acquireErr, pg.ErrOwnershipClosed),
		"Acquire error must be (or wrap) ErrOwnershipClosed; got: %v", acquireErr)

	// Release after Close must return ErrOwnershipClosed.
	releaseErr := o.Release(t.Context(), "post-close-id")
	require.Error(t, releaseErr)
	assert.True(t, errors.Is(releaseErr, pg.ErrOwnershipClosed),
		"Release error must be (or wrap) ErrOwnershipClosed; got: %v", releaseErr)

	// Second Close must be idempotent.
	assert.NoError(t, o.Close(), "second Close must return nil (idempotent)")
}
