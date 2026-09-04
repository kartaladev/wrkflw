package parity_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	ginlib "github.com/gin-gonic/gin"
	fiberlib "github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	fiberadapter "github.com/kartaladev/wrkflw/transport/http/fiber"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// TestParity_NosniffHeader asserts that every response this library writes,
// from every adapter, carries X-Content-Type-Options: nosniff.
//
// Why this belongs in the parity package rather than in each adapter's own
// tests: the three adapters write responses through three different framework
// APIs (http.ResponseWriter, gin's gc.JSON, fiber's c.Status().JSON()), so
// "all three set it" is precisely a cross-adapter claim, and a per-adapter test
// would let one drift without failing anything.
//
// ⚠ The rows cover three response paths that reach the wire by DIFFERENT code,
// not merely three different status codes:
//
//   - a 2xx body written by the endpoint handler itself;
//   - a 4xx envelope written by each adapter's writeErr;
//   - a health probe, registered through a separate Customize and easy to miss
//     when a header is added only to the main route groups.
//
// A row that only varied the status code would leave a whole write path
// unasserted while still passing — an earlier draft of this test did exactly
// that, with two rows that both happened to run through writeErr.
//
// ⚠ SCOPE: this covers responses THIS LIBRARY writes. A consumer middleware
// registered through WithMiddleware composes OUTSIDE the route wrapper, so a
// middleware that short-circuits (auth, rate limiting, CORS) answers without
// the header. That is a deliberate boundary, not an oversight — see
// TestParity_NosniffMiddleware_ShortCircuitingConsumerMiddleware below, which
// pins both the boundary and the opt-in NosniffMiddleware that crosses it.
func TestParity_NosniffHeader(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// withHealth mounts the health routes, which the probe row needs.
		withHealth bool
		mkReq      reqFactory
		// wantStatus is asserted on ALL THREE adapters, not just one: a row
		// whose status silently changed on one adapter would stop exercising
		// that adapter's intended write path while still passing.
		wantStatus int
	}

	cases := []testCase{
		{
			name:       "2xx handler-written body",
			withHealth: false,
			mkReq: jsonReqFactory(http.MethodPost, "/instances", map[string]any{
				"def_ref": "greeting",
				"vars":    map[string]any{"name": "ada"},
			}),
			wantStatus: http.StatusCreated,
		},
		{
			name:       "4xx error envelope",
			withHealth: false,
			mkReq:      getReqFactory("/instances/parity-nosniff-missing"),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "health probe",
			withHealth: true,
			mkReq:      getReqFactory("/readyz"),
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// LinearProcess is registered for every row so the 201 row has a
			// definition to start; the other rows are unaffected by it.
			_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())

			for _, a := range []struct {
				adapter string
				got     adapterResult
			}{
				{"stdlib", hitStdlib(t, svc, tc.mkReq, tc.withHealth)},
				{"gin", hitGin(t, svc, tc.mkReq, tc.withHealth)},
				{"fiber", hitFiber(t, svc, tc.mkReq, tc.withHealth)},
			} {
				assert.Equal(t, tc.wantStatus, a.got.status,
					"%s: row must exercise the intended write path (body=%s)",
					a.adapter, a.got.rawBody)
				assert.Equal(t, "nosniff", a.got.header.Get("X-Content-Type-Options"),
					"%s: must send X-Content-Type-Options: nosniff", a.adapter)
			}
		})
	}
}

// shortCircuitBody is the response a short-circuiting consumer middleware
// writes in the test below. Its leading bytes read as HTML deliberately: an
// HTML-shaped string echoed into a JSON envelope is the exact sniffing
// exposure X-Content-Type-Options exists to prevent, and reflecting a
// caller-influenced value into an error message is what a real auth or
// rate-limit middleware does.
var shortCircuitBody = map[string]any{"error": "<html>nope</html>"}

// hitStdlibShortCircuit mounts svc on a fresh mux, wraps it in a consumer
// middleware that answers 401 without delegating, and drives one request
// through it.
//
// ⚠ stdlib is wired differently from gin and fiber here on purpose, because it
// IS different: *http.ServeMux has no middleware seam, so this adapter exposes
// no WithMiddleware and a consumer composes handlers around the mux by hand.
// NosniffMiddleware therefore takes the next handler rather than being passed
// as an option — the same asymmetry a stdlib consumer meets.
func hitStdlibShortCircuit(t *testing.T, svc service.Service, mkReq reqFactory, withNosniff bool) adapterResult {
	t.Helper()
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	h := shortCircuitStdlib(mux)
	if withNosniff {
		h = stdlib.NosniffMiddleware(h)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mkReq(t))
	return parseAdapterResult(rr.Code, rr.Header(), rr.Body.Bytes())
}

// shortCircuitStdlib returns a middleware that answers 401 and never calls next.
// next is accepted, and ignored, so the handler reads as ordinary middleware
// rather than as a leaf handler that happens to sit where one belongs.
func shortCircuitStdlib(next http.Handler) http.Handler {
	_ = next
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(shortCircuitBody)
	})
}

// hitGinShortCircuit mounts svc behind a consumer middleware that aborts 401,
// optionally preceded by the adapter's NosniffMiddleware.
func hitGinShortCircuit(t *testing.T, svc service.Service, mkReq reqFactory, withNosniff bool) adapterResult {
	t.Helper()
	r := ginlib.New()

	var mw []ginlib.HandlerFunc
	if withNosniff {
		mw = append(mw, ginadapter.NosniffMiddleware())
	}
	mw = append(mw, func(gc *ginlib.Context) {
		gc.AbortWithStatusJSON(http.StatusUnauthorized, shortCircuitBody)
	})
	ginadapter.Mount(r, svc, ginadapter.WithMiddleware(mw...))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return hitServer(t, srv, mkReq)
}

// hitFiberShortCircuit mounts svc behind a consumer middleware that answers
// 401, optionally preceded by the adapter's NosniffMiddleware.
func hitFiberShortCircuit(t *testing.T, svc service.Service, mkReq reqFactory, withNosniff bool) adapterResult {
	t.Helper()
	app := fiberlib.New()

	var mw []fiberlib.Handler
	if withNosniff {
		mw = append(mw, fiberadapter.NosniffMiddleware())
	}
	mw = append(mw, func(c fiberlib.Ctx) error {
		return c.Status(http.StatusUnauthorized).JSON(shortCircuitBody)
	})
	fiberadapter.Mount(app, svc, fiberadapter.WithMiddleware(mw...))

	return hitFiberApp(t, app, mkReq)
}

// TestParity_NosniffMiddleware_ShortCircuitingConsumerMiddleware pins both
// halves of the boundary settled on #71: what the library does NOT cover on
// its own, and what NosniffMiddleware covers once a consumer opts in.
//
// The gap is real and is not a status-code detail. A consumer middleware
// registered through WithMiddleware (gin, fiber) — or composed around the mux
// by hand (stdlib) — runs OUTSIDE the per-route wrapper that sets the header,
// so a middleware that answers the request itself (auth 401, rate limit 429,
// CORS preflight reject, panic recovery 500) never reaches the wrapper.
//
// ⚠ The "without" row asserts the ABSENCE of the header on purpose. It is not
// a stale expectation waiting to be flipped: it is the recorded contract that
// this library does not insert its own handler into a router it was handed,
// and it must fail loudly if some later change starts doing so silently.
//
// ⚠ This lives in the parity package for the same reason TestParity_NosniffHeader
// does — "all three adapters offer this, and it works on all three" is exactly
// a cross-adapter claim, and the three wire it through three different seams.
//
// No ctx modifier: the subject is a handler chain, not a context-sensitive
// component, and the reqFactory helpers already build every request from
// t.Context().
func TestParity_NosniffMiddleware_ShortCircuitingConsumerMiddleware(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// withNosniff installs the adapter's NosniffMiddleware ahead of the
		// short-circuiting consumer middleware.
		withNosniff bool
		assert      func(t *testing.T, adapter string, got adapterResult)
	}

	cases := []testCase{
		{
			name:        "without NosniffMiddleware the consumer's own response carries no header",
			withNosniff: false,
			assert: func(t *testing.T, adapter string, got adapterResult) {
				require.Equal(t, http.StatusUnauthorized, got.status,
					"%s: the consumer middleware must be the one that answered (body=%s)",
					adapter, got.rawBody)
				assert.Empty(t, got.header.Get(httpcore.ContentTypeOptionsHeader),
					"%s: the library must not insert its own handler into a consumer's chain",
					adapter)
			},
		},
		{
			name:        "NosniffMiddleware covers the short-circuited response",
			withNosniff: true,
			assert: func(t *testing.T, adapter string, got adapterResult) {
				require.Equal(t, http.StatusUnauthorized, got.status,
					"%s: the consumer middleware must still be the one that answered (body=%s)",
					adapter, got.rawBody)
				assert.Equal(t, httpcore.NoSniff, got.header.Get(httpcore.ContentTypeOptionsHeader),
					"%s: NosniffMiddleware must set the header before the consumer short-circuits",
					adapter)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t)
			mkReq := getReqFactory("/instances/parity-nosniff-shortcircuit")

			for _, a := range []struct {
				adapter string
				got     adapterResult
			}{
				{"stdlib", hitStdlibShortCircuit(t, svc, mkReq, tc.withNosniff)},
				{"gin", hitGinShortCircuit(t, svc, mkReq, tc.withNosniff)},
				{"fiber", hitFiberShortCircuit(t, svc, mkReq, tc.withNosniff)},
			} {
				tc.assert(t, a.adapter, a.got)
			}
		})
	}
}
