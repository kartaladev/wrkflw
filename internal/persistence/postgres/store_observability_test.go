package postgres_test

import (
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestStoreLoadCommitSpans verifies that Load and Commit emit the expected
// "wrkflw.store.load" and "wrkflw.store.commit" spans when a TracerProvider
// is injected via WithStoreTracerProvider.
func TestStoreLoadCommitSpans(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	store := pg.NewStore(pool,
		pg.WithStoreTracerProvider(tp),
		pg.WithStoreMeterProvider(mp),
	)

	// Create → Load → Commit cycle.
	step := appliedStep("obs-store-1", "obs.topic")
	tok, err := store.Create(t.Context(), step)
	require.NoError(t, err)

	_, _, err = store.Load(t.Context(), "obs-store-1")
	require.NoError(t, err)

	_, err = store.Commit(t.Context(), tok, appliedStep("obs-store-1", "obs.topic2"))
	require.NoError(t, err)

	ended := sr.Ended()

	var loadSpan, commitSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		switch s.Name() {
		case "wrkflw.store.load":
			loadSpan = s
		case "wrkflw.store.commit":
			commitSpan = s
		}
	}
	require.NotNil(t, loadSpan, "expected a wrkflw.store.load span")
	require.NotNil(t, commitSpan, "expected a wrkflw.store.commit span")

	// Verify the duration histogram has data points for both ops.
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))

	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "wrkflw_store_duration_seconds" {
				continue
			}
			found = true
			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok, "wrkflw_store_duration_seconds must be a Float64Histogram")
			require.NotEmpty(t, hist.DataPoints, "histogram must have data points")

			// Collect the op attribute values seen.
			ops := make(map[string]bool)
			for _, dp := range hist.DataPoints {
				for _, attr := range dp.Attributes.ToSlice() {
					if string(attr.Key) == "op" {
						ops[attr.Value.AsString()] = true
					}
				}
			}
			assert.True(t, ops["load"], "expected op=load in histogram data points")
			assert.True(t, ops["commit"], "expected op=commit in histogram data points")
		}
	}
	require.True(t, found, "wrkflw_store_duration_seconds metric must be present")
}

// TestStoreNoOptionsNoPanic verifies that a Store built with no options (noop
// tracer/meter) still executes Load and Commit without panicking.
func TestStoreNoOptionsNoPanic(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	store := pg.NewStore(pool)

	step := appliedStep("obs-noop-1", "noop.topic")
	tok, err := store.Create(t.Context(), step)
	require.NoError(t, err)

	_, _, err = store.Load(t.Context(), "obs-noop-1")
	require.NoError(t, err)

	_, err = store.Commit(t.Context(), tok, appliedStep("obs-noop-1", "noop.topic2"))
	require.NoError(t, err)
}
