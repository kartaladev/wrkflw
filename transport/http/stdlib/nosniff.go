package stdlib

import (
	"net/http"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// NosniffMiddleware returns next wrapped so that every response written
// beneath it carries X-Content-Type-Options: nosniff — including responses
// this library never sees, because a consumer middleware answered the request
// itself.
//
// # Why it exists
//
// The adapters already set the header in their per-route wrapper, so every
// response THIS LIBRARY writes carries it. That wrapper covers only the
// library's own handlers. A consumer middleware that short-circuits — auth
// returning 401, a rate limiter returning 429, a CORS preflight reject, panic
// recovery returning 500 — answers before the wrapper runs, and those
// responses routinely echo a caller-influenced value into an error body. That
// is precisely the sniffing exposure the header prevents, and it is the one
// response the library cannot reach on its own.
//
// The alternative was for the adapters to insert their own handler into the
// consumer's chain. This library does not do that: [httpcore.CustomizeConfig.Wrap]
// is the consumer's composition point, and a library that silently mutates it
// is its own surprise. Opt in instead:
//
//	mux := http.NewServeMux()
//	stdlib.Mount(mux, svc)
//
//	var h http.Handler = mux
//	h = authenticate(h)   // may answer 401 without reaching the mux
//	h = rateLimit(h)      // may answer 429 without reaching the mux
//	h = stdlib.NosniffMiddleware(h)
//
//	http.ListenAndServe(":8080", h)
//
// ⚠ ORDER IS THE WHOLE POINT. Wrap OUTERMOST — outside every middleware whose
// response you want covered. A header can only be set before the status line
// is written, so NosniffMiddleware covers a short-circuiting middleware only
// when it runs BEFORE that middleware. Placed innermost it covers nothing the
// route wrapper does not already cover.
//
// ⚠ Its shape differs from the gin and fiber adapters', which return a bare
// handler for r.Use / WithMiddleware. That asymmetry is *http.ServeMux's:
// it has no middleware seam, so this adapter has no WithMiddleware to pass a
// handler to, and a consumer composes around the mux by hand. The decorator
// form is what that composition takes.
//
// Setting the header twice is harmless — Set replaces rather than appends — so
// there is no need to keep this off the library's own routes.
func NosniffMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(httpcore.ContentTypeOptionsHeader, httpcore.NoSniff)
		next.ServeHTTP(w, r)
	})
}
