package httpcore

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/internal/observability"
)

const instrumentationScope = "github.com/zakyalvan/krtlwrkflw/transport/http"

// Instrumentation holds OTel instruments for per-route HTTP observability.
// It is created once per mounted handler group and shared across requests.
// All fields are guaranteed non-nil after [NewInstrumentation].
type Instrumentation struct {
	tracer      trace.Tracer
	counter     metric.Int64Counter
	histogram   metric.Float64Histogram
	propagator  propagation.TextMapPropagator
}

// NewInstrumentation builds an [Instrumentation] from the observability
// providers stored in cfg. Nil providers fall back to the OTel globals (via
// [observability.New]). The metric names and span naming follow the same
// conventions as the legacy transport/rest traceMiddleware so that consumers
// migrating between the two transports see identical telemetry:
//
//   - counter:   wrkflw_rest_requests_total
//   - histogram: wrkflw_rest_request_duration_seconds
//   - span name: "wrkflw.rest <METHOD> <routeTemplate>"
//   - attributes: http.method, http.route, http.status_code
func NewInstrumentation[R any](cfg CustomizeConfig[R]) *Instrumentation {
	var opts []observability.Option
	if cfg.Logger != nil {
		opts = append(opts, observability.WithLogger(cfg.Logger))
	}
	if cfg.TracerProvider != nil {
		opts = append(opts, observability.WithTracerProvider(cfg.TracerProvider))
	}
	if cfg.MeterProvider != nil {
		opts = append(opts, observability.WithMeterProvider(cfg.MeterProvider))
	}

	tel := observability.New(instrumentationScope, opts...)

	return &Instrumentation{
		tracer: tel.Tracer,
		counter: tel.Int64Counter(
			"wrkflw_rest_requests_total",
			"Total number of HTTP requests handled by the workflow HTTP handler.",
		),
		histogram: tel.Float64Histogram(
			"wrkflw_rest_request_duration_seconds",
			"Duration of HTTP requests handled by the workflow HTTP handler, in seconds.",
		),
		propagator: otel.GetTextMapPropagator(),
	}
}

// Observe wraps a single HTTP request with a span and metric recording.
//
// It extracts any incoming trace context from hdr, starts a span named
// "wrkflw.rest <method> <routeTemplate>", passes the enriched context to run,
// and — once run returns the HTTP status code — records wrkflw_rest_requests_total
// and wrkflw_rest_request_duration_seconds, both labelled with:
//
//   - http.method      = method
//   - http.route       = routeTemplate  (STATIC — never reads r.Pattern)
//   - http.status_code = strconv.Itoa(status)
//
// The span also receives an http.route attribute and ends after run returns.
func (i *Instrumentation) Observe(
	ctx context.Context,
	method, routeTemplate string,
	hdr http.Header,
	run func(context.Context) (status int),
) {
	// Extract upstream trace context from the incoming headers.
	ctx = i.propagator.Extract(ctx, propagation.HeaderCarrier(hdr))

	spanName := "wrkflw.rest " + method + " " + routeTemplate
	ctx, span := i.tracer.Start(ctx, spanName, trace.WithAttributes(
		attribute.String("http.method", method),
		attribute.String("http.route", routeTemplate),
	))
	defer span.End()

	start := time.Now()
	status := run(ctx)

	// Set the route attribute on the span (already set at start; this is
	// idempotent and ensures the final value is readable even if the name was
	// set before routing was known).
	span.SetAttributes(attribute.String("http.route", routeTemplate))

	attrs := []attribute.KeyValue{
		attribute.String("http.method", method),
		attribute.String("http.route", routeTemplate),
		attribute.String("http.status_code", strconv.Itoa(status)),
	}
	i.counter.Add(ctx, 1, metric.WithAttributes(attrs...))
	i.histogram.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
}
