package mysql_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"go.uber.org/goleak"
)

// TestOwnership_AcquireExclusiveThenRelease verifies the exclusive-ownership contract:
//
//   - Owner A acquires instanceID → true.
//   - Owner A re-acquires (sticky) → true (no round-trip).
//   - Owner B acquires same instanceID while A holds it → false.
//   - After A releases, B acquires → true.
func TestOwnership_AcquireExclusiveThenRelease(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	procA, err := mypkg.NewAdvisoryLockOwnership(t.Context(), db)
	require.NoError(t, err)
	defer procA.Close() //nolint:errcheck

	procB, err := mypkg.NewAdvisoryLockOwnership(t.Context(), db)
	require.NoError(t, err)
	defer procB.Close() //nolint:errcheck

	id := "owned-instance"

	// A acquires the free instance.
	ownedA, err := procA.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, ownedA, "A acquires the free instance")

	// Sticky: A re-acquiring returns true with no DB round-trip.
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

// TestOwnership_CloseIdempotentReleasesAll verifies Close idempotency and goroutine cleanup.
func TestOwnership_CloseIdempotentReleasesAll(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
		goleak.IgnoreTopFunction("github.com/go-sql-driver/mysql.(*mysqlConn).startWatcher.func1"),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionCleaner"),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	db := dbtest.RunTestMySQL(t)

	o, err := mypkg.NewAdvisoryLockOwnership(t.Context(), db)
	require.NoError(t, err)

	// Acquire a couple of instances.
	_, err = o.Acquire(t.Context(), "inst-1")
	require.NoError(t, err)
	_, err = o.Acquire(t.Context(), "inst-2")
	require.NoError(t, err)

	// First Close releases everything and must not leak.
	require.NoError(t, o.Close())

	// Second Close is idempotent.
	assert.NoError(t, o.Close(), "second Close must be a no-op")

	// Acquire/Release after Close must return ErrOwnershipClosed.
	owned, acquireErr := o.Acquire(t.Context(), "inst-1")
	assert.False(t, owned)
	assert.ErrorIs(t, acquireErr, mypkg.ErrOwnershipClosed)

	releaseErr := o.Release(t.Context(), "inst-1")
	assert.ErrorIs(t, releaseErr, mypkg.ErrOwnershipClosed)
}

// TestHashKey_StableAndWithin64Chars verifies the internal hashKey function produces
// stable ≤64-char keys (via the exported test hook).
func TestHashKey_StableAndWithin64Chars(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"short id", "abc"},
		{"uuid", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
		{"long id exceeding 64 chars", "this-is-a-very-long-instance-id-that-definitely-exceeds-sixty-four-characters-in-length"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key1 := mypkg.HashKey(tc.id)
			key2 := mypkg.HashKey(tc.id)
			assert.Equal(t, key1, key2, "hashKey must be deterministic")
			assert.LessOrEqual(t, len(key1), 64, "hashKey must be ≤64 chars")
			assert.NotEmpty(t, key1)
		})
	}
}
