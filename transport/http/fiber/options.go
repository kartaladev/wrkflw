// Package fiber provides a fiber v3 adapter for the workflow HTTP transport.
// It exposes composable route-group structs (InstanceRoutes, TaskRoutes,
// MessageRoutes, AdminRoutes, HealthRoutes) that implement
// httpcore.RouteCustomizer[fiber.Router] and can be mounted on any fiber.Router
// (including a fiber.App, a fiber.Group, etc.).
//
// Consumers assemble the transport by calling Mount (convenience for
// Instance+Task+Message) and optionally AdminRoutes.Customize and MountHealth:
//
//	app := fiber.New()
//	fibertransport.Mount(app, svc, fibertransport.WithBasePath("/api/v1"))
//	fibertransport.MountHealth(app)
package fiber

import (
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// WithBasePath is a convenience alias for httpcore.WithBasePath typed for
// fiber.Router. It prefixes every route the group registers.
func WithBasePath(p string) httpcore.CustomizeOption[fiberlib.Router] {
	return httpcore.WithBasePath[fiberlib.Router](p)
}

// WithMiddleware wraps the router returned by cfg.Wrap in a fiber Group with
// mw as middleware handlers. This is the fiber-native way to apply middleware
// to a subset of routes — it mirrors the gin adapter's Use approach but uses
// fiber's Group("", mw...) signature.
func WithMiddleware(mw ...fiberlib.Handler) httpcore.CustomizeOption[fiberlib.Router] {
	return httpcore.WithRouterFunc(func(r fiberlib.Router) fiberlib.Router {
		// Convert []fiberlib.Handler to []any for fiber's variadic Group call.
		args := make([]any, len(mw))
		for i, h := range mw {
			args[i] = h
		}
		return r.Group("", args...)
	})
}
