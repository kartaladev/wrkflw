package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// recordingPub records the topics of all published events in order.
type recordingPub struct {
	mu     sync.Mutex
	topics []string
}

func (p *recordingPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, ev.Topic)
	return nil
}

// failingPub always returns an error, simulating a broker outage.
type failingPub struct{}

func (failingPub) Publish(context.Context, runtime.OutboxEvent) error {
	return errors.New("broker: down")
}

// seedOutbox inserts n unpublished rows directly into wrkflw_outbox.
// instance_id has no FK constraint on this table, so any string works.
func seedOutbox(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := t.Context()
	for i := range n {
		topic := "test.event"
		dedup := "seed-" + time.Now().Format("20060102150405.000000000") + "-" + string(rune('a'+i))
		_, err := pool.Exec(ctx,
			`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
			 VALUES ($1, $2, $3::jsonb, $4, $5)`,
			"test-instance",
			topic,
			`{"k":"v"}`,
			dedup,
			time.Now().UTC(),
		)
		require.NoError(t, err, "seed outbox row %d", i)
	}
}

// countUnpublished returns the number of rows in wrkflw_outbox where published_at IS NULL.
func countUnpublished(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE published_at IS NULL`,
	).Scan(&n)
	require.NoError(t, err)
	return n
}

// countPublished returns the number of rows where published_at IS NOT NULL.
func countPublished(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE published_at IS NOT NULL`,
	).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestRelayDrainsRows seeds 3 unpublished rows, calls DrainOnce, asserts all 3
// are published and a second drain returns 0.
func TestRelayDrainsRows(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	seedOutbox(t, pool, 3)

	pub := &recordingPub{}
	relay := pg.NewRelay(pool, pub)

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Len(t, pub.topics, 3, "publisher received all 3 events")
	require.Equal(t, 0, countUnpublished(t, pool), "all rows marked published")
	require.Equal(t, 3, countPublished(t, pool))

	// second drain finds nothing — rows are already published.
	n, err = relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestRelaySkipLockedNoDoublePublish seeds N rows; holds a FOR UPDATE lock on
// some rows in a separate connection to simulate a concurrent relay, then
// verifies DrainOnce only claims the unlocked rows (SKIP LOCKED semantics).
func TestRelaySkipLockedNoDoublePublish(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed 4 rows. We will lock 2 of them in a separate transaction to simulate
	// another relay worker holding them.
	seedOutbox(t, pool, 4)

	// Acquire IDs for the first 2 rows so we can lock them explicitly.
	rows, err := pool.Query(t.Context(),
		`SELECT id FROM wrkflw_outbox WHERE published_at IS NULL ORDER BY id LIMIT 2`)
	require.NoError(t, err)
	var lockedIDs []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		lockedIDs = append(lockedIDs, id)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	require.Len(t, lockedIDs, 2)

	// Open a separate connection (not from the pool) so we can hold a tx lock
	// independently of the pool's connections.
	conn, err := pool.Acquire(t.Context())
	require.NoError(t, err)
	defer conn.Release()

	lockTx, err := conn.Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = lockTx.Rollback(t.Context()) }()

	// Lock the first 2 rows in the background tx — they won't be visible to
	// a SKIP LOCKED query until this tx commits/rolls back.
	_, err = lockTx.Exec(t.Context(),
		`SELECT id FROM wrkflw_outbox WHERE id = ANY($1) FOR UPDATE`,
		lockedIDs,
	)
	require.NoError(t, err)

	// DrainOnce must skip the locked rows and only publish the remaining 2.
	pub := &recordingPub{}
	relay := pg.NewRelay(pool, pub)
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n, "should skip the 2 locked rows and publish only 2")
	require.Len(t, pub.topics, 2)

	// Release the lock; the 2 previously-locked rows are still unpublished.
	require.NoError(t, lockTx.Rollback(t.Context()))

	// Now DrainOnce picks up the remaining 2.
	n, err = relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, 0, countUnpublished(t, pool), "all 4 rows now published")
}

// TestRelayPublishErrorLeavesRowUnpublished verifies at-least-once semantics:
// a Publish failure must roll back the entire batch so no rows are silently
// lost (they stay claimable for retry).
func TestRelayPublishErrorLeavesRowUnpublished(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	seedOutbox(t, pool, 1)

	relay := pg.NewRelay(pool, failingPub{})
	_, err := relay.DrainOnce(t.Context())
	require.Error(t, err, "DrainOnce must propagate the publisher error")
	require.Equal(t, 1, countUnpublished(t, pool), "row must remain unpublished for retry")
	require.Equal(t, 0, countPublished(t, pool))
}

// TestRelayRunCancellation verifies that Run returns promptly when ctx is
// cancelled (no goroutine leak) and returns ctx.Err().
func TestRelayRunCancellation(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	// Give Run a moment to start its first DrainOnce, then cancel.
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after ctx cancellation")
	}
}

// TestRelayDrainOnceEmptyOutbox verifies that DrainOnce on an empty outbox
// returns 0 without error.
func TestRelayDrainOnceEmptyOutbox(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	relay := pg.NewRelay(pool, &recordingPub{})
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestRelayBatchSizeOption verifies that WithBatchSize limits rows per drain.
func TestRelayBatchSizeOption(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed 5 rows but set batch size to 2.
	seedOutbox(t, pool, 5)

	pub := &recordingPub{}
	relay := pg.NewRelay(pool, pub, pg.WithBatchSize(2))

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n, "batch size should limit rows per drain to 2")
	require.Len(t, pub.topics, 2)

	// 3 rows still unpublished.
	require.Equal(t, 3, countUnpublished(t, pool))

	// Drain the rest.
	n, err = relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n)

	n, err = relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	require.Equal(t, 0, countUnpublished(t, pool))
}

// TestRelayRunFailFastOnInitialDrainError verifies that Run fails immediately
// (fail-fast) if the initial DrainOnce before the first tick returns a non-cancel
// error, rather than swallowing it. This ensures symmetric error handling with
// the in-loop drain.
func TestRelayRunFailFastOnInitialDrainError(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed 1 row so the first drain attempts to publish.
	seedOutbox(t, pool, 1)

	relay := pg.NewRelay(pool, failingPub{}, pg.WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	// Run must return promptly with the publish error, not swallow it and keep polling.
	select {
	case err := <-done:
		require.Error(t, err)
		require.Contains(t, err.Error(), "broker: down")
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after initial drain error")
	}
}

// TestRelayRunFailFastOnInLoopDrainError verifies that Run exits immediately
// with a non-cancel error returned from DrainOnce during the ticker loop,
// not attempting another poll cycle.
func TestRelayRunFailFastOnInLoopDrainError(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Use a publisher that succeeds on the initial drain but fails on the second.
	pub := &failAfterNPub{successCount: 1}
	relay := pg.NewRelay(pool, pub, pg.WithPollInterval(10*time.Millisecond))

	// Seed 2 rows: first will be published in initial drain (success),
	// second will trigger the error in the in-loop drain.
	seedOutbox(t, pool, 2)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	// Run must return promptly with the publish error after the first successful drain.
	select {
	case err := <-done:
		require.Error(t, err)
		require.Contains(t, err.Error(), "injected error")
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after in-loop drain error")
	}
}

// failAfterNPub publishes successfully for the first N calls, then fails.
type failAfterNPub struct {
	successCount int
	count        int
	mu           sync.Mutex
}

func (p *failAfterNPub) Publish(_ context.Context, _ runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count++
	if p.count > p.successCount {
		return errors.New("injected error")
	}
	return nil
}

// capturingPub records the full OutboxEvents it receives (not just topics).
type capturingPub struct {
	mu     sync.Mutex
	events []runtime.OutboxEvent
}

func (p *capturingPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
	return nil
}

// TestRelayPopulatesDedupAndInstanceID verifies the relay maps the outbox row's
// instance_id and dedup_key columns onto the OutboxEvent it publishes, so a
// watermill adapter can set a stable message UUID + partition key.
func TestRelayPopulatesDedupAndInstanceID(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)`,
		"inst-42", "instance.completed", `{"ok":true}`, "inst-42:7:0", time.Now().UTC(),
	)
	require.NoError(t, err)

	pub := &capturingPub{}
	relay := pg.NewRelay(pool, pub)
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, pub.events, 1)
	require.Equal(t, "instance.completed", pub.events[0].Topic)
	require.Equal(t, "inst-42", pub.events[0].InstanceID)
	require.Equal(t, "inst-42:7:0", pub.events[0].DedupKey)
	require.Equal(t, map[string]any{"ok": true}, pub.events[0].Payload)
}

// TestRelayDrainOncePayloadUnmarshalError verifies that an invalid JSON payload
// (e.g., a non-object value like a string) causes Unmarshal to fail, and the
// error is properly wrapped and returned.
func TestRelayDrainOncePayloadUnmarshalError(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Insert a row with a JSON string value (not an object).
	// When DrainOnce tries to unmarshal it into map[string]any, it will fail.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, NOW())`,
		"unmarshal-error", "test.topic", `"string value"`, "dedup-unmarshal-1",
	)
	require.NoError(t, err)

	relay := pg.NewRelay(pool, &recordingPub{})
	_, err = relay.DrainOnce(t.Context())
	require.Error(t, err, "DrainOnce must propagate the Unmarshal error")
	require.Contains(t, err.Error(), "relay: unmarshal payload",
		"error message should indicate JSON unmarshal failure")
}
