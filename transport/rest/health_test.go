package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// stubCheck is a static rest.HealthCheck for tests.
type stubCheck struct {
	name string
	err  error
}

func (c stubCheck) Name() string                { return c.name }
func (c stubCheck) Check(context.Context) error { return c.err }

func TestHealthHandler(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		path   string
		checks []rest.HealthCheck
		ctx    func(ctx context.Context) context.Context // nil means identity
		assert func(t *testing.T, status int, body map[string]any)
	}

	cases := []testCase{
		{
			name:   "healthz is always 200 regardless of checks",
			path:   "/healthz",
			checks: []rest.HealthCheck{stubCheck{name: "db", err: errors.New("down")}},
			assert: func(t *testing.T, status int, body map[string]any) {
				assert.Equal(t, http.StatusOK, status)
				assert.Equal(t, "ok", body["status"])
			},
		},
		{
			name:   "readyz returns 200 when every check passes",
			path:   "/readyz",
			checks: []rest.HealthCheck{stubCheck{name: "db"}, stubCheck{name: "broker"}},
			assert: func(t *testing.T, status int, body map[string]any) {
				assert.Equal(t, http.StatusOK, status)
				assert.Equal(t, "ok", body["status"])
				checks, _ := body["checks"].(map[string]any)
				require.Len(t, checks, 2)
				assert.Equal(t, "ok", checks["db"])
				assert.Equal(t, "ok", checks["broker"])
			},
		},
		{
			name: "readyz returns 503 naming the failing check",
			path: "/readyz",
			checks: []rest.HealthCheck{
				stubCheck{name: "db", err: errors.New("connection refused")},
				stubCheck{name: "broker"},
			},
			assert: func(t *testing.T, status int, body map[string]any) {
				assert.Equal(t, http.StatusServiceUnavailable, status)
				assert.Equal(t, "unavailable", body["status"])
				checks, _ := body["checks"].(map[string]any)
				require.Len(t, checks, 2)
				// The failing check is NAMED but its raw error (which may carry
				// host/DSN fragments) is NOT leaked into the response body.
				assert.Equal(t, "unavailable", checks["db"])
				assert.NotContains(t, checks["db"], "connection refused")
				assert.Equal(t, "ok", checks["broker"])
			},
		},
		{
			name:   "readyz with no checks is ready",
			path:   "/readyz",
			checks: nil,
			assert: func(t *testing.T, status int, body map[string]any) {
				assert.Equal(t, http.StatusOK, status)
				assert.Equal(t, "ok", body["status"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			h := rest.NewHealthHandler(tc.checks...)
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			var body map[string]any
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
			tc.assert(t, rec.Code, body)
		})
	}
}

// TestHealthCheckFunc covers the function adapter so a consumer can register an
// inline check without a named type.
func TestHealthCheckFunc(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	c := rest.HealthCheckFunc("inline", func(context.Context) error { return sentinel })

	assert.Equal(t, "inline", c.Name())
	assert.ErrorIs(t, c.Check(t.Context()), sentinel)
}
