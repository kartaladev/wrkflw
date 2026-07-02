package store_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/goleak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newSQLiteDialectForTest returns the SQLite dialect without a real db connection.
func newSQLiteDialectForTest() dialect.Dialect { return dialect.NewSQLite() }

// ── fake publishers ───────────────────────────────────────────────────────────

type recordingRelayPub struct {
	mu     sync.Mutex
	topics []string
	events []runtime.OutboxEvent
}

func (p *recordingRelayPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, ev.Topic)
	p.events = append(p.events, ev)
	return nil
}

type failingRelayPub struct{}

func (failingRelayPub) Publish(context.Context, runtime.OutboxEvent) error {
	return errors.New("broker: down")
}

type poisonRelayPub struct {
	mu        sync.Mutex
	poisonKey string
	counts    map[string]int
}

func newPoisonRelayPub(poisonKey string) *poisonRelayPub {
	return &poisonRelayPub{poisonKey: poisonKey, counts: map[string]int{}}
}

func (p *poisonRelayPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[ev.DedupKey]++
	if ev.DedupKey == p.poisonKey {
		return errors.New("poison: permanent failure")
	}
	return nil
}

func (p *poisonRelayPub) publishCount(dedup string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[dedup]
}

// ── outbox seed helpers ───────────────────────────────────────────────────────

// relayTimeArg converts t using b's dialect (via a temporary Store).
func relayTimeArg(b backend, t time.Time) any {
	return store.TimeArgForDialect(store.New(b.conn, b.dialect), t)
}

// seedRelayOutbox inserts n pending outbox rows using the store's generic querier.
// next_attempt_at is set to now so the relay claims them immediately.
func seedRelayOutbox(t *testing.T, b backend, n int) {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	now := time.Now().UTC()
	for i := range n {
		dedup := fmt.Sprintf("relay-seed-%d-%d", now.UnixNano(), i)
		_, err := q.Exec(t.Context(), b.dialect.Rebind(
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
			 VALUES (?,?,?,?,?,'pending',0,?)`),
			"test-instance", "test.event", `{"k":"v"}`, dedup,
			relayTimeArg(b, now), relayTimeArg(b, now),
		)
		require.NoError(t, err, "seed outbox row %d on %s", i, b.name)
	}
}

// seedRelayOutboxRow inserts a single pending outbox row with a specific next_attempt_at.
func seedRelayOutboxRow(t *testing.T, b backend, dedup string, nextAttempt time.Time) {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	_, err := q.Exec(t.Context(), b.dialect.Rebind(
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?,?,?,?,?,'pending',0,?)`),
		"poison-test", "test.event", `{}`, dedup,
		relayTimeArg(b, nextAttempt.UTC()), relayTimeArg(b, nextAttempt.UTC()),
	)
	require.NoError(t, err, "seed outbox row %s on %s", dedup, b.name)
}

// seedRelayDeadRow inserts a status='dead' outbox row with explicit last_error.
// Returns the inserted ID (retrieved via SELECT after INSERT for all dialects).
func seedRelayDeadRow(t *testing.T, b backend, dedup, lastErr string, at time.Time) int64 {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	_, err := q.Exec(t.Context(), b.dialect.Rebind(
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at, last_error)
		 VALUES (?,?,?,?,?,'dead',5,?,?)`),
		"dlq-inst", "dlq.event", `{}`, dedup,
		relayTimeArg(b, at.UTC()), relayTimeArg(b, at.UTC()), lastErr,
	)
	require.NoError(t, err, "seed dead row %s on %s", dedup, b.name)
	return relayLastInsertedID(t, b, dedup)
}

// seedRelayPendingRow inserts a status='pending' outbox row and returns its id.
func seedRelayPendingRow(t *testing.T, b backend, dedup string, at time.Time) int64 {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	_, err := q.Exec(t.Context(), b.dialect.Rebind(
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?,?,?,?,?,'pending',0,?)`),
		"dlq-inst", "other.event", `{}`, dedup,
		relayTimeArg(b, at.UTC()), relayTimeArg(b, at.UTC()),
	)
	require.NoError(t, err, "seed pending row %s on %s", dedup, b.name)
	return relayLastInsertedID(t, b, dedup)
}

// relayLastInsertedID fetches the id of the outbox row with the given dedup_key.
func relayLastInsertedID(t *testing.T, b backend, dedup string) int64 {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	var id int64
	err := q.QueryRow(t.Context(), b.dialect.Rebind(
		`SELECT id FROM wrkflw_outbox WHERE dedup_key = ?`), dedup,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// outboxStatusAndRetry reads the status and retry_count of the single outbox row with given dedup.
func outboxStatusAndRetry(t *testing.T, b backend, dedup string) (status string, retry int) {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	err := q.QueryRow(t.Context(), b.dialect.Rebind(
		`SELECT status, retry_count FROM wrkflw_outbox WHERE dedup_key = ?`), dedup,
	).Scan(&status, &retry)
	require.NoError(t, err)
	return status, retry
}

// countOutboxByStatus returns the count of rows with the given status.
func countOutboxByStatus(t *testing.T, b backend, status string) int {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	var n int
	err := q.QueryRow(t.Context(), b.dialect.Rebind(
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE status = ?`), status,
	).Scan(&n)
	require.NoError(t, err)
	return n
}

// outboxStatusByID reads the status of the row with the given id.
func outboxStatusByID(t *testing.T, b backend, id int64) string {
	t.Helper()
	s := store.New(b.conn, b.dialect)
	q := s.QuerierForTest(t.Context())
	var status string
	err := q.QueryRow(t.Context(), b.dialect.Rebind(
		`SELECT status FROM wrkflw_outbox WHERE id = ?`), id,
	).Scan(&status)
	require.NoError(t, err)
	return status
}

// ── DrainOnce + publish tests ─────────────────────────────────────────────────

// TestRelayDrainOnce_PublishesAndMarks seeds 3 pending rows, calls DrainOnce,
// asserts all are published and a second drain returns 0.
func TestRelayDrainOnce_PublishesAndMarks(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 3)

		pub := &recordingRelayPub{}
		relay := store.NewRelay(b.conn, b.dialect, pub)

		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 3, n)
		assert.Len(t, pub.topics, 3, "publisher received all 3 events on %s", b.name)
		assert.Equal(t, 0, countOutboxByStatus(t, b, "pending"), "no pending rows remain")
		assert.Equal(t, 3, countOutboxByStatus(t, b, "published"), "3 rows marked published")

		// second drain: no pending rows.
		n, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})
}

// TestRelayDrainOnce_EmptyOutbox verifies DrainOnce on empty table returns 0, nil.
func TestRelayDrainOnce_EmptyOutbox(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{})
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})
}

// TestRelayDrainOnce_PublishError_Retry verifies that a Publish failure leaves
// the row pending with incremented retry_count and does not propagate as a drain error.
func TestRelayDrainOnce_PublishError_Retry(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 1)

		relay := store.NewRelay(b.conn, b.dialect, failingRelayPub{})
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err, "publish failure must be absorbed, not propagated")
		assert.Equal(t, 0, n)
		// Row should remain pending with retry_count = 1.
		assert.Equal(t, 1, countOutboxByStatus(t, b, "pending"), "row stays pending on %s", b.name)
	})
}

// TestRelayPoisonIsolation verifies per-row isolation, backoff, and dead-letter quarantine.
// A poison row must not block a healthy peer; after MaxDeliveryAttempts it becomes 'dead'.
func TestRelayPoisonIsolation(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		base := time.Now().UTC().Truncate(time.Second)
		fc := clockwork.NewFakeClockAt(base)

		seedRelayOutboxRow(t, b, "poison", base)
		seedRelayOutboxRow(t, b, "healthy", base)

		const maxDelivery = 3
		pub := newPoisonRelayPub("poison")
		relay := store.NewRelay(b.conn, b.dialect, pub,
			store.WithRelayClock(fc),
			store.WithRelayMaxDeliveryAttempts(maxDelivery),
			store.WithRelayBackoff(time.Second, 30*time.Second),
		)

		// Drain #1: healthy published despite poison failing.
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, n, "only healthy row published on %s", b.name)

		hStatus, _ := outboxStatusAndRetry(t, b, "healthy")
		assert.Equal(t, "published", hStatus, "healthy published on %s", b.name)

		pStatus, pRetry := outboxStatusAndRetry(t, b, "poison")
		assert.Equal(t, "pending", pStatus, "poison stays pending on %s", b.name)
		assert.Equal(t, 1, pRetry, "poison retry_count == 1 on %s", b.name)

		// Keep draining until quarantine.
		for pRetry < maxDelivery {
			fc.Advance(2 * time.Minute)
			n, err = relay.DrainOnce(t.Context())
			require.NoError(t, err)
			assert.Equal(t, 0, n)
			pStatus, pRetry = outboxStatusAndRetry(t, b, "poison")
		}
		assert.Equal(t, "dead", pStatus, "poison quarantined on %s", b.name)

		// Dead row no longer claimed.
		fc.Advance(2 * time.Minute)
		n, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		// Healthy unchanged.
		hStatus, _ = outboxStatusAndRetry(t, b, "healthy")
		assert.Equal(t, "published", hStatus, "healthy still published on %s", b.name)
		assert.Equal(t, 1, pub.publishCount("healthy"), "healthy published exactly once on %s", b.name)
	})
}

// ── ListDeadLettered / Redrive ────────────────────────────────────────────────

// TestRelayListDeadLettered verifies only dead rows are returned.
func TestRelayListDeadLettered(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		at := time.Now().UTC().Truncate(time.Second)
		deadID := seedRelayDeadRow(t, b, "dead-dedup-1", "boom", at)
		_ = seedRelayPendingRow(t, b, "pending-dedup-1", at)

		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{})
		dead, err := relay.ListDeadLettered(t.Context(), 10)
		require.NoError(t, err)
		require.Len(t, dead, 1, "only dead row on %s", b.name)
		assert.Equal(t, deadID, dead[0].ID, "id matches on %s", b.name)
		assert.Equal(t, "dlq-inst", dead[0].InstanceID)
		assert.Equal(t, "dlq.event", dead[0].Topic)
		assert.Equal(t, 5, dead[0].RetryCount)
		assert.Equal(t, "boom", dead[0].LastError)
	})
}

// TestRelayRedrive verifies Redrive re-queues a dead row and a subsequent DrainOnce publishes it.
func TestRelayRedrive(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		at := time.Now().UTC().Truncate(time.Second)
		fc := clockwork.NewFakeClockAt(at)

		deadID := seedRelayDeadRow(t, b, "redrive-dead-1", "oops", at)
		pendingID := seedRelayPendingRow(t, b, "redrive-pending-1", at)

		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayClock(fc),
		)

		// No-op with zero ids.
		n, err := relay.Redrive(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		// No-op on a pending (non-dead) row.
		n, err = relay.Redrive(t.Context(), pendingID)
		require.NoError(t, err)
		assert.Equal(t, 0, n, "pending row not affected on %s", b.name)

		// Redrive the dead row.
		n, err = relay.Redrive(t.Context(), deadID)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "one row re-queued on %s", b.name)

		// Now status='pending'.
		status := outboxStatusByID(t, b, deadID)
		assert.Equal(t, "pending", status, "redriven row is pending on %s", b.name)

		// DrainOnce publishes the redriven row.
		pub := &recordingRelayPub{}
		relay2 := store.NewRelay(b.conn, b.dialect, pub, store.WithRelayClock(fc))
		n, err = relay2.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, 1, "redriven row published on %s", b.name)

		status = outboxStatusByID(t, b, deadID)
		assert.Equal(t, "published", status, "redriven row now published on %s", b.name)
	})
}

// ── OutboxStats ───────────────────────────────────────────────────────────────

// TestRelayOutboxStats verifies pending/dead counts are correct.
func TestRelayOutboxStats(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		at := time.Now().UTC().Add(-5 * time.Second) // 5s in the past so age > 0
		seedRelayOutboxRow(t, b, "stats-pending-1", at)
		seedRelayOutboxRow(t, b, "stats-pending-2", at)
		_ = seedRelayDeadRow(t, b, "stats-dead-1", "err", at)

		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{})
		stats, err := relay.OutboxStats(t.Context())
		require.NoError(t, err)
		assert.Equal(t, int64(2), stats.Pending, "pending count on %s", b.name)
		assert.Equal(t, int64(1), stats.Dead, "dead count on %s", b.name)
		assert.Greater(t, stats.OldestPendingAge, time.Duration(0), "age > 0 on %s", b.name)
	})
}

// ── Run / ctx-cancel / goleak ─────────────────────────────────────────────────

// TestRelayRun_ExitsOnCtxCancel verifies Run exits promptly on cancellation
// with context.Canceled. Goleak is scoped to the Relay goroutine by capturing
// the current goroutine snapshot before Run starts.
func TestRelayRun_ExitsOnCtxCancel(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		// Capture goroutines that exist before Run starts — pool background
		// goroutines, sibling subtests, etc. — so goleak only checks Relay-owned
		// goroutines.
		opt := goleak.IgnoreCurrent()

		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- relay.Run(ctx) }()

		time.Sleep(25 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled, "Run must return context.Canceled on %s", b.name)
		case <-time.After(2 * time.Second):
			t.Fatalf("Run did not return within 2s after ctx cancel on %s", b.name)
		}

		// Verify the Relay goroutine exited cleanly (no leak).
		goleak.VerifyNone(t, opt)
	})
}

// TestRelayRun_DeadlineExceeded verifies Run returns ctx.Err() on deadline expiry.
func TestRelayRun_DeadlineExceeded(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)

		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancel()

		err := relay.Run(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded, "Run must return DeadlineExceeded on %s", b.name)
		assert.Equal(t, context.DeadlineExceeded, err, "must be ctx.Err() exactly on %s", b.name)
	})
}

// TestRelayRun_AbsorbsPublishFailures verifies Run keeps polling after publish errors.
func TestRelayRun_AbsorbsPublishFailures(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 1)

		relay := store.NewRelay(b.conn, b.dialect, failingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- relay.Run(ctx) }()

		// Wait until the poison row has retry_count >= 1.
		require.Eventually(t, func() bool {
			select {
			case err := <-done:
				t.Errorf("Run terminated unexpectedly: %v", err)
				return true
			default:
			}
			s := store.New(b.conn, b.dialect)
			q := s.QuerierForTest(t.Context())
			var retry int
			_ = q.QueryRow(t.Context(), b.dialect.Rebind(
				`SELECT retry_count FROM wrkflw_outbox LIMIT 1`),
			).Scan(&retry)
			return retry >= 1
		}, 5*time.Second, 20*time.Millisecond, "relay should retry on %s", b.name)

		cancel()
		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled, "Run returns context.Canceled on %s", b.name)
		case <-time.After(2 * time.Second):
			t.Fatalf("Run did not return within 2s on %s", b.name)
		}
	})
}

// ── Observability ─────────────────────────────────────────────────────────────

// TestRelayBatchSpan verifies that DrainOnce emits a "wrkflw.relay.batch" span
// with wrkflw.batch_size and wrkflw.published_count attributes.
func TestRelayBatchSpan(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 1)

		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

		pub := &recordingRelayPub{}
		relay := store.NewRelay(b.conn, b.dialect, pub,
			store.WithRelayTracerProvider(tp),
		)

		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, n)

		var batchSpan sdktrace.ReadOnlySpan
		for _, s := range sr.Ended() {
			if s.Name() == "wrkflw.relay.batch" {
				batchSpan = s
				break
			}
		}
		require.NotNil(t, batchSpan, "wrkflw.relay.batch span must be emitted on %s", b.name)

		attrs := map[string]int64{}
		for _, a := range batchSpan.Attributes() {
			switch string(a.Key) {
			case "wrkflw.batch_size", "wrkflw.published_count":
				attrs[string(a.Key)] = a.Value.AsInt64()
			}
		}
		assert.Equal(t, int64(1), attrs["wrkflw.batch_size"], "batch_size on %s", b.name)
		assert.Equal(t, int64(1), attrs["wrkflw.published_count"], "published_count on %s", b.name)
	})
}

// TestRelayBatchSpan_EmptyOutbox verifies a span is emitted even on empty drain.
func TestRelayBatchSpan_EmptyOutbox(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayTracerProvider(tp),
		)
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		var batchSpan sdktrace.ReadOnlySpan
		for _, s := range sr.Ended() {
			if s.Name() == "wrkflw.relay.batch" {
				batchSpan = s
				break
			}
		}
		require.NotNil(t, batchSpan, "span must be emitted on empty drain on %s", b.name)

		var found bool
		for _, a := range batchSpan.Attributes() {
			if string(a.Key) == "wrkflw.batch_size" {
				found = true
				assert.Equal(t, int64(0), a.Value.AsInt64(), "batch_size=0 on %s", b.name)
			}
		}
		assert.True(t, found, "wrkflw.batch_size attribute on %s", b.name)
	})
}

// TestRelayEventsPublishedCounter verifies wrkflw_relay_events_published_total is incremented.
func TestRelayEventsPublishedCounter(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 2)

		reader := sdkmetric.NewManualReader()
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayMeterProvider(mp),
		)
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 2, n)

		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &rm))

		var counter int64
		var found bool
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "wrkflw_relay_events_published_total" {
					found = true
					data, ok := m.Data.(metricdata.Sum[int64])
					require.True(t, ok)
					for _, dp := range data.DataPoints {
						counter += dp.Value
					}
				}
			}
		}
		assert.True(t, found, "wrkflw_relay_events_published_total metric on %s", b.name)
		assert.Equal(t, int64(2), counter, "counter == 2 on %s", b.name)
	})
}

// TestRelayBatchDurationHistogram verifies wrkflw_relay_batch_duration_seconds is recorded.
func TestRelayBatchDurationHistogram(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 1)

		reader := sdkmetric.NewManualReader()
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayMeterProvider(mp),
		)
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, n)

		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &rm))

		var histCount uint64
		var found bool
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "wrkflw_relay_batch_duration_seconds" {
					found = true
					data, ok := m.Data.(metricdata.Histogram[float64])
					require.True(t, ok)
					for _, dp := range data.DataPoints {
						histCount += dp.Count
					}
				}
			}
		}
		assert.True(t, found, "wrkflw_relay_batch_duration_seconds metric on %s", b.name)
		assert.GreaterOrEqual(t, histCount, uint64(1), "at least 1 histogram observation on %s", b.name)
	})
}

// TestRelayWithLogger verifies drain debug log is emitted via injected logger.
func TestRelayWithLogger(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 1)

		var buf logBuffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		relay := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayLogger(logger),
		)
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, n)
		assert.Contains(t, buf.String(), "persistence: relay drained batch", "drain log must be emitted on %s", b.name)
	})
}

// TestRelayRun_InfraErrorPropagates verifies Run returns an infra error when
// the initial drain fails (e.g. a nil/bad connection).
func TestRelayRun_InfraErrorPropagates(t *testing.T) {
	t.Run("sqlite_nil_conn", func(t *testing.T) {
		t.Parallel()
		d := newSQLiteDialectForTest()
		relay := store.NewRelay(nil, d, &recordingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)
		err := relay.Run(t.Context())
		require.Error(t, err)
		require.NotEqual(t, context.Canceled, err)
	})
}

// TestRelayListDeadLettered_NilConn verifies ListDeadLettered returns an error
// on a bad connection.
func TestRelayListDeadLettered_NilConn(t *testing.T) {
	t.Parallel()
	relay := store.NewRelay(nil, newSQLiteDialectForTest(), &recordingRelayPub{})
	_, err := relay.ListDeadLettered(t.Context(), 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "list dead-lettered")
}

// TestRelayRedrive_NilConn verifies Redrive returns an error on a bad connection.
func TestRelayRedrive_NilConn(t *testing.T) {
	t.Parallel()
	relay := store.NewRelay(nil, newSQLiteDialectForTest(), &recordingRelayPub{})
	_, err := relay.Redrive(t.Context(), 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "relay: redrive")
}

// TestRelayOutboxStats_NilConn verifies OutboxStats returns an error on a bad connection.
func TestRelayOutboxStats_NilConn(t *testing.T) {
	t.Parallel()
	relay := store.NewRelay(nil, newSQLiteDialectForTest(), &recordingRelayPub{})
	_, err := relay.OutboxStats(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "outbox stats")
}

// TestRelayBatchSize verifies WithRelayBatchSize limits rows per DrainOnce.
func TestRelayBatchSize(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 5)

		pub := &recordingRelayPub{}
		relay := store.NewRelay(b.conn, b.dialect, pub,
			store.WithRelayBatchSize(2),
		)

		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, n, "batch size limits to 2 on %s", b.name)
		assert.Equal(t, 3, countOutboxByStatus(t, b, "pending"), "3 remain on %s", b.name)
	})
}

// logBuffer is a thread-safe bytes.Buffer.
type logBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
