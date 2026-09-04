package fiber

import (
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// NosniffMiddleware returns a fiber handler that sets
// X-Content-Type-Options: nosniff and continues the chain, so that responses
// written by the handlers AFTER it — including a middleware that answers the
// request itself — carry the header.
//
// # Why it exists
//
// The adapter already sets the header in its per-route wrapper, so every
// response THIS LIBRARY writes carries it. That wrapper covers only the
// library's own handlers. A consumer middleware that short-circuits — auth
// returning 401, a rate limiter returning 429, a CORS preflight reject, panic
// recovery returning 500 — answers before the wrapper runs, and those
// responses routinely echo a caller-influenced value into an error body. That
// is precisely the sniffing exposure the header prevents, and it is the one
// response the library cannot reach on its own.
//
// The alternative was for this adapter to insert the handler into the
// consumer's chain itself. It does not: [httpcore.CustomizeConfig.Wrap] is the
// consumer's composition point, and a library that silently mutates it is its
// own surprise. Opt in instead:
//
//	fibertransport.Mount(app, svc, fibertransport.WithMiddleware(
//		fibertransport.NosniffMiddleware(),
//		authenticate,   // may answer 401 without reaching a library handler
//		rateLimit,      // may answer 429 without reaching a library handler
//	))
//
// Or on the app directly: app.Use(fibertransport.NosniffMiddleware()).
//
// ⚠ ORDER IS THE WHOLE POINT. List it FIRST, ahead of every middleware whose
// response you want covered. A header can only be set before the status line
// is written, so it covers a short-circuiting middleware only when it runs
// BEFORE that middleware. Listed last it covers nothing the route wrapper does
// not already cover.
//
// ⚠ SCOPE, on fiber only: [WithMiddleware] registers through Group("", ...),
// and fiber's routing is path-based rather than object-based, so a group at the
// empty prefix matches "/" and everything under it. MEASURED on fiber v3.4.0: a
// handler registered that way also runs for routes the CONSUMER registered on
// the same app, not only the ones this library mounted. For nosniff that is
// harmless — the header is inert on a response whose Content-Type is already
// honoured — but it is not what "middleware for the library's routes" sounds
// like, and it applies to every handler a consumer passes to WithMiddleware,
// not just this one. Use app.Use for app-wide coverage you actually intend.
//
// Setting the header twice is harmless — c.Set replaces rather than appends —
// so there is no need to keep this off the library's own routes.
func NosniffMiddleware() fiberlib.Handler {
	return func(c fiberlib.Ctx) error {
		c.Set(httpcore.ContentTypeOptionsHeader, httpcore.NoSniff)
		return c.Next()
	}
}
