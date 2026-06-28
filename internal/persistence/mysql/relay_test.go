package mysql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/goleak"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// recordingPub records the topics of all published events in order.
type recordingPub struct {
	mu     sync.Mutex
	topics []string
	events []runtime.OutboxEvent
}

func (p *recordingPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, ev.Topic)
	p.events = append(p.events, ev)
	return nil
}

// failingPub always returns an error, simulating a broker outage.
type failingPub struct{}

func (failingPub) Publish(context.Context, runtime.OutboxEvent) error {
	return errors.New("broker: down")
}

// poisonPub fails Publish for events whose DedupKey matches poisonKey.
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

// seedOutboxMySQL inserts n unpublished rows directly into wrkflw_outbox for MySQL.
func seedOutboxMySQL(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	ctx := t.Context()
	for i := range n {
		dedup := fmt.Sprintf("seed-%s-%d", time.Now().Format("20060102150405.000000000"), i)
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			"test-instance",
			"test.event",
			`{"k":"v"}`,
			dedup,
			time.Now().UTC(),
		)
		require.NoError(t, err, "seed outbox row %d", i)
	}
}

// seedOutboxRow inserts a single pending outbox row with explicit resilience columns.
func seedOutboxRowMySQL(t *testing.T, db *sql.DB, dedup string, nextAttempt time.Time) {
	t.Helper()
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
		"poison-test", "test.event", `{}`, dedup, nextAttempt.UTC(), nextAttempt.UTC(),
	)
	require.NoError(t, err, "seed outbox row %s", dedup)
}

// countUnpublished returns the number of rows where published_at IS NULL.
func countUnpublishedMySQL(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE published_at IS NULL`,
	).Scan(&n)
	require.NoError(t, err)
	return n
}

// countPublished returns the number of rows where published_at IS NOT NULL.
func countPublishedMySQL(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE published_at IS NOT NULL`,
	).Scan(&n)
	require.NoError(t, err)
	return n
}

// outboxRowStateMySQL returns the status, retry_count, and next_attempt_at of
// the single outbox row.
func outboxRowStateMySQL(t *testing.T, db *sql.DB) (status string, retry int, nextAttempt time.Time) {
	t.Helper()
	err := db.QueryRowContext(t.Context(),
		`SELECT status, retry_count, next_attempt_at FROM wrkflw_outbox`,
	).Scan(&status, &retry, &nextAttempt)
	require.NoError(t, err)
	return status, retry, nextAttempt
}

// outboxRowStateByDedupMySQL returns the status, retry_count, and next_attempt_at
// of the outbox row with the given dedup_key.
func outboxRowStateByDedupMySQL(t *testing.T, db *sql.DB, dedup string) (status string, retry int, nextAttempt time.Time) {
	t.Helper()
	err := db.QueryRowContext(t.Context(),
		`SELECT status, retry_count, next_attempt_at FROM wrkflw_outbox WHERE dedup_key = ?`,
		dedup,
	).Scan(&status, &retry, &nextAttempt)
	require.NoError(t, err)
	return status, retry, nextAttempt
}

// TestRelay_DrainOnce_PublishesAndMarks seeds rows via direct insert, drains, and
// asserts they are published and marked.
func TestRelay_DrainOnce_PublishesAndMarks(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	seedOutboxMySQL(t, db, 3)

	pub := &recordingPub{}
	relay := mypkg.NewRelay(db, pub)

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Len(t, pub.topics, 3, "publisher received all 3 events")
	require.Equal(t, 0, countUnpublishedMySQL(t, db), "all rows marked published")
	require.Equal(t, 3, countPublishedMySQL(t, db))

	// second drain finds nothing — rows are already published.
	n, err = relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestRelay_DrainOnce_EmptyOutbox verifies DrainOnce on an empty outbox returns 0.
func TestRelay_DrainOnce_EmptyOutbox(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	relay := mypkg.NewRelay(db, &recordingPub{})
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestRelay_Retry_Backoff_DeadLetter verifies that a permanently failing Publisher
// causes retry_count to climb and eventually quarantine the row as 'dead'.
func TestRelay_Retry_Backoff_DeadLetter(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	base := time.Now().UTC().Truncate(time.Second)
	fc := clockwork.NewFakeClockAt(base)

	// Seed two due pending rows: a poison row and a healthy row.
	seedOutboxRowMySQL(t, db, "poison", base)
	seedOutboxRowMySQL(t, db, "healthy", base)

	const maxDelivery = 3
	pub := newPoisonPub("poison")
	relay := mypkg.NewRelay(db, pub,
		mypkg.WithRelayClock(fc),
		mypkg.WithMaxDeliveryAttempts(maxDelivery),
		mypkg.WithRelayBackoff(time.Second, 30*time.Second),
	)

	// Drain #1: healthy is delivered despite poison failing in the same batch.
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n, "exactly one row (healthy) was successfully published")

	hStatus, _, _ := outboxRowStateByDedupMySQL(t, db, "healthy")
	require.Equal(t, "published", hStatus, "healthy row published despite poison failure")

	pStatus, pRetry, pNext := outboxRowStateByDedupMySQL(t, db, "poison")
	require.Equal(t, "pending", pStatus, "poison row stays pending for retry")
	require.Equal(t, 1, pRetry, "poison retry_count incremented")
	require.True(t, pNext.After(base), "poison next_attempt_at pushed into the future by backoff")
	require.Equal(t, 1, pub.publishCount("healthy"), "healthy published once")

	// Keep draining; advance fake clock past each backoff window.
	for pRetry < maxDelivery {
		fc.Advance(2 * time.Minute)
		n, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 0, n, "no new healthy rows; poison keeps failing")
		pStatus, pRetry, _ = outboxRowStateByDedupMySQL(t, db, "poison")
	}

	require.Equal(t, "dead", pStatus, "poison quarantined to dead after MaxDeliveryAttempts")

	// A dead row is no longer claimed.
	fc.Advance(2 * time.Minute)
	n, err = relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)

	// Healthy stayed published and was published exactly once.
	hStatus, _, _ = outboxRowStateByDedupMySQL(t, db, "healthy")
	require.Equal(t, "published", hStatus)
	require.Equal(t, 1, pub.publishCount("healthy"), "healthy never re-published")
}

// TestRelay_ListDeadLettered_And_Redrive verifies the DLQ admin API.
func TestRelay_ListDeadLettered_And_Redrive(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	base := time.Now().UTC().Truncate(time.Second)
	fc := clockwork.NewFakeClockAt(base)

	// Seed a dead row directly.
	var deadID int64
	res, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at, last_error)
		 VALUES (?, ?, ?, ?, ?, 'dead', 5, ?, 'boom')`,
		"dlq-test-instance", "dlq.event", `{}`, "dlq-dead-1", base.UTC(), base.UTC(),
	)
	require.NoError(t, err, "seed dead row")
	deadID, err = res.LastInsertId()
	require.NoError(t, err)

	// Seed a pending row to confirm ListDeadLettered doesn't return it.
	var pendingID int64
	res, err = db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
		"dlq-test-instance", "other.event", `{}`, "dlq-pending-1", base.UTC(), base.UTC(),
	)
	require.NoError(t, err, "seed pending row")
	pendingID, err = res.LastInsertId()
	require.NoError(t, err)

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayClock(fc))

	// ListDeadLettered must return exactly the one dead row.
	dead, err := relay.ListDeadLettered(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, dead, 1, "only the dead row should be returned")
	require.Equal(t, deadID, dead[0].ID)
	require.Equal(t, "dlq-test-instance", dead[0].InstanceID)
	require.Equal(t, "dlq.event", dead[0].Topic)
	require.Equal(t, 5, dead[0].RetryCount)
	require.Equal(t, "boom", dead[0].LastError)

	// Redrive with no ids must return 0, no error.
	n, err := relay.Redrive(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n, "Redrive with no ids should be a no-op")

	// Redrive a pending row ID (not dead) must return 0.
	n, err = relay.Redrive(t.Context(), pendingID)
	require.NoError(t, err)
	require.Equal(t, 0, n, "Redrive on a pending row must be a no-op")

	// Redrive the dead row: must return 1.
	n, err = relay.Redrive(t.Context(), deadID)
	require.NoError(t, err)
	require.Equal(t, 1, n, "Redrive must requeue exactly 1 row")

	// Verify the redriven row is now pending with reset state.
	var (
		status      string
		retryCount  int
		lastErrPtr  *string
		nextAttempt time.Time
	)
	err = db.QueryRowContext(t.Context(),
		`SELECT status, retry_count, last_error, next_attempt_at FROM wrkflw_outbox WHERE id = ?`,
		deadID,
	).Scan(&status, &retryCount, &lastErrPtr, &nextAttempt)
	require.NoError(t, err)
	require.Equal(t, "pending", status, "redriven row must be pending")
	require.Equal(t, 0, retryCount, "redriven row must have retry_count reset to 0")
	require.Nil(t, lastErrPtr, "redriven row must have last_error cleared")
	require.True(t, !nextAttempt.After(fc.Now()), "redriven row next_attempt_at must be <= now")

	// A subsequent DrainOnce with a healthy publisher must publish the redriven row.
	pub := &recordingPub{}
	healthyRelay := mypkg.NewRelay(db, pub, mypkg.WithRelayClock(fc))
	n, err = healthyRelay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1, "at least the redriven row must be published")

	// Confirm the redriven row is now published.
	var finalStatus string
	err = db.QueryRowContext(t.Context(),
		`SELECT status FROM wrkflw_outbox WHERE id = ?`, deadID,
	).Scan(&finalStatus)
	require.NoError(t, err)
	require.Equal(t, "published", finalStatus, "redriven row must be published after drain")
}

// TestRelay_Run_DrainsUntilCancelled verifies Run exits cleanly on ctx cancel
// and leaves no leaked goroutines.
func TestRelay_Run_DrainsUntilCancelled(t *testing.T) {
	// goleak ignores testcontainers/Reaper goroutines and go-sql-driver watcher
	// goroutines that are managed by *sql.DB lifecycle (not by the Relay itself).
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
		goleak.IgnoreTopFunction("github.com/go-sql-driver/mysql.(*mysqlConn).startWatcher.func1"),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionCleaner"),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)
	// Not parallel — goleak requires single-threaded goroutine state.
	db := database.RunTestMySQL(t)

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	// Give Run a moment to start, then cancel.
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after ctx cancellation")
	}
}

// TestRelay_PublishError_RowStaysUnpublished verifies at-least-once semantics:
// a Publish failure must NOT mark the row published; retry_count climbs.
func TestRelay_PublishError_RowStaysUnpublished(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	seedOutboxMySQL(t, db, 1)

	relay := mypkg.NewRelay(db, failingPub{})
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err, "a single poison row is quarantined, not propagated as a drain error")
	require.Equal(t, 0, n, "no row was successfully published")
	require.Equal(t, 1, countUnpublishedMySQL(t, db), "row must remain unpublished for retry")
	require.Equal(t, 0, countPublishedMySQL(t, db))

	status, retry, _ := outboxRowStateMySQL(t, db)
	require.Equal(t, "pending", status, "row stays pending after a transient failure")
	require.Equal(t, 1, retry, "retry_count incremented")
}

// TestRelay_BatchSize limits rows per drain.
func TestRelay_BatchSize(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	seedOutboxMySQL(t, db, 5)

	pub := &recordingPub{}
	relay := mypkg.NewRelay(db, pub, mypkg.WithBatchSize(2))

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n, "batch size should limit rows per drain to 2")
	require.Len(t, pub.topics, 2)
	require.Equal(t, 3, countUnpublishedMySQL(t, db))
}

// TestRelay_WithRelayClock_Nil_FallsBackToSystem asserts that passing a nil clock
// to WithRelayClock does NOT overwrite the default clock.System().
func TestRelay_WithRelayClock_Nil_FallsBackToSystem(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	pub := &recordingPub{}
	relay := mypkg.NewRelay(db, pub, mypkg.WithRelayClock(nil))

	assert.NotPanics(t, func() {
		_, _ = relay.DrainOnce(t.Context())
	}, "WithRelayClock(nil) must be ignored; DrainOnce must not panic on nil clock")
}

// TestRelay_PopulatesDedupAndInstanceID verifies the relay maps outbox columns
// onto the OutboxEvent it publishes.
func TestRelay_PopulatesDedupAndInstanceID(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	_, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		"inst-42", "instance.completed", `{"ok":true}`, "inst-42:7:0", time.Now().UTC(),
	)
	require.NoError(t, err)

	pub := &recordingPub{}
	relay := mypkg.NewRelay(db, pub)
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, pub.events, 1)
	require.Equal(t, "instance.completed", pub.events[0].Topic)
	require.Equal(t, "inst-42", pub.events[0].InstanceID)
	require.Equal(t, "inst-42:7:0", pub.events[0].DedupKey)
	require.Equal(t, map[string]any{"ok": true}, pub.events[0].Payload)
}

// TestRelay_TelemetryOptions verifies that WithRelayLogger, WithRelayTracerProvider,
// and WithRelayMeterProvider are accepted without error and don't break DrainOnce.
func TestRelay_TelemetryOptions(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	relay := mypkg.NewRelay(db, &recordingPub{},
		mypkg.WithRelayLogger(slog.Default()),
		mypkg.WithRelayTracerProvider(tracenoop.NewTracerProvider()),
		mypkg.WithRelayMeterProvider(noop.NewMeterProvider()),
	)
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestRelay_Run_AbsorbsPublishFailures verifies that a persistently-failing
// Publisher does not terminate Run — the loop keeps polling until cancelled.
func TestRelay_Run_AbsorbsPublishFailures(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	seedOutboxMySQL(t, db, 1)

	relay := mypkg.NewRelay(db, failingPub{}, mypkg.WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			t.Errorf("Run terminated unexpectedly on a publish failure: %v", err)
			return true
		default:
		}
		_, retry, _ := outboxRowStateMySQL(t, db)
		return retry >= 1
	}, 5*time.Second, 20*time.Millisecond, "relay should retry the poison row at least once without terminating Run")

	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after ctx cancellation")
	}

	status, retry, _ := outboxRowStateMySQL(t, db)
	require.Equal(t, "pending", status)
	require.GreaterOrEqual(t, retry, 1, "the poison row was retried at least once")
}

// TestRelayBackoff_Pure verifies the capped exponential backoff function.
func TestRelayBackoff_Pure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		retryCount  int
		base        time.Duration
		maxInterval time.Duration
		want        time.Duration
	}{
		{"retry0 base=1s max=1m", 0, time.Second, time.Minute, time.Second},
		{"retry1 base=1s max=1m", 1, time.Second, time.Minute, 2 * time.Second},
		{"retry2 base=1s max=1m", 2, time.Second, time.Minute, 4 * time.Second},
		{"capped at max", 10, time.Second, time.Minute, time.Minute},
		{"negative retry treated as 0", -1, time.Second, time.Minute, 0},
		{"non-positive base yields 0", 1, 0, time.Minute, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mypkg.RelayBackoff(tc.retryCount, tc.base, tc.maxInterval)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestRelay_ListDeadLettered_Empty returns an empty slice when no dead rows exist.
func TestRelay_ListDeadLettered_Empty(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	relay := mypkg.NewRelay(db, &recordingPub{})
	dead, err := relay.ListDeadLettered(t.Context(), 10)
	require.NoError(t, err)
	require.Empty(t, dead)
}

// TestRelay_Redrive_NoIds is a no-op.
func TestRelay_Redrive_NoIds(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	relay := mypkg.NewRelay(db, &recordingPub{})
	n, err := relay.Redrive(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestRelay_DrainOnce_InfraError verifies that an infrastructure error from the DB
// (e.g., closed connection) is propagated from DrainOnce and terminates Run.
func TestRelay_DrainOnce_InfraError(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	// Close the DB so all operations fail immediately.
	require.NoError(t, db.Close())

	relay := mypkg.NewRelay(db, &recordingPub{})
	_, err := relay.DrainOnce(t.Context())
	require.Error(t, err, "DrainOnce must propagate infrastructure errors")
	require.Contains(t, err.Error(), "workflow-persistence-mysql: relay")
}

// TestRelay_Run_PropagatesInfraError verifies that Run returns a non-nil infrastructure
// error (not just context.Canceled) when the DB is unavailable.
func TestRelay_Run_PropagatesInfraError(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	// Close the DB so DrainOnce fails on the first call within Run.
	require.NoError(t, db.Close())

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithPollInterval(10*time.Millisecond))
	err := relay.Run(t.Context())
	require.Error(t, err)
	// Must NOT be context.Canceled since the ctx was not cancelled.
	require.NotErrorIs(t, err, context.Canceled)
	require.Contains(t, err.Error(), "workflow-persistence-mysql: relay")
}

// TestRelay_ListDeadLettered_ClosedDB verifies that ListDeadLettered propagates a DB error.
func TestRelay_ListDeadLettered_ClosedDB(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	require.NoError(t, db.Close())

	relay := mypkg.NewRelay(db, &recordingPub{})
	_, err := relay.ListDeadLettered(t.Context(), 10)
	require.Error(t, err, "ListDeadLettered must propagate DB error")
	require.Contains(t, err.Error(), "workflow-persistence-mysql: relay: list dead-lettered")
}

// TestRelay_ListDeadLettered_MultipleRows verifies ordering and limit for multiple dead rows.
func TestRelay_ListDeadLettered_MultipleRows(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	base := time.Now().UTC().Truncate(time.Second)
	for i := range 5 {
		_, err := db.ExecContext(t.Context(),
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at, last_error)
			 VALUES (?, ?, ?, ?, ?, 'dead', 10, ?, 'boom')`,
			"dlq-inst", "ev.dead", `{}`, fmt.Sprintf("multi-dead-%d", i), base.UTC(), base.UTC(),
		)
		require.NoError(t, err)
	}

	relay := mypkg.NewRelay(db, &recordingPub{})

	// Limit=3 should return only 3.
	dead, err := relay.ListDeadLettered(t.Context(), 3)
	require.NoError(t, err)
	require.Len(t, dead, 3)
	// IDs must be in ascending order.
	require.LessOrEqual(t, dead[0].ID, dead[1].ID)
	require.LessOrEqual(t, dead[1].ID, dead[2].ID)
}

// TestRelay_DrainOnce_BadJSONReturnsError verifies that an outbox row with
// invalid JSON payload causes DrainOnce to return an unmarshal error.
func TestRelay_DrainOnce_BadJSONReturnsError(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	// MySQL JSON column rejects non-object JSON at insert time; use a valid
	// JSON value (a quoted string) that json.Unmarshal cannot decode into map[string]any.
	// Insert directly bypassing JSON column type validation using a raw string
	// that MySQL 8 stores as JSON type "string", which json.Unmarshal will fail
	// to decode into map[string]any.
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES (?, ?, CAST(? AS JSON), ?, ?)`,
		"bad-json-inst", "test.topic", `"this is a json string not an object"`, "bad-json-dedup-1", time.Now().UTC(),
	)
	require.NoError(t, err)

	relay := mypkg.NewRelay(db, &recordingPub{})
	_, err = relay.DrainOnce(t.Context())
	require.Error(t, err, "DrainOnce must propagate JSON unmarshal error")
	require.Contains(t, err.Error(), "relay: unmarshal payload", "error must indicate unmarshal failure")
}

// TestRelay_Redrive_MultipleIds verifies that Redrive handles multiple IDs at once.
func TestRelay_Redrive_MultipleIds(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	base := time.Now().UTC().Truncate(time.Second)
	fc := clockwork.NewFakeClockAt(base)

	var ids []int64
	for i := range 3 {
		res, err := db.ExecContext(t.Context(),
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at, last_error)
			 VALUES (?, ?, ?, ?, ?, 'dead', 10, ?, 'boom')`,
			"redrive-inst", "ev.dead", `{}`, fmt.Sprintf("multi-redrive-%d", i), base.UTC(), base.UTC(),
		)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		ids = append(ids, id)
	}

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayClock(fc))
	n, err := relay.Redrive(t.Context(), ids...)
	require.NoError(t, err)
	require.Equal(t, 3, n, "Redrive must reset all 3 dead rows")

	// Drain the re-queued rows.
	pub := &recordingPub{}
	drainRelay := mypkg.NewRelay(db, pub, mypkg.WithRelayClock(fc))
	n, err = drainRelay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, n)
}
