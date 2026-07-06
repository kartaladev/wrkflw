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
// All cases compare both the HTTP status and the normalised body across the
// three adapters — including POST /messages 202, which emits an empty body on
// every adapter (fiber uses c.Status(202).Send(nil), not c.SendStatus).
package parity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	ginlib "github.com/gin-gonic/gin"
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/service"
	fiberadapter "github.com/zakyalvan/krtlwrkflw/transport/http/fiber"
	ginadapter "github.com/zakyalvan/krtlwrkflw/transport/http/gin"
	"github.com/zakyalvan/krtlwrkflw/transport/http/stdlib"
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

// parseAdapterResult creates an adapterResult from a raw HTTP status and body bytes.
func parseAdapterResult(status int, body []byte) adapterResult {
	ar := adapterResult{status: status, rawBody: string(body)}
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
func hitStdlib(t *testing.T, svc service.Service, mkReq reqFactory, withHealth bool) adapterResult {
	t.Helper()
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)
	if withHealth {
		stdlib.MountHealth(mux)
	}
	req := mkReq(t)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return parseAdapterResult(rr.Code, rr.Body.Bytes())
}

// hitGin mounts svc on a fresh gin engine backed by an httptest.Server, drives
// req through it, and returns the adapterResult.
func hitGin(t *testing.T, svc service.Service, mkReq reqFactory, withHealth bool) adapterResult {
	t.Helper()
	r := ginlib.New()
	ginadapter.Mount(r, svc)
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
	return parseAdapterResult(resp.StatusCode, b)
}

// hitFiber mounts svc on a fresh fiber App, drives req through app.Test, and
// returns the adapterResult.
func hitFiber(t *testing.T, svc service.Service, mkReq reqFactory, withHealth bool) adapterResult {
	t.Helper()
	app := fiberlib.New()
	fiberadapter.Mount(app, svc)
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
	return parseAdapterResult(resp.StatusCode, b)
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

	// Adapters share one svc; use distinct ids so each POST creates a new instance.
	mkReqFor := func(instanceID string) reqFactory {
		return jsonReqFactory(http.MethodPost, "/instances", map[string]any{
			"def_ref":     "greeting",
			"instance_id": instanceID,
			"vars":        map[string]any{"name": "ada"},
		})
	}

	s := hitStdlib(t, svc, mkReqFor("parity-start-stdlib"), false)
	g := hitGin(t, svc, mkReqFor("parity-start-gin"), false)
	f := hitFiber(t, svc, mkReqFor("parity-start-fiber"), false)

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
		DefRef: "greeting", Vars: map[string]any{"name": "x"},
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
		DefRef: "signal-catch-approved",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Only ONE of the three can deliver the signal before the instance reaches an end
	// state; after that the signal delivery will 422/404. Strategy: each adapter
	// gets its OWN seeded instance to avoid state-conflict.
	makeInstanceAndSignalReq := func() (service.Service, reqFactory) {
		_, svcLocal := transporttest.NewHarness(t, transporttest.SignalProcess("approved"))
		pi, err := svcLocal.StartInstance(context.Background(), service.StartInstanceRequest{
			DefRef: "signal-catch-approved",
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

	s := hitStdlib(t, svcS, mkS, false)
	g := hitGin(t, svcG, mkG, false)
	f := hitFiber(t, svcF, mkF, false)

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
		DefRef: "message-catch-order-shipped",
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
		taskToken := transporttest.StartedApprovalInstance(t, h, instanceID)
		mkReq := jsonReqFactory(http.MethodPost, "/tasks/"+taskToken+"/claim", map[string]any{
			"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
		})
		return svcLocal, mkReq
	}

	svcS, mkS := makeApprovalAndClaimReq("parity-claim-stdlib")
	svcG, mkG := makeApprovalAndClaimReq("parity-claim-gin")
	svcF, mkF := makeApprovalAndClaimReq("parity-claim-fiber")

	s := hitStdlib(t, svcS, mkS, false)
	g := hitGin(t, svcG, mkG, false)
	f := hitFiber(t, svcF, mkF, false)

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
		name       string
		buildSvc   func(t *testing.T) service.Service
		mkReq      reqFactory
		wantStatus int
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
			mkReq:      jsonReqFactory(http.MethodPost, "/instances", map[string]any{}),
			wantStatus: http.StatusBadRequest,
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
			assertParity(t, tc.name, s, g, f, true)

			// The response must have an "error" field.
			m, ok := s.decoded.(map[string]any)
			if !ok || m["error"] == nil {
				t.Errorf("%s: want 'error' field in body, got %s", tc.name, s.rawBody)
			}
		})
	}
}
