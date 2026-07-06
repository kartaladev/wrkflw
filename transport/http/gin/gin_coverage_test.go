// gin_coverage_test.go — additional tests to drive error-path coverage for
// InstanceRoutes, TaskRoutes, and AdminRoutes error branches.
package gin_test

import (
	"context"
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

// ─── errInstanceSvc returns errors for every Service method ───────────────────

type errInstanceSvc struct{ service.Service }

func (e *errInstanceSvc) StartInstance(_ context.Context, _ service.StartInstanceRequest) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) GetInstance(_ context.Context, _ string) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) DeliverSignal(_ context.Context, _ service.DeliverSignalRequest) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) DeliverMessage(_ context.Context, _ service.DeliverMessageRequest) error {
	return kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) ClaimTask(_ context.Context, _ service.ClaimTaskRequest) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) CompleteTask(_ context.Context, _ service.CompleteTaskRequest) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) ReassignTask(_ context.Context, _ service.ReassignTaskRequest) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) ListInstances(_ context.Context, _ kernel.InstanceFilter) (kernel.InstancePage, error) {
	return kernel.InstancePage{}, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) CancelInstance(_ context.Context, _ service.CancelInstanceRequest) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}
func (e *errInstanceSvc) ResolveIncident(_ context.Context, _ service.ResolveIncidentRequest) (service.ProcessInstance, error) {
	return nil, kernel.ErrInstanceNotFound
}

// ─── InstanceRoutes error path tests ─────────────────────────────────────────

func TestInstanceRoutes_GetInstance_ErrorPath(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.InstanceRoutes{Svc: &errInstanceSvc{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/instances/gone")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestInstanceRoutes_Snapshot_ErrorPath(t *testing.T) {
	t.Parallel()

	// The snapshot endpoint now reads via GetInstance; we test the error path via
	// a real svc by requesting an unknown instance.
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	ginadapter.InstanceRoutes{Svc: svc}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/instances/no-such/snapshot")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("snapshot missing-id: want 404, got %d", resp.StatusCode)
	}
}

func TestInstanceRoutes_Actionable_ErrorPath(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	ginadapter.InstanceRoutes{Svc: svc}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/instances/no-such/actionable")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("actionable missing-id: want 404, got %d", resp.StatusCode)
	}
}

func TestInstanceRoutes_Signal_BadJSON(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	r := ginlib.New()
	ginadapter.InstanceRoutes{Svc: svc}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Malformed JSON body — a bare string where the handler expects an object —
	// is transmitted intact and fails ShouldBindJSON server-side → 400.
	resp := post(t, srv, "/instances/"+pi.State().InstanceID+"/signals", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for bad JSON, got %d", resp.StatusCode)
	}
}

func TestInstanceRoutes_Signal_ErrorPath(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.InstanceRoutes{Svc: &errInstanceSvc{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/instances/any/signals", map[string]any{"signal": "foo"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// ─── MessageRoutes error path tests ──────────────────────────────────────────

func TestMessageRoutes_DeliverMessage_ErrorPath(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.MessageRoutes{Svc: &errInstanceSvc{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/messages", map[string]any{
		"def_ref": "no-such:1",
		"name":    "evt",
	})
	// not-found propagates as 404 from ErrInstanceNotFound
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error, got 200")
	}
}

// ─── TaskRoutes error path tests ─────────────────────────────────────────────

func newTaskSrv(t *testing.T) *httptest.Server {
	t.Helper()
	r := ginlib.New()
	ginadapter.TaskRoutes{Svc: &errInstanceSvc{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestTaskRoutes_Claim_BadJSON(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	ginadapter.TaskRoutes{Svc: svc}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/tasks/tok/claim", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestTaskRoutes_Claim_ErrorPath(t *testing.T) {
	t.Parallel()
	resp := post(t, newTaskSrv(t), "/tasks/bad-token/claim", map[string]any{
		"actor": map[string]any{"id": "alice"},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestTaskRoutes_Complete_BadJSON(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	ginadapter.TaskRoutes{Svc: svc}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/tasks/tok/complete", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestTaskRoutes_Complete_ErrorPath(t *testing.T) {
	t.Parallel()
	resp := post(t, newTaskSrv(t), "/tasks/bad-token/complete", map[string]any{
		"actor": map[string]any{"id": "alice"},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestTaskRoutes_Reassign_BadJSON(t *testing.T) {
	t.Parallel()
	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	r := ginlib.New()
	ginadapter.TaskRoutes{Svc: svc}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/tasks/tok/reassign", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestTaskRoutes_Reassign_ErrorPath(t *testing.T) {
	t.Parallel()
	resp := post(t, newTaskSrv(t), "/tasks/bad-token/reassign", map[string]any{
		"from": "alice", "to": "carol", "by": map[string]any{"id": "alice"},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// ─── AdminRoutes error paths ──────────────────────────────────────────────────

func TestAdminRoutes_ListInstances_ErrorPath(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: &errInstanceSvc{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/instances")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("want error status, got 200")
	}
}

func TestAdminRoutes_ListInstances_BadStatus(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/admin/instances?status=bogus&limit=abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { drainClose(resp) })
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for bogus status, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_ListInstances_Total1(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// total=1 triggers IncludeTotal.
	resp, err := srv.Client().Get(srv.URL + "/admin/instances?total=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { drainClose(resp) })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

// ─── WithBasePath option on AdminRoutes ───────────────────────────────────────

func TestAdminRoutes_WithBasePath(t *testing.T) {
	t.Parallel()
	r := ginlib.New()
	admin := ginadapter.AdminRoutes{Svc: fakeAdminSvc{}}
	admin.Customize(r, httpcore.WithBasePath[ginlib.IRouter]("/v1"))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Without base path → 404.
	noBase := get(t, srv, "/admin/instances")
	if noBase.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 without prefix, got %d", noBase.StatusCode)
	}

	// With base path → 200.
	withBase := get(t, srv, "/v1/admin/instances")
	if withBase.StatusCode != http.StatusOK {
		t.Fatalf("want 200 with prefix, got %d", withBase.StatusCode)
	}
}
