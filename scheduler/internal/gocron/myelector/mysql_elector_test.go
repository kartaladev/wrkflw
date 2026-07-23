package myelector_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron/myelector"
)

// TestMySQLElectorLeadership exercises leader election as a stateful protocol
// (elect → contend → fail over) — mirrors TestPostgresElectorLeadership.
//
// It proves the single-leader guarantee gocron's Elector relies on: while one
// instance holds the leader advisory lock its IsLeader returns nil; a SECOND
// instance contending for the SAME leader key is refused (ErrNotLeader); and
// after the leader Closes, the follower wins leadership on its next attempt
// (natural failover, no lease loop). MySQL uses GET_LOCK/RELEASE_ALL_LOCKS.
func TestMySQLElectorLeadership(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	electorA, err := sched.NewMySQLElector(ctx, db)
	require.NoError(t, err)

	// A becomes leader on first ask.
	require.NoError(t, electorA.IsLeader(ctx), "first instance must be elected leader")
	// Sticky: a second ask is cheap and still leader (no round-trip needed).
	require.NoError(t, electorA.IsLeader(ctx), "leader must stay leader on repeat ask")

	// A second elector (its own dedicated connection from the same db) contends
	// for the same leader key and must lose while A holds it.
	electorB, err := sched.NewMySQLElector(ctx, db)
	require.NoError(t, err)
	require.ErrorIs(t, electorB.IsLeader(ctx), sched.ErrNotLeader,
		"a follower must not be leader while another instance holds leadership")

	// The leader dies: closing releases the leader lock.
	require.NoError(t, electorA.Close())

	// The follower now wins leadership on its next attempt — natural failover.
	require.NoError(t, electorB.IsLeader(ctx), "a follower must take leadership after the leader closes")
	require.NoError(t, electorB.Close())
}

// TestMySQLElectorInvokesOnLeadershipAcquired proves the Option-A failover hook
// (ADR-0072): when an instance wins leadership, the registered
// on-leadership-acquired callback fires asynchronously.
func TestMySQLElectorInvokesOnLeadershipAcquired(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	acquired := make(chan struct{}, 1)
	elector, err := sched.NewMySQLElector(ctx, db,
		sched.WithMySQLOnLeadershipAcquired(func(context.Context) {
			acquired <- struct{}{}
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = elector.Close() })

	require.NoError(t, elector.IsLeader(ctx), "first instance must be elected leader")

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("on-leadership-acquired callback was not invoked after winning leadership")
	}
}

// TestMySQLElectorCloseIdempotent proves Close is idempotent — mirrors the
// AdvisoryLockOwnership contract: a second Close returns nil without acting.
func TestMySQLElectorCloseIdempotent(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	elector, err := sched.NewMySQLElector(ctx, db)
	require.NoError(t, err)
	require.NoError(t, elector.Close())
	require.NoError(t, elector.Close(), "second Close must be a no-op")
}

// TestMySQLElectorCloseIdempotentAfterLeadership proves Close is idempotent
// even AFTER leadership has been won and the heartbeat goroutine started.
// The key scenario: (1) IsLeader succeeds → elector is leader, heartbeat
// goroutine running; (2) first Close releases the lock and stops the heartbeat;
// (3) second Close must still return nil without a panic or leak.
// This exercises the isLeader-independent RELEASE_ALL_LOCKS path: a heartbeat
// step-down can clear isLeader while the session lock is still held, so we need
// Close to release locks regardless of the isLeader flag.
func TestMySQLElectorCloseIdempotentAfterLeadership(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	elector, err := sched.NewMySQLElector(ctx, db)
	require.NoError(t, err)

	// Win leadership — this starts the heartbeat goroutine.
	require.NoError(t, elector.IsLeader(ctx), "must win leadership before close")

	// First Close: must stop heartbeat, release lock, return nil.
	require.NoError(t, elector.Close(), "first Close after leadership must return nil")
	// Second Close: must be a no-op.
	require.NoError(t, elector.Close(), "second Close after leadership must be a no-op (idempotent)")
}

// TestMySQLElectorKeyOverride proves WithMySQLElectorKey scopes leadership:
// two electors contending under DIFFERENT keys can both be leaders, letting
// multiple independent engines coexist in one database.
func TestMySQLElectorKeyOverride(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	electorA, err := sched.NewMySQLElector(ctx, db, sched.WithMySQLElectorKey("mysql-engine-a"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = electorA.Close() })

	electorB, err := sched.NewMySQLElector(ctx, db, sched.WithMySQLElectorKey("mysql-engine-b"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = electorB.Close() })

	require.NoError(t, electorA.IsLeader(ctx), "engine-a leader under its own key")
	require.NoError(t, electorB.IsLeader(ctx), "engine-b leader under its own independent key")
}

// TestMySQLElectorAfterCloseIsNotLeader proves that after Close, IsLeader
// returns ErrNotLeader and does not attempt further DB access.
func TestMySQLElectorAfterCloseIsNotLeader(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	elector, err := sched.NewMySQLElector(ctx, db)
	require.NoError(t, err)

	require.NoError(t, elector.IsLeader(ctx), "must win leadership before close")
	require.NoError(t, elector.Close())
	require.ErrorIs(t, elector.IsLeader(ctx), sched.ErrNotLeader,
		"closed elector must not report leadership")
}
