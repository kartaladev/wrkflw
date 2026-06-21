package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// TestWithInstanceMapperNilPanics asserts that passing nil to WithInstanceMapper
// panics immediately at option-construction time, not at request time.
func TestWithInstanceMapperNilPanics(t *testing.T) {
	assert.Panics(t, func() {
		rest.WithInstanceMapper(nil)
	}, "WithInstanceMapper(nil) must panic immediately")
}

// TestWithInstanceMapperNonNilDoesNotPanic asserts that a non-nil mapper is accepted.
func TestWithInstanceMapperNonNilDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		rest.WithInstanceMapper(func(engine.InstanceState) any {
			return map[string]string{"ok": "yes"}
		})
	}, "WithInstanceMapper with a valid fn must not panic")
}

// TestNilObservabilityOptionsAreIgnored asserts that passing nil to
// WithLogger, WithTracerProvider, and WithMeterProvider does not panic
// and produces a working handler (the nil option is silently ignored and
// telemetry falls back to defaults).
func TestNilObservabilityOptionsAreIgnored(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opt    rest.Option
		assert func(t *testing.T, h http.Handler)
	}

	def := linearProcess()
	_, svc := newTestHarness(t, def)

	cases := []testCase{
		{
			name: "WithLogger nil is ignored",
			opt:  rest.WithLogger(nil),
			assert: func(t *testing.T, h http.Handler) {
				t.Helper()
				require.NotNil(t, h, "handler must not be nil")
				body := strings.NewReader(`{"def_ref":"greeting","instance_id":"nil-log-1"}`)
				req := httptest.NewRequest(http.MethodPost, "/instances", body)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				assert.NotPanics(t, func() { h.ServeHTTP(rec, req) },
					"ServeHTTP must not panic when logger option was nil")
				assert.Equal(t, http.StatusCreated, rec.Code,
					"handler must still process requests with nil logger option")
			},
		},
		{
			name: "WithTracerProvider nil is ignored",
			opt:  rest.WithTracerProvider(nil),
			assert: func(t *testing.T, h http.Handler) {
				t.Helper()
				require.NotNil(t, h, "handler must not be nil")
				body := strings.NewReader(`{"def_ref":"greeting","instance_id":"nil-tp-1"}`)
				req := httptest.NewRequest(http.MethodPost, "/instances", body)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				assert.NotPanics(t, func() { h.ServeHTTP(rec, req) },
					"ServeHTTP must not panic when tracer provider option was nil")
				assert.Equal(t, http.StatusCreated, rec.Code,
					"handler must still process requests with nil tracer provider option")
			},
		},
		{
			name: "WithMeterProvider nil is ignored",
			opt:  rest.WithMeterProvider(nil),
			assert: func(t *testing.T, h http.Handler) {
				t.Helper()
				require.NotNil(t, h, "handler must not be nil")
				body := strings.NewReader(`{"def_ref":"greeting","instance_id":"nil-mp-1"}`)
				req := httptest.NewRequest(http.MethodPost, "/instances", body)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				assert.NotPanics(t, func() { h.ServeHTTP(rec, req) },
					"ServeHTTP must not panic when meter provider option was nil")
				assert.Equal(t, http.StatusCreated, rec.Code,
					"handler must still process requests with nil meter provider option")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var h http.Handler
			assert.NotPanics(t, func() { h = rest.NewHandler(svc, tc.opt) },
				"NewHandler must not panic when option wraps a nil value")
			tc.assert(t, h)
		})
	}
}
