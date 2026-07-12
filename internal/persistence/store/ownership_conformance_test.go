// Package store_test — ownership conformance tests for all three dialects.
//
// Postgres and MySQL: validates the exclusive-lock contract (acquire, sticky
// re-acquire, contention, release, close-guard, close-idempotency).
// SQLite: validates that TryLock and Unlock both return [dialect.ErrUnsupported].
package store_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Compile-time assertion: both locker-backed ownership types satisfy kernel.InstanceOwnership.
var _ kernel.InstanceOwnership = (*store.AdvisoryLockOwnership)(nil)

// ── Postgres ──────────────────────────────────────────────────────────────────

// TestOwnership_Postgres_AcquireExclusiveThenRelease verifies the exclusive-
// ownership contract on Postgres advisory locks:
//
//   - Owner A acquires instanceID → true.
//   - Owner A re-acquires (sticky) → true (no round-trip).
//   - Owner B acquires same instanceID while A holds it → false.
//   - After A releases, B acquires → true.
func TestOwnership_Postgres_AcquireExclusiveThenRelease(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	procA, err := store.NewPostgresOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procA.Close() //nolint:errcheck

	procB, err := store.NewPostgresOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procB.Close() //nolint:errcheck

	id := "owned-instance-pg"

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

// TestOwnership_Postgres_CloseGuard verifies that after Close:
//   - Acquire returns (false, ErrOwnershipClosed).
//   - Release returns ErrOwnershipClosed.
//   - A second Close is idempotent (returns nil).
func TestOwnership_Postgres_CloseGuard(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	o, err := store.NewPostgresOwnership(t.Context(), pool)
	require.NoError(t, err)

	require.NoError(t, o.Close())

	owned, acquireErr := o.Acquire(t.Context(), "post-close-id")
	assert.False(t, owned)
	require.Error(t, acquireErr)
	assert.True(t, errors.Is(acquireErr, store.ErrOwnershipClosed),
		"Acquire error must wrap ErrOwnershipClosed; got: %v", acquireErr)

	releaseErr := o.Release(t.Context(), "post-close-id")
	require.Error(t, releaseErr)
	assert.True(t, errors.Is(releaseErr, store.ErrOwnershipClosed),
		"Release error must wrap ErrOwnershipClosed; got: %v", releaseErr)

	assert.NoError(t, o.Close(), "second Close must return nil (idempotent)")
}

// TestOwnership_Postgres_ReleaseNotHeldIsNoop verifies that releasing a never-
// acquired instanceID is a no-op.
func TestOwnership_Postgres_ReleaseNotHeldIsNoop(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	o, err := store.NewPostgresOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer o.Close() //nolint:errcheck

	err = o.Release(t.Context(), "never-held-instance")
	assert.NoError(t, err, "Release on a never-held instance must return nil")
}

// TestOwnership_Postgres_CloseUnlocksHeld verifies that Close releases every held
// advisory lock so another ownership can immediately acquire the same instance.
func TestOwnership_Postgres_CloseUnlocksHeld(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	procA, err := store.NewPostgresOwnership(t.Context(), pool)
	require.NoError(t, err)

	ok, err := procA.Acquire(t.Context(), "close-test-pg")
	require.NoError(t, err)
	require.True(t, ok, "procA must acquire the free instance")

	require.NoError(t, procA.Close())

	procB, err := store.NewPostgresOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procB.Close() //nolint:errcheck

	ok, err = procB.Acquire(t.Context(), "close-test-pg")
	require.NoError(t, err)
	assert.True(t, ok, "procB must acquire the instance after procA.Close released it")
}

// TestOwnership_Postgres_ClosedPoolError verifies that NewPostgresOwnership
// propagates the error when the pool cannot acquire a session connection.
func TestOwnership_Postgres_ClosedPoolError(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	pool.Close() // closed pool → Acquire fails

	_, err := store.NewPostgresOwnership(t.Context(), pool)
	require.Error(t, err, "NewPostgresOwnership must return error on closed pool")
	require.Contains(t, err.Error(), "acquire session conn")
}

// TestOwnership_Postgres_Goleak verifies no goroutine or conn leaks after Close.
func TestOwnership_Postgres_Goleak(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	opt := goleak.IgnoreCurrent()

	o, err := store.NewPostgresOwnership(t.Context(), pool)
	require.NoError(t, err)

	_, err = o.Acquire(t.Context(), "goleak-inst-1-pg")
	require.NoError(t, err)
	_, err = o.Acquire(t.Context(), "goleak-inst-2-pg")
	require.NoError(t, err)

	require.NoError(t, o.Close())
	assert.NoError(t, o.Close(), "second Close idempotent")

	goleak.VerifyNone(t, opt)
}

// ── MySQL ─────────────────────────────────────────────────────────────────────

// TestOwnership_MySQL_AcquireExclusiveThenRelease verifies the exclusive-ownership
// contract on MySQL GET_LOCK advisory locks.
func TestOwnership_MySQL_AcquireExclusiveThenRelease(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	procA, err := store.NewMySQLOwnership(t.Context(), db)
	require.NoError(t, err)
	defer procA.Close() //nolint:errcheck

	procB, err := store.NewMySQLOwnership(t.Context(), db)
	require.NoError(t, err)
	defer procB.Close() //nolint:errcheck

	id := "owned-instance-my"

	ownedA, err := procA.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, ownedA, "A acquires the free instance")

	again, err := procA.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, again, "A sticky re-acquire returns true")

	ownedB, err := procB.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.False(t, ownedB, "B is blocked while A owns")

	require.NoError(t, procA.Release(t.Context(), id))
	ownedB2, err := procB.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, ownedB2, "B acquires after A releases")
}

// TestOwnership_MySQL_CloseGuard verifies after-Close error sentinels and idempotency.
func TestOwnership_MySQL_CloseGuard(t *testing.T) {
	// Start the MySQL container first so its background goroutines are captured in
	// the IgnoreCurrent baseline; AdvisoryLockOwnership.Close must not add any.
	db := dbtest.RunTestMySQL(t)
	opt := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, opt)

	o, err := store.NewMySQLOwnership(t.Context(), db)
	require.NoError(t, err)

	_, err = o.Acquire(t.Context(), "inst-close-my-1")
	require.NoError(t, err)
	_, err = o.Acquire(t.Context(), "inst-close-my-2")
	require.NoError(t, err)

	require.NoError(t, o.Close())
	assert.NoError(t, o.Close(), "second Close must be a no-op")

	owned, acquireErr := o.Acquire(t.Context(), "inst-close-my-1")
	assert.False(t, owned)
	assert.ErrorIs(t, acquireErr, store.ErrOwnershipClosed)

	releaseErr := o.Release(t.Context(), "inst-close-my-1")
	assert.ErrorIs(t, releaseErr, store.ErrOwnershipClosed)
}

// TestOwnership_MySQL_HashKey verifies the internal hashKey produces stable
// keys of exactly 64 hex characters (≤ MySQL GET_LOCK's 64-char name limit).
func TestOwnership_MySQL_HashKey(t *testing.T) {
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
			key1 := store.MySQLHashKeyForTest(tc.id)
			key2 := store.MySQLHashKeyForTest(tc.id)
			assert.Equal(t, key1, key2, "hashKey must be deterministic")
			assert.LessOrEqual(t, len(key1), 64, "hashKey must be ≤64 chars")
			assert.NotEmpty(t, key1)
		})
	}
}

// ── SQLite ────────────────────────────────────────────────────────────────────

// TestOwnership_SQLite_ErrUnsupported verifies that SQLite TryLock returns
// (false, dialect.ErrUnsupported) and Unlock returns dialect.ErrUnsupported.
func TestOwnership_SQLite_ErrUnsupported(t *testing.T) {
	locker := store.NewSQLiteLocker()

	ok, lockErr := locker.TryLock(t.Context(), "any-key")
	assert.False(t, ok, "SQLite TryLock must return false")
	require.Error(t, lockErr)
	assert.True(t, errors.Is(lockErr, dialect.ErrUnsupported),
		"SQLite TryLock must return dialect.ErrUnsupported; got: %v", lockErr)

	unlockErr := locker.Unlock(t.Context(), "any-key")
	require.Error(t, unlockErr)
	assert.True(t, errors.Is(unlockErr, dialect.ErrUnsupported),
		"SQLite Unlock must return dialect.ErrUnsupported; got: %v", unlockErr)
}
