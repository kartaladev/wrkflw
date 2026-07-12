// Package stdlib_test exercises the stdlib net/http adapter via black-box tests.
// It uses the real in-memory service harness from internal/transporttest so the
// full service layer runs without mocks.
package stdlib_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// helpers -----------------------------------------------------------------------

// decodeJSON decodes the response body into v. Fails the test on error.
func decodeJSON(t *testing.T, body *bytes.Buffer, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v (body=%s)", err, body)
	}
}

// do sends req to the mux and returns the recorder.
func do(mux *http.ServeMux, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// jsonBody creates a JSON-encoded strings.Reader from v.
func jsonBody(t *testing.T, v any) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return strings.NewReader(string(b))
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

// newDeleteRequest creates a DELETE request with a JSON body.
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
// Fakes / stubs

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

// newAlwaysPoliciesAdmin returns a MockPolicyAdmin configured to succeed on
// every call, suitable for tests that wire a Policies dep to exercise routing
// without caring about specific policy data.
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

// ---------------------------------------------------------------------------
// Tests

// TestMount_StartInstance verifies that POST /instances creates an instance (201).
func TestMount_StartInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/instances", map[string]any{
		"def_ref": "greeting",
		"vars":    map[string]any{"name": "ada"},
	})
	rr := do(mux, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (body=%s)", rr.Code, rr.Body)
	}
	var resp map[string]any
	decodeJSON(t, rr.Body, &resp)
	if resp["instance_id"] == nil {
		t.Fatalf("want instance_id in response, got %v", resp)
	}
}

// TestMount_StartInstance_MissingFields verifies missing required fields → 400.
func TestMount_StartInstance_MissingFields(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	tests := map[string]map[string]any{
		"missing def_ref": {
			"def_ref": "",
		},
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := newPostRequest(t, "/instances", body)
			rr := do(mux, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (body=%s)", rr.Code, rr.Body)
			}
			var errBody map[string]any
			decodeJSON(t, rr.Body, &errBody)
			if errBody["message"] == nil || errBody["message"] == "" {
				t.Fatalf("want error message in 400 response, got %v", errBody)
			}
		})
	}
}

// TestMount_GetInstance verifies GET /instances/{id} resolves the path param.
func TestMount_GetInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	// Seed an instance.
	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	instanceID := pi.State().InstanceID

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := do(mux, newGetRequest(t, "/instances/"+instanceID))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", rr.Code, rr.Body)
	}
	var resp map[string]any
	decodeJSON(t, rr.Body, &resp)
	if resp["instance_id"] != instanceID {
		t.Fatalf("want instance_id=%s, got %v", instanceID, resp)
	}
}

// TestMount_GetInstance_NotFound verifies unknown id → 404.
func TestMount_GetInstance_NotFound(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := do(mux, newGetRequest(t, "/instances/no-such-id"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestMount_WithBasePath verifies WithBasePath("/api/v1/workflow") shifts routes.
func TestMount_WithBasePath(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, stdlib.WithBasePath("/api/v1/workflow"))

	// Seed an instance.
	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	instanceID := pi.State().InstanceID

	// Route under base path works.
	rrGood := do(mux, newGetRequest(t, "/api/v1/workflow/instances/"+instanceID))
	if rrGood.Code != http.StatusOK {
		t.Fatalf("want 200 under base path, got %d (body=%s)", rrGood.Code, rrGood.Body)
	}

	// The un-prefixed path is now 404 (no route registered there).
	rrOld := do(mux, newGetRequest(t, "/instances/"+instanceID))
	if rrOld.Code != http.StatusNotFound {
		t.Fatalf("want 404 (no route) for old path, got %d (body=%s)", rrOld.Code, rrOld.Body)
	}
}

// TestMount_AdminAbsentUntilCustomize verifies admin routes are absent unless
// AdminRoutes.Customize is called.
func TestMount_AdminAbsentUntilCustomize(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc) // admin NOT mounted

	rr := do(mux, newGetRequest(t, "/admin/instances"))
	// stdlib mux returns 404 for unregistered routes.
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 (no admin route), got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_Customize registers admin routes explicitly.
func TestAdminRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	// Seed an instance so GET /admin/instances returns a result.
	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/instances"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", rr.Code, rr.Body)
	}
	var resp map[string]any
	decodeJSON(t, rr.Body, &resp)
	if resp["items"] == nil {
		t.Fatalf("want items in response, got %v", resp)
	}
}

// TestAdminRoutes_ConditionalDep_NilDeadLetters verifies that a conditional route
// (dead-letters) returns 404 when its dep is nil.
func TestAdminRoutes_ConditionalDep_NilDeadLetters(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	// DeadLetters is nil — the routes should NOT be registered.
	stdlib.AdminRoutes{Svc: svc, DeadLetters: nil}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/dead-letters"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 (dead-letters dep nil), got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestAdminRoutes_WithPolicies verifies that policy routes are registered when
// Policies dep is set.
func TestAdminRoutes_WithPolicies(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc, Policies: newAlwaysPoliciesAdmin(t)}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/policies"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 policies, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestHealthRoutes_Live verifies GET /healthz returns 200.
func TestHealthRoutes_Live(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	stdlib.MountHealth(mux)

	rr := do(mux, newGetRequest(t, "/healthz"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 healthz, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestHealthRoutes_Ready_OK verifies GET /readyz returns 200 when all checks pass.
func TestHealthRoutes_Ready_OK(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	stdlib.MountHealth(mux, httpcore.HealthCheckFunc("db", func(_ context.Context) error {
		return nil
	}))

	rr := do(mux, newGetRequest(t, "/readyz"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 readyz, got %d (body=%s)", rr.Code, rr.Body)
	}
	var resp map[string]any
	decodeJSON(t, rr.Body, &resp)
	if resp["status"] != "ok" {
		t.Fatalf("want status=ok, got %v", resp)
	}
}

// TestHealthRoutes_Ready_Fail verifies GET /readyz returns 503 when a check fails.
func TestHealthRoutes_Ready_Fail(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	stdlib.MountHealth(mux, httpcore.HealthCheckFunc("db", func(_ context.Context) error {
		return context.DeadlineExceeded
	}))

	rr := do(mux, newGetRequest(t, "/readyz"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 readyz, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestMount_5xx_NoRawError verifies that internal errors do NOT leak raw messages.
func TestMount_5xx_NoRawError(t *testing.T) {
	t.Parallel()

	// Use a service that always returns an internal (unclassified) error.
	svc := &alwaysErrorService{err: errInternal}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/instances", map[string]any{
		"def_ref": "greeting",
	})
	rr := do(mux, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d (body=%s)", rr.Code, rr.Body)
	}
	// The raw error message must NOT appear in the response body.
	if strings.Contains(rr.Body.String(), errInternal.Error()) {
		t.Fatalf("raw error message must not appear in 5xx response (body=%s)", rr.Body)
	}
	var errBody map[string]any
	decodeJSON(t, rr.Body, &errBody)
	// message must be absent or empty string for 5xx.
	if msg, ok := errBody["message"]; ok && msg != "" {
		t.Fatalf("message field must be empty/absent in 5xx response, got %v", msg)
	}
}

// TestMessageRoutes_Customize verifies POST /messages returns 202.
func TestMessageRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.MessageProcess("order-shipped")
	_, svc := transporttest.NewHarness(t, def)

	// Seed a waiting instance.
	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("message-catch-order-shipped"),
		Vars:   map[string]any{"orderId": "42"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/messages", map[string]any{
		"def_ref":         "message-catch-order-shipped:1",
		"name":            "order-shipped",
		"correlation_key": "42",
	})
	rr := do(mux, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestTaskRoutes_Customize verifies POST /tasks/{token}/claim returns 200.
func TestTaskRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskToken := transporttest.StartedApprovalInstance(t, h, "task-claim-stdlib-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/tasks/"+taskToken+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	rr := do(mux, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 claim, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestInstanceRoutes_Snapshot verifies GET /instances/{id}/snapshot returns 200.
func TestInstanceRoutes_Snapshot(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := do(mux, newGetRequest(t, "/instances/"+pi.State().InstanceID+"/snapshot"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 snapshot, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestInstanceRoutes_ActionableView verifies GET /instances/{id}/actionable returns 200.
func TestInstanceRoutes_ActionableView(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("approval"),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := do(mux, newGetRequest(t, "/instances/"+pi.State().InstanceID+"/actionable"))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 actionable, got %d (body=%s)", rr.Code, rr.Body)
	}
}

// TestDeliverSignal_Stdlib verifies POST /instances/{id}/signals returns 200.
func TestDeliverSignal_Stdlib(t *testing.T) {
	t.Parallel()

	def := transporttest.SignalProcess("approved")
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("signal-catch-approved"),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	req := newPostRequest(t, "/instances/"+pi.State().InstanceID+"/signals", map[string]any{
		"signal": "approved",
	})
	rr := do(mux, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 signal, got %d (body=%s)", rr.Code, rr.Body)
	}
}
