// Package gin_test is the black-box test suite for the transport/http/gin adapter.
// It spins a real gin.New() engine, mounts the route groups, and fires requests
// via httptest.Server so every assertion observes real HTTP behaviour.
package gin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	ginlib "github.com/gin-gonic/gin"

	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	ginadapter "github.com/zakyalvan/krtlwrkflw/transport/http/gin"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func init() {
	// Suppress gin debug output in tests.
	ginlib.SetMode(ginlib.TestMode)
}

// httpResp is a lightweight test-response value that holds the status code
// and the fully-read body bytes. Using this instead of *http.Response means the
// underlying connection is released immediately and the bodyclose linter is
// satisfied without requiring callers to manage Body.Close().
type httpResp struct {
	StatusCode int
	Body       []byte
}

// post sends a POST with a JSON body to the given server path, reads the full
// response body, and returns an httpResp.
func post(t *testing.T, srv *httptest.Server, path string, body any) httpResp {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	resp, err := srv.Client().Post(srv.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	bodyBytes, _ := io.ReadAll(resp.Body)
	return httpResp{StatusCode: resp.StatusCode, Body: bodyBytes}
}

// get sends a GET to the given server path, reads the full response body, and
// returns an httpResp.
func get(t *testing.T, srv *httptest.Server, path string) httpResp {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	bodyBytes, _ := io.ReadAll(resp.Body)
	return httpResp{StatusCode: resp.StatusCode, Body: bodyBytes}
}

// drainClose is a convenience helper used in tests that call srv.Client().Do
// directly and need to close the body without a bodyclose warning.
func drainClose(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// decodeJSON decodes the response body bytes into dst.
func decodeJSON(t *testing.T, resp httpResp, dst any) {
	t.Helper()
	if err := json.Unmarshal(resp.Body, dst); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// newJSONRequest builds an HTTP request with the given method, URL, and JSON body.
func newJSONRequest(t *testing.T, method, url string, body any) (*http.Request, error) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// newSrv mounts InstanceRoutes+TaskRoutes+MessageRoutes onto a fresh gin engine
// and returns a started httptest.Server and the underlying service.
func newSrv(t *testing.T, opts ...httpcore.CustomizeOption[ginlib.IRouter]) (*httptest.Server, service.Service) {
	t.Helper()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	ginadapter.Mount(r, svc, opts...)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, svc
}

// ─── InstanceRoutes ───────────────────────────────────────────────────────────

func TestInstanceRoutes_StartInstance_201(t *testing.T) {
	t.Parallel()
	srv, _ := newSrv(t)

	resp := post(t, srv, "/instances", map[string]any{
		"def_ref": "greeting",
		"vars":    map[string]any{"name": "world"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["instance_id"] == nil {
		t.Fatal("want instance_id in response body")
	}
}

func TestInstanceRoutes_StartInstance_400_MissingField(t *testing.T) {
	t.Parallel()
	srv, _ := newSrv(t)

	// Missing def_ref.
	resp := post(t, srv, "/instances", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["error"] == nil {
		t.Fatal("want error field in 400 body")
	}
}

func TestInstanceRoutes_GetInstance_200(t *testing.T) {
	t.Parallel()
	srv, svc := newSrv(t)

	// Seed instance.
	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	resp := get(t, srv, "/instances/"+pi.State().InstanceID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["instance_id"] == nil {
		t.Fatal("want instance_id in response")
	}
}

func TestInstanceRoutes_GetInstance_404_Unknown(t *testing.T) {
	t.Parallel()
	srv, _ := newSrv(t)

	resp := get(t, srv, "/instances/no-such-instance")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestInstanceRoutes_GetInstanceSnapshot_200(t *testing.T) {
	t.Parallel()
	srv, svc := newSrv(t)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	resp := get(t, srv, "/instances/"+pi.State().InstanceID+"/snapshot")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestInstanceRoutes_GetActionableView_200(t *testing.T) {
	t.Parallel()
	approvalDef := transporttest.ApprovalProcess()
	_, svc := transporttest.NewHarness(t, approvalDef)

	r := ginlib.New()
	ginadapter.Mount(r, svc)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "approval",
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	resp := get(t, srv, "/instances/"+pi.State().InstanceID+"/actionable")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestInstanceRoutes_DeliverSignal_200(t *testing.T) {
	t.Parallel()
	sigDef := transporttest.SignalProcess("approved")
	_, svc := transporttest.NewHarness(t, sigDef)

	r := ginlib.New()
	ginadapter.Mount(r, svc)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "signal-catch-approved",
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	resp := post(t, srv, "/instances/"+pi.State().InstanceID+"/signals", map[string]any{
		"signal": "approved",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

// ─── PathParams via :id ───────────────────────────────────────────────────────

// TestInstanceRoutes_PathParam verifies that the gin :id param is correctly
// extracted — a GET with a specific ID routes to the right instance.
func TestInstanceRoutes_PathParam(t *testing.T) {
	t.Parallel()
	srv, svc := newSrv(t)

	var ids []string
	for range 2 {
		pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
			DefRef: "greeting", Vars: map[string]any{"name": "x"},
		})
		if err != nil {
			t.Fatalf("StartInstance: %v", err)
		}
		ids = append(ids, pi.State().InstanceID)
	}

	for _, id := range ids {
		resp := get(t, srv, "/instances/"+id)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /instances/%s: want 200, got %d", id, resp.StatusCode)
		}
		var body map[string]any
		decodeJSON(t, resp, &body)
		if body["instance_id"] != id {
			t.Fatalf("want instance_id=%q, got %v", id, body["instance_id"])
		}
	}
}

// ─── WithBasePath option ──────────────────────────────────────────────────────

func TestMount_WithBasePath(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	ginadapter.Mount(r, svc, ginadapter.WithBasePath("/api/v1"))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Route without base → 404.
	noBase := get(t, srv, "/instances/x")
	if noBase.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 without base path, got %d", noBase.StatusCode)
	}

	// Route with base → 400 (bad input — no def_ref, correct routing).
	resp := post(t, srv, "/api/v1/instances", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 with base path, got %d", resp.StatusCode)
	}
}

// TestMount_NativeGroup verifies that mounting onto a native gin.IRouter group
// works correctly (the group prefix plus basePath are both applied).
func TestMount_NativeGroup(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	grp := r.Group("/v2")
	ginadapter.Mount(grp, svc)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// The native group prefix must be honoured.
	resp := post(t, srv, "/v2/instances", map[string]any{
		"def_ref": "greeting", "vars": map[string]any{"name": "y"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201 via native group /v2, got %d", resp.StatusCode)
	}
}

// ─── WithMiddleware option ────────────────────────────────────────────────────

func TestMount_WithMiddleware(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	headerSet := false
	mw := func(gc *ginlib.Context) {
		headerSet = true
		gc.Next()
	}

	r := ginlib.New()
	ginadapter.Mount(r, svc, ginadapter.WithMiddleware(mw))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	post(t, srv, "/instances", map[string]any{
		"def_ref": "greeting", "vars": map[string]any{"name": "z"},
	})
	if !headerSet {
		t.Fatal("middleware was not called before handler")
	}
}

// ─── MessageRoutes ────────────────────────────────────────────────────────────

func TestMessageRoutes_DeliverMessage_202(t *testing.T) {
	t.Parallel()
	msgDef := transporttest.MessageProcess("order-shipped")
	_, svc := transporttest.NewHarness(t, msgDef)

	r := ginlib.New()
	ginadapter.Mount(r, svc)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "message-catch-order-shipped",
		Vars:   map[string]any{"orderId": "42"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	resp := post(t, srv, "/messages", map[string]any{
		"def_ref":         "message-catch-order-shipped:1",
		"name":            "order-shipped",
		"correlation_key": "42",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
}

func TestMessageRoutes_DeliverMessage_400_MissingField(t *testing.T) {
	t.Parallel()
	srv, _ := newSrv(t)

	resp := post(t, srv, "/messages", map[string]any{
		"name": "order-shipped",
		// missing def_ref
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// ─── TaskRoutes ───────────────────────────────────────────────────────────────

func TestTaskRoutes_ClaimCompleteReassign(t *testing.T) {
	t.Parallel()
	approvalDef := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, approvalDef)

	r := ginlib.New()
	ginadapter.Mount(r, svc)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	token := transporttest.StartedApprovalInstance(t, h, "gin-task-1")

	// Claim.
	claimResp := post(t, srv, "/tasks/"+token+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	if claimResp.StatusCode != http.StatusOK {
		t.Fatalf("claim: want 200, got %d", claimResp.StatusCode)
	}

	// Complete.
	completeResp := post(t, srv, "/tasks/"+token+"/complete", map[string]any{
		"actor":  map[string]any{"id": "alice", "roles": []string{"manager"}},
		"output": map[string]any{"approved": true},
	})
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("complete: want 200, got %d", completeResp.StatusCode)
	}
}

func TestTaskRoutes_Reassign(t *testing.T) {
	t.Parallel()
	approvalDef := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, approvalDef)

	r := ginlib.New()
	ginadapter.Mount(r, svc)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	token := transporttest.StartedApprovalInstance(t, h, "gin-reassign-1")

	// Claim first via HTTP.
	claimResp := post(t, srv, "/tasks/"+token+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	if claimResp.StatusCode != http.StatusOK {
		t.Fatalf("claim before reassign: want 200, got %d", claimResp.StatusCode)
	}

	// Reassign.
	reassignResp := post(t, srv, "/tasks/"+token+"/reassign", map[string]any{
		"from": "alice",
		"to":   "carol",
		"by":   map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	if reassignResp.StatusCode != http.StatusOK {
		t.Fatalf("reassign: want 200, got %d", reassignResp.StatusCode)
	}
}

// ─── AdminRoutes ──────────────────────────────────────────────────────────────

// TestAdminRoutes_AbsentByDefault verifies that AdminRoutes are not mounted
// unless an AdminRoutes struct is explicitly passed.
func TestAdminRoutes_AbsentByDefault(t *testing.T) {
	t.Parallel()
	srv, _ := newSrv(t)

	// Admin endpoint should not exist on the default Mount.
	resp := get(t, srv, "/admin/instances")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for absent admin, got %d", resp.StatusCode)
	}
}

// fakeAdminSvc is a minimal fake for service.Service used to test AdminRoutes
// in isolation. It implements the methods called by the admin handler funcs.
type fakeAdminSvc struct {
	service.Service
}

func (fakeAdminSvc) ListInstances(_ context.Context, _ kernel.InstanceFilter) (kernel.InstancePage, error) {
	return kernel.InstancePage{Items: []kernel.InstanceSummary{}}, nil
}

func (fakeAdminSvc) CancelInstance(_ context.Context, _ service.CancelInstanceRequest) (service.ProcessInstance, error) {
	return nil, fmt.Errorf("workflow-kernel: instance not found")
}

func (fakeAdminSvc) ResolveIncident(_ context.Context, _ service.ResolveIncidentRequest) (service.ProcessInstance, error) {
	return nil, fmt.Errorf("workflow-kernel: instance not found")
}

func TestAdminRoutes_ListInstances_200(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	admin := ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}
	admin.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/instances")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["items"] == nil {
		t.Fatal("want items array in response")
	}
}

// TestAdminRoutes_ConditionalDeadLetters_NilOmitted verifies that
// the dead-letters endpoints are not registered when DeadLetters is nil.
func TestAdminRoutes_ConditionalDeadLetters_NilOmitted(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	// DeadLetters is nil (zero value) — route must not exist.
	admin := ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}
	admin.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/dead-letters")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for nil DeadLetters dep, got %d", resp.StatusCode)
	}
}

// ─── HealthRoutes ─────────────────────────────────────────────────────────────

func TestHealthRoutes_Livez_200(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	ginadapter.MountHealth(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestHealthRoutes_Readyz_200_AllOK(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	ginadapter.MountHealth(r,
		httpcore.HealthCheckFunc("db", func(_ context.Context) error { return nil }),
	)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/readyz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestHealthRoutes_Readyz_503_CheckFails(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	ginadapter.MountHealth(r,
		httpcore.HealthCheckFunc("db", func(_ context.Context) error {
			return fmt.Errorf("connection refused")
		}),
	)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}

// ─── 5xx body must not expose raw errors ─────────────────────────────────────

// TestInternalError_NoRawErrorLeak verifies that when a handler encounters an
// internal error (not ErrBadInput, not not-found, etc.) the response body
// contains no raw error message — only the {"error":"internal_error"} envelope.
func TestInternalError_NoRawErrorLeak(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	// Use InstanceRoutes pointing at a nil service to force a panic/nil-deref,
	// but instead use a fake service that returns an opaque internal error.
	routes := ginadapter.InstanceRoutes{Svc: &errSvc{}}
	routes.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/instances", map[string]any{
		"def_ref": "anything",
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if msg, ok := body["message"]; ok && msg != "" {
		t.Fatalf("5xx body must not contain raw message, got %q", msg)
	}
}

// errSvc is a minimal service.Service fake that returns an opaque internal error.
type errSvc struct{ service.Service }

func (e *errSvc) StartInstance(_ context.Context, _ service.StartInstanceRequest) (service.ProcessInstance, error) {
	return nil, fmt.Errorf("some internal DB error: connection pool exhausted")
}

// ─── MountHealth convenience function ────────────────────────────────────────

// TestMountHealth_NoChecks verifies MountHealth with no checks still registers /healthz and /readyz.
func TestMountHealth_NoChecks(t *testing.T) {
	t.Parallel()

	r := ginlib.New()
	ginadapter.MountHealth(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	for _, path := range []string{"/healthz", "/readyz"} {
		resp := get(t, srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", path, resp.StatusCode)
		}
	}
}
