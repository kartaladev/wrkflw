package gocron_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
)

// TestPostgresLockerExclusion exercises the advisory-lock primitive as a stateful
// protocol (acquire → contend → release → re-acquire), so it is one cohesive test
// rather than a table: the steps build on each other and do not share a
// single-call-varying-input shape.
//
// It proves the cross-replica guarantee the gocron Locker relies on: while one
// holder owns a key, a second acquisition of the SAME key is refused (so only one
// replica runs that timer's fire callback), while a DIFFERENT key is independent.
func TestPostgresLockerExclusion(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	locker := sched.NewPostgresLocker(pool)

	// Hold "timer-1".
	l1, err := locker.Lock(ctx, "timer-1")
	require.NoError(t, err)

	// A second acquisition of the same key (a different pooled session, i.e. a
	// different "replica") must be refused while l1 is held.
	_, err = locker.Lock(ctx, "timer-1")
	require.Error(t, err, "a held key must refuse a second lock")

	// A different key is independent.
	l2, err := locker.Lock(ctx, "timer-2")
	require.NoError(t, err)
	require.NoError(t, l2.Unlock(ctx))

	// Release "timer-1"; it becomes acquirable again.
	require.NoError(t, l1.Unlock(ctx))
	l3, err := locker.Lock(ctx, "timer-1")
	require.NoError(t, err, "a released key must be re-acquirable")
	require.NoError(t, l3.Unlock(ctx))
}
