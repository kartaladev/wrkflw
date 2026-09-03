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
	"time"

	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// WithBasePath is a convenience alias for httpcore.WithBasePath typed for
// fiber.Router. It prefixes every route the group registers.
func WithBasePath(p string) httpcore.CustomizeOption[fiberlib.Router] {
	return httpcore.WithBasePath[fiberlib.Router](p)
}

// WithMaxBodyBytes is a convenience alias for httpcore.WithMaxBodyBytes typed
// for fiber.Router. It bounds the inbound request body to n bytes; a larger
// body is refused with 413 before it is parsed. n <= 0 disables the cap. The
// default is 1 MiB.
//
// ⚠ The alias is not sugar — the generic httpcore.WithMaxBodyBytes CANNOT be
// called without spelling the router type out, because R appears only in its
// result type and so is never inferable from the argument list.
//
// ⚠ This bounds what the adapter PARSES, not what fiber READS. A body above
// fiber.Config.BodyLimit (default 4 MiB) is refused by fasthttp before any
// route in the group runs, with a plain-text response and no ErrorBody
// envelope. Peak per-request memory is therefore governed by
// fiber.Config.BodyLimit; set that too if it matters.
func WithMaxBodyBytes(n int64) httpcore.CustomizeOption[fiberlib.Router] {
	return httpcore.WithMaxBodyBytes[fiberlib.Router](n)
}

// WithRequestActor is a convenience alias for httpcore.WithRequestActor typed
// for fiber.Router. It overrides how the AUTHENTICATED principal of a
// human-task request is resolved. fn == nil restores the default, which reads
// the actor a consumer's authentication middleware placed on the request
// context with authz.ContextWithActor and refuses with 401 when nothing did.
//
// ⚠ The alias is not sugar — the generic httpcore.WithRequestActor CANNOT be
// called without spelling the router type out, because R appears only in its
// result type and so is never inferable from the argument list.
//
// ⚠ In fiber, middleware must publish the actor with c.SetContext, NOT
// c.Locals. fiber.Ctx.Context() returns a SEPARATE object from the Ctx itself
// (its godoc: "returns a non-nil, empty context, if it was not set earlier"),
// and this adapter hands c.Context() to httpcore — so a Locals write is
// invisible to the seam and the request fails closed with 401:
//
//	app.Use(func(c fiber.Ctx) error {
//		a, err := authenticate(c) // your credential check
//		if err != nil {
//			return c.SendStatus(fiber.StatusUnauthorized)
//		}
//		c.SetContext(authz.ContextWithActor(c.Context(), a))
//		return c.Next()
//	})
func WithRequestActor(fn httpcore.RequestActorFunc) httpcore.CustomizeOption[fiberlib.Router] {
	return httpcore.WithRequestActor[fiberlib.Router](fn)
}

// WithRequestActorTimeout is a convenience alias for
// httpcore.WithRequestActorTimeout typed for fiber.Router. It bounds how long
// the configured httpcore.RequestActorFunc may take; d <= 0 disables the bound.
// The default is 10s.
//
// ⚠ The alias is not sugar — see WithRequestActor for why R cannot be inferred.
//
// ⚠ It bounds only a resolver that honours ctx cancellation: the bound is a
// context deadline, not a hard kill.
func WithRequestActorTimeout(d time.Duration) httpcore.CustomizeOption[fiberlib.Router] {
	return httpcore.WithRequestActorTimeout[fiberlib.Router](d)
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
