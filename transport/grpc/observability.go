package grpctransport

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

// mdCarrier adapts a gRPC metadata.MD to the OTel propagation.TextMapCarrier
// interface so that W3C trace context can be extracted from incoming RPC metadata.
type mdCarrier metadata.MD

func (c mdCarrier) Get(key string) string {
	vals := metadata.MD(c).Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (c mdCarrier) Set(key, val string) {
	metadata.MD(c).Set(key, val)
}

func (c mdCarrier) Keys() []string {
	md := metadata.MD(c)
	keys := make([]string, 0, len(md))
	for k := range md {
		keys = append(keys, k)
	}
	return keys
}

// startSpan begins a per-RPC OTel span named "wrkflw.grpc <method>". It extracts
// any W3C trace context carried in the incoming gRPC metadata so that distributed
// traces propagate correctly across service boundaries. The returned span must be
// ended by the caller (typically via defer span.End()).
func (s *server) startSpan(ctx context.Context, method string) (context.Context, trace.Span) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = otel.GetTextMapPropagator().Extract(ctx, mdCarrier(md))
	}
	return s.tel.Tracer.Start(ctx, "wrkflw.grpc "+method, trace.WithAttributes(
		attribute.String("rpc.system", "grpc"),
		attribute.String("rpc.method", method),
	))
}

// recordSpanErr marks the span as failed: records the error and sets the OTel
// error status. Call this immediately before returning a gRPC error so that the
// span reflects the failure.
func recordSpanErr(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(otelcodes.Error, err.Error())
}
