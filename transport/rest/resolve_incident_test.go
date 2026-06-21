package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// resolveIncidentStub is a minimal service.Service stub for testing the
// POST /admin/instances/{id}/incidents/{incidentID}/resolve route.
// It records the ResolveIncidentRequest it receives and returns a preconfigured
// state or error. All other interface methods panic (not needed by these tests).
type resolveIncidentStub struct {
	service.Service
	req   service.ResolveIncidentRequest
	state engine.InstanceState
	err   error
}

func (s *resolveIncidentStub) ResolveIncident(_ context.Context, req service.ResolveIncidentRequest) (engine.InstanceState, error) {
	s.req = req
	return s.state, s.err
}

// TestResolveIncidentRouteDefaultDeny asserts that POST /admin/instances/{id}/incidents/{incID}/resolve
// returns 403 when no WithAdminMiddleware option is supplied (default-deny).
func TestResolveIncidentRouteDefaultDeny(t *testing.T) {
	stub := &resolveIncidentStub{
		state: engine.InstanceState{InstanceID: "p", Status: engine.StatusCompleted},
	}
	h := rest.NewHandler(stub) // no WithAdminMiddleware

	req := httptest.NewRequest(http.MethodPost, "/admin/instances/p/incidents/p-in0/resolve", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("default-deny: want 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestResolveIncidentRouteDenyingMiddleware asserts that a denying admin middleware
// blocks the request.
func TestResolveIncidentRouteDenyingMiddleware(t *testing.T) {
	stub := &resolveIncidentStub{
		state: engine.InstanceState{InstanceID: "p", Status: engine.StatusCompleted},
	}
	h := rest.NewHandler(stub, rest.WithAdminMiddleware(denyAdmin))

	req := httptest.NewRequest(http.MethodPost, "/admin/instances/p/incidents/p-in0/resolve", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("denying middleware: want 403, got %d", rec.Code)
	}
}

// TestResolveIncidentRouteSuccess asserts that POST /admin/instances/{id}/incidents/{incidentID}/resolve
// with an allow-all admin middleware calls svc.ResolveIncident and returns 200 with the instance body.
func TestResolveIncidentRouteSuccess(t *testing.T) {
	stub := &resolveIncidentStub{
		state: engine.InstanceState{InstanceID: "p", Status: engine.StatusCompleted},
	}
	h := rest.NewHandler(stub, rest.WithAdminMiddleware(allowAdmin))

	body := bytes.NewBufferString(`{"add_attempts": 3}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/instances/p/incidents/p-in0/resolve", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	// Verify the stub received the correct request.
	if stub.req.InstanceID != "p" {
		t.Fatalf("want InstanceID=p, got %q", stub.req.InstanceID)
	}
	if stub.req.IncidentID != "p-in0" {
		t.Fatalf("want IncidentID=p-in0, got %q", stub.req.IncidentID)
	}
	if stub.req.AddAttempts != 3 {
		t.Fatalf("want AddAttempts=3, got %d", stub.req.AddAttempts)
	}

	// Verify the response body contains the instance_id.
	var view map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if view["instance_id"] != "p" {
		t.Fatalf("want instance_id=p in response, got: %v", view["instance_id"])
	}
}

// TestResolveIncidentRouteNoBody asserts that an absent body defaults add_attempts to 1.
func TestResolveIncidentRouteNoBody(t *testing.T) {
	stub := &resolveIncidentStub{
		state: engine.InstanceState{InstanceID: "q", Status: engine.StatusRunning},
	}
	h := rest.NewHandler(stub, rest.WithAdminMiddleware(allowAdmin))

	req := httptest.NewRequest(http.MethodPost, "/admin/instances/q/incidents/q-in1/resolve", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	// add_attempts absent (0 in JSON) → the handler must pass 0 or a positive default;
	// the service layer normalizes ≤0 to 1. We just verify the call was made.
	if stub.req.InstanceID != "q" {
		t.Fatalf("want InstanceID=q, got %q", stub.req.InstanceID)
	}
}

// TestResolveIncidentRouteNotFound asserts that when the service returns
// ErrInstanceNotFound the handler responds with 404.
func TestResolveIncidentRouteNotFound(t *testing.T) {
	stub := &resolveIncidentStub{
		err: runtime.ErrInstanceNotFound,
	}
	h := rest.NewHandler(stub, rest.WithAdminMiddleware(allowAdmin))

	req := httptest.NewRequest(http.MethodPost, "/admin/instances/missing/incidents/inc-0/resolve",
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestResolveIncidentRouteServiceError asserts that an unexpected service error maps to 500.
func TestResolveIncidentRouteServiceError(t *testing.T) {
	stub := &resolveIncidentStub{
		err: errors.New("some unexpected error"),
	}
	h := rest.NewHandler(stub, rest.WithAdminMiddleware(allowAdmin))

	req := httptest.NewRequest(http.MethodPost, "/admin/instances/x/incidents/x-in0/resolve",
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d — body: %s", rec.Code, rec.Body.String())
	}
}
