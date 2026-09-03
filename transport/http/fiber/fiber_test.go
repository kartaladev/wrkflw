// Package fiber_test exercises the fiber v3 adapter via black-box tests.
// It uses the real in-memory service harness from internal/transporttest so the
// full service layer runs without mocks.
package fiber_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	fiberlib "github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/fiber"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
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

// staticActor returns a RequestActorFunc that authenticates EVERY request as
// the named principal. It stands in for a consumer's authentication middleware
// in tests that care about what the actor is allowed to do, not about how it
// was authenticated.
func staticActor(id string, roles ...string) httpcore.RequestActorFunc {
	return func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: id, Roles: roles}, nil
	}
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
		"def_ref": "greeting",
		"vars":    map[string]any{"name": "ada"},
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
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
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
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
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
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
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
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
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
		"def_ref": "greeting",
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
		DefRef: model.Latest("message-catch-order-shipped"),
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
//
// The request carries NO body: ClaimInput is an empty struct, so a
// correctly-migrated client sends nothing at all. The identity comes from
// the mount's RequestActor, never from the payload.
func TestTaskRoutes_Customize(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskID := transporttest.StartedApprovalInstance(t, h, "task-claim-fiber-1")

	app := newApp()
	fiber.Mount(app, svc, fiber.WithRequestActor(staticActor("alice", "manager")))

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/claim", nil))

	if status != http.StatusOK {
		t.Fatalf("want 200 claim, got %d (body=%s)", status, body)
	}
}

// TestTaskRoutes_Complete verifies POST /tasks/:token/complete returns 200.
func TestTaskRoutes_Complete(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskID := transporttest.StartedApprovalInstance(t, h, "task-complete-fiber-1")

	app := newApp()
	fiber.Mount(app, svc, fiber.WithRequestActor(staticActor("alice", "manager")))

	// Claim first, then complete. Neither body carries an actor any more.
	statusClaim, bodyClaim := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/claim", nil))
	if statusClaim != http.StatusOK {
		t.Fatalf("claim want 200, got %d (body=%s)", statusClaim, bodyClaim)
	}

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/complete", map[string]any{
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

	taskID := transporttest.StartedApprovalInstance(t, h, "task-reassign-fiber-1")

	app := newApp()
	fiber.Mount(app, svc, fiber.WithRequestActor(staticActor("alice", "manager")))

	// Claim first so alice is the claimant.
	statusClaim, bodyClaim := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/claim", nil))
	if statusClaim != http.StatusOK {
		t.Fatalf("claim want 200, got %d (body=%s)", statusClaim, bodyClaim)
	}

	// "by" is gone from ReassignInput: the reassigner is the authenticated
	// principal. from/to stay — they name task assignees, not the caller.
	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/reassign", map[string]any{
		"from": "alice",
		"to":   "bob",
	}))
	if status != http.StatusOK {
		t.Fatalf("reassign want 200, got %d (body=%s)", status, body)
	}
}

// TestIdentityOptionAliases asserts the two fiber-typed aliases set the fields
// their generic counterparts set.
//
// ⚠ The aliases are not sugar: in httpcore.WithRequestActor[R] the type
// parameter R appears ONLY in the result type, so it can never be inferred and
// a direct call must spell fiber.Router out. Deleting either alias would not
// break compilation of this adapter — it would only push that spelling onto
// every consumer — so the aliases need a test of their own.
//
// RequestActorTimeout is asserted on the CONFIG rather than through a mounted
// route on purpose: httpcore.ClaimTask/CompleteTask/ReassignTask currently pass
// a literal 0 to resolveRequestActor, so no end-to-end request can observe the
// value today. The alias still has to put it in the right field.
func TestIdentityOptionAliases(t *testing.T) {
	t.Parallel()

	type testCase struct {
		opt    httpcore.CustomizeOption[fiberlib.Router]
		assert func(t *testing.T, cfg httpcore.CustomizeConfig[fiberlib.Router])
	}

	cases := map[string]testCase{
		"WithRequestActor installs the resolver": {
			opt: fiber.WithRequestActor(staticActor("alice", "manager")),
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[fiberlib.Router]) {
				t.Helper()
				require.NotNil(t, cfg.RequestActor)
				a, err := cfg.RequestActor(t.Context())
				require.NoError(t, err)
				assert.Equal(t, "alice", a.ID)
				assert.Equal(t, []string{"manager"}, a.Roles)
			},
		},
		"WithRequestActor(nil) falls back to the context seam": {
			opt: fiber.WithRequestActor(nil),
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[fiberlib.Router]) {
				t.Helper()
				require.NotNil(t, cfg.RequestActor, "ResolveConfig must restore the default")
				seeded := authz.ContextWithActor(t.Context(), authz.Actor{ID: "bob"})
				a, err := cfg.RequestActor(seeded)
				require.NoError(t, err)
				assert.Equal(t, "bob", a.ID)
			},
		},
		"WithRequestActorTimeout sets the bound": {
			opt: fiber.WithRequestActorTimeout(3 * time.Second),
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[fiberlib.Router]) {
				t.Helper()
				assert.Equal(t, 3*time.Second, cfg.RequestActorTimeout)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, httpcore.ResolveConfig(tc.opt))
		})
	}
}

// TestTaskRoutes_ActorComesFromMiddlewareNotTheBody pins the core rule for
// the fiber adapter: the acting principal is whatever the
// consumer's AUTHENTICATION middleware put on the request context, and a body
// claiming a different identity is ignored rather than believed.
//
// The middleware authenticates a VIEWER — a role the approval task's
// eligibility spec (activity.WithEligibleRoles("manager")) does not admit —
// while the request body still carries the legacy
// {"actor": {..., "roles": ["manager"]}} payload a stale client would send.
// 403 is the only answer that proves the body lost: a 200 would mean the
// self-asserted manager role in the payload had been honoured.
//
// ⚠ The middleware writes the actor with c.SetContext, NOT c.Locals — see
// TestTaskRoutes_LocalsDoesNotAuthenticate for why that distinction is a
// contract of this adapter rather than an incidental style choice.
func TestTaskRoutes_ActorComesFromMiddlewareNotTheBody(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskID := transporttest.StartedApprovalInstance(t, h, "task-claim-fiber-middleware")

	app := newApp()
	app.Use(func(c fiberlib.Ctx) error {
		c.SetContext(authz.ContextWithActor(c.Context(), authz.Actor{
			ID: "eve", Roles: []string{"viewer"},
		}))
		return c.Next()
	})
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	}))

	assert.Equal(t, http.StatusForbidden, status, "body=%s", body)
	assert.Contains(t, body, `"forbidden"`)
}

// TestTaskRoutes_NoIdentity401 pins the fail-closed default: a mount with no
// RequestActor and no authentication middleware answers 401, never a 200 for
// the zero actor.
//
// ⚠ The task in this test EXISTS and the 401 still precedes the lookup, which
// is deliberate — an unauthenticated caller must not be able to tell a real
// task id from a fabricated one. TestTaskRoutes_NoIdentity401 and its sibling
// row in TestTaskRoutes_ClaimBodyIsOptionalButStillBounded together fix the
// order: 413 (body) → 401 (identity) → 404 (lookup).
func TestTaskRoutes_NoIdentity401(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskID := transporttest.StartedApprovalInstance(t, h, "task-claim-fiber-noidentity")

	app := newApp()
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/claim", nil))

	assert.Equal(t, http.StatusUnauthorized, status, "body=%s", body)
	assert.Contains(t, body, `"unauthenticated"`)
}

// TestTaskRoutes_LocalsDoesNotAuthenticate pins a CONTRACT, not an accident:
// fiber's most canonical middleware channel, c.Locals, does NOT authenticate a
// request for this adapter, and a consumer who reaches for it gets a
// fail-closed 401 instead of a silently wrong identity.
//
// The mechanism, MEASURED on fiber v3.4.0 with one middleware writing each way
// and a handler reading both:
//
//	SetContext   c.Context().Value=from-middleware   c.Value=<nil>
//	Locals       c.Context().Value=<nil>             c.Value=from-middleware
//
// fiber.Ctx.Context() returns a SEPARATE object from the Ctx itself — its own
// godoc says it "returns a non-nil, empty context, if it was not set earlier" —
// while Ctx additionally implements context.Context, whose Value reads Locals.
// groups.go hands c.Context() to httpcore, so only the SetContext half is
// visible to the actor seam.
//
// ⚠ If this test ever returns 403 (i.e. the viewer identity DID arrive), fiber
// has unified the two objects. That is not a licence to delete the test: it
// means the fiber examples must be revisited, because the
// documented "use c.SetContext" guidance would no longer be the only working
// channel — and because a channel that starts working silently changes which
// requests are authenticated.
func TestTaskRoutes_LocalsDoesNotAuthenticate(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)

	taskID := transporttest.StartedApprovalInstance(t, h, "task-claim-fiber-locals")

	app := newApp()
	app.Use(func(c fiberlib.Ctx) error {
		// The canonical fiber idiom — and the one that does NOT reach the seam.
		c.Locals("actor", authz.Actor{ID: "eve", Roles: []string{"viewer"}})
		return c.Next()
	})
	fiber.Mount(app, svc)

	status, body := appDo(t, app, newPostRequest(t, "/tasks/"+taskID+"/claim", nil))

	assert.Equal(t, http.StatusUnauthorized, status,
		"c.Locals must NOT authenticate — fail closed, body=%s", body)
	assert.Contains(t, body, `"unauthenticated"`)
}

// TestTaskRoutes_ClaimBodyIsOptionalButStillBounded pins the claim route's body
// contract now that ClaimInput is empty.
//
// A correctly-migrated client sends NO body at all, so requiring one would
// break every such client with a 400 (MEASURED before the optional decode
// existed: an absent body answered 400 "unexpected end of JSON input"). The
// route therefore ignores an absent or undecodable payload and proceeds with
// the zero ClaimInput.
//
// ⚠ Optional is not unbounded. The oversize row is the load-bearing one: it
// fails if the optional decode is written as a bare "ignore the error", which
// would drop this route out of the 413 contract while every other row
// here stayed green. TestEveryDecodeSiteIsBounded covers the same fact from the
// enumeration side; this row keeps it attached to the reason.
func TestTaskRoutes_ClaimBodyIsOptionalButStillBounded(t *testing.T) {
	t.Parallel()

	type testCase struct {
		raw    []byte
		assert func(t *testing.T, status int, body string)
	}

	ok := func(t *testing.T, status int, body string) {
		t.Helper()
		assert.Equal(t, http.StatusOK, status, "body=%s", body)
	}

	cases := map[string]testCase{
		"absent body": {
			raw:    nil,
			assert: ok,
		},
		"empty JSON object": {
			raw:    []byte(`{}`),
			assert: ok,
		},
		"undecodable body is ignored, not a 400": {
			raw:    []byte(`}{ not json`),
			assert: ok,
		},
		"oversize body is still 413": {
			raw: startBody(2 << 20),
			assert: func(t *testing.T, status int, body string) {
				t.Helper()
				assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body=%.200s", body)
				assert.Contains(t, body, `"request_too_large"`)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.ApprovalProcess()
			h, svc := transporttest.NewHarness(t, def)

			taskID := transporttest.StartedApprovalInstance(t, h, "task-claim-fiber-optional-"+name)

			app := newApp()
			fiber.Mount(app, svc,
				fiber.WithMaxBodyBytes(1<<20),
				fiber.WithRequestActor(staticActor("alice", "manager")),
			)

			status, body := appDo(t, app, newRawPostRequest(t, "/tasks/"+taskID+"/claim", tc.raw, ""))
			tc.assert(t, status, body)
		})
	}
}

// TestInstanceRoutes_Snapshot verifies GET /instances/:id/snapshot returns 200.
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
		DefRef: model.Latest("approval"),
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
		DefRef: model.Latest("signal-catch-approved"),
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

// TestAdminTimers exercises GET /admin/timers through the fiber app: the query
// string parsed into the filter, the aggregate gate behind total, the
// handler-side limit clamp, and the 400 mapping for a bad cursor. A
// route that drops the cursor silently re-serves page one forever, which no
// status-code-only assertion would catch.
func TestAdminTimers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path    string
		buildTA func(t *testing.T) service.TimerAdmin
		assert  func(t *testing.T, status int, body string)
	}{
		"total=true → aggregates present, and the store is asked for no total": {
			path: "/admin/timers?limit=2&cursor=opaque-cursor&total=true",
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
			assert: func(t *testing.T, status int, body string) {
				require.Equal(t, http.StatusOK, status, "body=%s", body)
				var got map[string]any
				require.NoError(t, json.Unmarshal([]byte(body), &got))
				assert.EqualValues(t, 3, got["total_count"], "total_count is the table total from Stats")
				assert.NotContains(t, got, "count", "count is the retired legacy field name")
				assert.Equal(t, "cursor-2", got["next_cursor"])
			},
		},
		"total=1 enables the aggregates just like total=true": {
			path: "/admin/timers?limit=2&total=1",
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
			assert: func(t *testing.T, status int, body string) {
				require.Equal(t, http.StatusOK, status, "body=%s", body)
				var got map[string]any
				require.NoError(t, json.Unmarshal([]byte(body), &got))
				assert.EqualValues(t, 3, got["total_count"])
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
			assert: func(t *testing.T, status int, body string) {
				require.Equal(t, http.StatusOK, status, "body=%s", body)
				var got map[string]any
				require.NoError(t, json.Unmarshal([]byte(body), &got))
				assert.NotContains(t, got, "total_count", "a plain paged request must not report a table total it never queried")
				assert.NotContains(t, got, "count")
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
			assert: func(t *testing.T, status int, body string) {
				require.Equal(t, http.StatusOK, status, "body=%s", body)
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
			assert: func(t *testing.T, status int, body string) {
				assert.Equal(t, http.StatusBadRequest, status, "body=%s", body)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t)

			app := newApp()
			fiber.AdminRoutes{Svc: svc, Timers: tc.buildTA(t)}.Customize(app)

			status, body := appDo(t, app, newGetRequest(t, tc.path))
			tc.assert(t, status, body)
		})
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
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
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
