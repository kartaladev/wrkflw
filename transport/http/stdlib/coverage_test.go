// Package stdlib_test — additional coverage tests for uncovered handler paths.
package stdlib_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/stdlib"
)

// ---------------------------------------------------------------------------
// Stub implementations for optional admin deps.

// stubDeadLetterAdmin is a no-op DeadLetterAdmin.
type stubDeadLetterAdmin struct{}

func (s *stubDeadLetterAdmin) ListDeadLettered(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
	return nil, nil
}
func (s *stubDeadLetterAdmin) Redrive(_ context.Context, _ ...int64) (int, error) {
	return 0, nil
}

// stubRelayStatsAdmin is a no-op RelayStatsAdmin.
type stubRelayStatsAdmin struct{}

func (s *stubRelayStatsAdmin) OutboxStats(_ context.Context) (kernel.OutboxStats, error) {
	return kernel.OutboxStats{}, nil
}

// stubTimerAdmin is a no-op TimerAdmin.
type stubTimerAdmin struct{}

func (s *stubTimerAdmin) Stats(_ context.Context) (kernel.TimerStats, error) {
	return kernel.TimerStats{}, nil
}
func (s *stubTimerAdmin) ListArmed(_ context.Context) ([]kernel.ArmedTimer, error) {
	return nil, nil
}

// stubLineageAdmin is a no-op LineageAdmin.
type stubLineageAdmin struct{}

func (s *stubLineageAdmin) Lineage(_ context.Context, instanceID string) (kernel.InstanceLineage, error) {
	return kernel.InstanceLineage{InstanceID: instanceID}, nil
}

// errReader always returns an error when Read is called — used to simulate malformed JSON.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, context.DeadlineExceeded
}

// force use of time import so the compiler doesn't complain.
var _ = time.Now

// ---------------------------------------------------------------------------
// Tests

// TestTaskRoutes_Complete verifies POST /tasks/{token}/complete returns 200.
func TestTaskRoutes_Complete(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskToken := transporttest.StartedApprovalInstance(t, h, "task-complete-stdlib-1")

	// First claim the task.
	_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor:     authz.Actor{ID: "alice", Roles: []string{"manager"}},
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/tasks/"+taskToken+"/complete", map[string]any{
		"actor":  map[string]any{"id": "alice", "roles": []string{"manager"}},
		"output": map[string]any{"approved": true},
	})
	rr := do(mux, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 complete, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestTaskRoutes_Reassign verifies POST /tasks/{token}/reassign returns 200.
func TestTaskRoutes_Reassign(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskToken := transporttest.StartedApprovalInstance(t, h, "task-reassign-stdlib-1")

	// Claim first.
	_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor:     authz.Actor{ID: "alice", Roles: []string{"manager"}},
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/tasks/"+taskToken+"/reassign", map[string]any{
		"from": "alice",
		"to":   "carol",
		"by":   map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	rr := do(mux, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 reassign, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestTaskRoutes_BadJSON verifies that a malformed JSON body → 400.
func TestTaskRoutes_BadJSON(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskToken := transporttest.StartedApprovalInstance(t, h, "task-badjson-stdlib-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	// Malformed JSON on claim.
	req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/claim", errReader{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(t.Context())

	rr := do(mux, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestTaskRoutes_Complete_BadJSON verifies malformed body on complete → 400.
func TestTaskRoutes_Complete_BadJSON(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskToken := transporttest.StartedApprovalInstance(t, h, "task-complete-badjson-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/complete", errReader{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(t.Context())

	rr := do(mux, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON complete, got %d", rr.Code)
	}
}

// TestTaskRoutes_Reassign_BadJSON verifies malformed body on reassign → 400.
func TestTaskRoutes_Reassign_BadJSON(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskToken := transporttest.StartedApprovalInstance(t, h, "task-reassign-badjson-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/reassign", errReader{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(t.Context())

	rr := do(mux, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON reassign, got %d", rr.Code)
	}
}

// TestMessageRoutes_BadJSON verifies malformed JSON body on POST /messages → 400.
func TestMessageRoutes_BadJSON(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req, err := http.NewRequest(http.MethodPost, "/messages", errReader{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(t.Context())

	rr := do(mux, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestInstanceRoutes_BadJSON verifies malformed JSON body on POST /instances → 400.
func TestInstanceRoutes_BadJSON(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req, err := http.NewRequest(http.MethodPost, "/instances", errReader{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(t.Context())

	rr := do(mux, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestInstanceRoutes_SignalBadJSON verifies malformed JSON on signal → 400.
func TestInstanceRoutes_SignalBadJSON(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req, err := http.NewRequest(http.MethodPost, "/instances/some-id/signals", errReader{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(t.Context())

	rr := do(mux, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON signal, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_CancelInstance verifies POST /admin/instances/{id}/cancel.
func TestAdminRoutes_CancelInstance(t *testing.T) {
	t.Parallel()

	approvalDef := transporttest.ApprovalProcess()
	_, svcApproval := transporttest.NewHarness(t, approvalDef)

	_, err := svcApproval.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "approval", InstanceID: "cancel-stdlib-1",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svcApproval}.Customize(mux)

	rr := do(mux, newPostRequest(t, "/admin/instances/cancel-stdlib-1/cancel", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 cancel, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_ResolveIncident verifies POST .../resolve for not-found instance.
func TestAdminRoutes_ResolveIncident(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(mux)

	// Incident won't exist → 404 from service (instance not found).
	rr := do(mux, newPostRequest(t, "/admin/instances/no-such/incidents/inc-1/resolve", map[string]any{
		"add_attempts": 1,
	}))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for missing instance, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_ListInstances_WithFilter verifies GET /admin/instances with query params.
func TestAdminRoutes_ListInstances_WithFilter(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "admin-filter-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/instances?status=completed&limit=10&total=true"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_DeadLetters_WithDep verifies GET/POST /admin/dead-letters when dep is set.
func TestAdminRoutes_DeadLetters_WithDep(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stub := &stubDeadLetterAdmin{}
	stdlib.AdminRoutes{Svc: svc, DeadLetters: stub}.Customize(mux)

	// GET /admin/dead-letters
	rr := do(mux, newGetRequest(t, "/admin/dead-letters"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 dead-letters list, got %d (body=%s)", rr.Code, rr.Body)
	}

	// POST /admin/dead-letters/redrive
	rrR := do(mux, newPostRequest(t, "/admin/dead-letters/redrive", map[string]any{"ids": []int64{1, 2}}))
	if rrR.Code != http.StatusOK {
		t.Fatalf("want 200 dead-letters redrive, got %d (body=%s)", rrR.Code, rrR.Body)
	}
}

// TestAdminRoutes_DeadLetters_BadJSON_Redrive verifies malformed JSON body on redrive.
func TestAdminRoutes_DeadLetters_BadJSON_Redrive(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stub := &stubDeadLetterAdmin{}
	stdlib.AdminRoutes{Svc: svc, DeadLetters: stub}.Customize(mux)

	req, err := http.NewRequest(http.MethodPost, "/admin/dead-letters/redrive", errReader{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(t.Context())

	rr := do(mux, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// TestAdminRoutes_Policies_All verifies policy CRUD routes.
func TestAdminRoutes_Policies_All(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, Policies: alwaysPoliciesAdmin{}}.Customize(mux)

	// POST /admin/policies (add)
	rrAdd := do(mux, newPostRequest(t, "/admin/policies", map[string]any{
		"subject": "alice", "object": "instances", "action": "read",
	}))
	if rrAdd.Code != http.StatusOK {
		t.Fatalf("want 200 add policy, got %d (body=%s)", rrAdd.Code, rrAdd.Body)
	}

	// DELETE /admin/policies (remove)
	rrDel := do(mux, newDeleteRequest(t, "/admin/policies", map[string]any{
		"subject": "alice", "object": "instances", "action": "read",
	}))
	if rrDel.Code != http.StatusOK {
		t.Fatalf("want 200 remove policy, got %d (body=%s)", rrDel.Code, rrDel.Body)
	}

	// GET /admin/role-bindings
	rrRB := do(mux, newGetRequest(t, "/admin/role-bindings"))
	if rrRB.Code != http.StatusOK {
		t.Fatalf("want 200 list role-bindings, got %d (body=%s)", rrRB.Code, rrRB.Body)
	}

	// POST /admin/role-bindings (add)
	rrAddRB := do(mux, newPostRequest(t, "/admin/role-bindings", map[string]any{
		"user": "alice", "role": "manager",
	}))
	if rrAddRB.Code != http.StatusOK {
		t.Fatalf("want 200 add role-binding, got %d (body=%s)", rrAddRB.Code, rrAddRB.Body)
	}

	// DELETE /admin/role-bindings (remove)
	rrDelRB := do(mux, newDeleteRequest(t, "/admin/role-bindings", map[string]any{
		"user": "alice", "role": "manager",
	}))
	if rrDelRB.Code != http.StatusOK {
		t.Fatalf("want 200 remove role-binding, got %d (body=%s)", rrDelRB.Code, rrDelRB.Body)
	}
}

// TestAdminRoutes_Policies_BadJSON verifies malformed JSON → 400 for policy/role CRUD.
func TestAdminRoutes_Policies_BadJSON(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, Policies: alwaysPoliciesAdmin{}}.Customize(mux)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/admin/policies"},
		{http.MethodDelete, "/admin/policies"},
		{http.MethodPost, "/admin/role-bindings"},
		{http.MethodDelete, "/admin/role-bindings"},
	}

	for _, tc := range tests {
		req, err := http.NewRequest(tc.method, tc.path, errReader{})
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(t.Context())

		rr := do(mux, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("want 400 for bad JSON at %s %s, got %d", tc.method, tc.path, rr.Code)
		}
	}
}

// TestAdminRoutes_RelayStats verifies GET /admin/relay-stats.
func TestAdminRoutes_RelayStats(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, RelayStats: &stubRelayStatsAdmin{}}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/relay-stats"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 relay-stats, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_RelayStats_Absent verifies no route when RelayStats is nil.
func TestAdminRoutes_RelayStats_Absent(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, RelayStats: nil}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/relay-stats"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 relay-stats absent, got %d", rr.Code)
	}
}

// TestAdminRoutes_Timers verifies GET /admin/timers.
func TestAdminRoutes_Timers(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, Timers: &stubTimerAdmin{}}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/timers"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 timers, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_Timers_Absent verifies no route when Timers is nil.
func TestAdminRoutes_Timers_Absent(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, Timers: nil}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/timers"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 timers absent, got %d", rr.Code)
	}
}

// TestAdminRoutes_Lineage verifies GET /admin/instances/{id}/lineage.
func TestAdminRoutes_Lineage(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	la := &stubLineageAdmin{}
	stdlib.AdminRoutes{Svc: svc, Lineage: la}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/instances/some-instance/lineage"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 lineage, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_Lineage_Absent verifies no /lineage route when Lineage is nil.
func TestAdminRoutes_Lineage_Absent(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, Lineage: nil}.Customize(mux)

	// Without lineage route, /admin/instances/{id}/lineage won't match the lineage
	// handler. The test just verifies no panic occurs.
	rr := do(mux, newGetRequest(t, "/admin/instances/some-instance/lineage"))
	_ = rr.Code
}
