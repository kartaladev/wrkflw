// Package gin provides a gin adapter for the wrkflw HTTP transport.
// It mounts composable route-group structs (InstanceRoutes, TaskRoutes,
// MessageRoutes, AdminRoutes, HealthRoutes) onto any gin.IRouter — a *gin.Engine,
// a gin.RouterGroup, or any framework-compatible router.
//
// Every group struct implements httpcore.RouteCustomizer[gin.IRouter] so that
// httpcore.MountGroups and consumer code can treat gin and stdlib groups uniformly.
//
// Typical usage:
//
//	r := gin.Default()
//	ginadapter.Mount(r, svc, ginadapter.WithBasePath("/api/v1"))
//	ginadapter.MountHealth(r, httpcore.HealthCheckFunc("db", dbPing))
//	http.ListenAndServe(":8080", r)
package gin

import (
	"time"

	ginlib "github.com/gin-gonic/gin"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// WithBasePath returns a CustomizeOption that prefixes every route the group
// registers (e.g. "/api/v1/workflow"). It is an alias for the generic
// httpcore.WithBasePath so callers need not import httpcore for the common case.
func WithBasePath(p string) httpcore.CustomizeOption[ginlib.IRouter] {
	return httpcore.WithBasePath[ginlib.IRouter](p)
}

// WithMaxBodyBytes returns a CustomizeOption that bounds the inbound request
// body to n bytes; a body exceeding n is answered 413 without being decoded.
// n <= 0 disables the cap. The default, applied when this option is absent, is
// 1 MiB.
//
// ⚠ This alias is REQUIRED, not a convenience: httpcore.WithMaxBodyBytes's type
// parameter R appears only in its result type, so Go cannot infer it and the
// generic form does not compile unless the caller spells the router type out
// (httpcore.WithMaxBodyBytes[gin.IRouter](n)).
func WithMaxBodyBytes(n int64) httpcore.CustomizeOption[ginlib.IRouter] {
	return httpcore.WithMaxBodyBytes[ginlib.IRouter](n)
}

// WithBodyReadTimeout returns a CustomizeOption bounding how long the CAPPED
// inbound body read may block before the handler proceeds with the bytes it
// already has. d <= 0 disables the deadline. The default is 30s — the same 30s
// action/httpcall's default client uses.
//
// ⚠ It is armed ONLY when the body cap is active. [WithMaxBodyBytes](0) installs
// no read wrapper, so there is nothing to bound and the pre-cap streaming
// behaviour is untouched.
//
// ⚠ This alias is REQUIRED for the same inference reason as [WithMaxBodyBytes]:
// R appears only in the result type of httpcore.WithBodyReadTimeout.
//
// See httpcore.CustomizeConfig.BodyReadTimeout for why the deadline exists and
// how it interacts with the consumer's own http.Server.ReadTimeout.
func WithBodyReadTimeout(d time.Duration) httpcore.CustomizeOption[ginlib.IRouter] {
	return httpcore.WithBodyReadTimeout[ginlib.IRouter](d)
}

// WithRequestActor returns a CustomizeOption that overrides how the human-task
// routes resolve the AUTHENTICATED principal of a request. fn == nil restores the
// default — the [authz.ContextWithActor] seam — which refuses with 401 when
// nothing authenticated the caller.
//
// ⚠ The actor is read from here and from nowhere else. A request body carrying
// an "actor" or "by" field is ignored (ADR-0189).
//
// ⚠ gin middleware must publish the actor on the REQUEST's context, not with
// gc.Set: gin.Context.Set stores on the gin.Context, which gc.Request.Context()
// — the context this adapter hands the engine — never sees. MEASURED: a gc.Set
// middleware leaves the route unauthenticated and the request is refused 401
// (TestTaskRoutes_GinSetDoesNotAuthenticate). Write it like this instead:
//
//	func authenticate(gc *gin.Context) {
//		actor, err := myAuth(gc.Request)
//		if err != nil {
//			gc.AbortWithStatus(http.StatusUnauthorized)
//			return
//		}
//		gc.Request = gc.Request.WithContext(authz.ContextWithActor(gc.Request.Context(), actor))
//		gc.Next()
//	}
//
// ⚠ This alias is REQUIRED for the same inference reason as [WithMaxBodyBytes]:
// R appears only in the result type of httpcore.WithRequestActor.
func WithRequestActor(fn httpcore.RequestActorFunc) httpcore.CustomizeOption[ginlib.IRouter] {
	return httpcore.WithRequestActor[ginlib.IRouter](fn)
}

// WithRequestActorTimeout returns a CustomizeOption bounding how long the
// configured [httpcore.RequestActorFunc] may take. d <= 0 disables the bound —
// the same convention as [WithMaxBodyBytes] and [WithBodyReadTimeout]. The
// default is 10s.
//
// ⚠ It bounds only a resolver that HONOURS ctx cancellation; see
// [httpcore.CustomizeConfig.RequestActorTimeout] for the measurement.
//
// ⚠ This alias is REQUIRED for the same inference reason as [WithMaxBodyBytes]:
// R appears only in the result type of httpcore.WithRequestActorTimeout.
func WithRequestActorTimeout(d time.Duration) httpcore.CustomizeOption[ginlib.IRouter] {
	return httpcore.WithRequestActorTimeout[ginlib.IRouter](d)
}

// WithMiddleware returns a CustomizeOption that applies mw as gin middleware on
// every route the group registers. Multiple WithMiddleware calls compose: each
// wraps the previous group (outermost-last order).
//
// Internally it calls r.Group("", mw...) so the middleware runs before the
// matched route handler.
func WithMiddleware(mw ...ginlib.HandlerFunc) httpcore.CustomizeOption[ginlib.IRouter] {
	return httpcore.WithRouterFunc(func(r ginlib.IRouter) ginlib.IRouter {
		return r.Group("", mw...)
	})
}
