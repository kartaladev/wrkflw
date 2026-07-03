package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// ---- fakes ----

type fakeRelayStatsAdmin struct {
	stats kernel.OutboxStats
	err   error
}

func (f *fakeRelayStatsAdmin) OutboxStats(_ context.Context) (kernel.OutboxStats, error) {
	return f.stats, f.err
}

type fakeTimerAdmin struct {
	stats    kernel.TimerStats
	statsErr error
	armed    []kernel.ArmedTimer
	armedErr error
}

func (f *fakeTimerAdmin) Stats(_ context.Context) (kernel.TimerStats, error) {
	return f.stats, f.statsErr
}

func (f *fakeTimerAdmin) ListArmed(_ context.Context) ([]kernel.ArmedTimer, error) {
	return f.armed, f.armedErr
}

// decodeRec decodes the response body of a recorder into map[string]any.
func decodeRec(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decodeRec: %v — body: %s", err, rec.Body.String())
	}
	return v
}

// ---- relay-stats tests ----

func TestAdminRelayStats(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		withMW     bool
		wired      bool
		wantCode   int
		wantFields map[string]any
	}{
		{
			name:     "default-deny without admin middleware -> 403",
			withMW:   false,
			wired:    true,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "not wired -> 404",
			withMW:   true,
			wired:    false,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "wired + admin-allow -> 200 with correct shape",
			withMW:   true,
			wired:    true,
			wantCode: http.StatusOK,
			wantFields: map[string]any{
				"pending":                    float64(5),
				"dead":                       float64(2),
				"oldest_pending_age_seconds": float64(30),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := []rest.Option{}
			if tc.withMW {
				opts = append(opts, rest.WithAdminMiddleware(allowAdmin))
			}
			if tc.wired {
				opts = append(opts, rest.WithRelayStatsAdmin(&fakeRelayStatsAdmin{
					stats: kernel.OutboxStats{
						Pending:          5,
						Dead:             2,
						OldestPendingAge: 30 * time.Second,
					},
				}))
			}
			h := rest.NewHandler(&dlqStubService{}, opts...)
			rec := doReq(t, h, http.MethodGet, "/admin/relay-stats", "")
			assert.Equal(t, tc.wantCode, rec.Code)
			if tc.wantFields != nil {
				body := decodeRec(t, rec)
				for k, want := range tc.wantFields {
					assert.Equal(t, want, body[k], "field %q", k)
				}
			}
		})
	}
}

func TestWithRelayStatsAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { rest.WithRelayStatsAdmin(nil) })
}

// ---- timer tests ----

func TestAdminTimers(t *testing.T) {
	t.Parallel()

	fireAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		withMW   bool
		wired    bool
		wantCode int
		check    func(t *testing.T, body map[string]any)
	}{
		{
			name:     "default-deny without admin middleware -> 403",
			withMW:   false,
			wired:    true,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "not wired -> 404",
			withMW:   true,
			wired:    false,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "wired + admin-allow -> 200 with count, nextFireAt, items",
			withMW:   true,
			wired:    true,
			wantCode: http.StatusOK,
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				count, ok := body["count"].(float64)
				require.True(t, ok, "count must be a number, got %T %v", body["count"], body["count"])
				assert.Equal(t, float64(1), count)

				assert.NotNil(t, body["next_fire_at"], "next_fire_at must be present")

				items, ok := body["items"].([]any)
				require.True(t, ok, "items must be a list, got %T", body["items"])
				require.Len(t, items, 1)

				item, ok := items[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "inst-1", item["instance_id"])
				assert.Equal(t, "def-1", item["def_id"])
				assert.Equal(t, float64(2), item["def_version"])
				assert.Equal(t, "timer-1", item["timer_id"])
				assert.Equal(t, "TimerIntermediate", item["kind"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := []rest.Option{}
			if tc.withMW {
				opts = append(opts, rest.WithAdminMiddleware(allowAdmin))
			}
			if tc.wired {
				nextFire := fireAt
				opts = append(opts, rest.WithTimerAdmin(&fakeTimerAdmin{
					stats: kernel.TimerStats{Armed: 1, NextFireAt: &nextFire},
					armed: []kernel.ArmedTimer{{
						InstanceID: "inst-1",
						DefID:      "def-1",
						DefVersion: 2,
						TimerID:    "timer-1",
						FireAt:     fireAt,
						Kind:       engine.TimerIntermediate,
					}},
				}))
			}
			h := rest.NewHandler(&dlqStubService{}, opts...)
			rec := doReq(t, h, http.MethodGet, "/admin/timers", "")
			assert.Equal(t, tc.wantCode, rec.Code)
			if tc.check != nil {
				tc.check(t, decodeRec(t, rec))
			}
		})
	}
}

func TestWithTimerAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { rest.WithTimerAdmin(nil) })
}
