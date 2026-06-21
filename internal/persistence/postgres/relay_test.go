package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
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

// outboxRowState returns the status, retry_count, and next_attempt_at of the
// single outbox row (use only when exactly one row is present).
func outboxRowState(t *testing.T, pool *pgxpool.Pool) (status string, retry int, nextAttempt time.Time) {
	t.Helper()
	err := pool.QueryRow(t.Context(),
		`SELECT status, retry_count, next_attempt_at FROM wrkflw_outbox`,
	).Scan(&status, &retry, &nextAttempt)
	require.NoError(t, err)
	return status, retry, nextAttempt
}

// outboxRowStateByDedup returns the status, retry_count, and next_attempt_at of
// the outbox row with the given dedup_key.
func outboxRowStateByDedup(t *testing.T, pool *pgxpool.Pool, dedup string) (status string, retry int, nextAttempt time.Time) {
	t.Helper()
	err := pool.QueryRow(t.Context(),
		`SELECT status, retry_count, next_attempt_at FROM wrkflw_outbox WHERE dedup_key = $1`,
		dedup,
	).Scan(&status, &retry, &nextAttempt)
	require.NoError(t, err)
	return status, retry, nextAttempt
}

// seedOutboxRow inserts a single pending outbox row with explicit resilience
// columns so a test can control the claim predicate (status / next_attempt_at).
func seedOutboxRow(t *testing.T, pool *pgxpool.Pool, dedup string, nextAttempt time.Time) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5, 'pending', 0, $6)`,
		"poison-test", "test.event", `{}`, dedup, nextAttempt.UTC(), nextAttempt.UTC(),
	)
	require.NoError(t, err, "seed outbox row %s", dedup)
}

// poisonPub fails Publish for events whose DedupKey matches poisonKey and
// succeeds for all others. It counts publishes per dedup key.
type poisonPub struct {
	mu        sync.Mutex
	poisonKey string
	counts    map[string]int
}

func newPoisonPub(poisonKey string) *poisonPub {
	return &poisonPub{poisonKey: poisonKey, counts: map[string]int{}}
}

func (p *poisonPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[ev.DedupKey]++
	if ev.DedupKey == p.poisonKey {
		return errors.New("poison: permanent failure")
	}
	return nil
}

func (p *poisonPub) publishCount(dedup string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[dedup]
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
// a Publish failure must NOT mark the row published; the row is quarantined for
// retry (status stays 'pending', retry_count climbs) and stays claimable. With
// per-row isolation DrainOnce no longer returns an error for a publish failure —
// it persists the row's retry state and reports 0 successfully-published rows.
func TestRelayPublishErrorLeavesRowUnpublished(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	seedOutbox(t, pool, 1)

	relay := pg.NewRelay(pool, failingPub{})
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err, "a single poison row is quarantined, not propagated as a drain error")
	require.Equal(t, 0, n, "no row was successfully published")
	require.Equal(t, 1, countUnpublished(t, pool), "row must remain unpublished for retry")
	require.Equal(t, 0, countPublished(t, pool))

	status, retry, _ := outboxRowState(t, pool)
	require.Equal(t, "pending", status, "row stays pending after a transient failure")
	require.Equal(t, 1, retry, "retry_count incremented")
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

// TestRelayRunAbsorbsPublishFailures verifies that, with per-row isolation, a
// persistently-failing Publisher no longer terminates Run (the old fail-fast
// behaviour). The poison row is quarantined for retry while Run keeps polling;
// Run returns only on context cancellation. This is the head-of-line-blocking
// fix: a broker outage no longer crashes the relay loop.
func TestRelayRunAbsorbsPublishFailures(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed 1 row so each drain attempts to publish and fails.
	seedOutbox(t, pool, 1)

	relay := pg.NewRelay(pool, failingPub{}, pg.WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	// Let Run poll a few times; it must NOT have returned an error on its own.
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Run terminated unexpectedly on a publish failure: %v", err)
	default:
	}

	// Cancellation is the only thing that stops Run.
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after ctx cancellation")
	}

	// The poison row stayed pending and was retried (retry_count climbed).
	status, retry, _ := outboxRowState(t, pool)
	require.Equal(t, "pending", status)
	require.GreaterOrEqual(t, retry, 1, "the poison row was retried at least once")
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

// TestRelayPoisonIsolation verifies per-row isolation, backoff, and dead-letter
// quarantine (ADR-0017). A poison row that always fails to publish must NOT block
// a healthy peer in the same batch (no head-of-line blocking), and must be
// retried with exponential backoff until it is quarantined as 'dead' after
// MaxDeliveryAttempts. The healthy row is published exactly once and never again.
func TestRelayPoisonIsolation(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	base := time.Now().UTC().Truncate(time.Second)
	fc := clockwork.NewFakeClockAt(base)

	// Seed two due pending rows: a poison row and a healthy row.
	seedOutboxRow(t, pool, "poison", base)
	seedOutboxRow(t, pool, "healthy", base)

	const maxDelivery = 3
	pub := newPoisonPub("poison")
	relay := pg.NewRelay(pool, pub,
		pg.WithClock(fc),
		pg.WithMaxDeliveryAttempts(maxDelivery),
		pg.WithRelayBackoff(time.Second, 30*time.Second),
	)

	// Drain #1: healthy is delivered despite poison failing in the same batch.
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n, "exactly one row (healthy) was successfully published")

	hStatus, _, _ := outboxRowStateByDedup(t, pool, "healthy")
	require.Equal(t, "published", hStatus, "healthy row published despite poison failure")

	pStatus, pRetry, pNext := outboxRowStateByDedup(t, pool, "poison")
	require.Equal(t, "pending", pStatus, "poison row stays pending for retry")
	require.Equal(t, 1, pRetry, "poison retry_count incremented")
	require.True(t, pNext.After(base), "poison next_attempt_at pushed into the future by backoff")
	require.Equal(t, 1, pub.publishCount("healthy"), "healthy published once")

	// Keep draining; each time advance the fake clock past the poison row's
	// backoff so it becomes due again. retry_count climbs until quarantine.
	for pRetry < maxDelivery {
		// Advance well past any backoff window so the row is due.
		fc.Advance(2 * time.Minute)
		n, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 0, n, "no new healthy rows; poison keeps failing")
		pStatus, pRetry, _ = outboxRowStateByDedup(t, pool, "poison")
	}

	require.Equal(t, "dead", pStatus, "poison quarantined to dead after MaxDeliveryAttempts")

	// A dead row is no longer claimed.
	fc.Advance(2 * time.Minute)
	n, err = relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)

	// Healthy stayed published and was published exactly once throughout.
	hStatus, _, _ = outboxRowStateByDedup(t, pool, "healthy")
	require.Equal(t, "published", hStatus)
	require.Equal(t, 1, pub.publishCount("healthy"), "healthy never re-published")
}
