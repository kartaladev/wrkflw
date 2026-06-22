// Package observability provides a public trace-correlating [slog.Handler] for
// use by library consumers. Mount it on a [*slog.Logger] and pass that logger
// to the engine's WithLogger options to get trace-correlated logs across the
// whole library with no per-call-site changes.
//
// The package depends only on the standard library and
// go.opentelemetry.io/otel/trace — it does not import the internal
// observability package or any other internal package.
package observability

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// compile-time assertion: *handler satisfies [slog.Handler].
var _ slog.Handler = (*handler)(nil)

// handler wraps a base [slog.Handler] and injects trace_id / span_id attrs
// from the active span on every [Handle] call.
type handler struct {
	base slog.Handler
}

// NewHandler wraps base so that every record carries trace_id and span_id from
// the span in the record's context (when a valid span is present). Mount it on
// a [*slog.Logger] and pass that logger to the engine's WithLogger options to
// get trace-correlated logs across the whole library with no per-call-site
// changes.
func NewHandler(base slog.Handler) slog.Handler {
	return &handler{base: base}
}

// NewLogger is a convenience constructor: slog.New(NewHandler(base)).
func NewLogger(base slog.Handler) *slog.Logger {
	return slog.New(NewHandler(base))
}

// Enabled reports whether the base handler is enabled for the given level.
func (h *handler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.base.Enabled(ctx, lvl)
}

// Handle injects trace_id and span_id attrs when a valid OTel span is present
// in ctx, then delegates to the base handler. The record is cloned before
// mutation to avoid corrupting shared state in the slog internals.
func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec = rec.Clone()
		rec.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.base.Handle(ctx, rec)
}

// WithAttrs returns a new handler whose base carries the given attrs, keeping
// the trace-correlation wrapper intact.
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{base: h.base.WithAttrs(attrs)}
}

// WithGroup returns a new handler whose base has the given group active,
// keeping the trace-correlation wrapper intact.
func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{base: h.base.WithGroup(name)}
}
