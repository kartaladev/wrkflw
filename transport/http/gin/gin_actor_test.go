// gin_actor_test.go — the identity seam for the gin adapter. The actor a
// human-task route acts as comes from the mount's RequestActorFunc (by default
// the authz context seam a consumer's middleware writes to), never from the
// request body. These tests pin that seam, the fail-closed 401 when nothing
// authenticated the request, and the claim route's now-optional body.
package gin_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ginlib "github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// staticActor returns a RequestActorFunc that authenticates every request as the
// same principal — the test stand-in for a consumer's real authentication.
func staticActor(id string, roles ...string) httpcore.RequestActorFunc {
	return func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: id, Roles: roles}, nil
	}
}

// postClaim sends a POST to the claim route. body == nil sends NO body at all —
// no Content-Length, no Content-Type — which is what a client migrated to the
// now-fieldless ClaimInput actually puts on the wire.
//
// ⚠ It exists because gin_test.go's post() marshals a nil body to the literal
// "null", a four-byte body that would mask the absent-body case entirely.
func postClaim(t *testing.T, srv *httptest.Server, token string, body *string) httpResp {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(*body)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/tasks/"+token+"/claim", rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer drainClose(resp)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return httpResp{StatusCode: resp.StatusCode, Body: buf.Bytes()}
}

// newApprovalSrv mounts the routes over an approval process (a user task
// eligible to role "manager") and returns the server and a token parked at it.
func newApprovalSrv(t *testing.T, instanceID string, opts ...httpcore.CustomizeOption[ginlib.IRouter]) (*httptest.Server, string) {
	t.Helper()

	h, svc := transporttest.NewHarness(t, transporttest.ApprovalProcess())
	r := ginlib.New()
	ginadapter.Mount(r, svc, opts...)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, transporttest.StartedApprovalInstance(t, h, instanceID)
}

// TestRequestActorOptionAliases pins that the two gin aliases reach the resolved
// config. They exist because httpcore's generic forms take R only in their result
// type, so httpcore.WithRequestActor(fn) does not compile without spelling
// gin.IRouter out; a broken alias would be a compile error at every call site.
//
// ⚠ The timeout row asserts the CONFIG FIELD, not an end-to-end deadline, and
// deliberately so. SOURCE-VERIFIED (2026-08-26, httpcore/endpoints.go:116,133,153):
// all three human-task endpoints call resolveRequestActor(ctx, actor, 0) with a
// hardcoded 0, so cfg.RequestActorTimeout does not currently bound anything on the
// routes and a route-level deadline test here would be vacuous. This row still
// fails if the alias stops writing the field.
func TestRequestActorOptionAliases(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opt    httpcore.CustomizeOption[ginlib.IRouter]
		assert func(t *testing.T, cfg httpcore.CustomizeConfig[ginlib.IRouter])
	}

	cases := []testCase{
		{
			name: "WithRequestActor installs the resolver",
			opt:  ginadapter.WithRequestActor(staticActor("alice", "manager")),
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[ginlib.IRouter]) {
				require.NotNil(t, cfg.RequestActor)
				got, err := cfg.RequestActor(t.Context())
				require.NoError(t, err)
				assert.Equal(t, authz.Actor{ID: "alice", Roles: []string{"manager"}}, got)
			},
		},
		{
			name: "WithRequestActorTimeout sets the bound",
			opt:  ginadapter.WithRequestActorTimeout(250 * time.Millisecond),
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[ginlib.IRouter]) {
				assert.Equal(t, 250*time.Millisecond, cfg.RequestActorTimeout)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, httpcore.ResolveConfig(tc.opt))
		})
	}
}

// TestTaskRoutes_ActorComesFromMiddlewareNotTheBody pins the headline case: an
// authenticated VIEWER whose request body claims to be a manager is judged as the
// viewer and refused with 403. Previously the body won and this was a 200.
//
// The middleware uses the idiom that actually works under gin — reassigning
// gc.Request with the enriched context — because gin/groups.go hands httpcore
// gc.Request.Context(). See TestTaskRoutes_GinSetDoesNotAuthenticate for the
// idiom that does NOT work.
func TestTaskRoutes_ActorComesFromMiddlewareNotTheBody(t *testing.T) {
	t.Parallel()

	authenticateViewer := func(gc *ginlib.Context) {
		ctx := authz.ContextWithActor(gc.Request.Context(), authz.Actor{
			ID:    "vera",
			Roles: []string{"viewer"},
		})
		gc.Request = gc.Request.WithContext(ctx)
		gc.Next()
	}

	srv, token := newApprovalSrv(t, "gin-actor-seam-1", ginadapter.WithMiddleware(authenticateViewer))

	// The body asserts a manager identity; it must be ignored entirely.
	forged := `{"actor":{"id":"mallory","roles":["manager"]}}`
	resp := postClaim(t, srv, token, &forged)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"the forged body actor must not be believed; got body %s", resp.Body)
}

// TestTaskRoutes_NoIdentity401 pins the fail-closed default: a bare mount has no
// authentication middleware, so nothing put an actor on the context and the route
// refuses with 401 instead of proceeding as the zero actor.
func TestTaskRoutes_NoIdentity401(t *testing.T) {
	t.Parallel()

	srv, token := newApprovalSrv(t, "gin-actor-seam-2")

	resp := postClaim(t, srv, token, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "body %s", resp.Body)
	assert.Contains(t, string(resp.Body), `"unauthenticated"`)
}

// TestTaskRoutes_GinSetDoesNotAuthenticate pins a CONTRACT, not an accident.
//
// gc.Set is gin's canonical way for middleware to pass a value down the chain,
// and it is the first thing a consumer will reach for. It does NOT reach
// gc.Request.Context(), which is the context gin/groups.go hands httpcore.
// MEASURED (throwaway probe, 2026-08-26, one request through each middleware,
// reading BOTH channels in the handler):
//
//	gc.Set (canonical)                  Request.Context()=(ok=false)       gc.Get={from-middleware [viewer] map[]}(ok=true)
//	gc.Request = ...WithContext(...)    Request.Context()=from-middleware  gc.Get=<nil>(ok=false)
//
// The two channels are disjoint: neither idiom is visible through the other.
//
// So a consumer who authenticates via gc.Set gets a fail-closed 401 rather than a
// silently unauthenticated request or a false identity. That is the desired
// outcome, and this test is what keeps it true.
//
// ⚠ If this test ever starts returning 403 — i.e. gc.Set began reaching the
// request context — gin's behaviour has changed underneath us. Do NOT "just fix
// the test": every piece of consumer guidance naming the authenticating channel
// (examples/, WithRequestActor's godoc) must be revisited first.
func TestTaskRoutes_GinSetDoesNotAuthenticate(t *testing.T) {
	t.Parallel()

	authenticateViaSet := func(gc *ginlib.Context) {
		gc.Set("actor", authz.Actor{ID: "vera", Roles: []string{"viewer"}})
		gc.Next()
	}

	srv, token := newApprovalSrv(t, "gin-actor-seam-3", ginadapter.WithMiddleware(authenticateViaSet))

	resp := postClaim(t, srv, token, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"gc.Set must NOT authenticate; got body %s", resp.Body)
}

// TestTaskRoutes_ClaimBodyIsOptionalButStillBounded covers the claim route's body
// contract after ClaimInput lost its last field: any body — including none — is
// accepted and ignored, but an OVERSIZE one is still refused with 413.
//
// Each row fails against the legacy required decoder for a different reason:
// the first three would decode-fail with EOF/`null` handling into 400, the
// malformed row into 400, and the oversize row is the response contract that
// an unguarded optional decode would silently drop to 200.
func TestTaskRoutes_ClaimBodyIsOptionalButStillBounded(t *testing.T) {
	t.Parallel()

	body := func(s string) *string { return &s }

	type testCase struct {
		name       string
		instanceID string
		body       *string
		opts       []httpcore.CustomizeOption[ginlib.IRouter]
		assert     func(t *testing.T, resp httpResp)
	}

	cases := []testCase{
		{
			name:       "absent body claims the task",
			instanceID: "gin-claim-body-1",
			body:       nil,
			assert: func(t *testing.T, resp httpResp) {
				assert.Equal(t, http.StatusOK, resp.StatusCode, "body %s", resp.Body)
			},
		},
		{
			name:       "empty body claims the task",
			instanceID: "gin-claim-body-2",
			body:       body(""),
			assert: func(t *testing.T, resp httpResp) {
				assert.Equal(t, http.StatusOK, resp.StatusCode, "body %s", resp.Body)
			},
		},
		{
			// A client marshalling a nil DTO sends this; gin_test.go's post()
			// helper does exactly that.
			name:       "null literal claims the task",
			instanceID: "gin-claim-body-3",
			body:       body("null"),
			assert: func(t *testing.T, resp httpResp) {
				assert.Equal(t, http.StatusOK, resp.StatusCode, "body %s", resp.Body)
			},
		},
		{
			// Documented consequence of an OPTIONAL decode: there is no field
			// left to misparse, so a malformed document is ignored rather than
			// refused. The same is already true of the optional-body admin route.
			name:       "malformed body is ignored, not refused",
			instanceID: "gin-claim-body-4",
			body:       body("not-json"),
			assert: func(t *testing.T, resp httpResp) {
				assert.Equal(t, http.StatusOK, resp.StatusCode, "body %s", resp.Body)
			},
		},
		{
			// ⚠ The row that keeps the body cap on this route. "Optional" is
			// not "unbounded": without the oversize arm the claim route would
			// read an unbounded body into memory and answer 200.
			name:       "oversize body is still 413",
			instanceID: "gin-claim-body-5",
			body:       body(`{"padding":"` + strings.Repeat("x", 2048) + `"}`),
			opts:       []httpcore.CustomizeOption[ginlib.IRouter]{ginadapter.WithMaxBodyBytes(64)},
			assert: func(t *testing.T, resp httpResp) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "body %s", resp.Body)
				assert.JSONEq(t, body413, string(resp.Body))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]httpcore.CustomizeOption[ginlib.IRouter]{
				ginadapter.WithRequestActor(staticActor("alice", "manager")),
			}, tc.opts...)
			srv, token := newApprovalSrv(t, tc.instanceID, opts...)

			tc.assert(t, postClaim(t, srv, token, tc.body))
		})
	}
}

// TestUnauthenticatedBadJSONIs401NotThe400 is a CONTRACT test on the ORDER in
// which the three human-task routes refuse a request, not on any one status.
//
// The contract: on a mount that cannot authenticate, a caller must receive 401
// WITHOUT the adapter having read or parsed its body. An unauthenticated caller
// therefore learns nothing about whether its body parsed — a malformed document
// that would be a 400 for an authenticated caller is a 401 for this one.
//
// The current ordering:
//
//	401 (identity) → 413 (body cap) → 400 (decode) → 404 (lookup)
//
// It was 413 → 400 → 401 → 404. Resolving identity behind the body read let an
// UNAUTHENTICATED caller force a full MaxBodyBytes read (1 MiB by default) and
// hold the handler for BodyReadTimeout (30 s by default) before being told it
// was never authorized — an unauthenticated resource-consumption primitive, on
// precisely the routes that authenticate.
//
// ⚠ If a row here ever reports 400 again, that is NOT a stale expectation to
// update: it means httpcore.RequestActor has moved back BEHIND the body decode
// in groups.go. Fix the handler, not this test.
//
// What makes it fail today: move the httpcore.RequestActor call in
// groups.go below the decode for a route and that route's row turns 400.
func TestUnauthenticatedBadJSONIs401NotThe400(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		path   string
		assert func(t *testing.T, resp httpResp)
	}

	// One row per route that resolves an actor. All three assert identically —
	// the rows exist so a regression names the route that broke.
	wants401 := func(t *testing.T, resp httpResp) {
		t.Helper()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
			"unauthenticated caller must be refused before its body is parsed; body %s", resp.Body)
	}

	cases := []testCase{
		{name: "claim", path: "/tasks/tok/claim", assert: wants401},
		{name: "complete", path: "/tasks/tok/complete", assert: wants401},
		{name: "reassign", path: "/tasks/tok/reassign", assert: wants401},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Deliberately mounted with NO WithRequestActor: nothing can
			// authenticate, so identity resolution must fail closed.
			_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
			r := ginlib.New()
			ginadapter.TaskRoutes{Svc: svc}.Customize(r)
			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)

			// post() marshals the string to the JSON document "not-json",
			// which is a well-formed string but not the object the route
			// expects — a decode error for an authenticated caller.
			tc.assert(t, post(t, srv, tc.path, "not-json"))
		})
	}
}
