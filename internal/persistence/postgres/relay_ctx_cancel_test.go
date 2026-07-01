package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestRelayRunReturnsDeadlineExceededOnAlreadyExpiredCtx deterministically
// exercises the first guard site in Run (the guard after drainUntilEmpty →
// DrainOnce → pool.Begin returns an error). It calls Run with an ALREADY-EXPIRED
// context so that pool.Begin fails immediately and the guard site is hit before
// the select loop. This is a strict gate: a guard that only checks
// errors.Is(err, context.Canceled) misses context.DeadlineExceeded and returns a
// workflow-postgres:-prefixed error instead of the clean ctx.Err() sentinel.
// After the fix (ctx.Err() != nil) Run returns context.DeadlineExceeded exactly.
func TestRelayRunReturnsDeadlineExceededOnAlreadyExpiredCtx(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)

	relay := pg.NewRelay(pool, &recordingPub{})

	// Construct a context whose deadline is already in the past — pool.Begin will
	// fail immediately with context.DeadlineExceeded, hitting the first guard site.
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	err := relay.Run(ctx)
	require.Equal(t, context.DeadlineExceeded, err,
		"Run must return ctx.Err() (context.DeadlineExceeded) exactly when the context is already expired; got: %v", err)
}

// TestRelayRunReturnsDeadlineExceededOnTimeout verifies that Run returns
// context.DeadlineExceeded (not a raw wrapped driver error) when the context
// deadline fires during the poll loop. This guards Fix I1: the original code only
// checked errors.Is(err, context.Canceled) at the three guard sites; a
// deadline-expired context fell through to "return err", returning a
// workflow-postgres: prefixed error instead of the clean context sentinel. After
// the fix (ctx.Err() != nil) Run returns ctx.Err() on context termination —
// specifically context.DeadlineExceeded here.
func TestRelayRunReturnsDeadlineExceededOnTimeout(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	// Migrate so that drainUntilEmpty reaches the Postgres driver and receives the
	// driver-level deadline error when the context fires — without this, the claim
	// query returns "relation does not exist" (an infra error, not a ctx error).
	require.NoError(t, pg.Migrate(t.Context(), pool))

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()

	err := relay.Run(ctx)
	// Must be exactly context.DeadlineExceeded, not a workflow-prefixed wrapper.
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"Run must return context.DeadlineExceeded when the context deadline fires, got: %v", err)
	// Additionally: the error must not carry the workflow-postgres prefix —
	// it should be ctx.Err() not a wrapped driver/relay error.
	require.Equal(t, context.DeadlineExceeded, err,
		"Run must return ctx.Err() exactly, not a wrapped error, on deadline expiry")
}
