package grpctransport

import (
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/metadata"
)

// TestMDCarrierMethods verifies the mdCarrier adapter satisfies the
// TextMapCarrier contract — Get, Set and Keys all work as expected.
func TestMDCarrierMethods(t *testing.T) {
	md := metadata.New(map[string]string{"traceparent": "00-abc-def-01"})
	c := mdCarrier(md)

	// Get: existing key returns first value.
	if got := c.Get("traceparent"); got != "00-abc-def-01" {
		t.Errorf("Get(traceparent) = %q, want %q", got, "00-abc-def-01")
	}

	// Get: missing key returns empty string.
	if got := c.Get("tracestate"); got != "" {
		t.Errorf("Get(tracestate) = %q, want %q", got, "")
	}

	// Set: adds a key and Get can retrieve it.
	c.Set("tracestate", "vendor=1")
	if got := c.Get("tracestate"); got != "vendor=1" {
		t.Errorf("after Set, Get(tracestate) = %q, want %q", got, "vendor=1")
	}

	// Keys: returns all keys in the carrier.
	keys := c.Keys()
	found := make(map[string]bool, len(keys))
	for _, k := range keys {
		found[k] = true
	}
	if !found["traceparent"] {
		t.Errorf("Keys() missing %q; got %v", "traceparent", keys)
	}
	if !found["tracestate"] {
		t.Errorf("Keys() missing %q; got %v", "tracestate", keys)
	}
}

// TestWithLoggerOption verifies that WithLogger stores the option without panicking.
func TestWithLoggerOption(t *testing.T) {
	cfg := &serverConfig{}
	WithLogger(slog.Default())(cfg)
	if cfg.logOpt == nil {
		t.Error("WithLogger: logOpt must not be nil after applying a non-nil logger")
	}
}

// TestWithMeterProviderOption verifies that WithMeterProvider stores the option.
func TestWithMeterProviderOption(t *testing.T) {
	cfg := &serverConfig{}
	WithMeterProvider(noop.NewMeterProvider())(cfg)
	if cfg.mpOpt == nil {
		t.Error("WithMeterProvider: mpOpt must not be nil after applying a non-nil provider")
	}
}

// TestWithTracerProviderOption verifies that WithTracerProvider stores the option.
func TestWithTracerProviderOption(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	cfg := &serverConfig{}
	WithTracerProvider(tp)(cfg)
	if cfg.tpOpt == nil {
		t.Error("WithTracerProvider: tpOpt must not be nil after applying a non-nil provider")
	}
}
