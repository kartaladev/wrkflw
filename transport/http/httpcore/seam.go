// Package httpcore holds the transport-neutral core shared by the stdlib, gin,
// and fiber HTTP adapter subpackages: pure per-endpoint logic, DTOs, error
// classification, the instance view, health-probe evaluation, observability
// recording, and the generic RouteCustomizer seam.
package httpcore

import (
	"log/slog"
	"time"

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
	// MaxBodyBytes bounds the INBOUND request body read into memory, in bytes.
	// A body exceeding it fails with [ErrRequestBodyTooLarge], which classifies
	// as 413. The default is 1 MiB, seeded by ResolveConfig.
	//
	// n <= 0 disables the cap. This matches the convention already established
	// by action/httpcall.WithMaxResponseSize ("A non-positive n disables the
	// bound"), so the library has ONE convention for bounding a body rather
	// than two that differ on the sign of zero.
	//
	// ⚠ The default lives in ResolveConfig's struct literal, NOT its post-loop
	// nil-guard block. An int64 has no nil, so a post-loop guard could not
	// distinguish "unset" from an explicit 0 and would silently re-impose the
	// default on a consumer who deliberately disabled the cap. That placement
	// is why this field needs no pointer.
	MaxBodyBytes int64
	// BodyReadTimeout bounds how long the CAPPED body read may block before the
	// adapter gives up and proceeds with whatever arrived. The default is 30s.
	//
	// ⚠ It exists because the cap itself creates the exposure. Capping means
	// reading the body to completion BEFORE parsing, where the uncapped path let
	// json.Decoder stop at the end of the first complete value and return.
	// MEASURED 2026-08-21 against a real http.Server on POST /instances,
	// Content-Length 400000 carrying a complete 41-byte value then a stall: with
	// the cap disabled the request answered 201 Created in 0s; with the cap at
	// its 1 MiB default it produced NO RESPONSE after 3s, the goroutine held and
	// its buffer growing toward MaxBodyBytes. Without this deadline the cap
	// trades a memory-exhaustion primitive for a cheaper slowloris one.
	//
	// ⚠ It is armed ONLY when the cap is active (MaxBodyBytes > 0). With the cap
	// disabled no read wrapper is installed at all and the streaming behaviour
	// that predates the cap is untouched, deadline included.
	//
	// d <= 0 disables the deadline — the same convention as MaxBodyBytes and
	// action/httpcall.WithMaxResponseSize, so the library has ONE rule for the
	// sign of zero rather than three that differ.
	//
	// ⚠ Like MaxBodyBytes, the default lives in ResolveConfig's struct literal,
	// NOT its post-loop nil-guard block: a time.Duration is an int64 with no nil,
	// so a post-loop guard could not distinguish "unset" from an explicit 0 and
	// would silently re-impose the default on a consumer who deliberately opted
	// out.
	//
	// ⚠ Honoured by the stdlib and gin adapters. The fiber adapter does not use
	// it: fasthttp has already read the entire body into memory before the
	// handler runs, so there is no read for a deadline to bound there.
	BodyReadTimeout time.Duration
	// Logger receives 5xx raw error details (never sent to clients).
	Logger         *slog.Logger
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
}

// CustomizeOption mutates a CustomizeConfig[R].
type CustomizeOption[R any] func(*CustomizeConfig[R])

// defaultMaxBodyBytes is the inbound request-body cap applied when a consumer
// sets none: 1 MiB.
const defaultMaxBodyBytes int64 = 1 << 20

// defaultBodyReadTimeout bounds the capped body read when a consumer sets none.
//
// ⚠ 30s is NOT a new number. action/httpcall's default client is
// &http.Client{Timeout: 30 * time.Second}, so the library already answers "how
// long may one HTTP body take?" with 30s; this reuses that answer for the
// inbound direction instead of inventing a second constant to keep in sync.
const defaultBodyReadTimeout = 30 * time.Second

// ResolveConfig applies opts over safe defaults.
func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R] {
	cfg := CustomizeConfig[R]{
		Wrap:           func(r R) R { return r },
		InstanceMapper: func(st engine.InstanceState) any { return NewInstanceView(st) },
		Logger:         slog.Default(),
		// ⚠ Both seeded HERE, in the literal, so the option loop below can
		// overwrite either with an explicit 0 (= disabled). Moving these into the
		// post-loop guard block would make an explicit 0 indistinguishable from
		// unset — neither an int64 nor a time.Duration has a nil to test.
		MaxBodyBytes:    defaultMaxBodyBytes,
		BodyReadTimeout: defaultBodyReadTimeout,
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

// WithMaxBodyBytes bounds the inbound request body to n bytes; a body exceeding
// n fails with [ErrRequestBodyTooLarge] (413). n <= 0 disables the cap, matching
// action/httpcall.WithMaxResponseSize. The default is 1 MiB.
//
// ⚠ Prefer the non-generic per-adapter alias — stdlib.WithMaxBodyBytes,
// gin.WithMaxBodyBytes, fiber.WithMaxBodyBytes. R cannot be inferred at a call
// site on this generic form, so calling it directly requires spelling the router
// type out (httpcore.WithMaxBodyBytes[*http.ServeMux](0)); omitting it does not
// compile. The aliases exist precisely so consumers never have to.
func WithMaxBodyBytes[R any](n int64) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.MaxBodyBytes = n }
}

// WithBodyReadTimeout bounds how long the CAPPED inbound body read may block
// before the adapter gives up and proceeds with the bytes it already has. d <= 0
// disables the deadline, matching WithMaxBodyBytes and
// action/httpcall.WithMaxResponseSize.
//
// The default is 30s — the same 30s action/httpcall's default client uses
// (&http.Client{Timeout: 30 * time.Second}), reused rather than reinvented.
//
// ⚠ It is armed ONLY when the cap is active. WithMaxBodyBytes(0) installs no
// read wrapper, so there is nothing for a deadline to bound and the pre-cap
// streaming behaviour is untouched.
//
// ⚠ stdlib and gin honour it; fiber does not, because fasthttp has already read
// the whole body before the handler runs. See [CustomizeConfig.BodyReadTimeout].
//
// ⚠ Prefer the non-generic per-adapter alias — stdlib.WithBodyReadTimeout,
// gin.WithBodyReadTimeout. R cannot be inferred at a call site on this generic
// form, exactly as with WithMaxBodyBytes.
func WithBodyReadTimeout[R any](d time.Duration) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.BodyReadTimeout = d }
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
