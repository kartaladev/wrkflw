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
	"net/http/httptest"
	"strings"
	"testing"

	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/fiber"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// ---------------------------------------------------------------------------
// Helpers

// newApp creates a fresh fiber app with disabled startup message and no delay
// on test shutdown. It returns the app; callers call appTest(t, app, req).
func newApp() *fiberlib.App {
	return fiberlib.New(fiberlib.Config{DisableStartupMessage: true})
}

// appTest drives req through app and returns the *http.Response.
func appTest(t *testing.T, app *fiberlib.App, req *http.Request) *http.Response {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

// readBody reads the full response body as a string.
func readBody(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(b)
}

// decodeJSON decodes the response body into v.
func decodeJSON(t *testing.T, r io.Reader, v any) {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode JSON: %v (body=%s)", err, b)
	}
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
		r, err = httptest.NewRequest(http.MethodPost, path, jsonBody(t, body))
	} else {
		r, err = httptest.NewRequest(http.MethodPost, path, http.NoBody)
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
	r, err := httptest.NewRequest(http.MethodGet, path, http.NoBody)
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
		r, err = httptest.NewRequest(http.MethodDelete, path, jsonBody(t, body))
	} else {
		r, err = httptest.NewRequest(http.MethodDelete, path, http.NoBody)
	}
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r.WithContext(t.Context())
}

// ---------------------------------------------------------------------------
// Fakes

var errInternal = errors.New("db connection refused: internal secret dsn info")

// alwaysErrorService is a minimal service.Service stub that returns err for
// every operation. Used to verify 5xx responses do not leak raw messages.
type alwaysErrorService struct {
	err error
	service.Service // embed to satisfy unused methods
}

func (s *alwaysErrorService) StartInstance(_ context.Context, _ service.StartInstanceRequest) (engine.InstanceState, error) {
	return engine.InstanceState{}, s.err
}

// alwaysPoliciesAdmin is a PolicyAdmin that always succeeds.
type alwaysPoliciesAdmin struct{}

func (alwaysPoliciesAdmin) AddPolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return true, nil
}
func (alwaysPoliciesAdmin) RemovePolicy(_ context.Context, _ service.PolicyRule) (bool, error) {
	return true, nil
}
func (alwaysPoliciesAdmin) ListPolicies(_ context.Context) ([]service.PolicyRule, error) {
	return []service.PolicyRule{{Subject: "alice", Object: "instances", Action: "read"}}, nil
}
func (alwaysPoliciesAdmin) AddRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return true, nil
}
func (alwaysPoliciesAdmin) RemoveRole(_ context.Context, _ service.RoleBinding) (bool, error) {
	return true, nil
}
func (alwaysPoliciesAdmin) ListRoles(_ context.Context) ([]service.RoleBinding, error) {
	return []service.RoleBinding{{User: "alice", Role: "manager"}}, nil
}

// ---------------------------------------------------------------------------
// Tests

// TestMount_StartInstance verifies that POST /instances creates an instance (201).
func TestMount_StartInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newPostRequest(t, "/instances", map[string]any{
		"def_ref":     "greeting",
		"instance_id": "start-fiber-1",
		"vars":        map[string]any{"name": "ada"},
	}))

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
	var result map[string]any
	decodeJSON(t, resp.Body, &result)
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
			"def_ref":     "",
			"instance_id": "x",
		},
		"missing instance_id": {
			"def_ref":     "greeting",
			"instance_id": "",
		},
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resp := appTest(t, app, newPostRequest(t, "/instances", body))

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
			}
			var errBody map[string]any
			decodeJSON(t, resp.Body, &errBody)
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

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "get-fiber-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newGetRequest(t, "/instances/get-fiber-1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
	var result map[string]any
	decodeJSON(t, resp.Body, &result)
	if result["instance_id"] != "get-fiber-1" {
		t.Fatalf("want instance_id=get-fiber-1, got %v", result)
	}
}

// TestMount_GetInstance_NotFound verifies unknown id → 404.
func TestMount_GetInstance_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newGetRequest(t, "/instances/no-such-id"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestMount_WithBasePath verifies WithBasePath("/api/v1/workflow") shifts routes.
func TestMount_WithBasePath(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "base-path-fiber-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc, fiber.WithBasePath("/api/v1/workflow"))

	// Route under base path works.
	resp := appTest(t, app, newGetRequest(t, "/api/v1/workflow/instances/base-path-fiber-1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 under base path, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}

	// The un-prefixed path is now 404 (no route registered there).
	resp2 := appTest(t, app, newGetRequest(t, "/instances/base-path-fiber-1"))
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 (no route) for old path, got %d", resp2.StatusCode)
	}
}

// TestMount_NativeGroup verifies that using app.Group("/base") then mounting works.
func TestMount_NativeGroup(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "native-group-fiber-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	grp := app.Group("/v2")
	fiber.Mount(grp, svc)

	resp := appTest(t, app, newGetRequest(t, "/v2/instances/native-group-fiber-1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 via native group, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
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
	appTest(t, app, newGetRequest(t, "/instances/any-id"))

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

	resp := appTest(t, app, newGetRequest(t, "/admin/instances"))
	// fiber returns 404 for unregistered routes.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 (no admin route), got %d", resp.StatusCode)
	}
}

// TestAdminRoutes_Customize registers admin routes explicitly.
func TestAdminRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "admin-list-fiber-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.AdminRoutes{Svc: svc}.Customize(app)

	resp := appTest(t, app, newGetRequest(t, "/admin/instances"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
	var result map[string]any
	decodeJSON(t, resp.Body, &result)
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

	resp := appTest(t, app, newGetRequest(t, "/admin/dead-letters"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 (dead-letters dep nil), got %d", resp.StatusCode)
	}
}

// TestAdminRoutes_ConditionalDep_NilPolicies verifies policy admin routes absent when nil.
func TestAdminRoutes_ConditionalDep_NilPolicies(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: nil}.Customize(app)

	resp := appTest(t, app, newGetRequest(t, "/admin/policies"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 (policies dep nil), got %d", resp.StatusCode)
	}
}

// TestHealthRoutes_Live verifies GET /healthz returns 200.
func TestHealthRoutes_Live(t *testing.T) {
	t.Parallel()

	app := newApp()
	fiber.MountHealth(app)

	resp := appTest(t, app, newGetRequest(t, "/healthz"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 healthz, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestHealthRoutes_Ready_OK verifies GET /readyz returns 200 when all checks pass.
func TestHealthRoutes_Ready_OK(t *testing.T) {
	t.Parallel()

	app := newApp()
	fiber.MountHealth(app, httpcore.HealthCheckFunc("db", func(_ context.Context) error {
		return nil
	}))

	resp := appTest(t, app, newGetRequest(t, "/readyz"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 readyz, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
	var result map[string]any
	decodeJSON(t, resp.Body, &result)
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

	resp := appTest(t, app, newGetRequest(t, "/readyz"))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 readyz, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestMount_5xx_NoRawError verifies internal errors do NOT leak raw messages.
func TestMount_5xx_NoRawError(t *testing.T) {
	t.Parallel()

	svc := &alwaysErrorService{err: errInternal}

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newPostRequest(t, "/instances", map[string]any{
		"def_ref":     "greeting",
		"instance_id": "x",
	}))

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", resp.StatusCode)
	}
	body := readBody(t, resp.Body)
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
		DefRef: "message-catch-order-shipped", InstanceID: "msg-fiber-1",
		Vars: map[string]any{"orderId": "42"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newPostRequest(t, "/messages", map[string]any{
		"def_ref":         "message-catch-order-shipped:1",
		"name":            "order-shipped",
		"correlation_key": "42",
	}))

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
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

	resp := appTest(t, app, newPostRequest(t, "/tasks/"+taskToken+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	}))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 claim, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestInstanceRoutes_Snapshot verifies GET /instances/:id/snapshot returns 200.
func TestInstanceRoutes_Snapshot(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "snap-fiber-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newGetRequest(t, "/instances/snap-fiber-1/snapshot"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 snapshot, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestInstanceRoutes_ActionableView verifies GET /instances/:id/actionable returns 200.
func TestInstanceRoutes_ActionableView(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "approval", InstanceID: "actionable-fiber-1",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newGetRequest(t, "/instances/actionable-fiber-1/actionable"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 actionable, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestDeliverSignal_Fiber verifies POST /instances/:id/signals returns 200.
func TestDeliverSignal_Fiber(t *testing.T) {
	t.Parallel()

	def := transporttest.SignalProcess("approved")
	_, svc := transporttest.NewHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "signal-catch-approved", InstanceID: "signal-fiber-1",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := newApp()
	fiber.Mount(app, svc)

	resp := appTest(t, app, newPostRequest(t, "/instances/signal-fiber-1/signals", map[string]any{
		"signal": "approved",
	}))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 signal, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestPoliciesAdmin_WithPolicies verifies policy admin routes work when dep provided.
func TestPoliciesAdmin_WithPolicies(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: alwaysPoliciesAdmin{}}.Customize(app)

	resp := appTest(t, app, newGetRequest(t, "/admin/policies"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 list policies, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestDeleteAdminPolicy verifies DELETE /admin/policies returns 200.
func TestDeleteAdminPolicy(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: alwaysPoliciesAdmin{}}.Customize(app)

	resp := appTest(t, app, newDeleteRequest(t, "/admin/policies", map[string]any{
		"subject": "alice",
		"object":  "instances",
		"action":  "read",
	}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 delete policy, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}

// TestListRoleBindings verifies GET /admin/role-bindings returns 200.
func TestListRoleBindings(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	app := newApp()
	fiber.AdminRoutes{Svc: svc, Policies: alwaysPoliciesAdmin{}}.Customize(app)

	resp := appTest(t, app, newGetRequest(t, "/admin/role-bindings"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 list role bindings, got %d (body=%s)", resp.StatusCode, readBody(t, resp.Body))
	}
}
