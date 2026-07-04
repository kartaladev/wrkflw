package rest_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
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

// actionMetadataProcess returns a definition built via DefinitionBuilder that
// has a definition-scoped action ("scoped-action") and an inline ServiceTask
// ("svc-inline"). Used to assert that snapshot responses surface action metadata.
func actionMetadataProcess(t *testing.T) *definition.ProcessDefinition {
	t.Helper()
	inlineAction := action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"done": true}, nil
	})
	def, err := definition.NewDefinition("action-meta", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("svc-named", activity.WithActionName("scoped-action"))).
		Add(activity.NewServiceTask("svc-inline", activity.WithAction(inlineAction))).
		Add(event.NewEnd("end")).
		Connect("start", "svc-named").
		Connect("svc-named", "svc-inline").
		Connect("svc-inline", "end").
		RegisterAction("scoped-action", action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return nil, nil
		})).
		Build()
	if err != nil {
		t.Fatalf("actionMetadataProcess Build: %v", err)
	}
	return def
}

// TestHandlerGetInstanceSnapshotActionMetadata asserts that GET /instances/{id}/snapshot
// includes scoped_actions and action_bindings in the JSON response when the
// process definition has scoped and inline service tasks.
func TestHandlerGetInstanceSnapshotActionMetadata(t *testing.T) {
	def := actionMetadataProcess(t)
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef:     "action-meta",
		InstanceID: "snap-meta-1",
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/instances/snap-meta-1/snapshot", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	var view map[string]any
	decodeBody(t, rec.Body.Bytes(), &view)

	// scoped_actions must contain the single registered scoped action name.
	scopedActionsRaw, ok := view["scoped_actions"]
	if !ok {
		t.Fatalf("scoped_actions key missing from snapshot JSON: %s", rec.Body.String())
	}
	scopedActions, ok := scopedActionsRaw.([]any)
	if !ok || len(scopedActions) != 1 {
		t.Fatalf("scoped_actions = %v, want [scoped-action]", scopedActionsRaw)
	}
	if scopedActions[0] != "scoped-action" {
		t.Fatalf("scoped_actions[0] = %v, want scoped-action", scopedActions[0])
	}

	// action_bindings must have 2 entries (one per service task), sorted by node_id.
	bindingsRaw, ok := view["action_bindings"]
	if !ok {
		t.Fatalf("action_bindings key missing from snapshot JSON: %s", rec.Body.String())
	}
	bindings, ok := bindingsRaw.([]any)
	if !ok || len(bindings) != 2 {
		t.Fatalf("action_bindings = %v, want 2 entries", bindingsRaw)
	}

	// Sorted by node_id: svc-inline < svc-named.
	b0, _ := bindings[0].(map[string]any)
	b1, _ := bindings[1].(map[string]any)
	if b0["node_id"] != "svc-inline" {
		t.Errorf("action_bindings[0].node_id = %v, want svc-inline", b0["node_id"])
	}
	if b1["node_id"] != "svc-named" {
		t.Errorf("action_bindings[1].node_id = %v, want svc-named", b1["node_id"])
	}

	// svc-inline: inline=true, action omitted (empty => default-by-id).
	if b0["node_kind"] != "serviceTask" {
		t.Errorf("action_bindings[0].node_kind = %v, want serviceTask", b0["node_kind"])
	}
	if inlineVal, _ := b0["inline"].(bool); !inlineVal {
		t.Errorf("action_bindings[0].inline = %v, want true", b0["inline"])
	}
	// The "action" key must be absent for inline tasks (omitempty + empty string).
	if _, present := b0["action"]; present {
		t.Errorf("action_bindings[0].action key present for inline task, want absent (omitempty)")
	}

	// svc-named: action=scoped-action, inline=false.
	if b1["action"] != "scoped-action" {
		t.Errorf("action_bindings[1].action = %v, want scoped-action", b1["action"])
	}
	if inlineVal, _ := b1["inline"].(bool); inlineVal {
		t.Errorf("action_bindings[1].inline = true, want false")
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
