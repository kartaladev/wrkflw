// Package stdlib_test — error-path coverage tests.
package stdlib_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/stdlib"
)

// ---------------------------------------------------------------------------
// Mock factories for error-injecting admin deps.

var errInternalAdmin = errors.New("admin store error")

// newErrDeadLetterAdmin returns a MockDeadLetterAdmin that returns errInternalAdmin on every call.
func newErrDeadLetterAdmin(t *testing.T) service.DeadLetterAdmin {
	t.Helper()
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(nil, errInternalAdmin).AnyTimes()
	// Redrive is variadic — use DoAndReturn to accept any number of id args.
	m.EXPECT().Redrive(gomock.Any()).DoAndReturn(
		func(_ context.Context, _ ...int64) (int, error) { return 0, errInternalAdmin }).AnyTimes()
	return m
}

// newErrPoliciesAdmin returns a MockPolicyAdmin that returns errInternalAdmin on every call.
func newErrPoliciesAdmin(t *testing.T) service.PolicyAdmin {
	t.Helper()
	m := service.NewMockPolicyAdmin(gomock.NewController(t))
	m.EXPECT().AddPolicy(gomock.Any(), gomock.Any()).Return(false, errInternalAdmin).AnyTimes()
	m.EXPECT().RemovePolicy(gomock.Any(), gomock.Any()).Return(false, errInternalAdmin).AnyTimes()
	m.EXPECT().ListPolicies(gomock.Any()).Return(nil, errInternalAdmin).AnyTimes()
	m.EXPECT().AddRole(gomock.Any(), gomock.Any()).Return(false, errInternalAdmin).AnyTimes()
	m.EXPECT().RemoveRole(gomock.Any(), gomock.Any()).Return(false, errInternalAdmin).AnyTimes()
	m.EXPECT().ListRoles(gomock.Any()).Return(nil, errInternalAdmin).AnyTimes()
	return m
}

// newErrRelayStatsAdmin returns a MockRelayStatsAdmin that returns errInternalAdmin on every call.
func newErrRelayStatsAdmin(t *testing.T) service.RelayStatsAdmin {
	t.Helper()
	m := service.NewMockRelayStatsAdmin(gomock.NewController(t))
	m.EXPECT().OutboxStats(gomock.Any()).Return(kernel.OutboxStats{}, errInternalAdmin).AnyTimes()
	return m
}

// newErrTimerAdmin returns a MockTimerAdmin that returns errInternalAdmin on every call.
func newErrTimerAdmin(t *testing.T) service.TimerAdmin {
	t.Helper()
	m := service.NewMockTimerAdmin(gomock.NewController(t))
	m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{}, errInternalAdmin).AnyTimes()
	m.EXPECT().ListArmed(gomock.Any()).Return(nil, errInternalAdmin).AnyTimes()
	return m
}

// newErrLineageAdmin returns a MockLineageAdmin that returns errInternalAdmin on every call.
func newErrLineageAdmin(t *testing.T) service.LineageAdmin {
	t.Helper()
	m := service.NewMockLineageAdmin(gomock.NewController(t))
	m.EXPECT().Lineage(gomock.Any(), gomock.Any()).Return(kernel.InstanceLineage{}, errInternalAdmin).AnyTimes()
	return m
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
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
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
	stdlib.AdminRoutes{Svc: svc, DeadLetters: newStubDeadLetterAdmin(t)}.Customize(mux)

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
		DeadLetters: newErrDeadLetterAdmin(t),
		Policies:    newErrPoliciesAdmin(t),
		RelayStats:  newErrRelayStatsAdmin(t),
		Timers:      newErrTimerAdmin(t),
		Lineage:     newErrLineageAdmin(t),
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
