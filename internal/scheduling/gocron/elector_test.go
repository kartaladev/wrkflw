package gocron_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
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
	pool := database.RunTestDatabase(t)
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

// TestPostgresElectorCloseIdempotent proves Close is idempotent (mirrors the
// AdvisoryLockOwnership contract): a second Close returns nil without acting.
func TestPostgresElectorCloseIdempotent(t *testing.T) {
	pool := database.RunTestDatabase(t)
	ctx := t.Context()

	elector, err := sched.NewPostgresElector(ctx, pool)
	require.NoError(t, err)
	require.NoError(t, elector.Close())
	require.NoError(t, elector.Close(), "second Close must be a no-op")
}

// TestPostgresElectorKeyOverride proves WithElectorKey scopes leadership: two
// electors contending under DIFFERENT keys can both be leaders, letting multiple
// independent engines coexist in one database.
func TestPostgresElectorKeyOverride(t *testing.T) {
	pool := database.RunTestDatabase(t)
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
