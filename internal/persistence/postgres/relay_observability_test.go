package postgres_test

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestRelayBatchSpan verifies that DrainOnce records a "wrkflw.relay.batch" span
// with a "wrkflw.batch_size" attribute when a TracerProvider is injected via
// WithRelayTracerProvider.
func TestRelayBatchSpan(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed one pending outbox row so DrainOnce has something to drain.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)`,
		"obs-test-instance", "obs.event", `{"k":"v"}`, "obs-dedup-1", time.Now().UTC(),
	)
	require.NoError(t, err, "seed outbox row")

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	pub := &recordingPub{}
	relay := pg.NewRelay(pool, pub, pg.WithRelayTracerProvider(tp))

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n, "one row published")

	// Flush the span processor (SpanRecorder synchronously records on End).
	ended := sr.Ended()

	var batchSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.Name() == "wrkflw.relay.batch" {
			batchSpan = s
			break
		}
	}
	require.NotNil(t, batchSpan, "expected a wrkflw.relay.batch span to be recorded")

	// Verify the batch_size attribute is present and correct.
	var batchSizeFound bool
	for _, attr := range batchSpan.Attributes() {
		if string(attr.Key) == "wrkflw.batch_size" {
			batchSizeFound = true
			require.Equal(t, int64(1), attr.Value.AsInt64(), "batch_size must equal the number of rows claimed")
		}
	}
	require.True(t, batchSizeFound, "expected wrkflw.batch_size attribute on the batch span")
}

// TestRelayBatchSpanReflectsClaimedNotPublished verifies that wrkflw.batch_size
// records the claimed count even when some rows fail to publish (poison-row
// scenario). With 1 row claimed but 0 successfully published, batch_size must
// still be 1 and a separate wrkflw.published_count attribute must be 0.
func TestRelayBatchSpanReflectsClaimedNotPublished(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed one pending outbox row.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)`,
		"poison-test-instance", "poison.event", `{"p":1}`, "poison-claimed-dedup-1", time.Now().UTC(),
	)
	require.NoError(t, err, "seed outbox row")

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	// Use a publisher that always fails — 1 row claimed, 0 published.
	relay := pg.NewRelay(pool, failingPub{}, pg.WithRelayTracerProvider(tp))

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err, "DrainOnce must not propagate publish failures")
	require.Equal(t, 0, n, "zero rows successfully published")

	ended := sr.Ended()
	var batchSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.Name() == "wrkflw.relay.batch" {
			batchSpan = s
			break
		}
	}
	require.NotNil(t, batchSpan, "expected a wrkflw.relay.batch span")

	var batchSizeVal int64 = -1
	var publishedCountVal int64 = -1
	for _, attr := range batchSpan.Attributes() {
		switch string(attr.Key) {
		case "wrkflw.batch_size":
			batchSizeVal = attr.Value.AsInt64()
		case "wrkflw.published_count":
			publishedCountVal = attr.Value.AsInt64()
		}
	}
	require.Equal(t, int64(1), batchSizeVal,
		"batch_size must equal the number of rows claimed (1), not published (0)")
	require.Equal(t, int64(0), publishedCountVal,
		"published_count must equal the number of rows successfully published (0)")
}

// TestRelayBatchSpanEmptyOutbox verifies that DrainOnce records a
// "wrkflw.relay.batch" span with batch_size=0 when the outbox is empty.
func TestRelayBatchSpanEmptyOutbox(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithRelayTracerProvider(tp))

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)

	ended := sr.Ended()
	var batchSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.Name() == "wrkflw.relay.batch" {
			batchSpan = s
			break
		}
	}
	require.NotNil(t, batchSpan, "batch span must be emitted even on empty drain")

	var batchSizeFound bool
	for _, attr := range batchSpan.Attributes() {
		if string(attr.Key) == "wrkflw.batch_size" {
			batchSizeFound = true
			require.Equal(t, int64(0), attr.Value.AsInt64(), "batch_size=0 for empty drain")
		}
	}
	require.True(t, batchSizeFound, "expected wrkflw.batch_size=0 attribute on empty-drain span")
}

// TestRelayWithLogger verifies that WithRelayLogger is accepted and that the
// relay emits a debug log through the injected logger when a batch is drained.
func TestRelayWithLogger(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed one row.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)`,
		"log-test-instance", "log.event", `{"x":1}`, "log-dedup-1", time.Now().UTC(),
	)
	require.NoError(t, err)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithRelayLogger(logger))
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// The debug drain log must contain a recognisable message.
	require.Contains(t, buf.String(), "persistence: relay drained batch",
		"expected drain debug log from injected logger")
}

// TestRelayWithMeterProvider verifies that WithRelayMeterProvider is accepted
// without error and that the relay emits metric instruments driven by the
// injected MeterProvider.
func TestRelayWithMeterProvider(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	mp := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithRelayMeterProvider(mp))

	// Drain an empty outbox — main goal is no panic and no error.
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestRelayDrainOnceBeginTxErrorSpan verifies that a pool.Begin failure (triggered
// by closing the pool) causes DrainOnce to record an error span with status Error
// and return a wrapped error.
func TestRelayDrainOnceBeginTxErrorSpan(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	// Close the pool before DrainOnce so pool.Begin will fail.
	pool.Close()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithRelayTracerProvider(tp))

	_, err := relay.DrainOnce(t.Context())
	require.Error(t, err, "DrainOnce on a closed pool must return an error")
	require.Contains(t, err.Error(), "begin tx", "error must reference the begin-tx step")

	ended := sr.Ended()
	var batchSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.Name() == "wrkflw.relay.batch" {
			batchSpan = s
			break
		}
	}
	require.NotNil(t, batchSpan, "batch span must be emitted even on infra error")
	require.Equal(t, "Error", batchSpan.Status().Code.String(),
		"batch span must carry Error status on infra failure")
}

// TestRelayDrainOnceBeginTxErrorLog verifies that the injected logger receives
// an error-level record when pool.Begin fails.
func TestRelayDrainOnceBeginTxErrorLog(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	pool.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithRelayLogger(logger))
	_, err := relay.DrainOnce(t.Context())
	require.Error(t, err)

	require.Contains(t, buf.String(), "persistence: relay begin tx failed",
		"expected begin-tx error log from injected logger")
}

// TestRelayRunInfraErrorPropagates verifies that Run propagates an infrastructure
// error (pool closed → pool.Begin fails on the initial DrainOnce) and does NOT
// silently swallow it.
func TestRelayRunInfraErrorPropagates(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	pool.Close()

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithPollInterval(10*time.Millisecond))
	err := relay.Run(t.Context())
	require.Error(t, err, "Run must propagate an infra error from DrainOnce")
	require.Contains(t, err.Error(), "begin tx",
		"propagated error must reference the begin-tx failure")
}

// TestRelayListDeadLetteredClosedPoolError verifies that ListDeadLettered
// propagates a pool-query error (triggered by closing the pool).
func TestRelayListDeadLetteredClosedPoolError(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	pool.Close()

	relay := pg.NewRelay(pool, &recordingPub{})
	_, err := relay.ListDeadLettered(t.Context(), 10)
	require.Error(t, err, "ListDeadLettered on a closed pool must return an error")
	require.Contains(t, err.Error(), "list dead-lettered",
		"error must reference the list operation")
}

// TestRelayRedriveClosedPoolError verifies that Redrive propagates a pool-exec
// error (triggered by closing the pool).
func TestRelayRedriveClosedPoolError(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	pool.Close()

	relay := pg.NewRelay(pool, &recordingPub{})
	_, err := relay.Redrive(t.Context(), 1)
	require.Error(t, err, "Redrive on a closed pool must return an error")
	require.Contains(t, err.Error(), "relay: redrive",
		"error must reference the redrive operation")
}

// TestRelayEventsPublishedCounter verifies that wrkflw_relay_events_published_total
// is incremented by exactly N when DrainOnce publishes N events.
func TestRelayEventsPublishedCounter(t *testing.T) {
	type testCase struct {
		name        string
		seedRows    int
		wantCounter int64
	}

	cases := []testCase{
		{name: "empty outbox — counter stays 0", seedRows: 0, wantCounter: 0},
		{name: "1 event published", seedRows: 1, wantCounter: 1},
		{name: "3 events published", seedRows: 3, wantCounter: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := dbtest.RunTestDatabase(t)
			require.NoError(t, pg.Migrate(t.Context(), pool))

			// Seed rows into the outbox.
			for i := range tc.seedRows {
				dedup := "relay-counter-" + tc.name + "-" + string(rune('a'+i))
				_, err := pool.Exec(t.Context(),
					`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
					 VALUES ($1, $2, $3::jsonb, $4, $5)`,
					"relay-counter-instance", "relay.counter.event", `{"n":1}`, dedup, time.Now().UTC(),
				)
				require.NoError(t, err, "seed outbox row")
			}

			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

			relay := pg.NewRelay(pool, &recordingPub{}, pg.WithRelayMeterProvider(mp))
			n, err := relay.DrainOnce(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.seedRows, n, "DrainOnce must return the seeded row count")

			// Collect metrics.
			var rm metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(t.Context(), &rm))

			var counterSum int64
			var found bool
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name == "wrkflw_relay_events_published_total" {
						found = true
						data, ok := m.Data.(metricdata.Sum[int64])
						require.True(t, ok, "expected Sum[int64] data type for counter")
						for _, dp := range data.DataPoints {
							counterSum += dp.Value
						}
					}
				}
			}
			if tc.wantCounter > 0 {
				require.True(t, found, "expected wrkflw_relay_events_published_total metric")
				assert.Equal(t, tc.wantCounter, counterSum,
					"counter must equal number of events published in the batch")
			}
		})
	}
}

// TestRelayBatchDurationHistogram verifies that wrkflw_relay_batch_duration_seconds
// records at least 1 observation after DrainOnce completes.
func TestRelayBatchDurationHistogram(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Seed one row so the drain does real work.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)`,
		"relay-hist-instance", "relay.hist.event", `{"h":1}`, "relay-hist-dedup-1", time.Now().UTC(),
	)
	require.NoError(t, err)

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	relay := pg.NewRelay(pool, &recordingPub{}, pg.WithRelayMeterProvider(mp))
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))

	var histCount uint64
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "wrkflw_relay_batch_duration_seconds" {
				found = true
				data, ok := m.Data.(metricdata.Histogram[float64])
				require.True(t, ok, "expected Histogram[float64] data type")
				for _, dp := range data.DataPoints {
					histCount += dp.Count
				}
			}
		}
	}
	require.True(t, found, "expected wrkflw_relay_batch_duration_seconds metric")
	assert.GreaterOrEqual(t, histCount, uint64(1), "histogram must have at least 1 observation")
}
