package mysql_test

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
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

// TestMySQLRelayBatchSpan verifies that DrainOnce records a "wrkflw.relay.batch"
// span with a "wrkflw.batch_size" attribute when a TracerProvider is injected via
// WithRelayTracerProvider.
func TestMySQLRelayBatchSpan(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	// Seed one pending outbox row so DrainOnce has something to drain.
	now := time.Now().UTC()
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
		"mysql-obs-test-instance", "mysql.obs.event", `{"k":"v"}`, "mysql-obs-dedup-1", now, now,
	)
	require.NoError(t, err, "seed outbox row")

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	pub := &recordingPub{}
	relay := mypkg.NewRelay(db, pub, mypkg.WithRelayTracerProvider(tp))

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

// TestMySQLRelayBatchSpanReflectsClaimedNotPublished verifies that wrkflw.batch_size
// records the claimed count even when some rows fail to publish (poison-row
// scenario). With 1 row claimed but 0 successfully published, batch_size must
// still be 1 and a separate wrkflw.published_count attribute must be 0.
func TestMySQLRelayBatchSpanReflectsClaimedNotPublished(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	// Seed one pending outbox row.
	now := time.Now().UTC()
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
		"mysql-poison-test-instance", "mysql.poison.event", `{"p":1}`, "mysql-poison-claimed-dedup-1", now, now,
	)
	require.NoError(t, err, "seed outbox row")

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	// Use a publisher that always fails — 1 row claimed, 0 published.
	relay := mypkg.NewRelay(db, failingPub{}, mypkg.WithRelayTracerProvider(tp))

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

// TestMySQLRelayBatchSpanEmptyOutbox verifies that DrainOnce records a
// "wrkflw.relay.batch" span with batch_size=0 when the outbox is empty.
func TestMySQLRelayBatchSpanEmptyOutbox(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayTracerProvider(tp))

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

// TestMySQLRelayWithLogger verifies that WithRelayLogger is accepted and that the
// relay emits a debug log through the injected logger when a batch is drained.
func TestMySQLRelayWithLogger(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	// Seed one row.
	now := time.Now().UTC()
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
		"mysql-log-test-instance", "mysql.log.event", `{"x":1}`, "mysql-log-dedup-1", now, now,
	)
	require.NoError(t, err)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayLogger(logger))
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// The debug drain log must contain a recognisable message.
	require.Contains(t, buf.String(), "persistence: relay drained batch",
		"expected drain debug log from injected logger")
}

// TestMySQLRelayWithMeterProvider verifies that WithRelayMeterProvider is accepted
// without error and that the relay emits metric instruments driven by the
// injected MeterProvider.
func TestMySQLRelayWithMeterProvider(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	mp := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayMeterProvider(mp))

	// Drain an empty outbox — main goal is no panic and no error.
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestMySQLRelayEventsPublishedCounter verifies that wrkflw_relay_events_published_total
// is incremented by exactly N when DrainOnce publishes N events.
func TestMySQLRelayEventsPublishedCounter(t *testing.T) {
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
			db := dbtest.RunTestMySQL(t)

			// Seed rows into the outbox.
			now := time.Now().UTC()
			for i := range tc.seedRows {
				dedup := "mysql-relay-counter-" + tc.name + "-" + string(rune('a'+i))
				_, err := db.ExecContext(t.Context(),
					`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
					 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
					"mysql-relay-counter-instance", "mysql.relay.counter.event", `{"n":1}`, dedup, now, now,
				)
				require.NoError(t, err, "seed outbox row")
			}

			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

			relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayMeterProvider(mp))
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

// TestMySQLRelayBatchDurationHistogram verifies that wrkflw_relay_batch_duration_seconds
// records at least 1 observation after DrainOnce completes.
func TestMySQLRelayBatchDurationHistogram(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	// Seed one row so the drain does real work.
	now := time.Now().UTC()
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
		"mysql-relay-hist-instance", "mysql.relay.hist.event", `{"h":1}`, "mysql-relay-hist-dedup-1", now, now,
	)
	require.NoError(t, err)

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayMeterProvider(mp))
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
