// Package httpcore holds the transport-neutral core shared by the stdlib, gin,
// and fiber HTTP adapter subpackages: pure per-endpoint logic, DTOs, error
// classification, the instance view, health-probe evaluation, observability
// recording, and the generic RouteCustomizer seam.
package httpcore

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/engine"
)

// CustomizeConfig carries per-mount configuration for a route group. R is the
// framework router type (*http.ServeMux, gin.IRouter, fiber.Router). The struct
// is exported so consumers may author their own CustomizeOption[R].
type CustomizeConfig[R any] struct {
	// BasePath prefixes every route the group registers. Under stdlib it is the
	// only way to sub-path a group; gin/fiber may use native groups instead.
	BasePath string
	// Wrap transforms the router before the group registers onto it — the vehicle
	// for framework-native middleware/subrouters. Defaults to identity.
	Wrap func(R) R
	// InstanceMapper customises the process-instance response shape. nil-safe:
	// ResolveConfig defaults it to NewInstanceView.
	InstanceMapper func(engine.InstanceState) any
	// Logger receives 5xx raw error details (never sent to clients).
	Logger         *slog.Logger
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
}

// CustomizeOption mutates a CustomizeConfig[R].
type CustomizeOption[R any] func(*CustomizeConfig[R])

// ResolveConfig applies opts over safe defaults.
func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R] {
	cfg := CustomizeConfig[R]{
		Wrap:           func(r R) R { return r },
		InstanceMapper: func(st engine.InstanceState) any { return NewInstanceView(st) },
		Logger:         slog.Default(),
	}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	if cfg.Wrap == nil {
		cfg.Wrap = func(r R) R { return r }
	}
	if cfg.InstanceMapper == nil {
		cfg.InstanceMapper = func(st engine.InstanceState) any { return NewInstanceView(st) }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

// WithBasePath prefixes every route the group registers (e.g. "/api/v1/workflow").
func WithBasePath[R any](p string) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.BasePath = p }
}

// WithInstanceMapper overrides the process-instance response shape.
func WithInstanceMapper[R any](fn func(engine.InstanceState) any) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.InstanceMapper = fn }
}

// WithRouterFunc composes fn onto Wrap; fn runs outermost (fn(previous(r))).
func WithRouterFunc[R any](fn func(R) R) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) {
		prev := c.Wrap
		if prev == nil {
			c.Wrap = fn
			return
		}
		c.Wrap = func(r R) R { return fn(prev(r)) }
	}
}

// WithLogger sets the logger used for 5xx raw-error logging.
func WithLogger[R any](l *slog.Logger) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.Logger = l }
}

// WithTracerProvider sets the OTel tracer provider for per-route spans.
func WithTracerProvider[R any](tp trace.TracerProvider) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.TracerProvider = tp }
}

// WithMeterProvider sets the OTel meter provider for per-route metrics.
func WithMeterProvider[R any](mp metric.MeterProvider) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.MeterProvider = mp }
}

// RouteCustomizer is a mountable route group for router type R.
type RouteCustomizer[R any] interface {
	Customize(r R, opts ...CustomizeOption[R])
}

// MountGroups mounts each group onto r at its current position (no extra opts).
// It is also the consumer extension seam: any RouteCustomizer[R] — including a
// consumer's own — can be passed. Groups needing distinct base paths or
// middleware call Customize directly with the relevant options.
func MountGroups[R any](r R, groups ...RouteCustomizer[R]) {
	for _, g := range groups {
		g.Customize(r)
	}
}
