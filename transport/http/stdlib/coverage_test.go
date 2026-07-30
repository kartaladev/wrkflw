// Package stdlib_test — additional coverage tests for uncovered handler paths.
package stdlib_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// ---------------------------------------------------------------------------
// Mock factories for stub (no-op / success) admin deps.

// newStubDeadLetterAdmin returns a MockDeadLetterAdmin that always succeeds with
// empty results on ListDeadLettered. Redrive expectations must be set up per-test.
func newStubDeadLetterAdmin(t *testing.T) *service.MockDeadLetterAdmin {
	t.Helper()
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	return m
}

// newStubRelayStatsAdmin returns a MockRelayStatsAdmin that always succeeds with zero stats.
func newStubRelayStatsAdmin(t *testing.T) service.RelayStatsAdmin {
	t.Helper()
	m := service.NewMockRelayStatsAdmin(gomock.NewController(t))
	m.EXPECT().OutboxStats(gomock.Any()).Return(kernel.OutboxStats{}, nil).AnyTimes()
	return m
}

// newStubLineageAdmin returns a MockLineageAdmin that always succeeds with a root lineage.
func newStubLineageAdmin(t *testing.T) service.LineageAdmin {
	t.Helper()
	m := service.NewMockLineageAdmin(gomock.NewController(t))
	m.EXPECT().Lineage(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, instanceID string) (kernel.InstanceLineage, error) {
			return kernel.InstanceLineage{InstanceID: instanceID}, nil
		}).AnyTimes()
	return m
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

	taskID := transporttest.StartedApprovalInstance(t, h, "task-complete-stdlib-1")

	// First claim the task.
	_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
		TaskID: taskID,
		Actor:  authz.Actor{ID: "alice", Roles: []string{"manager"}},
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/tasks/"+taskID+"/complete", map[string]any{
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

	taskID := transporttest.StartedApprovalInstance(t, h, "task-reassign-stdlib-1")

	// Claim first.
	_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
		TaskID: taskID,
		Actor:  authz.Actor{ID: "alice", Roles: []string{"manager"}},
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/tasks/"+taskID+"/reassign", map[string]any{
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

	taskID := transporttest.StartedApprovalInstance(t, h, "task-badjson-stdlib-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	// Malformed JSON on claim.
	req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskID+"/claim", errReader{})
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
	taskID := transporttest.StartedApprovalInstance(t, h, "task-complete-badjson-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskID+"/complete", errReader{})
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
	taskID := transporttest.StartedApprovalInstance(t, h, "task-reassign-badjson-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskID+"/reassign", errReader{})
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

	pi, err := svcApproval.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("approval"),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	cancelID := pi.State().InstanceID

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svcApproval}.Customize(mux)

	rr := do(mux, newPostRequest(t, "/admin/instances/"+cancelID+"/cancel", nil))
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
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
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
	// Build mock inline so we can set specific expectations for each call.
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(nil, nil)
	m.EXPECT().Redrive(gomock.Any(), int64(1), int64(2)).Return(2, nil)
	stdlib.AdminRoutes{Svc: svc, DeadLetters: m}.Customize(mux)

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
	// Bad-JSON test: the handler parses the body first, returns 400 before calling Redrive.
	// So no Redrive expectation needed; ListDeadLettered is also not called on this route.
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	stdlib.AdminRoutes{Svc: svc, DeadLetters: m}.Customize(mux)

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
	stdlib.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(mux)

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
	stdlib.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(mux)

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
	stdlib.AdminRoutes{Svc: svc, RelayStats: newStubRelayStatsAdmin(t)}.Customize(mux)

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

// TestAdminRoutes_Timers exercises GET /admin/timers through the mux: the
// query string parsed into the filter, the aggregate gate behind total, the
// handler-side limit clamp, the 400 mapping for a bad cursor, and the absence
// of the route when the dep is nil (ADR-0159). A route that drops the cursor
// silently re-serves page one forever, which no status-code-only assertion
// would catch. The nil-dep case shares the request shape and differs only in
// what is wired, so it belongs in the same table.
func TestAdminRoutes_Timers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path string
		// buildTA returns nil for the "dep not wired" case, which must leave the
		// route unregistered.
		buildTA func(t *testing.T) service.TimerAdmin
		assert  func(t *testing.T, rr *httptest.ResponseRecorder)
	}{
		"total=1 → aggregates present, and the store is asked for no total": {
			path: "/admin/timers?limit=2&cursor=opaque-cursor&total=1",
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{Armed: 3}, nil)
				// IncludeTotal is deliberately false even though the request asked
				// for the total: Stats already returns the count and MIN(next_run)
				// in ONE aggregate query and has to run regardless (NextFireAt is
				// not derivable from the page). Forwarding IncludeTotal here would
				// make the store issue a SECOND count(*) whose result is discarded.
				// Do not "helpfully" re-add it.
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{
					Limit:        2,
					Cursor:       "opaque-cursor",
					IncludeTotal: false,
				}).Return(kernel.ArmedTimerPage{NextCursor: "cursor-2", HasMore: true}, nil)
				return m
			},
			assert: func(t *testing.T, rr *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body)
				var body map[string]any
				decodeJSON(t, rr.Body, &body)
				assert.EqualValues(t, 3, body["total_count"], "total_count is the table total from Stats")
				assert.NotContains(t, body, "count", "count is the retired pre-ADR-0159 field name")
				assert.Equal(t, "cursor-2", body["next_cursor"])
			},
		},
		"total=true enables the aggregates just like total=1": {
			path: "/admin/timers?limit=2&total=true",
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{Armed: 3}, nil)
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{
					Limit:        2,
					IncludeTotal: false,
				}).Return(kernel.ArmedTimerPage{}, nil)
				return m
			},
			assert: func(t *testing.T, rr *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body)
				var body map[string]any
				decodeJSON(t, rr.Body, &body)
				assert.EqualValues(t, 3, body["total_count"])
			},
		},
		"no total → no aggregate query, no total, limit defaulted": {
			path: "/admin/timers",
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				// Deliberately no Stats expectation: calling it would fail the test.
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{Limit: 50}).
					Return(kernel.ArmedTimerPage{}, nil)
				return m
			},
			assert: func(t *testing.T, rr *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body)
				var body map[string]any
				decodeJSON(t, rr.Body, &body)
				assert.NotContains(t, body, "total_count", "a plain paged request must not report a table total it never queried")
				assert.NotContains(t, body, "count")
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
			assert: func(t *testing.T, rr *httptest.ResponseRecorder) {
				require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body)
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
			assert: func(t *testing.T, rr *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body)
			},
		},
		"dep not wired → route absent, 404": {
			path:    "/admin/timers",
			buildTA: func(_ *testing.T) service.TimerAdmin { return nil },
			assert: func(t *testing.T, rr *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t)

			mux := http.NewServeMux()
			stdlib.AdminRoutes{Svc: svc, Timers: tc.buildTA(t)}.Customize(mux)

			tc.assert(t, do(mux, newGetRequest(t, tc.path)))
		})
	}
}

// TestAdminRoutes_Lineage verifies GET /admin/instances/{id}/lineage.
func TestAdminRoutes_Lineage(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, Lineage: newStubLineageAdmin(t)}.Customize(mux)

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

	// Without the lineage dep, GET /admin/instances/{id}/lineage is never registered,
	// so the mux does not match it: 404.
	rr := do(mux, newGetRequest(t, "/admin/instances/some-instance/lineage"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 lineage absent, got %d", rr.Code)
	}
}
