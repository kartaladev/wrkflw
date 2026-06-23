package rest_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/service"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// TestHandlerGetInstanceSnapshot hits GET /instances/{id}/snapshot and asserts that:
//   - The response is 200 OK with JSON containing "instance_id" and "status".
//   - The JSON does NOT contain "scopes" or "armed" (no engine bookkeeping leaks).
func TestHandlerGetInstanceSnapshot(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceID: "snap-inst-1",
		Vars:       map[string]any{"name": "tester"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/instances/snap-inst-1/snapshot", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.Bytes()
	var view map[string]any
	decodeBody(t, body, &view)

	if view["instance_id"] != "snap-inst-1" {
		t.Fatalf("want instance_id=snap-inst-1, got: %v", view["instance_id"])
	}
	if view["status"] == nil {
		t.Fatalf("want status field present, got nil")
	}

	// No engine bookkeeping keys must appear in the wire JSON.
	bodyLower := bytes.ToLower(body)
	for _, banned := range []string{"scopes", "armed"} {
		if bytes.Contains(bodyLower, []byte(banned)) {
			t.Errorf("snapshot JSON leaks bookkeeping key %q: %s", banned, body)
		}
	}
}

// TestHandlerGetInstanceSnapshotNotFound asserts that a missing instance returns 404.
func TestHandlerGetInstanceSnapshotNotFound(t *testing.T) {
	_, svc := newTestHarness(t)
	h := rest.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/instances/no-such/snapshot", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerGetActionableView hits GET /instances/{id}/actionable and asserts
// the JSON body has "instance_id", "status", and "open_tasks".
func TestHandlerGetActionableView(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	_, err := h2.runner.Run(t.Context(), def, "action-inst-1", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/instances/action-inst-1/actionable", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	var view map[string]any
	decodeBody(t, rec.Body.Bytes(), &view)

	if view["instance_id"] != "action-inst-1" {
		t.Fatalf("want instance_id=action-inst-1, got: %v", view["instance_id"])
	}
	if view["status"] == nil {
		t.Fatalf("want status field present, got nil")
	}
	if _, ok := view["open_tasks"]; !ok {
		t.Fatalf("want open_tasks field present, body: %s", rec.Body.String())
	}
}

// TestHandlerGetActionableViewNotFound asserts that a missing instance returns 404.
func TestHandlerGetActionableViewNotFound(t *testing.T) {
	_, svc := newTestHarness(t)
	h := rest.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/instances/no-such/actionable", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d — body: %s", rec.Code, rec.Body.String())
	}
}
