package pgelector_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron/pgelector"
)

// TestPostgresElectorLeadership exercises leader election as a stateful protocol
// (elect → contend → fail over), so it is one cohesive test rather than a table:
// the steps build on each other and do not share a single-call-varying-input shape.
//
// It proves the single-leader guarantee gocron's Elector relies on: while one
// instance holds the leader advisory lock its IsLeader returns nil (it runs jobs);
// a SECOND instance contending for the SAME leader key is refused (ErrNotLeader),
// so it skips jobs; and after the leader Closes, the follower wins leadership on
// its next attempt (natural failover, no lease loop).
func TestPostgresElectorLeadership(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	electorA, err := sched.NewPostgresElector(ctx, pool)
	require.NoError(t, err)

	// A becomes leader on first ask.
	require.NoError(t, electorA.IsLeader(ctx), "first instance must be elected leader")
	// Sticky: a second ask is cheap and still leader (no round-trip needed).
	require.NoError(t, electorA.IsLeader(ctx), "leader must stay leader on repeat ask")

	// A second elector (its own dedicated connection from the same pool) contends
	// for the same leader key and must lose while A holds it.
	electorB, err := sched.NewPostgresElector(ctx, pool)
	require.NoError(t, err)
	require.ErrorIs(t, electorB.IsLeader(ctx), sched.ErrNotLeader,
		"a follower must not be leader while another instance holds leadership")

	// The leader dies: closing releases the leader lock.
	require.NoError(t, electorA.Close())

	// The follower now wins leadership on its next attempt — natural failover.
	require.NoError(t, electorB.IsLeader(ctx), "a follower must take leadership after the leader closes")
	require.NoError(t, electorB.Close())
}

// TestPostgresElectorInvokesOnLeadershipAcquired proves the Option-A failover
// hook (ADR-0072): when an instance wins leadership, the registered
// on-leadership-acquired callback fires. Wiring it to ProcessDriver.RehydrateTimers
// re-arms the full persisted timer set on a new leader after failover, closing
// the window where runtime-armed timers would otherwise be lost until restart.
// The callback runs asynchronously so it never blocks gocron's IsLeader hot path.
func TestPostgresElectorInvokesOnLeadershipAcquired(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	acquired := make(chan struct{}, 1)
	elector, err := sched.NewPostgresElector(ctx, pool,
		sched.WithOnLeadershipAcquired(func(context.Context) {
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

// TestPostgresElectorCloseIdempotent proves Close is idempotent (mirrors the
// AdvisoryLockOwnership contract): a second Close returns nil without acting.
func TestPostgresElectorCloseIdempotent(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	elector, err := sched.NewPostgresElector(ctx, pool)
	require.NoError(t, err)
	require.NoError(t, elector.Close())
	require.NoError(t, elector.Close(), "second Close must be a no-op")
}

// TestPostgresElectorCloseReleasesReentrantLockStack proves ADR-0061's Close fully
// releases leadership even when the session-level advisory lock was acquired more
// than once on the SAME dedicated connection. A transient heartbeat ping failure
// can falsely step the elector down (clearing isLeader while the lock is still
// held); the next IsLeader then re-runs pg_try_advisory_lock on the same conn,
// stacking the re-entrant counter. A single pg_advisory_unlock would drop the
// counter by one and leave the lock held; combined with conn.Release() returning
// the conn to the pool WITHOUT resetting session locks, the lock would linger on a
// pooled backend. Close must use pg_advisory_unlock_all so a fresh session acquires
// the key immediately after Close.
func TestPostgresElectorCloseReleasesReentrantLockStack(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	electorA, err := sched.NewPostgresElector(ctx, pool, sched.WithElectorKey("reentrant-key"))
	require.NoError(t, err)

	// A becomes leader (counter == 1).
	require.NoError(t, electorA.IsLeader(ctx), "first instance must be elected leader")
	// Simulate a false step-down + re-acquire: re-lock on A's OWN conn, stacking the
	// re-entrant counter to 2. A single pg_advisory_unlock would not fully release it.
	require.NoError(t, electorA.ReacquireLockForTest(ctx), "re-acquire must stack the lock")

	// Close must fully release every advisory lock the session holds.
	require.NoError(t, electorA.Close())

	// Assert directly from pg_locks (a cluster-global view, independent of which
	// pooled backend asks) that NO advisory lock for the key lingers. A side session
	// is used so re-entrancy on a reused backend cannot mask a still-held lock the way
	// pg_try_advisory_lock from the same backend would.
	side, err := pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(side.Release)

	var held int
	require.NoError(t, side.QueryRow(ctx,
		`SELECT count(*) FROM pg_locks
		  WHERE locktype = 'advisory'
		    AND ((classid::bigint << 32) | (objid::bigint & 4294967295)) = hashtextextended($1, 0)`,
		"reentrant-key",
	).Scan(&held))
	require.Zero(t, held,
		"Close must release the entire re-entrant advisory-lock stack; none may linger on the pooled backend")

	// And a fresh session must win the SAME key immediately.
	electorB, err := sched.NewPostgresElector(ctx, pool, sched.WithElectorKey("reentrant-key"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = electorB.Close() })
	require.NoError(t, electorB.IsLeader(ctx),
		"a fresh session must win the key after Close fully released the re-entrant lock stack")
}

// TestPostgresElectorKeyOverride proves WithElectorKey scopes leadership: two
// electors contending under DIFFERENT keys can both be leaders, letting multiple
// independent engines coexist in one database.
func TestPostgresElectorKeyOverride(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	electorA, err := sched.NewPostgresElector(ctx, pool, sched.WithElectorKey("engine-a"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = electorA.Close() })

	electorB, err := sched.NewPostgresElector(ctx, pool, sched.WithElectorKey("engine-b"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = electorB.Close() })

	require.NoError(t, electorA.IsLeader(ctx), "engine-a leader under its own key")
	require.NoError(t, electorB.IsLeader(ctx), "engine-b leader under its own independent key")
}
