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
	"sync"
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

// WithMiddleware applies mw as fiber middleware ahead of the routes this
// adapter mounts, by registering a Group("", mw...) on the router.
//
// ⚠ SCOPE: this is APP-WIDE middleware, not per-group middleware, despite
// mirroring the gin adapter's spelling. fiber's routing is path-based rather
// than object-based, so a group at the empty prefix registers at "/" and
// therefore matches every request the router sees — including routes the
// CONSUMER registered on the same app. MEASURED on fiber v3.4.0. The gin
// adapter's Group IS object-scoped and does confine mw to the library's routes;
// the two adapters genuinely diverge here, and this is the wider of the two.
// If you need the narrower scope, mount this library on a prefixed group of
// your own and pass the option there.
//
// ⚠ mw composes OUTSIDE this adapter's per-route wrapper, which is where
// X-Content-Type-Options: nosniff is set. A middleware that SHORT-CIRCUITS —
// auth answering 401, a rate limiter 429, a CORS preflight reject, panic
// recovery 500 — answers the request without ever reaching that wrapper, so its
// response carries no nosniff even though every response this library writes
// does.
//
// That is the boundary, not a bug: your middleware writes your response, and
// this library does not insert its own handler into the chain you built. When
// such a response can embed a caller-influenced value — and an error message
// usually can — list [NosniffMiddleware] FIRST and the header is set before
// anything after it can answer.
func WithMiddleware(mw ...fiberlib.Handler) httpcore.CustomizeOption[fiberlib.Router] {
	// Convert []fiberlib.Handler to []any for fiber's variadic Group call.
	// Hoisted out of the closure: it does not depend on the router, and the
	// closure now runs more than once.
	args := make([]any, len(mw))
	for i, h := range mw {
		args[i] = h
	}

	// ⚠ ONE group per router, memoised — the registration is a SIDE EFFECT, and
	// this closure is called once per Customize, not once per mount.
	//
	// httpcore.CustomizeConfig.Wrap is documented as transforming the router,
	// which on gin it does: Group returns a new *RouterGroup object and calling
	// it twice is harmless. On fiber, Group REGISTERS handlers at a path, so
	// each call added another copy of mw at "/". Mount runs Customize three
	// times (InstanceRoutes, TaskRoutes, MessageRoutes) and each calls Wrap
	// once, so mw was registered three times, interleaved with the routes
	// between them.
	//
	// MEASURED on fiber v3.4.0 before this memo, counting executions of one
	// consumer middleware for one request:
	//
	//	GET  /instances/:id       1   (registered after the 1st copy)
	//	POST /tasks/:token/claim  2   (after the 2nd)
	//	POST /messages            3   (after the 3rd)
	//	GET  <a consumer route>   3
	//	GET  <unrouted path>      3
	//
	// So the count varied BY ENDPOINT — a rate limiter counted one POST
	// /messages as three requests and one GET /instances/:id as one, and an
	// unmatched path ran the consumer's auth work three times. Non-uniform is
	// the part that made it survive: nothing in a response body shows it.
	//
	// Keyed by router rather than a plain sync.Once so that reusing one option
	// value across two apps still registers on both. fiber's Router
	// implementations are pointers, hence usable as map keys. The mutex guards
	// the pathological case of concurrent mounts; mounting is normally
	// single-threaded.
	var (
		mu     sync.Mutex
		groups = make(map[fiberlib.Router]fiberlib.Router)
	)
	return httpcore.WithRouterFunc(func(r fiberlib.Router) fiberlib.Router {
		mu.Lock()
		defer mu.Unlock()
		if g, ok := groups[r]; ok {
			return g
		}
		g := r.Group("", args...)
		groups[r] = g
		return g
	})
}
