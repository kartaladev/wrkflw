// Package observability_test is the black-box test suite for the observability root package.
package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/zakyalvan/krtlwrkflw/observability"
)

// bufHandler is a minimal slog.Handler that writes JSON to a buffer so we can
// assert on the emitted attributes without depending on slog internals.
type bufHandler struct {
	buf *bytes.Buffer
}

func newBufHandler() *bufHandler { return &bufHandler{buf: &bytes.Buffer{}} }

func (h *bufHandler) base() slog.Handler {
	return slog.NewJSONHandler(h.buf, &slog.HandlerOptions{Level: slog.LevelDebug})
}

// logAttrs parses the last JSON line and returns a flat map of all top-level
// and second-level keys (to cover attrs added before Handle and inside Handle).
func (h *bufHandler) logAttrs() map[string]string {
	m := map[string]string{}
	if h.buf.Len() == 0 {
		return m
	}
	// only look at the last line
	data := bytes.TrimRight(h.buf.Bytes(), "\n")
	lines := bytes.Split(data, []byte("\n"))
	last := lines[len(lines)-1]
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(last, &raw); err != nil {
		return m
	}
	for k, v := range raw {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			m[k] = s
		}
	}
	return m
}

func TestNewHandler_WithinSpan(t *testing.T) {
	t.Parallel()

	base := newBufHandler()
	lg := slog.New(observability.NewHandler(base.base()))

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()

	lg.InfoContext(ctx, "hello")

	attrs := base.logAttrs()
	wantTrace := span.SpanContext().TraceID().String()
	wantSpan := span.SpanContext().SpanID().String()

	assert.Equal(t, wantTrace, attrs["trace_id"], "trace_id should match active span")
	assert.Equal(t, wantSpan, attrs["span_id"], "span_id should match active span")
}

func TestNewHandler_NoSpan(t *testing.T) {
	t.Parallel()

	base := newBufHandler()
	lg := slog.New(observability.NewHandler(base.base()))

	lg.InfoContext(context.Background(), "no span")

	attrs := base.logAttrs()
	_, hasTrace := attrs["trace_id"]
	_, hasSpan := attrs["span_id"]

	assert.False(t, hasTrace, "trace_id must be absent when no span is active")
	assert.False(t, hasSpan, "span_id must be absent when no span is active")
}

func TestNewHandler_WithAttrs(t *testing.T) {
	t.Parallel()

	base := newBufHandler()
	lg := slog.New(observability.NewHandler(base.base())).With("k", "v")

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()

	lg.InfoContext(ctx, "with-attrs")

	attrs := base.logAttrs()
	wantTrace := span.SpanContext().TraceID().String()
	wantSpan := span.SpanContext().SpanID().String()

	assert.Equal(t, wantTrace, attrs["trace_id"], "trace_id should be injected after WithAttrs")
	assert.Equal(t, wantSpan, attrs["span_id"], "span_id should be injected after WithAttrs")
	assert.Equal(t, "v", attrs["k"], "pre-set attr k=v must survive WithAttrs")
}

func TestNewHandler_WithGroup(t *testing.T) {
	t.Parallel()

	base := newBufHandler()
	lg := slog.New(observability.NewHandler(base.base())).WithGroup("grp")

	// Must not panic; WithGroup delegates through our handler.
	require.NotPanics(t, func() {
		lg.InfoContext(context.Background(), "grouped log")
	})

	// The grouped log went to base — at minimum the buffer is non-empty.
	assert.Greater(t, base.buf.Len(), 0, "grouped log must reach base handler")
}

func TestNewLogger(t *testing.T) {
	t.Parallel()

	base := newBufHandler()
	lg := observability.NewLogger(base.base())

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()

	lg.InfoContext(ctx, "via NewLogger")

	attrs := base.logAttrs()
	assert.Equal(t, span.SpanContext().TraceID().String(), attrs["trace_id"])
	assert.Equal(t, span.SpanContext().SpanID().String(), attrs["span_id"])
}
