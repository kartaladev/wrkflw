package store_test

import (
	"testing"

	otelcodes "go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestStoreLoadCommitSpans verifies that Load and Commit emit the expected
// "wrkflw.store.load" and "wrkflw.store.commit" spans when a TracerProvider
// is injected via WithStoreTracerProvider, and that the wrkflw_store_duration_seconds
// histogram records data points for both operations.
//
// Uses SQLite (in-process, no Docker) to keep the test fast.
func TestStoreLoadCommitSpans(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	s, err := store.New(db, dialect.NewSQLite(),
		store.WithStoreTracerProvider(tp),
		store.WithStoreMeterProvider(mp),
	)
	require.NoError(t, err)

	// Create → Load → Commit cycle.
	step := appliedStep("obs-store-1", "obs.topic")
	tok, err := s.Create(t.Context(), step)
	require.NoError(t, err)

	_, _, err = s.Load(t.Context(), "obs-store-1")
	require.NoError(t, err)

	_, err = s.Commit(t.Context(), tok, appliedStep("obs-store-1", "obs.topic2"))
	require.NoError(t, err)

	ended := sr.Ended()

	var loadSpan, commitSpan sdktrace.ReadOnlySpan
	for _, sp := range ended {
		switch sp.Name() {
		case "wrkflw.store.load":
			loadSpan = sp
		case "wrkflw.store.commit":
			commitSpan = sp
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

// TestStoreLoadErrorSpan verifies that a Load of a missing instance records the
// error on the wrkflw.store.load span and sets the span status to Error.
func TestStoreLoadErrorSpan(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	s, err := store.New(db, dialect.NewSQLite(), store.WithStoreTracerProvider(tp))
	require.NoError(t, err)

	_, _, err = s.Load(t.Context(), "no-such-instance")
	require.ErrorIs(t, err, runtime.ErrInstanceNotFound)

	var loadSpan sdktrace.ReadOnlySpan
	for _, sp := range sr.Ended() {
		if sp.Name() == "wrkflw.store.load" {
			loadSpan = sp
		}
	}
	require.NotNil(t, loadSpan, "expected a wrkflw.store.load span")
	assert.Equal(t, otelcodes.Error, loadSpan.Status().Code,
		"a missing-instance Load must mark the span as Error")
}

// TestStoreCommitConcurrentUpdateNotSpanError verifies that an optimistic-CAS
// conflict on Commit is recorded as a contention attribute and does NOT mark the
// span as Error (it is expected, retryable control flow).
func TestStoreCommitConcurrentUpdateNotSpanError(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	s, err := store.New(db, dialect.NewSQLite(), store.WithStoreTracerProvider(tp))
	require.NoError(t, err)

	_, err = s.Create(t.Context(), appliedStep("obs-cas-1", "cas.topic"))
	require.NoError(t, err)

	// Commit with a stale (wrong) expected token → version mismatch.
	_, err = s.Commit(t.Context(), 999, appliedStep("obs-cas-1", "cas.topic2"))
	require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)

	var commitSpan sdktrace.ReadOnlySpan
	for _, sp := range sr.Ended() {
		if sp.Name() == "wrkflw.store.commit" {
			commitSpan = sp
		}
	}
	require.NotNil(t, commitSpan)
	assert.NotEqual(t, otelcodes.Error, commitSpan.Status().Code,
		"a concurrent-update conflict must NOT mark the span as Error")

	var sawContention bool
	for _, attr := range commitSpan.Attributes() {
		if string(attr.Key) == "wrkflw.concurrent_update" && attr.Value.AsBool() {
			sawContention = true
		}
	}
	assert.True(t, sawContention, "expected wrkflw.concurrent_update=true attribute on the span")
}

// TestStoreNoOptionsNoPanic verifies that a Store built with no observability
// options (noop tracer/meter) still executes Load and Commit without panicking.
func TestStoreNoOptionsNoPanic(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	s, err := store.New(db, dialect.NewSQLite())
	require.NoError(t, err)

	step := appliedStep("obs-noop-1", "noop.topic")
	tok, err := s.Create(t.Context(), step)
	require.NoError(t, err)

	_, _, err = s.Load(t.Context(), "obs-noop-1")
	require.NoError(t, err)

	_, err = s.Commit(t.Context(), tok, appliedStep("obs-noop-1", "noop.topic2"))
	require.NoError(t, err)
}
