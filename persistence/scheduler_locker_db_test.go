package persistence_test

// scheduler_locker_db_test.go — backlog 34.
//
// persistence.NewPostgresSchedulerLocker / NewMySQLSchedulerLocker and the
// poolSchedulerLocker.Lock / poolSchedulerLock.Unlock they return were at 0.0 %
// coverage: the multi-replica timer-exclusion primitive was entirely untested
// against a real database. The existing scheduler_locker_test.go covers only the
// single-session bridge through an in-memory fake, which cannot observe whether
// the advisory lock is really session-scoped and really per-key.
//
// Per Golang rule #8 the point is the hot path, not the percentage: these tests
// drive real contention between two sessions rather than padding the number with
// the eight MySQLWith… option setters that are also at 0.0 %.
//
// ⚠ Needs Docker: advisory locks are a Postgres/MySQL capability. SQLite omits
// the Locker capability interface entirely (ADR-0082), so there is no SQLite leg.

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/scheduler"
)

// assertSchedulerLockerContract drives the full acquire/contend/release cycle
// against a real advisory-lock backend. Both dialect legs run the identical
// assertions, so a divergence between Postgres and MySQL shows up as a failure
// rather than as an untested gap.
func assertSchedulerLockerContract(t *testing.T, locker scheduler.Locker) {
	t.Helper()
	ctx := t.Context()

	const keyA = "wrkflw-test-timer-a"
	const keyB = "wrkflw-test-timer-b"

	// 1. A free key is obtainable.
	held, err := locker.Lock(ctx, keyA)
	require.NoError(t, err, "a free advisory key must be obtainable")
	require.NotNil(t, held)

	// 2. Contention: a SECOND acquisition of the same key must be refused, and
	//    refused with the documented sentinel. This is the assertion the whole
	//    multi-replica exclusion guarantee rests on — if it passed, two replicas
	//    would fire the same timer.
	//
	//    It also proves the lock is genuinely session-scoped: Lock takes a fresh
	//    connection per call, so a lock leaking to the transaction or to the pool
	//    rather than the session would let this second call succeed.
	second, err := locker.Lock(ctx, keyA)
	require.ErrorIs(t, err, persistence.ErrSchedulerLockNotObtained,
		"a key held by another session must be refused with ErrSchedulerLockNotObtained")
	assert.Nil(t, second, "a refused Lock must not return a usable lock")

	// 3. Control: exclusion is PER KEY, not global. Without this a locker that
	//    refused everything while any lock was held would pass step 2, and every
	//    timer in the deployment would serialise onto one key.
	other, err := locker.Lock(ctx, keyB)
	require.NoError(t, err, "a DIFFERENT key must remain obtainable while keyA is held")
	require.NotNil(t, other)
	require.NoError(t, other.Unlock(ctx))

	// 4. Unlock releases: the same key becomes obtainable again. This is what
	//    makes step 2's refusal a lock rather than a permanent wedge.
	require.NoError(t, held.Unlock(ctx), "Unlock must release the advisory lock and its session")

	reacquired, err := locker.Lock(ctx, keyA)
	require.NoError(t, err, "after Unlock the key must be obtainable again")
	require.NotNil(t, reacquired)
	require.NoError(t, reacquired.Unlock(ctx))
}

func TestPostgresSchedulerLockerContract(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	assertSchedulerLockerContract(t, persistence.NewPostgresSchedulerLocker(pool))
}

func TestMySQLSchedulerLockerContract(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	assertSchedulerLockerContract(t, persistence.NewMySQLSchedulerLocker(db))
}

// TestMySQLSchedulerLockerAcquireFailure covers Lock's session-acquisition error
// branch: when no session can be obtained the caller must get an error rather
// than a nil lock it would later dereference.
//
// The handle is opened and closed by this test (via RunTestMySQLDSN) rather than
// borrowed from RunTestMySQL, so closing it cannot disturb the shared helper's
// teardown.
func TestMySQLSchedulerLockerAcquireFailure(t *testing.T) {
	t.Parallel()

	dsn := dbtest.RunTestMySQLDSN(t)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	require.NoError(t, db.PingContext(t.Context()), "control: the handle must work before it is closed")
	require.NoError(t, db.Close())

	locker := persistence.NewMySQLSchedulerLocker(db)

	held, err := locker.Lock(t.Context(), "wrkflw-test-timer-closed")
	require.Error(t, err, "a locker that cannot obtain a session must report it")
	assert.Nil(t, held, "a failed Lock must not return a usable lock")
	assert.NotErrorIs(t, err, persistence.ErrSchedulerLockNotObtained,
		"an infrastructure failure must not be reported as ordinary lock contention — "+
			"the scheduler would treat it as 'another replica has it' and stay silent")
}
