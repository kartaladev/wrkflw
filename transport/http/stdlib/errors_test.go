// Package stdlib_test — error-path coverage tests.
package stdlib_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/stdlib"
)

// ---------------------------------------------------------------------------
// Error-injecting stub implementations.

var errInternalAdmin = errors.New("admin store error")

// errDeadLetterAdmin returns errors on every call.
type errDeadLetterAdmin struct{ err error }

func (s *errDeadLetterAdmin) ListDeadLettered(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
	return nil, s.err
}
func (s *errDeadLetterAdmin) Redrive(_ context.Context, _ ...int64) (int, error) {
	return 0, s.err
}

// errPoliciesAdmin returns errors on every call.
type errPoliciesAdmin struct{ err error }

func (s *errPoliciesAdmin) AddPolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return false, s.err
}
func (s *errPoliciesAdmin) RemovePolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return false, s.err
}
func (s *errPoliciesAdmin) ListPolicies(_ context.Context) ([]service.PolicyRule, error) {
	return nil, s.err
}
func (s *errPoliciesAdmin) AddRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return false, s.err
}
func (s *errPoliciesAdmin) RemoveRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return false, s.err
}
func (s *errPoliciesAdmin) ListRoles(_ context.Context) ([]service.RoleBinding, error) {
	return nil, s.err
}

// errRelayStatsAdmin always returns an error.
type errRelayStatsAdmin struct{ err error }

func (s *errRelayStatsAdmin) OutboxStats(_ context.Context) (kernel.OutboxStats, error) {
	return kernel.OutboxStats{}, s.err
}

// errTimerAdmin always returns an error.
type errTimerAdmin struct{ err error }

func (s *errTimerAdmin) Stats(_ context.Context) (kernel.TimerStats, error) {
	return kernel.TimerStats{}, s.err
}
func (s *errTimerAdmin) ListArmed(_ context.Context) ([]kernel.ArmedTimer, error) {
	return nil, s.err
}

// errLineageAdmin always returns an error.
type errLineageAdmin struct{ err error }

func (s *errLineageAdmin) Lineage(_ context.Context, _ string) (kernel.InstanceLineage, error) {
	return kernel.InstanceLineage{}, s.err
}

// ---------------------------------------------------------------------------
// Tests

// TestInstanceRoutes_Snapshot_NotFound exercises the error path in GET /instances/{id}/snapshot.
func TestInstanceRoutes_Snapshot_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := do(mux, newGetRequest(t, "/instances/missing-snap/snapshot"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 snapshot not-found, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestInstanceRoutes_Actionable_NotFound exercises the error path in GET /instances/{id}/actionable.
func TestInstanceRoutes_Actionable_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := do(mux, newGetRequest(t, "/instances/missing-actionable/actionable"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 actionable not-found, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestInstanceRoutes_Signal_NotFound exercises the error path in POST /instances/{id}/signals.
func TestInstanceRoutes_Signal_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/instances/missing-signal/signals", map[string]any{
		"signal": "approved",
	})
	rr := do(mux, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 signal not-found, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestMessageRoutes_ServiceError exercises the error path when service.DeliverMessage fails.
func TestMessageRoutes_ServiceError(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/messages", map[string]any{
		"def_ref": "no-such-def:1",
		"name":    "order-shipped",
	})
	rr := do(mux, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 message service error, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestTaskRoutes_Complete_ServiceError exercises the error path in POST /tasks/{token}/complete.
func TestTaskRoutes_Complete_ServiceError(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskToken := transporttest.StartedApprovalInstance(t, h, "task-complete-err-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	// Forbidden actor → service error.
	req := newPostRequest(t, "/tasks/"+taskToken+"/complete", map[string]any{
		"actor": map[string]any{"id": "bob", "roles": []string{"viewer"}},
	})
	rr := do(mux, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 complete forbidden, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestTaskRoutes_Reassign_ServiceError exercises the error path in POST /tasks/{token}/reassign.
func TestTaskRoutes_Reassign_ServiceError(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskToken := transporttest.StartedApprovalInstance(t, h, "task-reassign-err-1")

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

	// Unauthorized reassigner.
	req := newPostRequest(t, "/tasks/"+taskToken+"/reassign", map[string]any{
		"from": "alice",
		"to":   "carol",
		"by":   map[string]any{"id": "bob", "roles": []string{"viewer"}},
	})
	rr := do(mux, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 reassign forbidden, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_Cancel_NotFound exercises the error path in POST /admin/instances/{id}/cancel.
func TestAdminRoutes_Cancel_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)
	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(mux)

	rr := do(mux, newPostRequest(t, "/admin/instances/no-such/cancel", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 cancel not-found, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_ListInstances_BadStatus exercises the error path when status is unknown.
func TestAdminRoutes_ListInstances_BadStatus(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)
	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/instances?status=unknown_status"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 bad status, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_ListInstances_Total_1 exercises total=1 query param.
func TestAdminRoutes_ListInstances_Total_1(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "admin-total-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/instances?total=1"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_DeadLetters_WithLimit verifies GET /admin/dead-letters with limit param.
func TestAdminRoutes_DeadLetters_WithLimit(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)
	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, DeadLetters: &stubDeadLetterAdmin{}}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/dead-letters?limit=5"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 dead-letters with limit, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_ServiceErrors exercises writeErr paths in admin handlers.
func TestAdminRoutes_ServiceErrors(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{
		Svc:         svc,
		DeadLetters: &errDeadLetterAdmin{errInternalAdmin},
		Policies:    &errPoliciesAdmin{errInternalAdmin},
		RelayStats:  &errRelayStatsAdmin{errInternalAdmin},
		Timers:      &errTimerAdmin{errInternalAdmin},
		Lineage:     &errLineageAdmin{errInternalAdmin},
	}.Customize(mux)

	tests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"dead-letters list error", http.MethodGet, "/admin/dead-letters", nil},
		{"dead-letters redrive error", http.MethodPost, "/admin/dead-letters/redrive", map[string]any{"ids": []int64{}}},
		{"list policies error", http.MethodGet, "/admin/policies", nil},
		{"add policy error", http.MethodPost, "/admin/policies", map[string]any{"subject": "a", "object": "b", "action": "c"}},
		{"remove policy error", http.MethodDelete, "/admin/policies", map[string]any{"subject": "a", "object": "b", "action": "c"}},
		{"list role-bindings error", http.MethodGet, "/admin/role-bindings", nil},
		{"add role-binding error", http.MethodPost, "/admin/role-bindings", map[string]any{"user": "alice", "role": "manager"}},
		{"remove role-binding error", http.MethodDelete, "/admin/role-bindings", map[string]any{"user": "alice", "role": "manager"}},
		{"relay-stats error", http.MethodGet, "/admin/relay-stats", nil},
		{"timers error", http.MethodGet, "/admin/timers", nil},
		{"lineage error", http.MethodGet, "/admin/instances/some-id/lineage", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var req *http.Request
			switch tc.method {
			case http.MethodGet:
				req = newGetRequest(t, tc.path)
			case http.MethodDelete:
				req = newDeleteRequest(t, tc.path, tc.body)
			default:
				req = newPostRequest(t, tc.path, tc.body)
			}

			rr := do(mux, req)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("want 500 for %s %s, got %d (body=%s)", tc.method, tc.path, rr.Code, rr.Body)
			}
		})
	}
}
