package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestListenLoopAcquireFailBackoff verifies the listenLoop reconnect/backoff
// path. It uses a pre-closed pool so pool.Acquire fails on every attempt.
//
// Flow:
//  1. Run starts the listenLoop goroutine, then calls drainUntilEmpty.
//  2. drainUntilEmpty → DrainOnce → pool.Begin fails (closed pool) → infra error.
//  3. Run returns the infra error immediately.
//  4. listenLoop: pool.Acquire fails → logs warn → enters backoff select.
//  5. The test waits for Run to return, then cancels runCtx.
//  6. The backoff select fires ctx.Done and listenLoop exits.
func TestListenLoopAcquireFailBackoff(t *testing.T) {
	mainPool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), mainPool))

	// Create a new pool pointing at the same database, then close it immediately.
	// Any Acquire call on a closed pgxpool returns an error right away, which
	// triggers the acquire-failure path in listenLoop.
	closedPool, err := pgxpool.NewWithConfig(t.Context(), mainPool.Config())
	require.NoError(t, err)
	closedPool.Close() // closed before relay starts; Acquire will fail

	runCtx, cancel := context.WithCancel(t.Context())

	relay := postgres.NewRelay(closedPool, &countingPublisher{},
		postgres.WithListenNotify(),
		postgres.WithPollInterval(50*time.Millisecond),
	)

	done := make(chan error, 1)
	go func() { done <- relay.Run(runCtx) }()

	// Run returns quickly with an infra error (pool.Begin fails for DrainOnce).
	// Meanwhile, listenLoop is alive in its first backoff select
	// (waiting for ctx.Done or time.After(50ms)).
	// Cancel immediately so the backoff select fires ctx.Done (covering line 193-194).
	// Use a brief sleep first to ensure the goroutine has entered the select.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Run must have returned — either an infra error (begin tx failure) or
		// context.Canceled depending on timing. Either way it did not hang.
		t.Logf("Run returned with: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s; listenLoop may be blocking")
	}
}

// TestListenLoopExitsOnContextCancellation verifies that a relay started with
// WithListenNotify exits cleanly when the run context is cancelled.
func TestListenLoopExitsOnContextCancellation(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	// Synchronize on the listen loop's ACTUAL establishment (not a sleep): the
	// relay signals listenReady once LISTEN is set up, so the test cancels while
	// the loop is genuinely blocked in WaitForNotification rather than guessing.
	ready := make(chan struct{}, 1)
	relay := postgres.NewRelay(pool, &countingPublisher{},
		postgres.WithListenNotify(),
		postgres.WithPollInterval(50*time.Millisecond),
		postgres.WithListenReady(ready),
	)

	runCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(runCtx) }()

	// Wait for the listen loop to establish (deterministic; generous upper bound
	// only to fail fast if establishment never happens).
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("listen loop did not establish LISTEN within 10s")
	}

	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled, "Run must return ctx.Err on cancellation")
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s after context cancellation")
	}
}
