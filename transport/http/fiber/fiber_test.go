// Package fiber_test exercises the fiber v3 adapter via black-box tests.
// It uses the real in-memory service harness from internal/transporttest so the
// full service layer runs without mocks.
package fiber_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	fiberlib "github.com/gofiber/fiber/v3"
	"go.uber.org/mock/gomock"

	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/fiber"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// ---------------------------------------------------------------------------
// Helpers

// newApp creates a fresh fiber app for testing. No special config needed —
// app.Test() does not start the server so no startup banner is printed.
func newApp() *fiberlib.App {
	return fiberlib.New()
}

// appDo drives req through app, reads and closes the response body, and returns
// the status code and body string. The body is always closed before return,
// satisfying the bodyclose linter.
func appDo(t *testing.T, app *fiberlib.App, req *http.Request) (statusCode int, body string) {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, err2 := io.ReadAll(resp.Body)
	if err2 != nil {
		t.Fatalf("ReadAll: %v", err2)
	}
	return resp.StatusCode, string(b)
}

// appDoJSON drives req through app, reads and closes the body, and decodes the
// JSON result into v. Returns the status code.
func appDoJSON(t *testing.T, app *fiberlib.App, req *http.Request, v any) int {
	t.Helper()
	status, body := appDo(t, app, req)
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode JSON (status=%d body=%s): %v", status, body, err)
	}
	return status
}

// jsonBody returns a *bytes.Reader containing the JSON encoding of v.
func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return bytes.NewReader(b)
}

// newPostRequest creates a POST request with a JSON body.
func newPostRequest(t *testing.T, path string, body any) *http.Request {
	t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		r, err = http.NewRequest(http.MethodPost, path, jsonBody(t, body))
	} else {
		r, err = http.NewRequest(http.MethodPost, path, http.NoBody)
	}
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r.WithContext(t.Context())
}

// newGetRequest creates a GET request.
func newGetRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, path, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return r.WithContext(t.Context())
}

// newDeleteRequest creates a DELETE request with optional JSON body.
func newDeleteRequest(t *testing.T, path string, body any) *http.Request {
	t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		r, err = http.NewRequest(http.MethodDelete, path, jsonBody(t, body))
	} else {
		r, err = http.NewRequest(http.MethodDelete, path, http.NoBody)
	}
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r.WithContext(t.Context())
}

// ---------------------------------------------------------------------------
// Non-admin fakes (service.Service stub — NOT replaced by mockgen)

var errInternal = errors.New("db connection refused: internal secret dsn info")

// alwaysErrorService is a minimal service.Service stub that returns err for
// every operation. Used to verify 5xx responses do not leak raw messages.
type alwaysErrorService struct {
	err             error
	service.Service // embed to satisfy unused methods
}

func (s *alwaysErrorService) StartInstance(_ context.Context, _ service.StartInstanceRequest) (service.ProcessInstance, error) {
	return nil, s.err
}

// ---------------------------------------------------------------------------
// Admin mock factories

// newAlwaysPoliciesAdmin returns a MockPolicyAdmin configured to always succeed.
func newAlwaysPoliciesAdmin(t *testing.T) service.PolicyAdmin {
	t.Helper()
	m := service.NewMockPolicyAdmin(gomock.NewController(t))
	m.EXPECT().AddPolicy(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	m.EXPECT().RemovePolicy(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	m.EXPECT().ListPolicies(gomock.Any()).Return(
		[]service.PolicyRule{{Subject: "alice", Object: "instances", Action: "read"}}, nil).AnyTimes()
	m.EXPECT().AddRole(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	m.EXPECT().RemoveRole(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	m.EXPECT().ListRoles(gomock.Any()).Return(
		[]service.RoleBinding{{User: "alice", Role: "manager"}}, nil).AnyTimes()
	return m
}

// newAlwaysDeadLetterAdmin returns a MockDeadLetterAdmin that always succeeds with empty results.
// It does NOT register a Redrive expectation — tests that invoke Redrive must set it up inline.
func newAlwaysDeadLetterAdmin(t *testing.T) service.DeadLetterAdmin {
	t.Helper()
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	return m
}

// newAlwaysRelayStatsAdmin returns a MockRelayStatsAdmin that always succeeds with zero stats.
func newAlwaysRelayStatsAdmin(t *testing.T) service.RelayStatsAdmin {
	t.Helper()
	m := service.NewMockRelayStatsAdmin(gomock.NewController(t))
	m.EXPECT().OutboxStats(gomock.Any()).Return(kernel.OutboxStats{}, nil).AnyTimes()
	return m
}

// newAlwaysTimerAdmin returns a MockTimerAdmin that always succeeds with empty results.
func newAlwaysTimerAdmin(t *testing.T) service.TimerAdmin {
	t.Helper()
	m := service.NewMockTimerAdmin(gomock.NewController(t))
	m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{}, nil).AnyTimes()
	m.EXPECT().ListArmed(gomock.Any()).Return([]kernel.ArmedTimer{}, nil).AnyTimes()
	return m
}

// newAlwaysLineageAdmin returns a MockLineageAdmin that always succeeds with a root lineage.
func newAlwaysLineageAdmin(t *testing.T) service.LineageAdmin {
	t.Helper()
	m := service.NewMockLineageAdmin(gomock.NewController(t))
	m.EXPECT().Lineage(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, instanceID string) (kernel.InstanceLineage, error) {
			return kernel.InstanceLineage{
				InstanceID:      instanceID,
				CallChildren:    []kernel.CallLinkRef{},
				ChainSuccessors: []kernel.ChainLinkRef{},
			}, nil
		}).AnyTimes()
	return m
}

// ---------------------------------------------------------------------------
// Tests — instance routes

// TestMount_StartInstance verifies that POST /instances creates an instance (201).
func TestMount_StartInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	app := newApp()
	fiber.Mount(app, svc)

	var result map[string]any
	status := appDoJSON(t, app, newPostRequest(t, "/instances", map[string]any{
		"def_ref":     "greeting",
		"instance_id": "start-fiber-1",
		"vars":        map[string]any{"name": "ada"},
	}), &result)

	if status != http.StatusCreated {
		t.Fatalf("want 201, got %d", status)
	}
	if result["instance_id"] == nil {
		t.Fatalf("want instance_id in response, got %v", result)
	}
}

// TestMount_StartInstance_MissingFields verifies missing required fields → 400.
func TestMount_StartInstance_MissingFields(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	app := newApp()
	fiber.Mount(app, svc)

	tests := map[string]map[string]any{
		"missing def_ref": {
			"def_ref": "",
		},
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var errBody map[string]any
			status := appDoJSON(t, app, newPostRequest(t, "/instances", body), &errBody)

			if status != http.StatusBadRequest {
				t.Fatalf("want 400, got %d", status)
			}
			if errBody["message"] == nil || errBody["message"] == "" {
				t.Fatalf("want error message in 400 response, got %v", errBody)
			}
		})
	}
}

// TestMount_GetInstance verifies GET /instances/:id resolves the path param.
func TestMount_GetInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	instanceID := pi.State().InstanceID

	app := newApp()
	fiber.Mount(app, svc)

	var result map[string]any
	status := appDoJSON(t, app, newGetRequest(t, "/instances/"+instanceID), &result)
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if result["instance_id"] != instanceID {
		t.Fatalf("want instance_id=%s, got %v", instanceID, result)
	}
}

// TestMount_GetInstance_NotFound verifies unknown id → 404.
func TestMount_GetInstance_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newGetRequest(t, "/instances/no-such-id"))
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d (body=%s)", status, body)
	}
}

// TestMount_WithBasePath verifies WithBasePath("/api/v1/workflow") shifts routes.
func TestMount_WithBasePath(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	instanceID := pi.State().InstanceID

	app := newApp()
	fiber.Mount(app, svc, fiber.WithBasePath("/api/v1/workflow"))

	// Route under base path works.
	status, body := appDo(t, app, newGetRequest(t, "/api/v1/workflow/instances/"+instanceID))
	if status != http.StatusOK {
		t.Fatalf("want 200 under base path, got %d (body=%s)", status, body)
	}

	// The un-prefixed path is now 404 (no route registered there).
	status2, _ := appDo(t, app, newGetRequest(t, "/instances/"+instanceID))
	if status2 != http.StatusNotFound {
		t.Fatalf("want 404 (no route) for old path, got %d", status2)
	}
}

// TestMount_NativeGroup verifies that using app.Group("/base") then mounting works.
func TestMount_NativeGroup(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	grp := app.Group("/v2")
	fiber.Mount(grp, svc)

	status, body := appDo(t, app, newGetRequest(t, "/v2/instances/"+pi.State().InstanceID))
	if status != http.StatusOK {
		t.Fatalf("want 200 via native group, got %d (body=%s)", status, body)
	}
}

// TestMount_WithMiddleware verifies that WithMiddleware(mw) runs before handlers.
func TestMount_WithMiddleware(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	called := false
	mw := func(c fiberlib.Ctx) error {
		called = true
		return c.Next()
	}

	app := newApp()
	fiber.Mount(app, svc, fiber.WithMiddleware(mw))

	// Hit any route — we just need the middleware to fire.
	appDo(t, app, newGetRequest(t, "/instances/any-id"))

	if !called {
		t.Fatal("want middleware to have been called")
	}
}

// TestMount_AdminAbsentByDefault verifies admin routes are absent when only
// Mount (not AdminRoutes.Customize) is called.
func TestMount_AdminAbsentByDefault(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.Mount(app, svc) // admin NOT mounted

	status, _ := appDo(t, app, newGetRequest(t, "/admin/instances"))
	// fiber returns 404 for unregistered routes.
	if status != http.StatusNotFound {
		t.Fatalf("want 404 (no admin route), got %d", status)
	}
}

// TestAdminRoutes_Customize registers admin routes explicitly.
func TestAdminRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.AdminRoutes{Svc: svc}.Customize(app)

	var result map[string]any
	status := appDoJSON(t, app, newGetRequest(t, "/admin/instances"), &result)
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if result["items"] == nil {
		t.Fatalf("want items in response, got %v", result)
	}
}

// TestAdminRoutes_ConditionalDep_NilDeadLetters verifies that a conditional route
// (dead-letters) returns 404 when its dep is nil.
func TestAdminRoutes_ConditionalDep_NilDeadLetters(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	// DeadLetters is nil — the routes should NOT be registered.
	fiber.AdminRoutes{Svc: svc, DeadLetters: nil}.Customize(app)

	status, _ := appDo(t, app, newGetRequest(t, "/admin/dead-letters"))
	if status != http.StatusNotFound {
		t.Fatalf("want 404 (dead-letters dep nil), got %d", status)
	}
}

// TestAdminRoutes_ConditionalDep_NilPolicies verifies policy admin routes absent when nil.
func TestAdminRoutes_ConditionalDep_NilPolicies(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: nil}.Customize(app)

	status, _ := appDo(t, app, newGetRequest(t, "/admin/policies"))
	if status != http.StatusNotFound {
		t.Fatalf("want 404 (policies dep nil), got %d", status)
	}
}

// TestHealthRoutes_Live verifies GET /healthz returns 200.
func TestHealthRoutes_Live(t *testing.T) {
	t.Parallel()

	app := newApp()
	fiber.MountHealth(app)

	status, body := appDo(t, app, newGetRequest(t, "/healthz"))
	if status != http.StatusOK {
		t.Fatalf("want 200 healthz, got %d (body=%s)", status, body)
	}
}

// TestHealthRoutes_Ready_OK verifies GET /readyz returns 200 when all checks pass.
func TestHealthRoutes_Ready_OK(t *testing.T) {
	t.Parallel()

	app := newApp()
	fiber.MountHealth(app, httpcore.HealthCheckFunc("db", func(_ context.Context) error {
		return nil
	}))

	var result map[string]any
	status := appDoJSON(t, app, newGetRequest(t, "/readyz"), &result)
	if status != http.StatusOK {
		t.Fatalf("want 200 readyz, got %d", status)
	}
	if result["status"] != "ok" {
		t.Fatalf("want status=ok, got %v", result)
	}
}

// TestHealthRoutes_Ready_Fail verifies GET /readyz returns 503 when a check fails.
func TestHealthRoutes_Ready_Fail(t *testing.T) {
	t.Parallel()

	app := newApp()
	fiber.MountHealth(app, httpcore.HealthCheckFunc("db", func(_ context.Context) error {
		return context.DeadlineExceeded
	}))

	status, body := appDo(t, app, newGetRequest(t, "/readyz"))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("want 503 readyz, got %d (body=%s)", status, body)
	}
}

// TestMount_5xx_NoRawError verifies internal errors do NOT leak raw messages.
func TestMount_5xx_NoRawError(t *testing.T) {
	t.Parallel()

	svc := &alwaysErrorService{err: errInternal}

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newPostRequest(t, "/instances", map[string]any{
		"def_ref":     "greeting",
		"instance_id": "x",
	}))

	if status != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", status)
	}
	if strings.Contains(body, errInternal.Error()) {
		t.Fatalf("raw error message must not appear in 5xx response (body=%s)", body)
	}
	var errBody map[string]any
	if err := json.Unmarshal([]byte(body), &errBody); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if msg, ok := errBody["message"]; ok && msg != "" {
		t.Fatalf("message field must be empty/absent in 5xx response, got %v", msg)
	}
}

// TestMessageRoutes_Customize verifies POST /messages returns 202.
func TestMessageRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.MessageProcess("order-shipped")
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "message-catch-order-shipped",
		Vars:   map[string]any{"orderId": "42"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newPostRequest(t, "/messages", map[string]any{
		"def_ref":         "message-catch-order-shipped:1",
		"name":            "order-shipped",
		"correlation_key": "42",
	}))

	if status != http.StatusAccepted {
		t.Fatalf("want 202, got %d (body=%s)", status, body)
	}
}

// TestTaskRoutes_Customize verifies POST /tasks/:token/claim returns 200.
func TestTaskRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskToken := transporttest.StartedApprovalInstance(t, h, "task-claim-fiber-1")

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskToken+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	}))

	if status != http.StatusOK {
		t.Fatalf("want 200 claim, got %d (body=%s)", status, body)
	}
}

// TestTaskRoutes_Complete verifies POST /tasks/:token/complete returns 200.
func TestTaskRoutes_Complete(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskToken := transporttest.StartedApprovalInstance(t, h, "task-complete-fiber-1")

	app := newApp()
	fiber.Mount(app, svc)

	// Claim first, then complete.
	statusClaim, bodyClaim := appDo(t, app, newPostRequest(t, "/tasks/"+taskToken+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	}))
	if statusClaim != http.StatusOK {
		t.Fatalf("claim want 200, got %d (body=%s)", statusClaim, bodyClaim)
	}

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskToken+"/complete", map[string]any{
		"actor":  map[string]any{"id": "alice", "roles": []string{"manager"}},
		"output": map[string]any{"approved": true},
	}))
	if status != http.StatusOK {
		t.Fatalf("complete want 200, got %d (body=%s)", status, body)
	}
}

// TestTaskRoutes_Reassign verifies POST /tasks/:token/reassign returns 200.
// The task must be claimed by alice first before it can be reassigned from alice.
func TestTaskRoutes_Reassign(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskToken := transporttest.StartedApprovalInstance(t, h, "task-reassign-fiber-1")

	app := newApp()
	fiber.Mount(app, svc)

	// Claim first so alice is the claimant.
	statusClaim, bodyClaim := appDo(t, app, newPostRequest(t, "/tasks/"+taskToken+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	}))
	if statusClaim != http.StatusOK {
		t.Fatalf("claim want 200, got %d (body=%s)", statusClaim, bodyClaim)
	}

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskToken+"/reassign", map[string]any{
		"from": "alice",
		"to":   "bob",
		"by":   map[string]any{"id": "alice", "roles": []string{"manager"}},
	}))
	if status != http.StatusOK {
		t.Fatalf("reassign want 200, got %d (body=%s)", status, body)
	}
}

// TestInstanceRoutes_Snapshot verifies GET /instances/:id/snapshot returns 200.
func TestInstanceRoutes_Snapshot(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newGetRequest(t, "/instances/"+pi.State().InstanceID+"/snapshot"))
	if status != http.StatusOK {
		t.Fatalf("want 200 snapshot, got %d (body=%s)", status, body)
	}
}

// TestInstanceRoutes_ActionableView verifies GET /instances/:id/actionable returns 200.
func TestInstanceRoutes_ActionableView(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "approval",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newGetRequest(t, "/instances/"+pi.State().InstanceID+"/actionable"))
	if status != http.StatusOK {
		t.Fatalf("want 200 actionable, got %d (body=%s)", status, body)
	}
}

// TestDeliverSignal_Fiber verifies POST /instances/:id/signals returns 200.
func TestDeliverSignal_Fiber(t *testing.T) {
	t.Parallel()

	def := transporttest.SignalProcess("approved")
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "signal-catch-approved",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newPostRequest(t, "/instances/"+pi.State().InstanceID+"/signals", map[string]any{
		"signal": "approved",
	}))

	if status != http.StatusOK {
		t.Fatalf("want 200 signal, got %d (body=%s)", status, body)
	}
}

// ---------------------------------------------------------------------------
// Tests — admin routes

// TestPoliciesAdmin_WithPolicies verifies policy admin routes work when dep provided.
func TestPoliciesAdmin_WithPolicies(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/policies"))
	if status != http.StatusOK {
		t.Fatalf("want 200 list policies, got %d (body=%s)", status, body)
	}
}

// TestDeleteAdminPolicy verifies DELETE /admin/policies returns 200.
func TestDeleteAdminPolicy(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newDeleteRequest(t, "/admin/policies", map[string]any{
		"subject": "alice",
		"object":  "instances",
		"action":  "read",
	}))
	if status != http.StatusOK {
		t.Fatalf("want 200 delete policy, got %d (body=%s)", status, body)
	}
}

// TestListRoleBindings verifies GET /admin/role-bindings returns 200.
func TestListRoleBindings(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/role-bindings"))
	if status != http.StatusOK {
		t.Fatalf("want 200 list role bindings, got %d (body=%s)", status, body)
	}
}

// TestAdminDeadLetters_List verifies GET /admin/dead-letters returns 200 with dep.
func TestAdminDeadLetters_List(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, DeadLetters: newAlwaysDeadLetterAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/dead-letters"))
	if status != http.StatusOK {
		t.Fatalf("want 200 dead-letters, got %d (body=%s)", status, body)
	}
}

// TestAdminDeadLetters_Redrive verifies POST /admin/dead-letters/redrive returns 200.
func TestAdminDeadLetters_Redrive(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	// Use inline mock to set specific expectations for the exact ids in this test.
	m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
	m.EXPECT().Redrive(gomock.Any(), int64(1), int64(2), int64(3)).Return(3, nil)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, DeadLetters: m}.Customize(app)

	status, body := appDo(t, app, newPostRequest(t, "/admin/dead-letters/redrive", map[string]any{
		"ids": []int64{1, 2, 3},
	}))
	if status != http.StatusOK {
		t.Fatalf("want 200 redrive, got %d (body=%s)", status, body)
	}
}

// TestAdminRelayStats verifies GET /admin/relay-stats returns 200.
func TestAdminRelayStats(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, RelayStats: newAlwaysRelayStatsAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/relay-stats"))
	if status != http.StatusOK {
		t.Fatalf("want 200 relay-stats, got %d (body=%s)", status, body)
	}
}

// TestAdminTimers verifies GET /admin/timers returns 200.
func TestAdminTimers(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Timers: newAlwaysTimerAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/timers"))
	if status != http.StatusOK {
		t.Fatalf("want 200 timers, got %d (body=%s)", status, body)
	}
}

// TestAdminLineage verifies GET /admin/instances/:id/lineage returns 200.
func TestAdminLineage(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Lineage: newAlwaysLineageAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/instances/some-id/lineage"))
	if status != http.StatusOK {
		t.Fatalf("want 200 lineage, got %d (body=%s)", status, body)
	}
}

// TestAddRoleBinding verifies POST /admin/role-bindings returns 200.
func TestAddRoleBinding(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newPostRequest(t, "/admin/role-bindings", map[string]any{
		"user": "alice",
		"role": "manager",
	}))
	if status != http.StatusOK {
		t.Fatalf("want 200 add role binding, got %d (body=%s)", status, body)
	}
}

// TestDeleteRoleBinding verifies DELETE /admin/role-bindings returns 200.
func TestDeleteRoleBinding(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newDeleteRequest(t, "/admin/role-bindings", map[string]any{
		"user": "alice",
		"role": "manager",
	}))
	if status != http.StatusOK {
		t.Fatalf("want 200 delete role binding, got %d (body=%s)", status, body)
	}
}

// TestAddPolicy verifies POST /admin/policies returns 200.
func TestAddPolicy(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(app)

	status, body := appDo(t, app, newPostRequest(t, "/admin/policies", map[string]any{
		"subject": "alice",
		"object":  "instances",
		"action":  "write",
	}))
	if status != http.StatusOK {
		t.Fatalf("want 200 add policy, got %d (body=%s)", status, body)
	}
}

// TestAdminCancelInstance verifies POST /admin/instances/:id/cancel returns 200.
// Uses an approval process so the instance parks at a user task (StatusRunning).
func TestAdminCancelInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	// Start the approval instance — it parks at the user task.
	_ = transporttest.StartedApprovalInstance(t, h, "cancel-fiber-1")

	app := newApp()
	fiber.AdminRoutes{Svc: svc}.Customize(app)

	status, body := appDo(t, app, newPostRequest(t, "/admin/instances/cancel-fiber-1/cancel", nil))
	if status != http.StatusOK {
		t.Fatalf("want 200 cancel, got %d (body=%s)", status, body)
	}
}

// TestAdminResolveIncident_NotFound verifies POST resolve with optional body → 404.
// We use a non-existent instance to get a 404 (still tests the body-bind path).
func TestAdminResolveIncident_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc}.Customize(app)

	status, _ := appDo(t, app, newPostRequest(t, "/admin/instances/no-such-id/incidents/inc-1/resolve",
		map[string]any{"add_attempts": 1}))
	if status != http.StatusNotFound {
		t.Fatalf("want 404 (not found), got %d", status)
	}
}

// TestAdminListInstances_WithStatusFilter verifies GET /admin/instances with status+limit.
func TestAdminListInstances_WithStatusFilter(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.AdminRoutes{Svc: svc}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/instances?status=running&limit=10"))
	if status != http.StatusOK {
		t.Fatalf("want 200 list, got %d (body=%s)", status, body)
	}
}

// TestAdminListInstances_BadStatus verifies an unknown status query param returns 400.
func TestAdminListInstances_BadStatus(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc}.Customize(app)

	status, body := appDo(t, app, newGetRequest(t, "/admin/instances?status=unknown-status"))
	if status != http.StatusBadRequest {
		t.Fatalf("want 400 bad status, got %d (body=%s)", status, body)
	}
}
