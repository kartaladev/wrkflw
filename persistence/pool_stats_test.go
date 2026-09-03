package persistence_test

// pool_stats_test.go covers persistence.NewPoolStatsCollector and
// persistence.NewPostgresPoolStatsCollector: DB pool saturation was invisible —
// no db.Stats()/pool.Stat() call site existed anywhere in the module.
//
// The *sql.DB variant is container-free (dbtest.RunTestSQLite is pure Go); the
// pgxpool variant uses the shared dbtest.RunTestDatabase helper.

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
)

// poolGauge returns the int64 gauge value for the named metric in rm.
func poolGauge(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			g, ok := m.Data.(metricdata.Gauge[int64])
			require.True(t, ok, "%s must be an int64 gauge, got %T", name, m.Data)
			require.NotEmpty(t, g.DataPoints, "%s must carry a datapoint", name)
			return g.DataPoints[0].Value
		}
	}
	t.Fatalf("metric %q was not collected", name)
	return 0
}

// poolCounter returns the int64 sum value for the named metric in rm.
func poolCounter(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			s, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "%s must be an int64 sum, got %T", name, m.Data)
			require.NotEmpty(t, s.DataPoints, "%s must carry a datapoint", name)
			return s.DataPoints[0].Value
		}
	}
	t.Fatalf("metric %q was not collected", name)
	return 0
}

// TestPoolStatsCollectorObservesPoolState verifies that NewPoolStatsCollector
// registers the four pool instruments over a *sql.DB and that each collection
// cycle reports the pool's CURRENT state — a held connection shows as in_use,
// and releasing it moves the same connection to idle.
//
// Every assertion pins an exact value: a collector that registered the
// instruments but observed nothing would still surface the names in some
// readers, so presence alone proves nothing.
func TestPoolStatsCollectorObservesPoolState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// dbtest.RunTestSQLite sets SetMaxOpenConns(1), which makes every assertion
	// below exact rather than "at least".
	db := dbtest.RunTestSQLite(t)

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	c := persistence.NewPoolStatsCollector(db, persistence.WithPoolStatsMeterProvider(mp))
	require.NotNil(t, c)

	// Hold the pool's only connection.
	conn, err := db.Conn(ctx)
	require.NoError(t, err)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))

	assert.Equal(t, int64(1), poolGauge(t, rm, "wrkflw_db_pool_in_use"),
		"the held connection must be reported as in use")
	assert.Equal(t, int64(0), poolGauge(t, rm, "wrkflw_db_pool_idle"),
		"the only connection is held, so none is idle")
	assert.Equal(t, int64(1), poolGauge(t, rm, "wrkflw_db_pool_max_open"),
		"max_open is the saturation denominator; RunTestSQLite pins it to 1")

	// Force a real pool wait: the only connection is held, so this acquire
	// blocks and database/sql increments WaitCount before it gives up.
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, waitErr := db.Conn(waitCtx)
	cancel()
	require.Error(t, waitErr, "control: the second acquire must have had to wait and time out")

	require.NoError(t, reader.Collect(ctx, &rm))
	assert.GreaterOrEqual(t, poolCounter(t, rm, "wrkflw_db_pool_waits_total"), int64(1),
		"an acquire that blocked on an exhausted pool must be counted")

	// Release: the same connection must move from in_use to idle.
	require.NoError(t, conn.Close())

	require.NoError(t, reader.Collect(ctx, &rm))
	assert.Equal(t, int64(0), poolGauge(t, rm, "wrkflw_db_pool_in_use"),
		"after Close the connection must no longer be in use")
	assert.Equal(t, int64(1), poolGauge(t, rm, "wrkflw_db_pool_idle"),
		"after Close the connection must be returned to the idle set")
}

// TestPoolStatsCollectorToleratesNilOptions covers the defensive branches in the
// option plumbing: a nil PoolStatsOption in the variadic, and nil provider/logger
// values inside otherwise-valid options. All three must be skipped rather than
// dereferenced, and the collector must still observe correctly afterwards —
// asserting only "does not panic" would pass for a collector that silently
// registered nothing.
func TestPoolStatsCollectorToleratesNilOptions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db := dbtest.RunTestSQLite(t)

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	c := persistence.NewPoolStatsCollector(db,
		nil,                                  // a nil option in the variadic
		persistence.WithPoolStatsLogger(nil), // nil logger: keep the default
		persistence.WithPoolStatsMeterProvider(nil), // nil provider: keep the default
		persistence.WithPoolStatsLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		persistence.WithPoolStatsMeterProvider(mp),
	)
	require.NotNil(t, c)

	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	assert.Equal(t, int64(1), poolGauge(t, rm, "wrkflw_db_pool_in_use"),
		"the collector must still be wired to the last non-nil MeterProvider")
}

// TestPostgresPoolStatsCollectorObservesPoolState is the pgxpool analogue. It
// needs a real Postgres (dbtest.RunTestDatabase); the *sql.DB test above is the
// container-free gate.
func TestPostgresPoolStatsCollectorObservesPoolState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	pool := dbtest.RunTestDatabase(t)

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	c := persistence.NewPostgresPoolStatsCollector(pool, persistence.WithPoolStatsMeterProvider(mp))
	require.NotNil(t, c)

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))

	assert.Equal(t, int64(1), poolGauge(t, rm, "wrkflw_db_pool_in_use"),
		"the acquired connection must be reported as in use")
	assert.Positive(t, poolGauge(t, rm, "wrkflw_db_pool_max_open"),
		"max_open must report the pool's configured ceiling")

	conn.Release()

	require.NoError(t, reader.Collect(ctx, &rm))
	assert.Equal(t, int64(0), poolGauge(t, rm, "wrkflw_db_pool_in_use"),
		"after Release the connection must no longer be in use")
	assert.Positive(t, poolGauge(t, rm, "wrkflw_db_pool_idle"),
		"after Release the connection must be returned to the idle set")
}
