// Package parity_test proves that the three HTTP transport adapters —
// transport/http/stdlib, transport/http/gin, and transport/http/fiber — behave
// identically for every request in the core parity table.
//
// # Design
//
// Each test case uses ONE harness (one service.Service backed by a shared
// in-memory store) and mounts all three adapters against the same svc. Because
// all adapters share the same service, the state they observe (timestamps,
// variables) is identical, so HTTP status codes and JSON-decoded response bodies
// must match after JSON normalisation.
//
// For write-once paths (POST /instances creating a new instance) each adapter
// hit uses a unique instance ID but the SAME definition so the returned envelope
// shape is compared structurally (field names + error codes) rather than
// value-for-value. The normalisation step re-encodes the decoded JSON, so map
// key ordering is canonical.
//
// Most cases compare both the HTTP status and the normalised body across the
// three adapters — including POST /messages 202, which emits an empty body on
// every adapter (fiber uses c.Status(202).Send(nil), not c.SendStatus).
//
// ⚠ "Most", not "all". Four kinds of case deliberately compare less, and reading
// the weaker assertion as an oversight is the mistake this note exists to
// prevent:
//
//   - Cases whose response embeds a generated instance ID compare JSON FIELD
//     NAMES only (fieldNames), never values.
//   - TestParity_ErrorEnvelopes marks one row noBodyParity, because each adapter
//     wraps a Qualifier.UnmarshalJSON failure in its own prose.
//   - TestParity_MaxBodyBytes_UnderCap's trailing-bytes row asserts a REAL
//     divergence (stdlib 201, gin 201, fiber 400) rather than parity.
//   - TestFiberDivergence_AboveFiberConfigBodyLimit asserts that fiber produces
//     NO response at all.
//
// ⚠ Parity is over the DECODED document, never the raw bytes: the stdlib adapter
// writes with json.NewEncoder, which appends a trailing "\n" that gin and fiber
// do not emit, so the raw bodies differ on every JSON response.
package parity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kartaladev/wrkflw/authz"

	ginlib "github.com/gin-gonic/gin"
	fiberlib "github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	fiberadapter "github.com/kartaladev/wrkflw/transport/http/fiber"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

func init() {
	// Suppress gin debug output in tests.
	ginlib.SetMode(ginlib.TestMode)
}

// adapterResult captures the HTTP status code and parsed JSON body returned by
// one adapter execution.
type adapterResult struct {
	// status is the HTTP response status code.
	status int
	// rawBody is the raw response body string.
	rawBody string
	// decoded is non-nil when rawBody is valid JSON.
	decoded any
	// header is the response header set, always populated —
	// parseAdapterResult takes it as a required argument.
	header http.Header
}

// normJSON round-trips decoded through JSON so that map key ordering is
// deterministic for comparison.
func normJSON(t *testing.T, v any) string {
	t.Helper()
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("normJSON marshal: %v", err)
	}
	return string(b)
}

// parseAdapterResult creates an adapterResult from a raw HTTP status, response
// header and body bytes.
//
// ⚠ header is a required argument rather than a field callers fill in
// afterwards, deliberately: five call sites build an adapterResult, and a
// header-asserting test passes vacuously against any that forgot to set it —
// an absent header and an unrecorded one are indistinguishable at the
// assertion.
func parseAdapterResult(status int, header http.Header, body []byte) adapterResult {
	ar := adapterResult{status: status, rawBody: string(body), header: header}
	if len(body) > 0 {
		var v any
		if json.Unmarshal(body, &v) == nil {
			ar.decoded = v
		}
	}
	return ar
}

// reqFactory is a function that produces a fresh *http.Request every time it is
// called. Using a factory instead of sharing one request avoids the "body already
// consumed" problem when the same request is driven through multiple handlers.
type reqFactory func(t *testing.T) *http.Request

// jsonReqFactory returns a reqFactory that builds a POST request with the given
// JSON body.
func jsonReqFactory(method, path string, body any) reqFactory {
	payload, _ := json.Marshal(body)
	return func(t *testing.T) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("jsonReqFactory: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		return req
	}
}

// getReqFactory returns a reqFactory that builds a GET request with no body.
func getReqFactory(path string) reqFactory {
	return func(t *testing.T) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		if err != nil {
			t.Fatalf("getReqFactory: %v", err)
		}
		return req
	}
}

// hitStdlib mounts svc on a fresh stdlib ServeMux, drives req through it, and
// returns the adapterResult.
func hitStdlib(t *testing.T, svc service.Service, mkReq reqFactory, withHealth bool, actor ...authz.Actor) adapterResult {
	t.Helper()
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, stdlib.WithRequestActor(parityActor(actor)))
	if withHealth {
		stdlib.MountHealth(mux)
	}
	req := mkReq(t)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return parseAdapterResult(rr.Code, rr.Header(), rr.Body.Bytes())
}

// hitGin mounts svc on a fresh gin engine backed by an httptest.Server, drives
// req through it, and returns the adapterResult.
func hitGin(t *testing.T, svc service.Service, mkReq reqFactory, withHealth bool, actor ...authz.Actor) adapterResult {
	t.Helper()
	r := ginlib.New()
	ginadapter.Mount(r, svc, ginadapter.WithRequestActor(parityActor(actor)))
	if withHealth {
		ginadapter.MountHealth(r)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Build a server-relative request and re-target it at the test server URL.
	localReq := mkReq(t)
	outReq, err := http.NewRequestWithContext(
		localReq.Context(),
		localReq.Method,
		srv.URL+localReq.URL.RequestURI(),
		localReq.Body,
	)
	if err != nil {
		t.Fatalf("hitGin: clone request: %v", err)
	}
	for k, vv := range localReq.Header {
		outReq.Header[k] = vv
	}

	resp, err := srv.Client().Do(outReq)
	if err != nil {
		t.Fatalf("hitGin: Do: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return parseAdapterResult(resp.StatusCode, resp.Header, b)
}

// hitFiber mounts svc on a fresh fiber App, drives req through app.Test, and
// returns the adapterResult.
func hitFiber(t *testing.T, svc service.Service, mkReq reqFactory, withHealth bool, actor ...authz.Actor) adapterResult {
	t.Helper()
	app := fiberlib.New()
	fiberadapter.Mount(app, svc, fiberadapter.WithRequestActor(parityActor(actor)))
	if withHealth {
		fiberadapter.MountHealth(app)
	}

	req := mkReq(t)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("hitFiber: app.Test: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return parseAdapterResult(resp.StatusCode, resp.Header, b)
}

// parityActor turns the variadic actor argument into a resolver.
//
// With no actor supplied it returns nil, which restores httpcore's default — the
// context seam — so an unauthenticated request is refused. That is what
// TestParity_PostTasksClaim_Unauthenticated_401 relies on.
func parityActor(actor []authz.Actor) httpcore.RequestActorFunc {
	if len(actor) == 0 {
		return nil
	}
	a := actor[0]
	return func(context.Context) (authz.Actor, error) { return a, nil }
}

// hitServer drives mkReq against an already-running httptest.Server, for the
// admin-route tests that mount a group by hand rather than through Mount.
func hitServer(t *testing.T, srv *httptest.Server, mkReq reqFactory) adapterResult {
	t.Helper()
	local := mkReq(t)
	out, err := http.NewRequestWithContext(local.Context(), local.Method,
		srv.URL+local.URL.RequestURI(), local.Body)
	if err != nil {
		t.Fatalf("hitServer: new request: %v", err)
	}
	out.Header = local.Header
	resp, err := srv.Client().Do(out)
	if err != nil {
		t.Fatalf("hitServer: do: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return parseAdapterResult(resp.StatusCode, resp.Header, b)
}

// hitFiberApp drives mkReq against an already-configured fiber app.
func hitFiberApp(t *testing.T, app *fiberlib.App, mkReq reqFactory) adapterResult {
	t.Helper()
	resp, err := app.Test(mkReq(t))
	if err != nil {
		t.Fatalf("hitFiberApp: app.Test: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return parseAdapterResult(resp.StatusCode, resp.Header, b)
}

// assertParity compares three adapter results. It always checks HTTP status
// parity. When bodyParity is true it also checks that the JSON-normalised bodies
// are identical.
func assertParity(t *testing.T, caseName string, s, g, f adapterResult, bodyParity bool) {
	t.Helper()
	if s.status != g.status || s.status != f.status {
		t.Errorf("[%s] HTTP status divergence: stdlib=%d gin=%d fiber=%d",
			caseName, s.status, g.status, f.status)
	}
	if !bodyParity {
		return
	}
	sn := normJSON(t, s.decoded)
	gn := normJSON(t, g.decoded)
	fn := normJSON(t, f.decoded)
	if sn != gn {
		t.Errorf("[%s] body divergence stdlib vs gin:\n  stdlib: %s\n     gin: %s", caseName, sn, gn)
	}
	if sn != fn {
		t.Errorf("[%s] body divergence stdlib vs fiber:\n  stdlib: %s\n   fiber: %s", caseName, sn, fn)
	}
}

// ---------------------------------------------------------------------------
// Individual parity tests
// ---------------------------------------------------------------------------

// TestParity_PostInstances_201 verifies that POST /instances with a valid body
// returns 201 across all three adapters with an identical response shape.
//
// Each adapter gets a unique instance ID because they all share the same
// service.Service; creating the same ID twice returns 422.
func TestParity_PostInstances_201(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	// The server generates a fresh instance ID per POST, so no client-supplied id is needed.
	mkReqFor := func() reqFactory {
		return jsonReqFactory(http.MethodPost, "/instances", map[string]any{
			"def_ref": "greeting",
			"vars":    map[string]any{"name": "ada"},
		})
	}

	s := hitStdlib(t, svc, mkReqFor(), false)
	g := hitGin(t, svc, mkReqFor(), false)
	f := hitFiber(t, svc, mkReqFor(), false)

	if s.status != http.StatusCreated {
		t.Fatalf("stdlib: want 201 got %d (body=%s)", s.status, s.rawBody)
	}
	if g.status != http.StatusCreated {
		t.Fatalf("gin: want 201 got %d (body=%s)", g.status, g.rawBody)
	}
	if f.status != http.StatusCreated {
		t.Fatalf("fiber: want 201 got %d (body=%s)", f.status, f.rawBody)
	}

	// All three return 201. Compare JSON field names/structure only (instance IDs differ).
	sFields := fieldNames(t, s.decoded)
	gFields := fieldNames(t, g.decoded)
	fFields := fieldNames(t, f.decoded)
	if sFields != gFields {
		t.Errorf("201 field names diverge stdlib vs gin: %q vs %q", sFields, gFields)
	}
	if sFields != fFields {
		t.Errorf("201 field names diverge stdlib vs fiber: %q vs %q", sFields, fFields)
	}
}

// fieldNames extracts the sorted JSON object keys from v as a JSON array string.
// Used to compare response structure without comparing values.
func fieldNames(t *testing.T, v any) string {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		return "<not-an-object>"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Sort deterministically.
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	b, _ := json.Marshal(keys)
	return string(b)
}

// TestParity_PostInstances_400_Validation verifies that POST /instances with an
// empty JSON body returns 400 and an identical error envelope across all adapters.
func TestParity_PostInstances_400_Validation(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mkReq := jsonReqFactory(http.MethodPost, "/instances", map[string]any{})

	s := hitStdlib(t, svc, mkReq, false)
	g := hitGin(t, svc, mkReq, false)
	f := hitFiber(t, svc, mkReq, false)

	if s.status != http.StatusBadRequest {
		t.Fatalf("stdlib: want 400 got %d (body=%s)", s.status, s.rawBody)
	}

	assertParity(t, "POST /instances 400", s, g, f, true)
}

// TestParity_GetInstance_200 verifies that GET /instances/{id} for an existing
// instance returns 200 and an identical body across all adapters.
// All three adapters share the same svc so the seeded instance is visible
// to all of them.
func TestParity_GetInstance_200(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()
	_, svc := transporttest.NewHarness(t, def)

	// Seed via the service — state is visible to all three adapters.
	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	mkReq := getReqFactory("/instances/" + pi.State().InstanceID)

	s := hitStdlib(t, svc, mkReq, false)
	g := hitGin(t, svc, mkReq, false)
	f := hitFiber(t, svc, mkReq, false)

	if s.status != http.StatusOK {
		t.Fatalf("stdlib: want 200 got %d (body=%s)", s.status, s.rawBody)
	}

	assertParity(t, "GET /instances/:id 200", s, g, f, true)
}

// TestParity_GetInstance_404 verifies that GET /instances/{id} for a missing id
// returns 404 and an identical error envelope across all adapters.
func TestParity_GetInstance_404(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mkReq := getReqFactory("/instances/parity-missing-does-not-exist")

	s := hitStdlib(t, svc, mkReq, false)
	g := hitGin(t, svc, mkReq, false)
	f := hitFiber(t, svc, mkReq, false)

	if s.status != http.StatusNotFound {
		t.Fatalf("stdlib: want 404 got %d (body=%s)", s.status, s.rawBody)
	}

	assertParity(t, "GET /instances/:id 404", s, g, f, true)
}

// TestParity_PostSignals_200 verifies that POST /instances/{id}/signals returns
// 200 and an identical body across all adapters.
func TestParity_PostSignals_200(t *testing.T) {
	t.Parallel()

	def := transporttest.SignalProcess("approved")
	_, svc := transporttest.NewHarness(t, def)

	if _, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("signal-catch-approved"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Only ONE of the three can deliver the signal before the instance reaches an end
	// state; after that the signal delivery will 422/404. Strategy: each adapter
	// gets its OWN seeded instance to avoid state-conflict.
	makeInstanceAndSignalReq := func() (service.Service, reqFactory) {
		_, svcLocal := transporttest.NewHarness(t, transporttest.SignalProcess("approved"))
		pi, err := svcLocal.StartInstance(context.Background(), service.StartInstanceRequest{
			DefRef: model.Latest("signal-catch-approved"),
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		instanceID := pi.State().InstanceID
		mkReq := jsonReqFactory(http.MethodPost, "/instances/"+instanceID+"/signals", map[string]any{
			"signal": "approved",
		})
		return svcLocal, mkReq
	}

	svcS, mkS := makeInstanceAndSignalReq()
	svcG, mkG := makeInstanceAndSignalReq()
	svcF, mkF := makeInstanceAndSignalReq()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	s := hitStdlib(t, svcS, mkS, false, manager)
	g := hitGin(t, svcG, mkG, false, manager)
	f := hitFiber(t, svcF, mkF, false, manager)

	if s.status != http.StatusOK {
		t.Fatalf("stdlib: want 200 got %d (body=%s)", s.status, s.rawBody)
	}
	if g.status != http.StatusOK {
		t.Fatalf("gin: want 200 got %d (body=%s)", g.status, g.rawBody)
	}
	if f.status != http.StatusOK {
		t.Fatalf("fiber: want 200 got %d (body=%s)", f.status, f.rawBody)
	}

	// Each adapter returned 200 with a signal-delivered body. Compare structure only
	// because instance IDs differ.
	sFields := fieldNames(t, s.decoded)
	gFields := fieldNames(t, g.decoded)
	fFields := fieldNames(t, f.decoded)
	if sFields != gFields {
		t.Errorf("signal 200 field names diverge stdlib vs gin: %q vs %q", sFields, gFields)
	}
	if sFields != fFields {
		t.Errorf("signal 200 field names diverge stdlib vs fiber: %q vs %q", sFields, fFields)
	}
}

// TestParity_PostMessages_202 verifies that POST /messages returns 202 with an
// empty body across ALL three adapters. The fiber adapter uses
// c.Status(202).Send(nil) (not c.SendStatus, which would append the status text
// "Accepted" as the body), so full body parity holds.
func TestParity_PostMessages_202(t *testing.T) {
	t.Parallel()

	def := transporttest.MessageProcess("order-shipped")
	_, svc := transporttest.NewHarness(t, def)

	if _, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("message-catch-order-shipped"),
		Vars:   map[string]any{"orderId": "42"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mkReq := jsonReqFactory(http.MethodPost, "/messages", map[string]any{
		"def_ref":         "message-catch-order-shipped:1",
		"name":            "order-shipped",
		"correlation_key": "42",
	})

	s := hitStdlib(t, svc, mkReq, false)
	g := hitGin(t, svc, mkReq, false)
	f := hitFiber(t, svc, mkReq, false)

	if s.status != http.StatusAccepted {
		t.Fatalf("stdlib: want 202 got %d (body=%s)", s.status, s.rawBody)
	}

	// All three adapters emit an empty body on 202 → full body parity.
	assertParity(t, "POST /messages 202", s, g, f, true)
}

// TestParity_PostTasksClaim_200 verifies that POST /tasks/{token}/claim returns
// 200 and an identical body structure across all adapters.
func TestParity_PostTasksClaim_200(t *testing.T) {
	t.Parallel()

	// Each adapter needs its own svc+token to avoid "already claimed" on second hit.
	makeApprovalAndClaimReq := func(instanceID string) (service.Service, reqFactory) {
		def := transporttest.ApprovalProcess()
		h, svcLocal := transporttest.NewHarness(t, def)
		taskID := transporttest.StartedApprovalInstance(t, h, instanceID)
		// ⚠ The body no longer carries the actor; the identity is supplied
		// through the seam by the hit helpers below. An empty object is sent rather than
		// no body so this case stays about PARITY of the 200 path, not about the
		// optional-body decode, which each adapter pins separately.
		mkReq := jsonReqFactory(http.MethodPost, "/tasks/"+taskID+"/claim", map[string]any{})
		return svcLocal, mkReq
	}

	svcS, mkS := makeApprovalAndClaimReq("parity-claim-stdlib")
	svcG, mkG := makeApprovalAndClaimReq("parity-claim-gin")
	svcF, mkF := makeApprovalAndClaimReq("parity-claim-fiber")

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	s := hitStdlib(t, svcS, mkS, false, manager)
	g := hitGin(t, svcG, mkG, false, manager)
	f := hitFiber(t, svcF, mkF, false, manager)

	if s.status != http.StatusOK {
		t.Fatalf("stdlib: want 200 got %d (body=%s)", s.status, s.rawBody)
	}
	if g.status != http.StatusOK {
		t.Fatalf("gin: want 200 got %d (body=%s)", g.status, g.rawBody)
	}
	if f.status != http.StatusOK {
		t.Fatalf("fiber: want 200 got %d (body=%s)", f.status, f.rawBody)
	}

	// Compare field structure (instance IDs differ per adapter).
	sFields := fieldNames(t, s.decoded)
	gFields := fieldNames(t, g.decoded)
	fFields := fieldNames(t, f.decoded)
	if sFields != gFields {
		t.Errorf("claim 200 field names diverge stdlib vs gin: %q vs %q", sFields, gFields)
	}
	if sFields != fFields {
		t.Errorf("claim 200 field names diverge stdlib vs fiber: %q vs %q", sFields, fFields)
	}
}

// TestParity_GetReadyz_200 verifies that GET /readyz returns 200 and an
// identical body across all adapters.
func TestParity_GetReadyz_200(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t)

	mkReq := getReqFactory("/readyz")

	s := hitStdlib(t, svc, mkReq, true)
	g := hitGin(t, svc, mkReq, true)
	f := hitFiber(t, svc, mkReq, true)

	if s.status != http.StatusOK {
		t.Fatalf("stdlib: want 200 got %d (body=%s)", s.status, s.rawBody)
	}

	assertParity(t, "GET /readyz 200", s, g, f, true)
}

// ---------------------------------------------------------------------------
// Error envelope parity
// ---------------------------------------------------------------------------

// TestParity_ErrorEnvelopes verifies that the JSON error envelope
// {"error":"<code>","message":"<text>"} is byte-for-byte identical across all
// three adapters for every error-producing case. These responses contain no
// timestamps, so exact JSON equality is achievable.
func TestParity_ErrorEnvelopes(t *testing.T) {
	t.Parallel()

	type errCase struct {
		name         string
		buildSvc     func(t *testing.T) service.Service
		mkReq        reqFactory
		wantStatus   int
		noBodyParity bool // set when adapters produce known-divergent error text
	}

	cases := []errCase{
		{
			name:       "404 unknown instance",
			buildSvc:   func(t *testing.T) service.Service { _, svc := transporttest.NewHarness(t); return svc },
			mkReq:      getReqFactory("/instances/does-not-exist"),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "400 missing def_ref",
			buildSvc:   func(t *testing.T) service.Service { _, svc := transporttest.NewHarness(t); return svc },
			mkReq:      jsonReqFactory(http.MethodPost, "/instances", map[string]any{"def_ref": ""}),
			wantStatus: http.StatusBadRequest,
			// Qualifier.UnmarshalJSON returns an error for "", which each adapter
			// wraps differently (fiber adds "bind from body: " prefix).
			noBodyParity: true,
		},
		{
			name:       "400 empty JSON body",
			buildSvc:   func(t *testing.T) service.Service { _, svc := transporttest.NewHarness(t); return svc },
			mkReq:      jsonReqFactory(http.MethodPost, "/instances", map[string]any{}),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "404 GET missing instance",
			buildSvc: func(t *testing.T) service.Service {
				_, svc := transporttest.NewHarness(t)
				return svc
			},
			mkReq:      getReqFactory("/instances/no-such-id"),
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := tc.buildSvc(t)

			s := hitStdlib(t, svc, tc.mkReq, false)
			g := hitGin(t, svc, tc.mkReq, false)
			f := hitFiber(t, svc, tc.mkReq, false)

			if s.status != tc.wantStatus {
				t.Fatalf("stdlib: want %d got %d (body=%s)", tc.wantStatus, s.status, s.rawBody)
			}

			// Error envelope must be byte-for-byte identical (no timestamps).
			assertParity(t, tc.name, s, g, f, !tc.noBodyParity)

			// The response must have an "error" field.
			m, ok := s.decoded.(map[string]any)
			if !ok || m["error"] == nil {
				t.Errorf("%s: want 'error' field in body, got %s", tc.name, s.rawBody)
			}
		})
	}
}

// TestParity_PostResolveCompensationStall_400 verifies that the
// compensation-stall escape endpoint is MOUNTED on all three adapters and
// rejects an unknown disposition identically.
//
// Mounting is the property under test. Each adapter's route table is written by
// hand and nothing enumerates them, so "mounted in the stdlib, gin and fiber
// groups" is otherwise an assumption — a missing route would surface as a 404
// here rather than as a silently absent feature.
//
// The unknown-verb case is chosen deliberately over a happy path: the zero value
// of engine.CompensationDisposition is CompensationRetry, a remote re-execution
// primitive, so an adapter that decoded a bogus verb to the zero value would
// re-invoke a compensation action nobody asked for. A 400 from all three proves
// none of them defaults.
func TestParity_PostResolveCompensationStall_400(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	mk := func() (service.Service, reqFactory) {
		h, svcLocal := transporttest.NewHarness(t, def)
		transporttest.StartedApprovalInstance(t, h, "parity-stall")
		return svcLocal, jsonReqFactory(http.MethodPost,
			"/admin/instances/parity-stall/compensation/resolve-stall",
			map[string]any{"command_id": "c1", "disposition": "obliterate"})
	}

	// Admin routes are deliberately NOT part of Mount — a consumer opts into them
	// separately, so they can sit behind different authorization. Each adapter is
	// therefore mounted by hand here rather than through hit{Stdlib,Gin,Fiber}.
	svcS, mkS := mk()
	mux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svcS}.Customize(mux)
	rrS := httptest.NewRecorder()
	mux.ServeHTTP(rrS, mkS(t))
	s := parseAdapterResult(rrS.Code, rrS.Header(), rrS.Body.Bytes())

	svcG, mkG := mk()
	ginRouter := ginlib.New()
	ginadapter.AdminRoutes{Svc: svcG}.Customize(ginRouter)
	srvG := httptest.NewServer(ginRouter)
	t.Cleanup(srvG.Close)
	g := hitServer(t, srvG, mkG)

	svcF, mkF := mk()
	fiberApp := fiberlib.New()
	fiberadapter.AdminRoutes{Svc: svcF}.Customize(fiberApp)
	f := hitFiberApp(t, fiberApp, mkF)

	for name, got := range map[string]adapterResult{"stdlib": s, "gin": g, "fiber": f} {
		if got.status == http.StatusNotFound {
			t.Fatalf("%s: route not mounted (404) — the endpoint must exist on every adapter", name)
		}
		if got.status != http.StatusBadRequest {
			t.Fatalf("%s: want 400 for an unknown disposition, got %d (body=%s)",
				name, got.status, got.rawBody)
		}
	}

	sFields := fieldNames(t, s.decoded)
	if gFields := fieldNames(t, g.decoded); sFields != gFields {
		t.Errorf("400 envelope diverges stdlib vs gin: %q vs %q", sFields, gFields)
	}
	if fFields := fieldNames(t, f.decoded); sFields != fFields {
		t.Errorf("400 envelope diverges stdlib vs fiber: %q vs %q", sFields, fFields)
	}
}

// ---------------------------------------------------------------------------
// Inbound body cap — cross-adapter parity
// ---------------------------------------------------------------------------
//
// The three adapters bound the inbound body by three DIFFERENT mechanisms, so
// agreement between them is a property that has to be tested, not derived:
//
//   - stdlib caps the READ with http.MaxBytesReader and classifies oversize by
//     errors.As against *http.MaxBytesError (transport/http/stdlib/body.go);
//   - gin does the same but re-buffers so ShouldBindJSON still sees a replayable
//     body (transport/http/gin/bodycap.go);
//   - fiber cannot use MaxBytesReader at all and pre-checks BOTH len(c.BodyRaw())
//     and len(c.Body()) before parsing (transport/http/fiber/bodylimit.go).
//
// Every test below is a PIN over behaviour phases 1 and 2 already built; none of
// them drove a production change. They exist because nothing else compares the
// three mechanisms against each other.

// startBody returns a syntactically valid POST /instances body of EXACTLY n
// bytes, padding the "name" process variable to make up the length. Exact
// lengths are what make the boundary cases meaningful: the cap is a
// strictly-greater-than comparison on all three adapters, so a body of exactly n
// bytes must be ADMITTED and n+1 refused.
func startBody(t *testing.T, n int) []byte {
	t.Helper()
	const prefix = `{"def_ref":"greeting","vars":{"name":"`
	const suffix = `"}}`
	require.GreaterOrEqual(t, n, len(prefix)+len(suffix),
		"startBody: n is below the fixed JSON scaffolding")
	return []byte(prefix + strings.Repeat("x", n-len(prefix)-len(suffix)) + suffix)
}

// rawReqFactory returns a reqFactory that sends payload verbatim, without the
// json.Marshal round-trip jsonReqFactory performs. The body-cap cases need byte
// control: an exact length, or trailing bytes that no marshaller would produce.
func rawReqFactory(method, path string, payload []byte) reqFactory {
	return func(t *testing.T) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(payload))
		require.NoError(t, err, "rawReqFactory")
		req.Header.Set("Content-Type", "application/json")
		return req
	}
}

// routeGroup bundles the three adapters' mount calls for ONE route group, so a
// case can name "the core routes" or "the admin routes" once and have all three
// adapters mounted identically.
//
// The three fields cannot be collapsed into one generic field: each adapter's
// httpcore.CustomizeOption is parametrised by its own router type
// (*http.ServeMux, gin.IRouter, fiber.Router), and Go has no way to quantify
// over the three in a single field.
type routeGroup struct {
	stdlib func(*http.ServeMux, service.Service, ...httpcore.CustomizeOption[*http.ServeMux])
	gin    func(ginlib.IRouter, service.Service, ...httpcore.CustomizeOption[ginlib.IRouter])
	fiber  func(fiberlib.Router, service.Service, ...httpcore.CustomizeOption[fiberlib.Router])
}

// coreRouteGroup is everything Mount registers — POST /instances among them.
// Its POST /instances handler is a REQUIRED-body decode site on all three
// adapters.
var coreRouteGroup = routeGroup{
	stdlib: stdlib.Mount,
	gin:    ginadapter.Mount,
	fiber:  fiberadapter.Mount,
}

// adminRouteGroup is the admin group, which carries the OPTIONAL-body decode
// site (POST /admin/instances/{id}/incidents/{incidentID}/resolve).
//
// ⚠ AdminRoutes is deliberately kept OUT of Mount so a consumer can put
// it behind separate authorization. That does NOT put admin routes beyond this
// suite's reach: they are mounted BY HAND here, exactly as
// TestParity_PostResolveCompensationStall_400 already does. Any claim that the
// parity suite "structurally cannot see admin routes" is false — this variable
// is the counter-example.
var adminRouteGroup = routeGroup{
	stdlib: func(mux *http.ServeMux, svc service.Service, o ...httpcore.CustomizeOption[*http.ServeMux]) {
		stdlib.AdminRoutes{Svc: svc}.Customize(mux, o...)
	},
	gin: func(r ginlib.IRouter, svc service.Service, o ...httpcore.CustomizeOption[ginlib.IRouter]) {
		ginadapter.AdminRoutes{Svc: svc}.Customize(r, o...)
	},
	fiber: func(r fiberlib.Router, svc service.Service, o ...httpcore.CustomizeOption[fiberlib.Router]) {
		fiberadapter.AdminRoutes{Svc: svc}.Customize(r, o...)
	},
}

// capResults holds one request's outcome on all three adapters.
type capResults struct {
	stdlib adapterResult
	gin    adapterResult
	fiber  adapterResult
	// fiberErr is non-nil when app.Test returned a TRANSPORT error instead of an
	// HTTP response — which is what fasthttp does when the body exceeds
	// fiber.Config.BodyLimit, before any route in the group runs. It is captured
	// rather than fatal so the divergence can be asserted; every parity case
	// requires it to be nil.
	fiberErr error
}

// runCapped mounts grp on all three adapters against the SAME svc, applying the
// body cap described by capBytes, and drives mk through each.
//
// capBytes nil means NO WithMaxBodyBytes option is passed at all — that is the
// only way to exercise the 1 MiB default httpcore.ResolveConfig installs, and it
// is a different code path from passing 1<<20 explicitly.
func runCapped(t *testing.T, grp routeGroup, svc service.Service, capBytes *int64, mk reqFactory) capResults {
	t.Helper()

	var (
		stdlibOpts []httpcore.CustomizeOption[*http.ServeMux]
		ginOpts    []httpcore.CustomizeOption[ginlib.IRouter]
		fiberOpts  []httpcore.CustomizeOption[fiberlib.Router]
	)
	if capBytes != nil {
		stdlibOpts = append(stdlibOpts, stdlib.WithMaxBodyBytes(*capBytes))
		ginOpts = append(ginOpts, ginadapter.WithMaxBodyBytes(*capBytes))
		fiberOpts = append(fiberOpts, fiberadapter.WithMaxBodyBytes(*capBytes))
	}

	var out capResults

	mux := http.NewServeMux()
	grp.stdlib(mux, svc, stdlibOpts...)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, mk(t))
	out.stdlib = parseAdapterResult(rr.Code, rr.Header(), rr.Body.Bytes())

	ginRouter := ginlib.New()
	grp.gin(ginRouter, svc, ginOpts...)
	srv := httptest.NewServer(ginRouter)
	t.Cleanup(srv.Close)
	out.gin = hitServer(t, srv, mk)

	app := fiberlib.New()
	grp.fiber(app, svc, fiberOpts...)
	resp, err := app.Test(mk(t))
	if err != nil {
		out.fiberErr = err
		return out
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	out.fiber = parseAdapterResult(resp.StatusCode, resp.Header, b)

	return out
}

// assertSameStatus requires all three adapters to have answered want.
func assertSameStatus(t *testing.T, got capResults, want int) {
	t.Helper()
	require.NoError(t, got.fiberErr, "fiber: expected an HTTP response, got a transport error")
	assert.Equal(t, want, got.stdlib.status, "stdlib status (body=%s)", got.stdlib.rawBody)
	assert.Equal(t, want, got.gin.status, "gin status (body=%s)", got.gin.rawBody)
	assert.Equal(t, want, got.fiber.status, "fiber status (body=%s)", got.fiber.rawBody)
}

// assertSameEnvelope requires the three JSON bodies to be identical after
// normalisation.
//
// ⚠ Normalised, not raw: the stdlib adapter writes with json.NewEncoder, which
// appends a trailing "\n" that gin and fiber do not emit. The three raw byte
// strings therefore differ by exactly that newline on every JSON response, and
// have since long before this delivery. Parity is over the decoded document.
func assertSameEnvelope(t *testing.T, got capResults) {
	t.Helper()
	sn := normJSON(t, got.stdlib.decoded)
	assert.Equal(t, sn, normJSON(t, got.gin.decoded), "envelope diverges stdlib vs gin")
	assert.Equal(t, sn, normJSON(t, got.fiber.decoded), "envelope diverges stdlib vs fiber")
}

// tooLargeEnvelope is the STATIC 413 body httpcore.ClassifyError emits for
// httpcore.ErrRequestBodyTooLarge. It carries no attacker-controlled text by
// design, so it can be pinned exactly.
var tooLargeEnvelope = httpcore.ErrorBody{
	Error:   "request_too_large",
	Message: "request body exceeds the configured limit",
}

// assertTooLargeEnvelope requires each adapter's body to be exactly the static
// 413 envelope — not merely identical to its siblings, which three identically
// wrong adapters would also satisfy.
func assertTooLargeEnvelope(t *testing.T, got capResults) {
	t.Helper()
	want, err := json.Marshal(tooLargeEnvelope)
	require.NoError(t, err)
	for name, ar := range map[string]adapterResult{
		"stdlib": got.stdlib, "gin": got.gin, "fiber": got.fiber,
	} {
		assert.JSONEq(t, string(want), ar.rawBody, "%s: 413 envelope", name)
	}
}

// capOf returns a pointer to n, for the runCapped capBytes parameter.
func capOf(n int64) *int64 { return &n }

// TestParity_MaxBodyBytes_CoreRoute_413 pins the inbound cap on an
// ordinary REQUIRED-body route (POST /instances): all three adapters must refuse
// an oversize body with the same status AND the same envelope.
//
// This is the case a per-adapter test cannot cover — each adapter's own package
// can only prove its own mechanism, and the three mechanisms are unrelated
// (MaxBytesReader vs MaxBytesReader-plus-rebuffer vs a BodyRaw/Body pre-check).
// Divergence here would mean a consumer who swaps adapters silently changes the
// status their clients see.
//
// ⚠ Every fixture stays BELOW fiber.Config.BodyLimit (4 MiB default). Above it
// fasthttp refuses the request before the route group is entered, so fiber
// produces no ErrorBody at all and parity is not merely violated but undefined —
// see TestFiberDivergence_AboveFiberConfigBodyLimit.
func TestParity_MaxBodyBytes_CoreRoute_413(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		capBytes *int64
		body     func(t *testing.T) []byte
		assert   func(t *testing.T, got capResults)
	}

	cases := []testCase{
		{
			// No option at all: this is the ONLY case that exercises the 1 MiB
			// default installed by httpcore.ResolveConfig's struct literal.
			name:     "default 1 MiB cap refuses a 2 MiB body on all three",
			capBytes: nil,
			body:     func(t *testing.T) []byte { return startBody(t, 2<<20) },
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusRequestEntityTooLarge)
				assertSameEnvelope(t, got)
				assertTooLargeEnvelope(t, got)
			},
		},
		{
			name:     "explicit 64-byte cap refuses a 256-byte body on all three",
			capBytes: capOf(64),
			body:     func(t *testing.T) []byte { return startBody(t, 256) },
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusRequestEntityTooLarge)
				assertSameEnvelope(t, got)
				assertTooLargeEnvelope(t, got)
			},
		},
		{
			// Boundary, refusing side. All three compare strictly greater-than,
			// so exactly one byte over must refuse on all three.
			name:     "one byte over the cap refuses on all three",
			capBytes: capOf(1024),
			body:     func(t *testing.T) []byte { return startBody(t, 1025) },
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusRequestEntityTooLarge)
				assertTooLargeEnvelope(t, got)
			},
		},
		{
			// Boundary, admitting side — the half that catches an off-by-one in
			// the direction that BREAKS working traffic rather than the direction
			// that leaks. A body of exactly n bytes is admitted and reaches the
			// engine, so the answer is 201.
			name:     "a body of exactly the cap is admitted on all three",
			capBytes: capOf(1024),
			body:     func(t *testing.T) []byte { return startBody(t, 1024) },
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusCreated)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// One harness, all three adapters — the file's standing design.
			_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
			mk := rawReqFactory(http.MethodPost, "/instances", tc.body(t))

			tc.assert(t, runCapped(t, coreRouteGroup, svc, tc.capBytes, mk))
		})
	}
}

// TestParity_MaxBodyBytes_AdminOptionalBody pins the cap on the OPTIONAL-body
// admin route, POST /admin/instances/{id}/incidents/{incidentID}/resolve.
//
// This route is the reason the cap is not simply "wrap the decoder": all three
// adapters DISCARD its decode error so that an absent body still succeeds. An
// implementation that reached oversize-detection through the discarded error
// would leave exactly this one route reading an unbounded body into memory. The
// three "under-cap" rows are what prove the refusal did not also break the
// route's actual contract.
//
// The instance does not exist, so an admitted request answers 404 — that 404 is
// the evidence the handler RAN, which a 413 would not distinguish from a
// short-circuit.
func TestParity_MaxBodyBytes_AdminOptionalBody(t *testing.T) {
	t.Parallel()

	const adminPath = "/admin/instances/parity-cap-inst/incidents/inc-1/resolve"

	type testCase struct {
		name     string
		capBytes *int64
		body     []byte
		assert   func(t *testing.T, got capResults)
	}

	cases := []testCase{
		{
			name:     "oversize body is refused before the optional decode",
			capBytes: capOf(64),
			body:     []byte(`{"add_attempts":1,"pad":"` + strings.Repeat("x", 256) + `"}`),
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusRequestEntityTooLarge)
				assertSameEnvelope(t, got)
				assertTooLargeEnvelope(t, got)
			},
		},
		{
			name:     "under-cap body still reaches the handler",
			capBytes: capOf(4096),
			body:     []byte(`{"add_attempts":1}`),
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusNotFound)
				assertSameEnvelope(t, got)
			},
		},
		{
			// The route's whole point: no body is a legal request. A cap that
			// answered 413 (or 400) here would break it on every adapter.
			name:     "an EMPTY body still reaches the handler",
			capBytes: capOf(4096),
			body:     nil,
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusNotFound)
				assertSameEnvelope(t, got)
			},
		},
		{
			name:     "the cap disabled admits a 2 MiB body on this route too",
			capBytes: capOf(0),
			body:     []byte(`{"add_attempts":1,"pad":"` + strings.Repeat("x", 2<<20) + `"}`),
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusNotFound)
				assertSameEnvelope(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
			mk := rawReqFactory(http.MethodPost, adminPath, tc.body)

			tc.assert(t, runCapped(t, adminRouteGroup, svc, tc.capBytes, mk))
		})
	}
}

// TestParity_MaxBodyBytes_UnderCap pins what an under-cap body does, which is
// the half of the cap that ordinary traffic exercises.
//
// ⚠ The trailing-bytes row records a REAL, PRE-EXISTING divergence rather than
// asserting parity: stdlib and gin decode with a json.Decoder, which stops at the
// end of the first complete value and never looks at what follows, while fiber's
// binder calls json.Unmarshal, which rejects trailing content. MEASURED
// 2026-08-21 with a 4096-byte cap and `{...}` + 64 trailing "x": stdlib 201, gin
// 201, fiber 400 "bind from body: invalid character 'x' after top-level value".
// The body cap did not cause this and does not change it — the row exists so the
// divergence is pinned rather than discovered later and mistaken for a cap bug.
func TestParity_MaxBodyBytes_UnderCap(t *testing.T) {
	t.Parallel()

	clean := `{"def_ref":"greeting","vars":{"name":"ada"}}`

	type testCase struct {
		name   string
		body   []byte
		assert func(t *testing.T, got capResults)
	}

	cases := []testCase{
		{
			name: "a clean under-cap body succeeds identically on all three",
			body: []byte(clean),
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusCreated)
			},
		},
		{
			name: "under-cap trailing bytes: stdlib and gin admit, fiber rejects",
			body: []byte(clean + "   " + strings.Repeat("x", 64)),
			assert: func(t *testing.T, got capResults) {
				require.NoError(t, got.fiberErr)
				assert.Equal(t, http.StatusCreated, got.stdlib.status,
					"stdlib: json.Decoder ignores trailing bytes (body=%s)", got.stdlib.rawBody)
				assert.Equal(t, http.StatusCreated, got.gin.status,
					"gin: ShouldBindJSON uses json.Decoder too (body=%s)", got.gin.rawBody)
				assert.Equal(t, http.StatusBadRequest, got.fiber.status,
					"fiber: Bind().JSON uses json.Unmarshal, which refuses trailing bytes (body=%s)",
					got.fiber.rawBody)
				// Not 413: the whole body is under the cap, so this is a PARSE
				// divergence and not a cap divergence.
				assert.NotEqual(t, http.StatusRequestEntityTooLarge, got.fiber.status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
			mk := rawReqFactory(http.MethodPost, "/instances", tc.body)

			tc.assert(t, runCapped(t, coreRouteGroup, svc, capOf(4096), mk))
		})
	}
}

// TestParity_MaxBodyBytes_Disabled pins that n <= 0 disables the cap on all
// three adapters, admitting a body far above the 1 MiB default.
//
// The disabled path is a distinct branch in each adapter and each got it wrong in
// a different way at first: http.MaxBytesReader(w, body, 0) refuses EVERY
// non-empty body, and fiber's plain comparison against 0 refuses every body of
// one byte or more. "Disabled means 413 on everything" is the failure this pins
// against, so the assertion checks explicitly that the answer is not 413.
//
// ⚠ 2 MiB, not 8: the fixture must stay below fiber.Config.BodyLimit (4 MiB), or
// fasthttp refuses it before the route runs and the test would prove nothing
// about the adapter's own cap.
func TestParity_MaxBodyBytes_Disabled(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		capBytes *int64
		assert   func(t *testing.T, got capResults)
	}

	cases := []testCase{
		{
			name:     "zero disables the cap",
			capBytes: capOf(0),
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusCreated)
				assert.NotEqual(t, http.StatusRequestEntityTooLarge, got.stdlib.status)
			},
		},
		{
			name:     "a negative cap disables it too",
			capBytes: capOf(-1),
			assert: func(t *testing.T, got capResults) {
				assertSameStatus(t, got, http.StatusCreated)
				assert.NotEqual(t, http.StatusRequestEntityTooLarge, got.stdlib.status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
			mk := rawReqFactory(http.MethodPost, "/instances", startBody(t, 2<<20))

			tc.assert(t, runCapped(t, coreRouteGroup, svc, tc.capBytes, mk))
		})
	}
}

// TestFiberDivergence_AboveFiberConfigBodyLimit records the ONE place the three
// adapters cannot be brought into parity, and is deliberately NOT written as a
// parity assertion.
//
// Above fiber.Config.BodyLimit (4 MiB by default) fasthttp refuses the request
// before the route group is entered. Nothing in this repo runs: no oversizeBody
// check, no writeErr, no ErrorBody envelope, no observability log. stdlib and gin
// have no equivalent ceiling and answer with the ordinary 413 envelope.
//
// ⚠ Under app.Test the refusal does not even surface as an HTTP response —
// app.Test returns an ERROR ("body size exceeds the given limit") and there is no
// status code to compare. MEASURED 2026-08-21 with a 5 MiB body and a 1 MiB
// adapter cap: stdlib 413, gin 413, fiber → app.Test error, no response.
//
// A consumer who needs the 413 envelope for bodies this large raises
// fiber.Config.BodyLimit above their WithMaxBodyBytes value, so the adapter's own
// check is the one that fires. MEASURED 2026-08-21 by re-running this exact case
// against fiber.New(fiber.Config{BodyLimit: 16 << 20}): app.Test returned no
// error and fiber answered 413 with the ordinary
// {"error":"request_too_large",...} envelope — full parity with stdlib and gin.
// The ceiling is fiber's, not this adapter's, and it is the consumer's to raise.
// That trade-off is documented on fiber.WithMaxBodyBytes.
func TestFiberDivergence_AboveFiberConfigBodyLimit(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
	// 5 MiB: above fiber's 4 MiB default BodyLimit, and above the 1 MiB cap the
	// adapters are given, so stdlib and gin refuse it through the normal path.
	mk := rawReqFactory(http.MethodPost, "/instances", startBody(t, 5<<20))

	got := runCapped(t, coreRouteGroup, svc, capOf(1<<20), mk)

	assert.Equal(t, http.StatusRequestEntityTooLarge, got.stdlib.status,
		"stdlib has no framework-level ceiling and answers the ordinary 413")
	assert.Equal(t, http.StatusRequestEntityTooLarge, got.gin.status,
		"gin has no framework-level ceiling and answers the ordinary 413")

	require.Error(t, got.fiberErr,
		"fiber: fasthttp must refuse a body above fiber.Config.BodyLimit before the route runs")
	assert.Contains(t, got.fiberErr.Error(), "body size exceeds the given limit")
	assert.Empty(t, got.fiber.rawBody,
		"fiber: no ErrorBody envelope is produced — there is no response at all")
}

// TestParity_PostTasksClaim_Unauthenticated_401 pins that all three adapters are
// identically FAIL-CLOSED, not merely identically functional.
//
// ⚠ Parity of the happy path is the weaker property. Previously every adapter
// accepted a body-supplied actor identically; what matters now is that each refuses an
// unauthenticated caller with the same status AND the same error envelope, so a
// consumer cannot tell the adapters apart by how they reject.
func TestParity_PostTasksClaim_Unauthenticated_401(t *testing.T) {
	t.Parallel()

	makeReq := func(instanceID string) (service.Service, reqFactory) {
		def := transporttest.ApprovalProcess()
		h, svcLocal := transporttest.NewHarness(t, def)
		taskID := transporttest.StartedApprovalInstance(t, h, instanceID)
		// The body still carries the legacy body-supplied actor on purpose: a
		// forged manager must not promote an unauthenticated caller on ANY adapter.
		return svcLocal, jsonReqFactory(http.MethodPost, "/tasks/"+taskID+"/claim", map[string]any{
			"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
		})
	}

	svcS, mkS := makeReq("parity-claim-401-stdlib")
	svcG, mkG := makeReq("parity-claim-401-gin")
	svcF, mkF := makeReq("parity-claim-401-fiber")

	// No actor argument ⇒ the default context-seam resolver ⇒ nothing authenticated.
	s := hitStdlib(t, svcS, mkS, false)
	g := hitGin(t, svcG, mkG, false)
	f := hitFiber(t, svcF, mkF, false)

	for name, got := range map[string]adapterResult{"stdlib": s, "gin": g, "fiber": f} {
		if got.status != http.StatusUnauthorized {
			t.Fatalf("%s: want 401 for an unauthenticated claim, got %d (body=%s)", name, got.status, got.rawBody)
		}
	}
	assertParity(t, "POST /tasks/{token}/claim unauthenticated", s, g, f, true)
}
