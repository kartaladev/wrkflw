package gin_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ginlib "github.com/gin-gonic/gin"
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

func TestAdminRoutes_Timers(t *testing.T) {
	t.Parallel()

	fireAt := time.Now().Add(time.Minute)

	m := service.NewMockTimerAdmin(gomock.NewController(t))
	m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{Armed: 2}, nil)
	m.EXPECT().ListArmed(gomock.Any()).Return(
		[]kernel.ArmedTimer{
			{
				InstanceID: "inst-1",
				DefID:      "def-a",
				DefVersion: 1,
				TimerID:    "t1",
				NextRun:    fireAt,
			},
		}, nil)

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:    fakeAdminSvc{},
		Timers: m,
	})

	resp := get(t, srv, "/admin/timers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["count"] == nil {
		t.Fatal("want count in timers response")
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
