package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/service"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// ---- harness ----

type testHarness struct {
	runner    *runtime.ProcessDriver
	store     *kernel.MemStore
	taskStore *humantask.MemTaskStore
	clk       *clockwork.FakeClock
}

func newTestHarness(t *testing.T, defs ...*model.ProcessDefinition) (*testHarness, service.Service) {
	t.Helper()
	fc := clockwork.NewFakeClock()
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {{ID: "alice", Roles: []string{"manager"}}},
	})
	az := authz.RoleAuthorizer{}
	store, err := kernel.NewMemStore()
	require.NoError(t, err)
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": greetServiceAction{},
	})
	r, err := runtime.NewProcessDriver(cat, store, runtime.WithClock(fc), runtime.WithHumanTasks(resolver, taskStore, az))
	require.NoError(t, err)
	defsMap := make(map[string]*model.ProcessDefinition, len(defs)*2)
	for _, d := range defs {
		defsMap[fmt.Sprintf("%s:%d", d.ID, d.Version)] = d
		defsMap[d.ID] = d
	}
	reg := kernel.NewMapDefinitionRegistry(defsMap)
	svc, err := task.NewTaskService(taskStore, az, task.WithClock(fc))
	require.NoError(t, err)
	facade := service.New(r, svc, reg, store, store, taskStore, service.WithEngineClock(fc))
	return &testHarness{runner: r, store: store, taskStore: taskStore, clk: fc}, facade
}

type greetServiceAction struct{}

func (greetServiceAction) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	name, _ := in["name"].(string)
	return map[string]any{"greeting": "hi " + name}, nil
}

func linearProcess() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "greeting", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("greet", activity.WithActionName("greet")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func approvalProcess() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "approval", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", []string{"manager"}),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

func signalProcess(signalName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "signal-catch-" + signalName, Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewCatch("wait", event.WithCatchSignal(signalName)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}
}

func messageProcess(msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "message-catch-" + msgName, Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewCatch("wait-msg", event.WithCatchMessage(msgName, "orderId")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-msg"},
			{ID: "f2", Source: "wait-msg", Target: "end"},
		},
	}
}

func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return bytes.NewReader(b)
}

func decodeBody(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode JSON: %v — body: %s", err, body)
	}
}

// ---- tests ----

func TestHandlerStartInstance(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	body := jsonBody(t, map[string]any{
		"def_ref":     "greeting",
		"instance_id": "inst-h1",
		"vars":        map[string]any{"name": "ada"},
	})
	req := httptest.NewRequest(http.MethodPost, "/instances", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	decodeBody(t, rec.Body.Bytes(), &view)
	if view["instance_id"] != "inst-h1" {
		t.Fatalf("unexpected instance_id: %v", view["instance_id"])
	}
}

func TestHandlerGetInstance(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "get-inst-1", Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/instances/get-inst-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	decodeBody(t, rec.Body.Bytes(), &view)
	if view["instance_id"] != "get-inst-1" {
		t.Fatalf("unexpected instance_id: %v", view["instance_id"])
	}
}

func TestHandlerGetInstanceNotFound(t *testing.T) {
	_, svc := newTestHarness(t)
	h := rest.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/instances/no-such", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestHandlerDeliverSignal(t *testing.T) {
	def := signalProcess("approved")
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "signal-catch-approved", InstanceID: "sig-h1",
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	body := jsonBody(t, map[string]any{
		"signal":  "approved",
		"payload": map[string]any{"decision": "yes"},
	})
	req := httptest.NewRequest(http.MethodPost, "/instances/sig-h1/signals", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	decodeBody(t, rec.Body.Bytes(), &view)
	if view["status"] != "completed" {
		t.Fatalf("want status=completed, got: %v", view["status"])
	}
}

func TestHandlerDeliverMessage(t *testing.T) {
	def := messageProcess("order-shipped")
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "message-catch-order-shipped", InstanceID: "msg-h1",
		Vars: map[string]any{"orderId": "42"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	body := jsonBody(t, map[string]any{
		"def_ref":         "message-catch-order-shipped:1",
		"name":            "order-shipped",
		"correlation_key": "42",
		"payload":         map[string]any{"shipped": true},
	})
	req := httptest.NewRequest(http.MethodPost, "/messages", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("want exactly 202 Accepted, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerClaimTask(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)

	parked, err := h2.runner.Run(t.Context(), def, "approval-h1", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	if parked.Status != engine.StatusRunning {
		t.Fatalf("want running, got %v", parked.Status)
	}
	taskToken := parked.Tokens[0].AwaitCommand
	if taskToken == "" {
		t.Fatal("task token empty")
	}

	h := rest.NewHandler(svc)

	body := jsonBody(t, map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/claim", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerClaimTaskForbidden(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)

	parked, err := h2.runner.Run(t.Context(), def, "approval-h2", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	taskToken := parked.Tokens[0].AwaitCommand

	h := rest.NewHandler(svc)

	body := jsonBody(t, map[string]any{
		"actor": map[string]any{"id": "bob", "roles": []string{"viewer"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/claim", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCompleteTask(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)

	parked, err := h2.runner.Run(t.Context(), def, "approval-complete-1", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	taskToken := parked.Tokens[0].AwaitCommand

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	_, err = svc.ClaimTask(t.Context(), service.ClaimTaskRequest{TaskToken: taskToken, Actor: manager})
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	h := rest.NewHandler(svc)

	body := jsonBody(t, map[string]any{
		"actor":  map[string]any{"id": "alice", "roles": []string{"manager"}},
		"output": map[string]any{"approved": true},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/complete", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	decodeBody(t, rec.Body.Bytes(), &view)
	if view["status"] != "completed" {
		t.Fatalf("want status=completed, got: %v", view["status"])
	}
}

func TestHandlerReassignTask(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)

	parked, err := h2.runner.Run(t.Context(), def, "approval-reassign-1", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	taskToken := parked.Tokens[0].AwaitCommand

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	_, err = svc.ClaimTask(t.Context(), service.ClaimTaskRequest{TaskToken: taskToken, Actor: manager})
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	h := rest.NewHandler(svc)

	body := jsonBody(t, map[string]any{
		"from": "alice",
		"to":   "carol",
		"by":   map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/reassign", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerBadJSONBody(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/instances", bytes.NewBufferString(`{bad json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStartInstanceUnknownDef(t *testing.T) {
	_, svc := newTestHarness(t)
	h := rest.NewHandler(svc)

	body := jsonBody(t, map[string]any{
		"def_ref":     "no-such",
		"instance_id": "nope",
	})
	req := httptest.NewRequest(http.MethodPost, "/instances", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerWithInstanceMapper(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)

	customMapper := func(st engine.InstanceState) any {
		return map[string]string{"custom": "yes", "id": st.InstanceID}
	}
	h := rest.NewHandler(svc, rest.WithInstanceMapper(customMapper))

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "mapper-inst-1", Vars: map[string]any{"name": "z"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/instances/mapper-inst-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var view map[string]string
	decodeBody(t, rec.Body.Bytes(), &view)
	if view["custom"] != "yes" {
		t.Fatalf("want custom=yes, got: %v", view)
	}
}

func TestHandlerDeliverSignalMissingField(t *testing.T) {
	def := signalProcess("x")
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	// Omit signal field → 400.
	body := jsonBody(t, map[string]any{"payload": map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/instances/any/signals", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeliverMessageMissingField(t *testing.T) {
	_, svc := newTestHarness(t)
	h := rest.NewHandler(svc)

	// Omit def_ref → 400.
	body := jsonBody(t, map[string]any{"name": "x"})
	req := httptest.NewRequest(http.MethodPost, "/messages", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStartInstanceMissingField(t *testing.T) {
	_, svc := newTestHarness(t)
	h := rest.NewHandler(svc)

	// Only def_ref, no instance_id → 400.
	body := jsonBody(t, map[string]any{"def_ref": "x"})
	req := httptest.NewRequest(http.MethodPost, "/instances", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCompleteTaskUnauthorized(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)

	parked, err := h2.runner.Run(t.Context(), def, "complete-unauth-1", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	taskToken := parked.Tokens[0].AwaitCommand

	h := rest.NewHandler(svc)
	body := jsonBody(t, map[string]any{
		"actor":  map[string]any{"id": "bob", "roles": []string{"viewer"}},
		"output": map[string]any{},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/complete", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerReassignTaskUnauthorized(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)

	parked, err := h2.runner.Run(t.Context(), def, "reassign-unauth-1", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	taskToken := parked.Tokens[0].AwaitCommand

	// Claim first with manager.
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	_, err = svc.ClaimTask(t.Context(), service.ClaimTaskRequest{TaskToken: taskToken, Actor: manager})
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	h := rest.NewHandler(svc)
	body := jsonBody(t, map[string]any{
		"from": "alice",
		"to":   "carol",
		"by":   map[string]any{"id": "bob", "roles": []string{"viewer"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/reassign", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerMountableUnderStripPrefix(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "greeting", InstanceID: "mount-inst-1", Vars: map[string]any{"name": "p"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	outer := http.NewServeMux()
	outer.Handle("/api/wf/", http.StripPrefix("/api/wf", rest.NewHandler(svc)))

	req := httptest.NewRequest(http.MethodGet, "/api/wf/instances/mount-inst-1", nil)
	rec := httptest.NewRecorder()
	outer.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 under /api/wf prefix, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerDeliverMessageStatus asserts that POST /messages returns exactly 202 Accepted.
func TestHandlerDeliverMessageStatus(t *testing.T) {
	def := messageProcess("order-created")
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "message-catch-order-created", InstanceID: "msg-status-1",
		Vars: map[string]any{"orderId": "99"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	body := jsonBody(t, map[string]any{
		"def_ref":         "message-catch-order-created:1",
		"name":            "order-created",
		"correlation_key": "99",
		"payload":         map[string]any{"created": true},
	})
	req := httptest.NewRequest(http.MethodPost, "/messages", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("want exactly 202 Accepted, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerWithInstanceMapperAppliedToSignal asserts that a custom WithInstanceMapper
// is applied to the POST /instances/{id}/signals response, not just GET /instances/{id}.
func TestHandlerWithInstanceMapperAppliedToSignal(t *testing.T) {
	def := signalProcess("pay")
	_, svc := newTestHarness(t, def)

	customMapper := func(st engine.InstanceState) any {
		return map[string]string{"custom": "mapper-applied", "id": st.InstanceID}
	}
	h := rest.NewHandler(svc, rest.WithInstanceMapper(customMapper))

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "signal-catch-pay", InstanceID: "sig-mapper-1",
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	body := jsonBody(t, map[string]any{
		"signal":  "pay",
		"payload": map[string]any{},
	})
	req := httptest.NewRequest(http.MethodPost, "/instances/sig-mapper-1/signals", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var view map[string]string
	decodeBody(t, rec.Body.Bytes(), &view)
	if view["custom"] != "mapper-applied" {
		t.Fatalf("want custom mapper applied to signal response, got: %v", view)
	}
}

// TestHandlerWithInstanceMapperAppliedToClaimTask asserts that a custom WithInstanceMapper
// is applied to the POST /tasks/{token}/claim response.
func TestHandlerWithInstanceMapperAppliedToClaimTask(t *testing.T) {
	def := approvalProcess()
	h2, svc := newTestHarness(t, def)

	parked, err := h2.runner.Run(t.Context(), def, "approval-mapper-claim-1", nil)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	taskToken := parked.Tokens[0].AwaitCommand

	customMapper := func(st engine.InstanceState) any {
		return map[string]string{"custom": "mapper-applied", "id": st.InstanceID}
	}
	h := rest.NewHandler(svc, rest.WithInstanceMapper(customMapper))

	body := jsonBody(t, map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskToken+"/claim", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var view map[string]string
	decodeBody(t, rec.Body.Bytes(), &view)
	if view["custom"] != "mapper-applied" {
		t.Fatalf("want custom mapper applied to claim response, got: %v", view)
	}
}
