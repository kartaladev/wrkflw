package gin

import (
	ginlib "github.com/gin-gonic/gin"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// NosniffMiddleware returns gin middleware that sets
// X-Content-Type-Options: nosniff and continues the chain, so that responses
// written by the middleware AFTER it — including a middleware that answers the
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
//	ginadapter.Mount(r, svc, ginadapter.WithMiddleware(
//		ginadapter.NosniffMiddleware(),
//		authenticate,   // may abort 401 without reaching a library handler
//		rateLimit,      // may abort 429 without reaching a library handler
//	))
//
// Or, to cover the consumer's own routes too, on the engine: r.Use(ginadapter.NosniffMiddleware()).
//
// ⚠ ORDER IS THE WHOLE POINT. List it FIRST, ahead of every middleware whose
// response you want covered. A header can only be set before the status line
// is written, so it covers a short-circuiting middleware only when it runs
// BEFORE that middleware. Listed last it covers nothing the route wrapper does
// not already cover.
//
// Setting the header twice is harmless — gc.Header replaces rather than
// appends — so there is no need to keep this off the library's own routes.
func NosniffMiddleware() ginlib.HandlerFunc {
	return func(gc *ginlib.Context) {
		gc.Header(httpcore.ContentTypeOptionsHeader, httpcore.NoSniff)
		gc.Next()
	}
}
