package watermill_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	watermillpub "github.com/zakyalvan/krtlwrkflw/internal/eventing/watermill"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPublishRecordsSpanAndCounter(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))

	pub := watermillpub.NewPublisher(&fakePub{},
		watermillpub.WithTracerProvider(tp),
		watermillpub.WithMeterProvider(mp))

	err := pub.Publish(context.Background(), runtime.OutboxEvent{
		Topic: "instance.completed", Payload: map[string]any{"ok": true}, InstanceID: "inst-9",
	})
	require.NoError(t, err)

	spans := sr.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "eventing.publish", spans[0].Name())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "wrkflw_eventing_published_total" {
				found = true
			}
		}
	}
	require.True(t, found, "published counter must be recorded")
}
