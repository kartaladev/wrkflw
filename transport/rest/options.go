package rest

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// config holds the resolved handler configuration.
type config struct {
	instanceMapper func(engine.InstanceState) any
	// adminMiddleware wraps the admin routes. If nil, the built-in denyAllMiddleware
	// is used so the admin endpoints are never openly accessible (default-deny).
	adminMiddleware func(http.Handler) http.Handler

	// deadLetters, when non-nil, enables the DLQ admin routes.
	deadLetters service.DeadLetterAdmin

	// observability options — nil entries are filtered out before calling observability.New.
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	// tel is the built Telemetry; populated in NewHandler after opts are applied.
	tel observability.Telemetry
}

// denyAllMiddleware is the default admin middleware: it always returns 403 Forbidden
// so that admin routes are inaccessible unless the consumer explicitly supplies a
// middleware via WithAdminMiddleware.
func denyAllMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
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

// WithDeadLetterAdmin enables the DLQ admin routes (GET /admin/dead-letters and
// POST /admin/dead-letters/redrive) by supplying a [service.DeadLetterAdmin]
// (e.g. a persistence.Relay). When this option is NOT supplied, those routes are
// not registered (a request returns 404). The routes sit behind the configured
// admin middleware (default-deny), like the other admin routes.
//
// Panics immediately if dla is nil.
func WithDeadLetterAdmin(dla service.DeadLetterAdmin) Option {
	if dla == nil {
		panic("rest: WithDeadLetterAdmin: dla must not be nil")
	}
	return func(c *config) {
		c.deadLetters = dla
	}
}

// WithLogger sets the structured logger used by the REST handler. A nil value
// is ignored and slog.Default() is kept.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) { c.logOpt = observability.WithLogger(l) }
}

// WithTracerProvider sets the OTel tracer provider used by the REST handler.
// A nil value is ignored and the OTel global provider is used.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) { c.tpOpt = observability.WithTracerProvider(tp) }
}

// WithMeterProvider sets the OTel meter provider used by the REST handler.
// A nil value is ignored and the OTel global provider is used.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) { c.mpOpt = observability.WithMeterProvider(mp) }
}
