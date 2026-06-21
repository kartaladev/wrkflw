package rest

import (
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// config holds the resolved handler configuration.
type config struct {
	instanceMapper func(engine.InstanceState) any
	// adminMiddleware wraps the admin routes. If nil, the built-in denyAllMiddleware
	// is used so the admin endpoints are never openly accessible (default-deny).
	adminMiddleware func(http.Handler) http.Handler
}

// denyAllMiddleware is the default admin middleware: it always returns 403 Forbidden
// so that admin routes are inaccessible unless the consumer explicitly supplies a
// middleware via WithAdminMiddleware.
func denyAllMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":   "forbidden",
			"message": "admin access requires WithAdminMiddleware",
		})
	})
}

func defaultConfig() config {
	return config{
		instanceMapper:  func(st engine.InstanceState) any { return NewInstanceView(st) },
		adminMiddleware: denyAllMiddleware,
	}
}

// Option is a functional option for NewHandler.
type Option func(*config)

// WithInstanceMapper overrides the function used to convert an engine.InstanceState
// into the JSON body returned by any endpoint that returns a process instance
// (start, get, signal, claim, complete, reassign). The default is NewInstanceView.
//
// Panics immediately if fn is nil — a nil mapper would only be caught at request
// time, producing a cryptic nil-pointer panic in production.
func WithInstanceMapper(fn func(engine.InstanceState) any) Option {
	if fn == nil {
		panic("rest: WithInstanceMapper: fn must not be nil")
	}
	return func(c *config) {
		c.instanceMapper = fn
	}
}

// WithAdminMiddleware sets the middleware that wraps the admin routes
// (e.g. GET /admin/instances). The middleware is responsible for enforcing
// authentication and authorization before the request reaches the admin handler.
//
// Default-deny: if WithAdminMiddleware is NOT supplied, the admin routes return
// 403 Forbidden for every request. This prevents accidental open exposure of
// admin endpoints.
//
// Panics immediately if mw is nil.
func WithAdminMiddleware(mw func(http.Handler) http.Handler) Option {
	if mw == nil {
		panic("rest: WithAdminMiddleware: mw must not be nil")
	}
	return func(c *config) {
		c.adminMiddleware = mw
	}
}
