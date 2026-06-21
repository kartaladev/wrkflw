package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// TestRESTRequestSpan verifies that a per-request OTel span is emitted whose
// name starts with "wrkflw.rest". The handler is constructed with a real
// TracerProvider backed by a SpanRecorder so we can inspect recorded spans.
func TestRESTRequestSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	def := linearProcess()
	_, svc := newTestHarness(t, def)

	h := rest.NewHandler(svc, rest.WithTracerProvider(tp))

	body := strings.NewReader(`{"def_ref":"greeting","instance_id":"span-test-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/instances", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d — body: %s", rec.Code, rec.Body.String())
	}

	var sawSpan bool
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "wrkflw.rest") {
			sawSpan = true
		}
	}
	if !sawSpan {
		t.Fatalf("expected a wrkflw.rest span; got %d spans: %v",
			len(sr.Ended()), spanNames(sr.Ended()))
	}
}

// TestRESTRequestSpanAttributes verifies that the per-request span carries
// http.method and http.target attributes.
func TestRESTRequestSpanAttributes(t *testing.T) {
	type testCase struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		assert     func(t *testing.T, spans []sdktrace.ReadOnlySpan)
	}

	cases := []testCase{
		{
			name:       "POST /instances has span with correct attributes",
			method:     http.MethodPost,
			path:       "/instances",
			body:       `{"def_ref":"greeting","instance_id":"attr-test-1"}`,
			wantStatus: http.StatusCreated,
			assert: func(t *testing.T, spans []sdktrace.ReadOnlySpan) {
				t.Helper()
				var found bool
				var gotMethod, gotTarget string
				for _, s := range spans {
					if strings.HasPrefix(s.Name(), "wrkflw.rest") {
						found = true
						for _, attr := range s.Attributes() {
							switch string(attr.Key) {
							case "http.method":
								gotMethod = attr.Value.AsString()
							case "http.target":
								gotTarget = attr.Value.AsString()
							}
						}
					}
				}
				if !found {
					t.Fatalf("expected a wrkflw.rest span; got %d spans: %v",
						len(spans), spanNames(spans))
				}
				if gotMethod != http.MethodPost {
					t.Errorf("http.method attribute = %q, want %q", gotMethod, http.MethodPost)
				}
				if gotTarget != "/instances" {
					t.Errorf("http.target attribute = %q, want %q", gotTarget, "/instances")
				}
			},
		},
		{
			name:       "GET /instances/{id} has span with correct attributes",
			method:     http.MethodGet,
			path:       "/instances/get-attr-1",
			body:       "",
			wantStatus: http.StatusNotFound,
			assert: func(t *testing.T, spans []sdktrace.ReadOnlySpan) {
				t.Helper()
				var found bool
				var gotMethod, gotTarget string
				for _, s := range spans {
					if strings.HasPrefix(s.Name(), "wrkflw.rest") {
						found = true
						for _, attr := range s.Attributes() {
							switch string(attr.Key) {
							case "http.method":
								gotMethod = attr.Value.AsString()
							case "http.target":
								gotTarget = attr.Value.AsString()
							}
						}
					}
				}
				if !found {
					t.Fatalf("expected a wrkflw.rest span; got %d spans: %v",
						len(spans), spanNames(spans))
				}
				if gotMethod != http.MethodGet {
					t.Errorf("http.method attribute = %q, want %q", gotMethod, http.MethodGet)
				}
				if gotTarget != "/instances/get-attr-1" {
					t.Errorf("http.target attribute = %q, want %q", gotTarget, "/instances/get-attr-1")
				}
			},
		},
	}

	def := linearProcess()
	_, svc := newTestHarness(t, def)

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

			if rec.Code != tc.wantStatus {
				t.Fatalf("want %d, got %d — body: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}

			tc.assert(t, sr.Ended())
		})
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name()
	}
	return names
}
