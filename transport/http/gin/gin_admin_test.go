package gin_test

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	ginlib "github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/monitor"
	"github.com/kartaladev/wrkflw/service"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
)

// Keep errors imported for test helper usage.
var _ = errors.New

// ─── Tests ────────────────────────────────────────────────────────────────────

func newAdminSrv(t *testing.T, admin ginadapter.AdminRoutes) *httptest.Server {
	t.Helper()
	r := ginlib.New()
	admin.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestAdminRoutes_CancelInstance(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	admin := ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}
	admin.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// fakeAdminSvc embeds service.Service (nil underlying), cancel will fail.
	// We just assert routing works (not 404).
	resp := post(t, srv, "/admin/instances/some-id/cancel", nil)
	// Could be 500 or 404 depending on fake — it's routed correctly if not 404.
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("cancel route not registered, got 404")
	}
}

func TestAdminRoutes_ResolveIncident(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	admin := ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}
	admin.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/admin/instances/some-id/incidents/inc-1/resolve",
		map[string]any{"add_attempts": 1})
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("resolve-incident route not registered, got 404")
	}
}

func TestAdminRoutes_DeadLetters_WhenPresent(t *testing.T) {
	t.Parallel()

	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(
		[]monitor.DeadLetter{
			{
				ID:         1,
				InstanceID: "inst-1",
				Topic:      "topic.foo",
				RetryCount: 2,
				LastError:  "timeout",
				CreatedAt:  time.Now(),
			},
		}, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:         fakeAdminSvc{},
		DeadLetters: m,
	})

	resp := get(t, srv, "/admin/dead-letters")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["items"] == nil {
		t.Fatal("want items in DLQ response")
	}
}

func TestAdminRoutes_DeadLetters_Redrive(t *testing.T) {
	t.Parallel()

	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().Redrive(gomock.Any(), int64(1), int64(2)).Return(2, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:         fakeAdminSvc{},
		DeadLetters: m,
	})

	resp := post(t, srv, "/admin/dead-letters/redrive", map[string]any{"ids": []int64{1, 2}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_Policies_List(t *testing.T) {
	t.Parallel()

	m := service.NewMockPolicyAdmin(gomock.NewController(t))
	m.EXPECT().ListPolicies(gomock.Any()).Return(
		[]service.PolicyRule{
			{Subject: "alice", Object: "process-*", Action: "start"},
		}, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:      fakeAdminSvc{},
		Policies: m,
	})

	resp := get(t, srv, "/admin/policies")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["policies"] == nil {
		t.Fatal("want policies in response")
	}
}

func TestAdminRoutes_Policies_AddRemove(t *testing.T) {
	t.Parallel()

	m := service.NewMockPolicyAdmin(gomock.NewController(t))
	m.EXPECT().AddPolicy(gomock.Any(), gomock.Any()).Return(true, nil)
	m.EXPECT().RemovePolicy(gomock.Any(), gomock.Any()).Return(true, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:      fakeAdminSvc{},
		Policies: m,
	})

	// Add policy.
	addResp := post(t, srv, "/admin/policies", map[string]any{
		"subject": "alice", "object": "process-*", "action": "start",
	})
	if addResp.StatusCode != http.StatusOK {
		t.Fatalf("add policy: want 200, got %d", addResp.StatusCode)
	}

	// Remove policy (DELETE with body — gin supports it).
	delReq, err := newJSONRequest(t, "DELETE", srv.URL+"/admin/policies", map[string]any{
		"subject": "alice", "object": "process-*", "action": "start",
	})
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	delResp, err := srv.Client().Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /admin/policies: %v", err)
	}
	t.Cleanup(func() { drainClose(delResp) })
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("remove policy: want 200, got %d", delResp.StatusCode)
	}
}

func TestAdminRoutes_RoleBindings(t *testing.T) {
	t.Parallel()

	m := service.NewMockPolicyAdmin(gomock.NewController(t))
	m.EXPECT().ListRoles(gomock.Any()).Return(
		[]service.RoleBinding{{User: "bob", Role: "viewer"}}, nil)
	m.EXPECT().AddRole(gomock.Any(), gomock.Any()).Return(true, nil)
	m.EXPECT().RemoveRole(gomock.Any(), gomock.Any()).Return(true, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:      fakeAdminSvc{},
		Policies: m,
	})

	// List.
	listResp := get(t, srv, "/admin/role-bindings")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list role-bindings: want 200, got %d", listResp.StatusCode)
	}

	// Add.
	addResp := post(t, srv, "/admin/role-bindings", map[string]any{"user": "carol", "role": "manager"})
	if addResp.StatusCode != http.StatusOK {
		t.Fatalf("add role-binding: want 200, got %d", addResp.StatusCode)
	}

	// Remove.
	delReq, err := newJSONRequest(t, "DELETE", srv.URL+"/admin/role-bindings", map[string]any{"user": "carol", "role": "manager"})
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	delResp, err := srv.Client().Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /admin/role-bindings: %v", err)
	}
	t.Cleanup(func() { drainClose(delResp) })
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("remove role-binding: want 200, got %d", delResp.StatusCode)
	}
}

func TestAdminRoutes_RelayStats(t *testing.T) {
	t.Parallel()

	m := service.NewMockRelayStatsAdmin(gomock.NewController(t))
	m.EXPECT().OutboxStats(gomock.Any()).Return(
		kernel.OutboxStats{Pending: 5, Dead: 1, OldestPendingAge: 30 * time.Second}, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:        fakeAdminSvc{},
		RelayStats: m,
	})

	resp := get(t, srv, "/admin/relay-stats")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["pending"] == nil {
		t.Fatal("want pending in relay-stats response")
	}
}

// TestAdminRoutes_Timers exercises GET /admin/timers end-to-end through the gin
// router: query-string parsing into the filter, the aggregate gate behind
// total, the handler-side limit clamp, and the 400 mapping for a bad cursor.
// A route that drops the cursor silently re-serves page one forever, which no
// status-code-only assertion would catch.
func TestAdminRoutes_Timers(t *testing.T) {
	t.Parallel()

	fireAt := time.Now().Add(time.Minute)

	tests := map[string]struct {
		path    string
		buildTA func(t *testing.T) service.TimerAdmin
		assert  func(t *testing.T, resp httpResp)
	}{
		"total=true → aggregates present, and the store is asked for no total": {
			path: "/admin/timers?limit=1&cursor=opaque-cursor&total=true",
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{Armed: 2, NextFireAt: &fireAt}, nil)
				// IncludeTotal is deliberately false even though the request asked
				// for the total: Stats already returns the count and MIN(next_run)
				// in ONE aggregate query and has to run regardless (NextFireAt is
				// not derivable from the page). Forwarding IncludeTotal here would
				// make the store issue a SECOND count(*) whose result is discarded.
				// Do not "helpfully" re-add it.
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{
					Limit:        1,
					Cursor:       "opaque-cursor",
					IncludeTotal: false,
				}).Return(kernel.ArmedTimerPage{
					Items: []kernel.ArmedTimer{
						{InstanceID: "inst-1", DefID: "def-a", DefVersion: 1, TimerID: "t1", NextRun: fireAt},
					},
					NextCursor: "cursor-2",
					HasMore:    true,
				}, nil)
				return m
			},
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusOK, resp.StatusCode)
				var body map[string]any
				decodeJSON(t, resp, &body)
				assert.EqualValues(t, 2, body["total_count"], "total_count is the table total from Stats")
				assert.NotContains(t, body, "count", "count is the retired legacy field name")
				assert.Contains(t, body, "next_fire_at")
				assert.Equal(t, "cursor-2", body["next_cursor"])
				assert.Equal(t, true, body["has_more"])
			},
		},
		"total=1 enables the aggregates just like total=true": {
			path: "/admin/timers?limit=1&total=1",
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{Armed: 2}, nil)
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{
					Limit:        1,
					IncludeTotal: false,
				}).Return(kernel.ArmedTimerPage{}, nil)
				return m
			},
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusOK, resp.StatusCode)
				var body map[string]any
				decodeJSON(t, resp, &body)
				assert.EqualValues(t, 2, body["total_count"])
			},
		},
		"no total → no aggregate query, no total, limit defaulted": {
			path: "/admin/timers",
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				// Deliberately no Stats expectation: calling it would fail the test.
				// The unset limit is clamped to the kernel default before the port
				// is reached, so the port never sees a zero limit.
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{Limit: 50}).
					Return(kernel.ArmedTimerPage{Items: []kernel.ArmedTimer{{InstanceID: "inst-1", TimerID: "t1"}}}, nil)
				return m
			},
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusOK, resp.StatusCode)
				var body map[string]any
				decodeJSON(t, resp, &body)
				assert.NotContains(t, body, "total_count", "a plain paged request must not report a table total it never queried")
				assert.NotContains(t, body, "count")
				assert.Equal(t, false, body["has_more"])
			},
		},
		"limit above the maximum is clamped before the port sees it": {
			path: "/admin/timers?limit=" + strconv.Itoa(math.MaxInt),
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{Limit: 200}).
					Return(kernel.ArmedTimerPage{}, nil)
				return m
			},
			assert: func(t *testing.T, resp httpResp) {
				require.Equal(t, http.StatusOK, resp.StatusCode)
			},
		},
		"malformed cursor → 400 from the route, not a silent reset to page one": {
			path: "/admin/timers?cursor=garbage",
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().ListArmedPage(gomock.Any(), gomock.Any()).
					Return(kernel.ArmedTimerPage{}, fmt.Errorf("decode cursor: %w", kernel.ErrBadArmedTimerCursor))
				return m
			},
			assert: func(t *testing.T, resp httpResp) {
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%s", resp.Body)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := newAdminSrv(t, ginadapter.AdminRoutes{
				Svc:    fakeAdminSvc{},
				Timers: tc.buildTA(t),
			})
			tc.assert(t, get(t, srv, tc.path))
		})
	}
}

func TestAdminRoutes_Lineage(t *testing.T) {
	t.Parallel()

	m := service.NewMockLineageAdmin(gomock.NewController(t))
	m.EXPECT().Lineage(gomock.Any(), "inst-lineage-1").Return(
		kernel.InstanceLineage{
			InstanceID:      "inst-lineage-1",
			CallChildren:    []kernel.CallLinkRef{},
			ChainSuccessors: []kernel.ChainLinkRef{},
		}, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:     fakeAdminSvc{},
		Lineage: m,
	})

	resp := get(t, srv, "/admin/instances/inst-lineage-1/lineage")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["instance_id"] == nil {
		t.Fatal("want instance_id in lineage response")
	}
}

func TestAdminRoutes_ListInstances_WithFilters(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	admin := ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}
	admin.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// With query params.
	resp, err := srv.Client().Get(srv.URL + "/admin/instances?status=running&limit=10&cursor=abc&total=true")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { drainClose(resp) })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}
