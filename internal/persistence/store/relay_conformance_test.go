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
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
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
	s, err := store.New(b.conn, b.dialect)
	if err != nil {
		panic("relayTimeArg: store.New: " + err.Error())
	}
	return store.TimeArgForDialect(s, t)
}

// seedRelayOutbox inserts n pending outbox rows using the store's generic querier.
// next_attempt_at is set to now so the relay claims them immediately.
func seedRelayOutbox(t *testing.T, b backend, n int) {
	t.Helper()
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
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
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())
	_, err = q.Exec(t.Context(), b.dialect.Rebind(
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
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())
	_, err = q.Exec(t.Context(), b.dialect.Rebind(
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
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())
	_, err = q.Exec(t.Context(), b.dialect.Rebind(
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
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())
	var id int64
	err = q.QueryRow(t.Context(), b.dialect.Rebind(
		`SELECT id FROM wrkflw_outbox WHERE dedup_key = ?`), dedup,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// outboxStatusAndRetry reads the status and retry_count of the single outbox row with given dedup.
func outboxStatusAndRetry(t *testing.T, b backend, dedup string) (status string, retry int) {
	t.Helper()
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())
	err = q.QueryRow(t.Context(), b.dialect.Rebind(
		`SELECT status, retry_count FROM wrkflw_outbox WHERE dedup_key = ?`), dedup,
	).Scan(&status, &retry)
	require.NoError(t, err)
	return status, retry
}

// countOutboxByStatus returns the count of rows with the given status.
func countOutboxByStatus(t *testing.T, b backend, status string) int {
	t.Helper()
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())
	var n int
	err = q.QueryRow(t.Context(), b.dialect.Rebind(
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE status = ?`), status,
	).Scan(&n)
	require.NoError(t, err)
	return n
}

// outboxStatusByID reads the status of the row with the given id.
func outboxStatusByID(t *testing.T, b backend, id int64) string {
	t.Helper()
	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())
	var status string
	err = q.QueryRow(t.Context(), b.dialect.Rebind(
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
		relay, err := store.NewRelay(b.conn, b.dialect, pub)
		require.NoError(t, err)

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
		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{})
		require.NoError(t, err)
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

		relay, err := store.NewRelay(b.conn, b.dialect, failingRelayPub{})
		require.NoError(t, err)
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
		relay, err := store.NewRelay(b.conn, b.dialect, pub,
			store.WithRelayClock(fc),
			store.WithRelayMaxDeliveryAttempts(maxDelivery),
			store.WithRelayBackoff(time.Second, 30*time.Second),
		)
		require.NoError(t, err)

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

		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{})
		require.NoError(t, err)
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

		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayClock(fc),
		)
		require.NoError(t, err)

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
		relay2, err := store.NewRelay(b.conn, b.dialect, pub, store.WithRelayClock(fc))
		require.NoError(t, err)
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

		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{})
		require.NoError(t, err)
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

		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)
		require.NoError(t, err)

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
		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancel()

		err = relay.Run(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded, "Run must return DeadlineExceeded on %s", b.name)
		assert.Equal(t, context.DeadlineExceeded, err, "must be ctx.Err() exactly on %s", b.name)
	})
}

// TestRelayRun_AbsorbsPublishFailures verifies Run keeps polling after publish errors.
func TestRelayRun_AbsorbsPublishFailures(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 1)

		relay, err := store.NewRelay(b.conn, b.dialect, failingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)
		require.NoError(t, err)

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
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)
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
		relay, err := store.NewRelay(b.conn, b.dialect, pub,
			store.WithRelayTracerProvider(tp),
		)
		require.NoError(t, err)

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

		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayTracerProvider(tp),
		)
		require.NoError(t, err)
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

		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayMeterProvider(mp),
		)
		require.NoError(t, err)
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

		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayMeterProvider(mp),
		)
		require.NoError(t, err)
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
		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{},
			store.WithRelayLogger(logger),
		)
		require.NoError(t, err)
		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, n)
		assert.Contains(t, buf.String(), "persistence: relay drained batch", "drain log must be emitted on %s", b.name)
	})
}

// TestRelayDrainOnce_NilConn verifies that NewRelay returns ErrNilDependency
// when the connection is nil. The nil guard is enforced at construction time.
func TestRelayDrainOnce_NilConn(t *testing.T) {
	t.Parallel()
	_, err := store.NewRelay(nil, newSQLiteDialectForTest(), &recordingRelayPub{})
	require.Error(t, err)
	require.ErrorIs(t, err, store.ErrNilDependency)
}

// TestRelayDrainOnce_ClaimQueryFails verifies that a claim-query failure
// (cancelled context after tx begin) is reported as an infra error from
// DrainOnce and does NOT panic or leak. Only exercised on SupportsSkipLocked
// backends; the SQLite (non-SkipLocked) DrainOnce infra-error path is covered
// separately by TestRelayDrainOnce_SQLiteInfraError below.
func TestRelayDrainOnce_ClaimQueryFails(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		if !b.dialect.SupportsSkipLocked() {
			t.Skipf("skip SQLite — non-SkipLocked path covered by TestRelayDrainOnce_SQLiteInfraError")
		}
		relay, err := store.NewRelay(b.conn, b.dialect, &recordingRelayPub{})
		require.NoError(t, err)

		// A pre-cancelled context causes the claim query to fail immediately
		// after transaction.Begin succeeds (begin uses a background context under
		// the hood in pgx/sql; the query honours the caller's context).
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // cancel before DrainOnce

		_, err = relay.DrainOnce(ctx)
		require.Error(t, err, "DrainOnce must propagate query error on %s", b.name)
	})
}

// TestRelayDrainOnce_SQLiteInfraError exercises the SQLite (non-SkipLocked)
// DrainOnce claim-path infra-error branch on the in-process backend: with the
// outbox table dropped mid-flight, DrainOnce must surface the driver error (not
// panic or leak) and publish nothing. This restores the runtime error-path
// coverage that the removed nil-conn probe used to provide (dropped-table style,
// cf. TestStoreWriteErrors).
func TestRelayDrainOnce_SQLiteInfraError(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	b := backend{name: "sqlite", conn: db, dialect: dialect.NewSQLite()}
	seedRelayOutbox(t, b, 1)

	_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_outbox")
	require.NoError(t, err, "drop wrkflw_outbox")

	pub := &recordingRelayPub{}
	relay, err := store.NewRelay(b.conn, b.dialect, pub)
	require.NoError(t, err)

	_, err = relay.DrainOnce(t.Context())
	require.Error(t, err, "DrainOnce must surface the dropped-table claim-query error")
	require.Empty(t, pub.topics, "nothing must be published when the claim query fails")
}

// TestRelayRun_InfraErrorPropagates verifies NewRelay returns ErrNilDependency
// when the connection is nil (nil guard enforced at construction time).
func TestRelayRun_InfraErrorPropagates(t *testing.T) {
	t.Run("sqlite_nil_conn", func(t *testing.T) {
		t.Parallel()
		d := newSQLiteDialectForTest()
		_, err := store.NewRelay(nil, d, &recordingRelayPub{},
			store.WithRelayPollInterval(10*time.Millisecond),
		)
		require.Error(t, err)
		require.ErrorIs(t, err, store.ErrNilDependency)
	})
}

// TestRelayListDeadLettered_NilConn verifies NewRelay returns ErrNilDependency
// when the connection is nil (nil guard at construction time).
func TestRelayListDeadLettered_NilConn(t *testing.T) {
	t.Parallel()
	_, err := store.NewRelay(nil, newSQLiteDialectForTest(), &recordingRelayPub{})
	require.Error(t, err)
	require.ErrorIs(t, err, store.ErrNilDependency)
}

// TestRelayRedrive_NilConn verifies NewRelay returns ErrNilDependency
// when the connection is nil (nil guard at construction time).
func TestRelayRedrive_NilConn(t *testing.T) {
	t.Parallel()
	_, err := store.NewRelay(nil, newSQLiteDialectForTest(), &recordingRelayPub{})
	require.Error(t, err)
	require.ErrorIs(t, err, store.ErrNilDependency)
}

// TestRelayOutboxStats_NilConn verifies NewRelay returns ErrNilDependency
// when the connection is nil (nil guard at construction time).
func TestRelayOutboxStats_NilConn(t *testing.T) {
	t.Parallel()
	_, err := store.NewRelay(nil, newSQLiteDialectForTest(), &recordingRelayPub{})
	require.Error(t, err)
	require.ErrorIs(t, err, store.ErrNilDependency)
}

// TestRelayBatchSize verifies WithRelayBatchSize limits rows per DrainOnce.
func TestRelayBatchSize(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		seedRelayOutbox(t, b, 5)

		pub := &recordingRelayPub{}
		relay, err := store.NewRelay(b.conn, b.dialect, pub,
			store.WithRelayBatchSize(2),
		)
		require.NoError(t, err)

		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, n, "batch size limits to 2 on %s", b.name)
		assert.Equal(t, 3, countOutboxByStatus(t, b, "pending"), "3 remain on %s", b.name)
	})
}

// ── Concurrency: no double-publish ───────────────────────────────────────────

// blockingRelayPub records every Publish call (thread-safe) and stalls
// briefly after acquiring the lock — widening the window in which a second
// concurrent Relay replica can re-claim the same still-pending rows and
// publish them again (exposes the two-tx bug pre-fix).
type blockingRelayPub struct {
	mu        sync.Mutex
	delay     time.Duration
	published []runtime.OutboxEvent
}

func newBlockingRelayPub(delay time.Duration) *blockingRelayPub {
	return &blockingRelayPub{delay: delay}
}

func (p *blockingRelayPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	// Hold the lock for `delay` so the race window is wide enough for a
	// concurrent DrainOnce to re-claim and re-publish the same rows.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	p.published = append(p.published, ev)
	return nil
}

func (p *blockingRelayPub) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.published)
}

// dedupCounts returns a map[dedupKey]→publishCount so the test can verify
// each event was published exactly once.
func (p *blockingRelayPub) dedupCounts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := make(map[string]int, len(p.published))
	for _, e := range p.published {
		m[e.DedupKey]++
	}
	return m
}

// TestRelayDrainOnce_NoConcurrentDoublePublish is the gate test for the
// single-tx drain fix.
//
// Two Relay instances share the same connection pool and the same
// blockingRelayPub (which stalls each Publish call for 20 ms to widen the
// window). With the pre-fix two-transaction structure, the second DrainOnce
// re-claims the same pending rows after the first committed its claim tx but
// before it committed the publish tx → double-publish. After the fix (one tx
// holds SELECT…FOR UPDATE SKIP LOCKED until commit), SKIP LOCKED correctly
// skips already-locked rows so each dedup_key is published exactly once and
// the total equals N.
//
// SQLite is excluded: it is single-writer (no concurrent Relay replicas
// expected) and SupportsSkipLocked()==false.
func TestRelayDrainOnce_NoConcurrentDoublePublish(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		if !b.dialect.SupportsSkipLocked() {
			t.Skipf("SKIP LOCKED not supported on %s — single-writer, concurrent relay not expected", b.name)
		}

		const N = 20
		seedRelayOutbox(t, b, N)

		// A single shared publisher that stalls 20 ms per Publish call so the
		// two goroutines' Publish phases overlap, maximising double-publish odds.
		pub := newBlockingRelayPub(20 * time.Millisecond)

		relay1, err := store.NewRelay(b.conn, b.dialect, pub, store.WithRelayBatchSize(N))
		require.NoError(t, err)
		relay2, err := store.NewRelay(b.conn, b.dialect, pub, store.WithRelayBatchSize(N))
		require.NoError(t, err)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = relay1.DrainOnce(t.Context()) }()
		go func() { defer wg.Done(); _, _ = relay2.DrainOnce(t.Context()) }()
		wg.Wait()

		counts := pub.dedupCounts()
		total := pub.count()

		// Every event published exactly once; total equals N.
		assert.Equal(t, N, total, "total published events must equal N on %s (got %d, want %d)", b.name, total, N)
		for key, n := range counts {
			assert.Equal(t, 1, n, "dedup_key %s published %d times (want 1) on %s", key, n, b.name)
		}
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
