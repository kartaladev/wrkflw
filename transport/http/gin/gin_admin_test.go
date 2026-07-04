package gin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ginlib "github.com/gin-gonic/gin"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	"github.com/zakyalvan/krtlwrkflw/service"
	ginadapter "github.com/zakyalvan/krtlwrkflw/transport/http/gin"
)

// ─── Admin fake implementations ───────────────────────────────────────────────

// fakeDeadLetterAdmin satisfies service.DeadLetterAdmin for tests.
type fakeDeadLetterAdmin struct {
	items []monitor.DeadLetter
	err   error
}

func (f *fakeDeadLetterAdmin) ListDeadLettered(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
	return f.items, f.err
}

func (f *fakeDeadLetterAdmin) Redrive(_ context.Context, _ ...int64) (int, error) {
	return len(f.items), f.err
}

// fakePolicyAdmin satisfies service.PolicyAdmin for tests.
type fakePolicyAdmin struct {
	policies []service.PolicyRule
	bindings []service.RoleBinding
	err      error
}

func (f *fakePolicyAdmin) ListPolicies(_ context.Context) ([]service.PolicyRule, error) {
	return f.policies, f.err
}

func (f *fakePolicyAdmin) AddPolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return true, f.err
}

func (f *fakePolicyAdmin) RemovePolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return true, f.err
}

func (f *fakePolicyAdmin) ListRoles(_ context.Context) ([]service.RoleBinding, error) {
	return f.bindings, f.err
}

func (f *fakePolicyAdmin) AddRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return true, f.err
}

func (f *fakePolicyAdmin) RemoveRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return true, f.err
}

// fakeRelayStatsAdmin satisfies service.RelayStatsAdmin for tests.
type fakeRelayStatsAdmin struct {
	stats kernel.OutboxStats
	err   error
}

func (f *fakeRelayStatsAdmin) OutboxStats(_ context.Context) (kernel.OutboxStats, error) {
	return f.stats, f.err
}

// fakeTimerAdmin satisfies service.TimerAdmin for tests.
type fakeTimerAdmin struct {
	stats kernel.TimerStats
	armed []kernel.ArmedTimer
	err   error
}

func (f *fakeTimerAdmin) Stats(_ context.Context) (kernel.TimerStats, error) {
	return f.stats, f.err
}

func (f *fakeTimerAdmin) ListArmed(_ context.Context) ([]kernel.ArmedTimer, error) {
	return f.armed, f.err
}

// fakeLineageAdmin satisfies service.LineageAdmin for tests.
type fakeLineageAdmin struct {
	lineage kernel.InstanceLineage
	err     error
}

func (f *fakeLineageAdmin) Lineage(_ context.Context, _ string) (kernel.InstanceLineage, error) {
	return f.lineage, f.err
}

// Keep errors imported.
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

	fakeDL := &fakeDeadLetterAdmin{
		items: []monitor.DeadLetter{
			{
				ID:         1,
				InstanceID: "inst-1",
				Topic:      "topic.foo",
				RetryCount: 2,
				LastError:  "timeout",
				CreatedAt:  time.Now(),
			},
		},
	}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:         fakeAdminSvc{},
		DeadLetters: fakeDL,
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

	fakeDL := &fakeDeadLetterAdmin{}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:         fakeAdminSvc{},
		DeadLetters: fakeDL,
	})

	resp := post(t, srv, "/admin/dead-letters/redrive", map[string]any{"ids": []int64{1, 2}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_Policies_List(t *testing.T) {
	t.Parallel()

	fakePol := &fakePolicyAdmin{
		policies: []service.PolicyRule{
			{Subject: "alice", Object: "process-*", Action: "start"},
		},
	}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:      fakeAdminSvc{},
		Policies: fakePol,
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

	fakePol := &fakePolicyAdmin{}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:      fakeAdminSvc{},
		Policies: fakePol,
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

	fakePol := &fakePolicyAdmin{
		bindings: []service.RoleBinding{
			{User: "bob", Role: "viewer"},
		},
	}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:      fakeAdminSvc{},
		Policies: fakePol,
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

	fakeRS := &fakeRelayStatsAdmin{
		stats: kernel.OutboxStats{Pending: 5, Dead: 1, OldestPendingAge: 30 * time.Second},
	}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:        fakeAdminSvc{},
		RelayStats: fakeRS,
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

	fakeT := &fakeTimerAdmin{
		stats: kernel.TimerStats{Armed: 2},
		armed: []kernel.ArmedTimer{
			{
				InstanceID: "inst-1",
				DefID:      "def-a",
				DefVersion: 1,
				TimerID:    "t1",
				FireAt:     time.Now().Add(time.Minute),
			},
		},
	}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:    fakeAdminSvc{},
		Timers: fakeT,
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

	fakeL := &fakeLineageAdmin{
		lineage: kernel.InstanceLineage{
			InstanceID:      "inst-lineage-1",
			CallChildren:    []kernel.CallLinkRef{},
			ChainSuccessors: []kernel.ChainLinkRef{},
		},
	}

	srv := newAdminSrv(t, ginadapter.AdminRoutes{
		Svc:     fakeAdminSvc{},
		Lineage: fakeL,
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
