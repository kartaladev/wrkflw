// gin_admin_errors_test.go — error-branch tests for conditional AdminRoutes deps.
package gin_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	ginlib "github.com/gin-gonic/gin"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	"github.com/zakyalvan/krtlwrkflw/service"
	ginadapter "github.com/zakyalvan/krtlwrkflw/transport/http/gin"
)

// ─── Fakes that return errors ─────────────────────────────────────────────────

type errDeadLetterAdmin struct{}

func (e errDeadLetterAdmin) ListDeadLettered(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
	return nil, fmt.Errorf("dead-letter backend error")
}
func (e errDeadLetterAdmin) Redrive(_ context.Context, _ ...int64) (int, error) {
	return 0, fmt.Errorf("dead-letter backend error")
}

type errPolicyAdmin struct{}

func (e errPolicyAdmin) ListPolicies(_ context.Context) ([]service.PolicyRule, error) {
	return nil, fmt.Errorf("policy backend error")
}
func (e errPolicyAdmin) AddPolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return false, fmt.Errorf("policy backend error")
}
func (e errPolicyAdmin) RemovePolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return false, fmt.Errorf("policy backend error")
}
func (e errPolicyAdmin) ListRoles(_ context.Context) ([]service.RoleBinding, error) {
	return nil, fmt.Errorf("policy backend error")
}
func (e errPolicyAdmin) AddRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return false, fmt.Errorf("policy backend error")
}
func (e errPolicyAdmin) RemoveRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return false, fmt.Errorf("policy backend error")
}

type errRelayStatsAdmin struct{}

func (e errRelayStatsAdmin) OutboxStats(_ context.Context) (kernel.OutboxStats, error) {
	return kernel.OutboxStats{}, fmt.Errorf("relay stats error")
}

type errTimerAdmin struct{}

func (e errTimerAdmin) Stats(_ context.Context) (kernel.TimerStats, error) {
	return kernel.TimerStats{}, fmt.Errorf("timer stats error")
}
func (e errTimerAdmin) ListArmed(_ context.Context) ([]kernel.ArmedTimer, error) {
	return nil, fmt.Errorf("timer list error")
}

type errLineageAdmin struct{}

func (e errLineageAdmin) Lineage(_ context.Context, _ string) (kernel.InstanceLineage, error) {
	return kernel.InstanceLineage{}, fmt.Errorf("lineage error")
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestAdminRoutes_DeadLetters_ListError(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, DeadLetters: errDeadLetterAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, DeadLetters: errDeadLetterAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, DeadLetters: &fakeDeadLetterAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Policies: errPolicyAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, RelayStats: errRelayStatsAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Timers: errTimerAdmin{}}.Customize(r)
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
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}, Lineage: errLineageAdmin{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/instances/missing/lineage")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}
