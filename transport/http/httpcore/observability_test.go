package httpcore_test

import (
	"context"
	"net/http"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// TestNewInstrumentation_StaticTemplateMetric verifies that Observe records
// wrkflw_rest_requests_total with the static routeTemplate passed in — never
// "unmatched" — and that the http.status_code attribute matches the status
// returned by the run func.
func TestNewInstrumentation_StaticTemplateMetric(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	cfg := httpcore.ResolveConfig(httpcore.WithMeterProvider[int](mp))
	inst := httpcore.NewInstrumentation(cfg)

	inst.Observe(t.Context(), "POST", "/instances/{id}", http.Header{}, func(_ context.Context) int {
		return 201
	})

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))

	var counterFound bool
	var matchCount int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "wrkflw_rest_requests_total" {
				continue
			}
			counterFound = true
			data, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "wrkflw_rest_requests_total must be Sum[int64]")
			for _, dp := range data.DataPoints {
				var gotRoute, gotStatus string
				for _, attr := range dp.Attributes.ToSlice() {
					switch string(attr.Key) {
					case "http.route":
						gotRoute = attr.Value.AsString()
					case "http.status_code":
						gotStatus = attr.Value.AsString()
					}
				}
				if gotRoute == "/instances/{id}" && gotStatus == "201" {
					matchCount += dp.Value
				}
			}
		}
	}
	require.True(t, counterFound, "wrkflw_rest_requests_total metric must be present")
	assert.Equal(t, int64(1), matchCount,
		"counter must be recorded with http.route=/instances/{id} and http.status_code=201")
}

// TestNewInstrumentation_DurationHistogram verifies that
// wrkflw_rest_request_duration_seconds is recorded with the static template.
func TestNewInstrumentation_DurationHistogram(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	cfg := httpcore.ResolveConfig(httpcore.WithMeterProvider[int](mp))
	inst := httpcore.NewInstrumentation(cfg)

	inst.Observe(t.Context(), "GET", "/instances/{id}/snapshot", http.Header{}, func(_ context.Context) int {
		return 200
	})

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))

	var histCount uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "wrkflw_rest_request_duration_seconds" {
				continue
			}
			data, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok, "wrkflw_rest_request_duration_seconds must be Histogram[float64]")
			for _, dp := range data.DataPoints {
				for _, attr := range dp.Attributes.ToSlice() {
					if string(attr.Key) == "http.route" && attr.Value.AsString() == "/instances/{id}/snapshot" {
						histCount += dp.Count
					}
				}
			}
		}
	}
	assert.GreaterOrEqual(t, histCount, uint64(1),
		"histogram must have at least one observation with route template label")
}

// TestNewInstrumentation_SpanNaming verifies that Observe produces a span named
// "wrkflw.rest <METHOD> <routeTemplate>" and sets the http.route attribute.
func TestNewInstrumentation_SpanNaming(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	cfg := httpcore.ResolveConfig(httpcore.WithTracerProvider[int](tp))
	inst := httpcore.NewInstrumentation(cfg)

	inst.Observe(t.Context(), "DELETE", "/instances/{id}", http.Header{}, func(_ context.Context) int {
		return 204
	})

	spans := sr.Ended()
	require.Len(t, spans, 1, "exactly one span should be recorded")
	assert.Equal(t, "wrkflw.rest DELETE /instances/{id}", spans[0].Name())

	var gotRoute string
	for _, attr := range spans[0].Attributes() {
		if string(attr.Key) == "http.route" {
			gotRoute = attr.Value.AsString()
		}
	}
	assert.Equal(t, "/instances/{id}", gotRoute)
}

// TestNewInstrumentation_MethodAttribute verifies that http.method is included
// in the recorded metric data points.
func TestNewInstrumentation_MethodAttribute(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	cfg := httpcore.ResolveConfig(httpcore.WithMeterProvider[int](mp))
	inst := httpcore.NewInstrumentation(cfg)

	inst.Observe(t.Context(), "PATCH", "/tasks/{token}/complete", http.Header{}, func(_ context.Context) int {
		return 200
	})

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))

	var foundMethod string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "wrkflw_rest_requests_total" {
				continue
			}
			data, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			for _, dp := range data.DataPoints {
				for _, attr := range dp.Attributes.ToSlice() {
					if string(attr.Key) == "http.method" {
						foundMethod = attr.Value.AsString()
					}
				}
			}
		}
	}
	assert.Equal(t, "PATCH", foundMethod)
}
