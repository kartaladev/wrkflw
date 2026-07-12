// gin_admin_errors_test.go — error-branch tests for conditional AdminRoutes deps.
package gin_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	ginlib "github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"

	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
)

// ─── Mock factories that return errors ───────────────────────────────────────

func newErrDeadLetterAdminGin(t *testing.T) service.DeadLetterAdmin {
	t.Helper()
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).
		Return(nil, fmt.Errorf("dead-letter backend error")).AnyTimes()
	// Redrive is variadic. Set expectations for 0 ids (empty redrive) and 1 id (our test sends 1).
	redriveErr := fmt.Errorf("dead-letter backend error")
	m.EXPECT().Redrive(gomock.Any()).Return(0, redriveErr).AnyTimes()
	m.EXPECT().Redrive(gomock.Any(), gomock.Any()).Return(0, redriveErr).AnyTimes()
	return m
}

func newErrPolicyAdminGin(t *testing.T) service.PolicyAdmin {
	t.Helper()
	m := service.NewMockPolicyAdmin(gomock.NewController(t))
	m.EXPECT().ListPolicies(gomock.Any()).Return(nil, fmt.Errorf("policy backend error")).AnyTimes()
	m.EXPECT().AddPolicy(gomock.Any(), gomock.Any()).Return(false, fmt.Errorf("policy backend error")).AnyTimes()
	m.EXPECT().RemovePolicy(gomock.Any(), gomock.Any()).Return(false, fmt.Errorf("policy backend error")).AnyTimes()
	m.EXPECT().ListRoles(gomock.Any()).Return(nil, fmt.Errorf("policy backend error")).AnyTimes()
	m.EXPECT().AddRole(gomock.Any(), gomock.Any()).Return(false, fmt.Errorf("policy backend error")).AnyTimes()
	m.EXPECT().RemoveRole(gomock.Any(), gomock.Any()).Return(false, fmt.Errorf("policy backend error")).AnyTimes()
	return m
}

func newErrRelayStatsAdminGin(t *testing.T) service.RelayStatsAdmin {
	t.Helper()
	m := service.NewMockRelayStatsAdmin(gomock.NewController(t))
	m.EXPECT().OutboxStats(gomock.Any()).Return(kernel.OutboxStats{}, fmt.Errorf("relay stats error")).AnyTimes()
	return m
}

func newErrTimerAdminGin(t *testing.T) service.TimerAdmin {
	t.Helper()
	m := service.NewMockTimerAdmin(gomock.NewController(t))
	m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{}, fmt.Errorf("timer stats error")).AnyTimes()
	m.EXPECT().ListArmed(gomock.Any()).Return(nil, fmt.Errorf("timer list error")).AnyTimes()
	return m
}

func newErrLineageAdminGin(t *testing.T) service.LineageAdmin {
	t.Helper()
	m := service.NewMockLineageAdmin(gomock.NewController(t))
	m.EXPECT().Lineage(gomock.Any(), gomock.Any()).Return(kernel.InstanceLineage{}, fmt.Errorf("lineage error")).AnyTimes()
	return m
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestAdminRoutes_DeadLetters_ListError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, DeadLetters: newErrDeadLetterAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/dead-letters")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error status, got 200")
	}
}

func TestAdminRoutes_DeadLetters_RedriveError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, DeadLetters: newErrDeadLetterAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/admin/dead-letters/redrive", map[string]any{"ids": []int64{1}})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error status, got 200")
	}
}

func TestAdminRoutes_DeadLetters_RedriveBadJSON(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	// Bad-JSON test: handler returns 400 before calling Redrive; only set list expectation.
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, DeadLetters: m}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Send a string where an object is expected.
	resp := post(t, srv, "/admin/dead-letters/redrive", "not-an-object")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_Policies_ListError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/policies")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error status, got 200")
	}
}

func TestAdminRoutes_Policies_AddBadJSON(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/admin/policies", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_Policies_AddError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/admin/policies", map[string]any{
		"subject": "alice", "object": "x", "action": "read",
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

func TestAdminRoutes_Policies_DeleteBadJSON(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := newJSONRequest(t, "DELETE", srv.URL+"/admin/policies", "not-json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	t.Cleanup(func() { drainClose(resp) })
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_Policies_DeleteError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := newJSONRequest(t, "DELETE", srv.URL+"/admin/policies", map[string]any{
		"subject": "alice", "object": "x", "action": "read",
	})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	t.Cleanup(func() { drainClose(resp) })
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

func TestAdminRoutes_RoleBindings_ListError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/role-bindings")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

func TestAdminRoutes_RoleBindings_AddBadJSON(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/admin/role-bindings", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_RoleBindings_AddError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/admin/role-bindings", map[string]any{"user": "alice", "role": "admin"})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

func TestAdminRoutes_RoleBindings_DeleteBadJSON(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := newJSONRequest(t, "DELETE", srv.URL+"/admin/role-bindings", "not-json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	t.Cleanup(func() { drainClose(resp) })
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_RoleBindings_DeleteError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: newErrPolicyAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := newJSONRequest(t, "DELETE", srv.URL+"/admin/role-bindings", map[string]any{"user": "alice", "role": "admin"})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	t.Cleanup(func() { drainClose(resp) })
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

func TestAdminRoutes_RelayStats_Error(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, RelayStats: newErrRelayStatsAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/relay-stats")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

func TestAdminRoutes_Timers_Error(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Timers: newErrTimerAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/timers")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

func TestAdminRoutes_Lineage_Error(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Lineage: newErrLineageAdminGin(t)}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/instances/missing/lineage")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}
