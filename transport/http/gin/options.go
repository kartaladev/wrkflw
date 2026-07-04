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
	ginlib "github.com/gin-gonic/gin"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// WithBasePath returns a CustomizeOption that prefixes every route the group
// registers (e.g. "/api/v1/workflow"). It is an alias for the generic
// httpcore.WithBasePath so callers need not import httpcore for the common case.
func WithBasePath(p string) httpcore.CustomizeOption[ginlib.IRouter] {
	return httpcore.WithBasePath[ginlib.IRouter](p)
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
