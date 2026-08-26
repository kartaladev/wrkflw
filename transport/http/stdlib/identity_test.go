// Package stdlib_test — request-actor identity seam (ADR-0189).
//
// These tests pin the property the ADR exists for: the human-task routes act on
// the actor an AUTHENTICATION middleware established, never on one the request
// body asserts about itself.
package stdlib_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// staticActor is a [httpcore.RequestActorFunc] that authenticates every request
// as a, standing in for the consumer's real credential check.
func staticActor(a authz.Actor) httpcore.RequestActorFunc {
	return func(context.Context) (authz.Actor, error) { return a, nil }
}

// doHandler sends req to an arbitrary [http.Handler] — the mux WRAPPED in
// middleware — and returns the recorder. The package's do() takes a bare
// *http.ServeMux, which cannot carry the authentication layer under test.
func doHandler(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// withActorMiddleware is the idiomatic stdlib authentication shape: it places the
// actor on the REQUEST CONTEXT with [authz.ContextWithActor] and hands the derived
// request to next. The default [httpcore.RequestActorFunc] reads exactly that.
func withActorMiddleware(a authz.Actor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(authz.ContextWithActor(r.Context(), a)))
	})
}

// TestTaskRoutes_ActorComesFromMiddlewareNotTheBody is the whole point of
// ADR-0189: a middleware authenticates a VIEWER while the body still asserts a
// MANAGER. The manager in the body must be ignored, so the service refuses the
// viewer's claim with 403.
//
// FALSIFIER: fails against any implementation that reads the actor out of the
// request body — the body's manager is authorized to claim, so such an
// implementation answers 200.
func TestTaskRoutes_ActorComesFromMiddlewareNotTheBody(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskID := transporttest.StartedApprovalInstance(t, h, "task-identity-middleware-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	// The middleware authenticates a viewer; the body claims to be a manager.
	handler := withActorMiddleware(authz.Actor{ID: "bob", Roles: []string{"viewer"}}, mux)

	req := newPostRequest(t, "/tasks/"+taskID+"/claim", map[string]any{
		"actor": map[string]any{"id": "alice", "roles": []string{"manager"}},
	})
	rr := doHandler(handler, req)

	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body)
}

// TestTaskRoutes_NoIdentity401 pins the fail-closed default: mounted bare, with
// nothing on the request context, a claim is refused as unauthenticated — it is
// never downgraded to the zero actor.
//
// FALSIFIER: the request carries NO BODY at all, so before the claim route
// switched to the optional decoder this answered 400 ("bad input: EOF"), not 401.
func TestTaskRoutes_NoIdentity401(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskID := transporttest.StartedApprovalInstance(t, h, "task-identity-none-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := do(mux, newPostRequest(t, "/tasks/"+taskID+"/claim", nil))

	require.Equal(t, http.StatusUnauthorized, rr.Code, "body=%s", rr.Body)
	var errBody map[string]any
	decodeJSON(t, rr.Body, &errBody)
	assert.Equal(t, "unauthenticated", errBody["error"])
}

// TestTaskRoutes_ClaimAcceptsAbsentBody is the migration regression pin: with
// httpcore.ClaimInput now a ZERO-FIELD struct, a correctly migrated client sends
// no body at all. That must succeed, not fail on an EOF decode.
//
// FALSIFIER: against the REQUIRED-body decoder this answers 400 with
// "workflow-httpcore: bad input: EOF" (MEASURED before the change).
func TestTaskRoutes_ClaimAcceptsAbsentBody(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskID := transporttest.StartedApprovalInstance(t, h, "task-identity-nobody-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc, stdlib.WithRequestActor(
		staticActor(authz.Actor{ID: "alice", Roles: []string{"manager"}}),
	))

	rr := do(mux, newPostRequest(t, "/tasks/"+taskID+"/claim", nil))

	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body)
}

// TestTaskRoutes_ClaimOversizeBodyStill413 pins that making the claim body
// OPTIONAL did not also make it UNBOUNDED. decodeOptionalRequestBody discards the
// DECODE error and nothing else: an oversize body still fails at the READ, with
// the bare [httpcore.ErrRequestBodyTooLarge] that classifies as 413.
//
// FALSIFIER: an optional decoder that swallowed the reader error too would let
// the request through to actor resolution and answer 401 here.
func TestTaskRoutes_ClaimOversizeBodyStill413(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()
	h, svc := transporttest.NewHarness(t, def)
	taskID := transporttest.StartedApprovalInstance(t, h, "task-identity-oversize-1")

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc,
		stdlib.WithMaxBodyBytes(testCap),
		stdlib.WithRequestActor(staticActor(authz.Actor{ID: "alice", Roles: []string{"manager"}})),
	)

	raw := `{"junk":"` + bigPad(500) + `"}`
	require.Greater(t, len(raw), testCap, "fixture must exceed the cap")

	rr := do(mux, newRawPostRequest(t, "/tasks/"+taskID+"/claim", raw))

	wantStatic413(t, rr)
}

// TestWithRequestActorTimeout_SetsTheBound pins the alias that would otherwise be
// unreachable: httpcore.WithRequestActorTimeout's type parameter R appears only in
// its RESULT type, so the generic form cannot be called without spelling R out.
//
// ⚠ It asserts the option REACHES the config, not that the bound is enforced —
// httpcore's own endpoints currently pass a hardcoded 0 to resolveRequestActor,
// so no adapter-level test could observe enforcement.
//
// FALSIFIER: fails if the alias forwards the wrong value or drops the option.
func TestWithRequestActorTimeout_SetsTheBound(t *testing.T) {
	t.Parallel()

	cfg := httpcore.ResolveConfig(stdlib.WithRequestActorTimeout(2 * time.Second))
	assert.Equal(t, 2*time.Second, cfg.RequestActorTimeout)

	// Default when the option is not passed.
	assert.Equal(t, 10*time.Second, httpcore.ResolveConfig[*http.ServeMux]().RequestActorTimeout)
}

// TestUnauthenticatedBadJSONIs401NotThe400 is a CONTRACT, not a coverage test.
//
// It pins the ordering the ADR-0189 /code-review fix (F6) established on the
// three human-task routes: 401 (identity) → 413 (body cap) → 400 (decode) →
// 404 (lookup). Before that fix identity was resolved INSIDE the endpoint,
// AFTER the adapter had already read the body, which handed an unauthenticated
// caller a resource-consumption primitive — a full MaxBodyBytes read (1 MiB by
// default) held for up to BodyReadTimeout (30s by default) before its 401 — on
// the only routes that authenticate at all.
//
// Two properties are pinned here at once:
//
//  1. Refusal is CHEAP: nothing reads the body before the 401. The bodies below
//     are unreadable (errReader fails on the first Read), so a 400 can only be
//     produced by a handler that touched the body first.
//  2. Refusal is OPAQUE: an unauthenticated caller must not learn whether its
//     body parsed. A 400 leaks that the decode ran; a 401 says only "who are
//     you?".
//
// ⚠ If this test ever reports 400 again, resolution has moved back BEHIND the
// decode and F6 has regressed. Do not "fix" it by relaxing the assertion —
// restore the ordering in stdlib/groups.go instead.
//
// FALSIFIER: MEASURED — moving the httpcore.RequestActor call in the
// /tasks/{token}/complete handler in groups.go to AFTER its decodeRequestBody
// call flips that row and only that row:
//
//	--- FAIL: TestUnauthenticatedBadJSONIs401NotThe400/complete
//	    expected: 401
//	    actual  : 400
//	    body={"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
//	--- PASS: .../claim   --- PASS: .../reassign
//
// i.e. the rows are independent — each one pins its own handler's ordering.
func TestUnauthenticatedBadJSONIs401NotThe400(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		route  string
		assert func(t *testing.T, rr *httptest.ResponseRecorder)
	}

	// wantUnauthenticated is shared by every row: identity is refused first, and
	// the refusal says nothing about the body.
	wantUnauthenticated := func(t *testing.T, rr *httptest.ResponseRecorder) {
		t.Helper()

		require.Equal(t, http.StatusUnauthorized, rr.Code, "body=%s", rr.Body)

		var errBody map[string]any
		decodeJSON(t, rr.Body, &errBody)
		assert.Equal(t, "unauthenticated", errBody["error"])
	}

	cases := []testCase{
		{name: "claim", route: "/claim", assert: wantUnauthenticated},
		{name: "complete", route: "/complete", assert: wantUnauthenticated},
		{name: "reassign", route: "/reassign", assert: wantUnauthenticated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := transporttest.ApprovalProcess()
			h, svc := transporttest.NewHarness(t, def)
			taskID := transporttest.StartedApprovalInstance(t, h, "task-order-"+tc.name+"-1")

			// Mounted BARE — no WithRequestActor, nothing on the request context.
			mux := http.NewServeMux()
			stdlib.Mount(mux, svc)

			req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskID+tc.route, errReader{})
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(t.Context())

			tc.assert(t, do(mux, req))
		})
	}
}
