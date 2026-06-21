package watermill_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	watermillpub "github.com/zakyalvan/krtlwrkflw/internal/eventing/watermill"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newObs(t *testing.T) (
	sr *tracetest.SpanRecorder,
	reader *metric.ManualReader,
	pub *watermillpub.Publisher,
) {
	t.Helper()
	sr = tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader = metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	pub = watermillpub.NewPublisher(&fakePub{},
		watermillpub.WithTracerProvider(tp),
		watermillpub.WithMeterProvider(mp))
	return sr, reader, pub
}

// collectCounter extracts all data points from the wrkflw_eventing_published_total counter.
func collectCounter(t *testing.T, reader *metric.ManualReader) []metricdata.DataPoint[int64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "wrkflw_eventing_published_total" {
				sum, ok := m.Data.(metricdata.Sum[int64])
				require.True(t, ok, "expected Sum[int64] data")
				return sum.DataPoints
			}
		}
	}
	t.Fatal("counter wrkflw_eventing_published_total not found")
	return nil
}

func TestPublishRecordsSpanAndCounter(t *testing.T) {
	sr, reader, pub := newObs(t)

	err := pub.Publish(t.Context(), runtime.OutboxEvent{
		Topic: "instance.completed", Payload: map[string]any{"ok": true}, InstanceID: "inst-9",
	})
	require.NoError(t, err)

	spans := sr.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "eventing.publish", spans[0].Name())

	dps := collectCounter(t, reader)
	require.NotEmpty(t, dps, "counter must have at least one data point")

	// Verify the data point carries status="ok".
	var found bool
	for _, dp := range dps {
		val, ok := dp.Attributes.Value(attribute.Key("status"))
		if ok && val.AsString() == "ok" {
			found = true
			break
		}
	}
	require.True(t, found, "expected a data point with status=ok")
}

func TestPublishFailureRecordsSpanErrorAndCounter(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))

	brokerErr := errors.New("broker down")
	errPub := &fakePub{err: brokerErr}
	pub := watermillpub.NewPublisher(errPub,
		watermillpub.WithTracerProvider(tp),
		watermillpub.WithMeterProvider(mp))

	err := pub.Publish(t.Context(), runtime.OutboxEvent{
		Topic: "instance.completed", Payload: map[string]any{"ok": true}, InstanceID: "inst-9",
	})
	require.Error(t, err)

	// Span must carry error status.
	spans := sr.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code, "span status must be Error")

	// Counter must have a data point with status="error".
	dps := collectCounter(t, reader)
	var found bool
	for _, dp := range dps {
		val, ok := dp.Attributes.Value(attribute.Key("status"))
		if ok && val.AsString() == "error" {
			found = true
			break
		}
	}
	require.True(t, found, "expected a data point with status=error")
}
