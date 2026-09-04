// Package parity_test — the scope and multiplicity of WithMiddleware.
//
// The table here pins two properties of middleware a consumer passes to an
// adapter's WithMiddleware: how MANY times it runs for one request, and WHICH
// routes it runs on. Both are cross-adapter claims, which is why they live here
// rather than in either adapter's own tests — the whole point is that gin and
// fiber, which spell the option identically, do not agree on the second one.
package parity_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	ginlib "github.com/gin-gonic/gin"
	fiberlib "github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	fiberadapter "github.com/kartaladev/wrkflw/transport/http/fiber"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
)

// The three library paths below are owned by DIFFERENT route groups, and Mount
// registers those groups in this order: InstanceRoutes, TaskRoutes,
// MessageRoutes. That ordering is the whole point of having three rows instead
// of one — see the multiplicity note on the test.
const (
	instanceRoutePath = "/instances/parity-middleware-scope"
	taskRoutePath     = "/tasks/parity-middleware-scope/claim"
	messageRoutePath  = "/messages"
	// consumerRoutePath is a route the CONSUMER registers on the same router.
	consumerRoutePath = "/parity-consumer-route"
	// unroutedPath matches nothing either side registered.
	unroutedPath = "/parity-nothing-here"
)

// countingMountFunc mounts svc plus a consumer-owned route on a fresh router,
// passing a middleware that increments runs through the adapter's
// WithMiddleware, then drives exactly one request through it.
type countingMountFunc func(t *testing.T, svc service.Service, runs *atomic.Int32, mkReq reqFactory) adapterResult

// mountGinCounted wires the gin adapter with a counting consumer middleware and
// a consumer-owned route alongside the library's.
func mountGinCounted(t *testing.T, svc service.Service, runs *atomic.Int32, mkReq reqFactory) adapterResult {
	t.Helper()
	r := ginlib.New()
	ginadapter.Mount(r, svc, ginadapter.WithMiddleware(func(gc *ginlib.Context) {
		runs.Add(1)
		gc.Next()
	}))
	r.GET(consumerRoutePath, func(gc *ginlib.Context) { gc.String(http.StatusOK, "consumer") })

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return hitServer(t, srv, mkReq)
}

// mountFiberCounted wires the fiber adapter the same way.
func mountFiberCounted(t *testing.T, svc service.Service, runs *atomic.Int32, mkReq reqFactory) adapterResult {
	t.Helper()
	app := fiberlib.New()
	fiberadapter.Mount(app, svc, fiberadapter.WithMiddleware(func(c fiberlib.Ctx) error {
		runs.Add(1)
		return c.Next()
	}))
	app.Get(consumerRoutePath, func(c fiberlib.Ctx) error { return c.SendString("consumer") })

	return hitFiberApp(t, app, mkReq)
}

// postReqFactory returns a reqFactory for a POST carrying a JSON body. The
// bodies here only need to be well-formed enough to reach a handler; what is
// counted is middleware executions, not the response.
func postReqFactory(path, body string) reqFactory {
	return func(t *testing.T) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("postReqFactory: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		return req
	}
}

// TestParity_WithMiddleware_ScopeAndMultiplicity settles #74: how many times a
// consumer middleware passed to WithMiddleware runs for one request, and on
// which routes.
//
// # The two properties, and why both need rows
//
// Both adapters spell the option the same way — gin `r.Group("", mw...)`, fiber
// `r.Group("", args...)` — so a reader comparing them reasonably concludes they
// mean the same thing. They do not. gin's Group is OBJECT-scoped: it returns a
// new *RouterGroup carrying the handlers, and only routes registered on that
// object inherit them. fiber's routing is PATH-based: a group at the empty
// prefix registers at "/" and matches every request under it.
//
// MULTIPLICITY — the property that had actually broken. Mount calls Customize
// three times and each calls cfg.Wrap(r) once, so the option's router func runs
// three times per mount. On gin that is harmless: Group is a pure constructor,
// and three groups each see only their own routes. On fiber, Group REGISTERS,
// so mw landed at "/" three times, interleaved with the routes registered
// between them. MEASURED on fiber v3.4.0 before the fix, counting one
// middleware's executions for one request:
//
//	GET  /instances/:id       1      POST /messages           3
//	POST /tasks/:token/claim  2      <consumer route>         3
//	                                 <unrouted path>          3
//
// The count varied BY ENDPOINT, which is why it survived: a rate limiter
// counted one POST /messages as three requests and one GET /instances/:id as
// one, and nothing in any response body showed it. fiber's WithMiddleware now
// memoises one group per router, so every row below is exactly 1.
//
// ⚠ That is why the three library rows are three DIFFERENT route groups rather
// than three paths. A table using only instance routes would have read 1 both
// before and after the fix and asserted nothing — the pre-fix bug was invisible
// on exactly that endpoint.
//
// SCOPE — the property that remains divergent, deliberately. fiber cannot
// express gin's object scoping through Group at all: with the default empty
// base path there is no prefix to confine the middleware to, and fiber offers
// no other seam here short of prepending handlers per route, which is the
// httpcore route-table seam #40 reworks. So the fiber rows assert 1 on the
// consumer's own route and on an unrouted path, where gin asserts 0. That is a
// recorded divergence, not a passing expectation: it must fail loudly if it
// silently changes in either direction.
//
// ⚠ Counts are asserted EXACTLY, never as a lower bound. "At least once" would
// pass against the very defect this test exists to catch.
//
// No ctx modifier: the subject is a router's handler chain, not a
// context-sensitive component, and the reqFactory helpers already build every
// request from t.Context().
func TestParity_WithMiddleware_ScopeAndMultiplicity(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		mount  countingMountFunc
		mkReq  reqFactory
		assert func(t *testing.T, runs int32, got adapterResult)
	}

	// runsExactly builds the common assertion: one request ran the consumer's
	// middleware exactly n times.
	runsExactly := func(adapter string, n int32, why string) func(*testing.T, int32, adapterResult) {
		return func(t *testing.T, runs int32, _ adapterResult) {
			assert.Equal(t, n, runs, "%s: %s", adapter, why)
		}
	}

	cases := []testCase{
		{
			name:  "gin runs it once on an instance route",
			mount: mountGinCounted,
			mkReq: getReqFactory(instanceRoutePath),
			assert: runsExactly("gin", 1,
				"one request must run the consumer's middleware exactly once"),
		},
		{
			name:  "gin runs it once on a task route",
			mount: mountGinCounted,
			mkReq: postReqFactory(taskRoutePath, `{}`),
			assert: runsExactly("gin", 1,
				"the second-registered group must not see a second copy"),
		},
		{
			name:  "gin runs it once on a message route",
			mount: mountGinCounted,
			mkReq: postReqFactory(messageRoutePath, `{"name":"parity"}`),
			assert: runsExactly("gin", 1,
				"the third-registered group must not see a third copy"),
		},
		{
			name:  "gin does not run it on the consumer's own route",
			mount: mountGinCounted,
			mkReq: getReqFactory(consumerRoutePath),
			assert: func(t *testing.T, runs int32, got adapterResult) {
				assert.Equal(t, http.StatusOK, got.status,
					"gin: the consumer's own route must still answer")
				assert.Equal(t, int32(0), runs,
					"gin: Group is object-scoped, so mw must not reach a consumer route")
			},
		},
		{
			name:  "fiber runs it once on an instance route",
			mount: mountFiberCounted,
			mkReq: getReqFactory(instanceRoutePath),
			assert: runsExactly("fiber", 1,
				"one request must run the consumer's middleware exactly once"),
		},
		{
			name:  "fiber runs it once on a task route",
			mount: mountFiberCounted,
			mkReq: postReqFactory(taskRoutePath, `{}`),
			assert: runsExactly("fiber", 1,
				"was 2 before the memo — the 2nd Customize registered a 2nd copy"),
		},
		{
			name:  "fiber runs it once on a message route",
			mount: mountFiberCounted,
			mkReq: postReqFactory(messageRoutePath, `{"name":"parity"}`),
			assert: runsExactly("fiber", 1,
				"was 3 before the memo — a rate limiter counted one request as three"),
		},
		{
			name:  "fiber runs it once, not zero, on the consumer's own route",
			mount: mountFiberCounted,
			mkReq: getReqFactory(consumerRoutePath),
			assert: func(t *testing.T, runs int32, got adapterResult) {
				assert.Equal(t, http.StatusOK, got.status,
					"fiber: the consumer's own route must still answer")
				assert.Equal(t, int32(1), runs,
					"fiber: RECORDED DIVERGENCE from gin — path-based routing makes "+
						"this app-wide; the fix made it once, not narrower")
			},
		},
		{
			name:  "fiber runs it once on an unrouted path",
			mount: mountFiberCounted,
			mkReq: getReqFactory(unroutedPath),
			assert: runsExactly("fiber", 1,
				"was 3 before the memo — an unmatched path ran consumer auth work three times"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, svc := transporttest.NewHarness(t)
			var runs atomic.Int32

			got := tc.mount(t, svc, &runs, tc.mkReq)
			tc.assert(t, runs.Load(), got)
		})
	}
}
