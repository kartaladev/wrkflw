package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// TestRESTRouteTemplateSpan verifies that after a request to a templated route
// (e.g. GET /instances/{id}/snapshot) the span is named with the route template,
// not the concrete path, and carries an http.route attribute.
func TestRESTRouteTemplateSpan(t *testing.T) {
	type testCase struct {
		name           string
		method         string
		path           string
		body           string
		wantSpanName   string
		wantRoute      string
		wantStatusCode int
	}

	def := linearProcess()
	_, svc := newTestHarness(t, def)

	cases := []testCase{
		{
			name:           "GET /instances/{id} uses route template in span name",
			method:         http.MethodGet,
			path:           "/instances/some-concrete-id-123",
			body:           "",
			wantSpanName:   "wrkflw.rest GET /instances/{id}",
			wantRoute:      "/instances/{id}",
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "GET /instances/{id}/snapshot uses route template in span name",
			method:         http.MethodGet,
			path:           "/instances/some-concrete-id-456/snapshot",
			body:           "",
			wantSpanName:   "wrkflw.rest GET /instances/{id}/snapshot",
			wantRoute:      "/instances/{id}/snapshot",
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:           "POST /instances uses exact pattern (no wildcard)",
			method:         http.MethodPost,
			path:           "/instances",
			body:           `{"def_ref":"greeting","instance_id":"route-tpl-test-1"}`,
			wantSpanName:   "wrkflw.rest POST /instances",
			wantRoute:      "/instances",
			wantStatusCode: http.StatusCreated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

			h := rest.NewHandler(svc, rest.WithTracerProvider(tp))

			bodyReader := strings.NewReader(tc.body)
			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatusCode, rec.Code,
				"unexpected HTTP status: %s", rec.Body.String())

			// Find the REST span.
			var found sdktrace.ReadOnlySpan
			for _, s := range sr.Ended() {
				if strings.HasPrefix(s.Name(), "wrkflw.rest") {
					found = s
					break
				}
			}
			require.NotNil(t, found, "expected a wrkflw.rest span to be recorded")
			assert.Equal(t, tc.wantSpanName, found.Name(),
				"span name must use route template, not concrete path")

			// Verify http.route attribute is set to the template.
			var gotRoute string
			for _, attr := range found.Attributes() {
				if string(attr.Key) == "http.route" {
					gotRoute = attr.Value.AsString()
				}
			}
			assert.Equal(t, tc.wantRoute, gotRoute,
				"http.route attribute must be the route template")
		})
	}
}

// TestRESTUnmatchedPathSpan verifies that a request to an unregistered path
// yields span name "wrkflw.rest <METHOD> unmatched" and http.route="unmatched".
func TestRESTUnmatchedPathSpan(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	h := rest.NewHandler(svc, rest.WithTracerProvider(tp))

	req := httptest.NewRequest(http.MethodGet, "/no/such/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// 404 is expected for an unmatched path.
	assert.Equal(t, http.StatusNotFound, rec.Code)

	var found sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "wrkflw.rest") {
			found = s
			break
		}
	}
	require.NotNil(t, found, "expected a wrkflw.rest span to be recorded")
	assert.Equal(t, "wrkflw.rest GET unmatched", found.Name(),
		"unmatched path must produce 'unmatched' in span name")

	var gotRoute string
	for _, attr := range found.Attributes() {
		if string(attr.Key) == "http.route" {
			gotRoute = attr.Value.AsString()
		}
	}
	assert.Equal(t, "unmatched", gotRoute, "http.route must be 'unmatched' for 404 paths")
}

// TestRESTRequestsMetrics verifies that wrkflw_rest_requests_total and
// wrkflw_rest_request_duration_seconds are recorded per request, using the
// route template (not the concrete path) as the http.route label.
func TestRESTRequestsMetrics(t *testing.T) {
	type testCase struct {
		name        string
		method      string
		path        string
		body        string
		wantStatus  int
		wantRoute   string
		wantCounter int64
	}

	def := linearProcess()
	_, svc := newTestHarness(t, def)

	cases := []testCase{
		{
			name:        "matched route — template in metric attrs",
			method:      http.MethodGet,
			path:        "/instances/concrete-metrics-id",
			body:        "",
			wantStatus:  http.StatusNotFound,
			wantRoute:   "/instances/{id}",
			wantCounter: 1,
		},
		{
			name:        "unmatched route — 'unmatched' label",
			method:      http.MethodGet,
			path:        "/no/such/route",
			body:        "",
			wantStatus:  http.StatusNotFound,
			wantRoute:   "unmatched",
			wantCounter: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

			h := rest.NewHandler(svc, rest.WithMeterProvider(mp))

			bodyReader := strings.NewReader(tc.body)
			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)

			var rm metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(t.Context(), &rm))

			// Check wrkflw_rest_requests_total.
			var totalFound bool
			var counterWithRoute int64
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name == "wrkflw_rest_requests_total" {
						totalFound = true
						data, ok := m.Data.(metricdata.Sum[int64])
						require.True(t, ok, "expected Sum[int64] for requests_total")
						for _, dp := range data.DataPoints {
							// Find the data point with the expected http.route.
							for _, attr := range dp.Attributes.ToSlice() {
								if string(attr.Key) == "http.route" && attr.Value.AsString() == tc.wantRoute {
									counterWithRoute += dp.Value
								}
							}
						}
					}
				}
			}
			require.True(t, totalFound, "expected wrkflw_rest_requests_total metric to be present")
			assert.Equal(t, tc.wantCounter, counterWithRoute,
				"counter must be recorded with route template as http.route label")

			// Check wrkflw_rest_request_duration_seconds (histogram).
			var histFound bool
			var histWithRoute uint64
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name == "wrkflw_rest_request_duration_seconds" {
						histFound = true
						data, ok := m.Data.(metricdata.Histogram[float64])
						require.True(t, ok, "expected Histogram[float64] for request_duration_seconds")
						for _, dp := range data.DataPoints {
							for _, attr := range dp.Attributes.ToSlice() {
								if string(attr.Key) == "http.route" && attr.Value.AsString() == tc.wantRoute {
									histWithRoute += dp.Count
								}
							}
						}
					}
				}
			}
			require.True(t, histFound, "expected wrkflw_rest_request_duration_seconds metric to be present")
			assert.GreaterOrEqual(t, histWithRoute, uint64(1),
				"histogram must have at least 1 observation with the route template label")
		})
	}
}

// TestRESTHttpTargetPreserved verifies that the raw path (http.target attribute)
// is still recorded alongside the route template attributes — it is NOT removed.
func TestRESTHttpTargetPreserved(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	h := rest.NewHandler(svc, rest.WithTracerProvider(tp))

	req := httptest.NewRequest(http.MethodGet, "/instances/raw-path-preserve-test", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var found sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "wrkflw.rest") {
			found = s
			break
		}
	}
	require.NotNil(t, found)

	var gotTarget string
	for _, attr := range found.Attributes() {
		if string(attr.Key) == "http.target" {
			gotTarget = attr.Value.AsString()
		}
	}
	assert.Equal(t, "/instances/raw-path-preserve-test", gotTarget,
		"http.target (raw path) must still be present and not replaced by the template")
}
