package eventing_test

import (
	"log/slog"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakePub struct{ published int }

func (f *fakePub) Publish(_ string, _ ...*message.Message) error { f.published++; return nil }
func (f *fakePub) Close() error                                  { return nil }

func TestNewPublisherReturnsWorkingRuntimePublisher(t *testing.T) {
	fp := &fakePub{}
	pub := eventing.NewPublisher(fp)
	err := pub.Publish(t.Context(), kernel.OutboxEvent{
		Topic: "instance.completed", Payload: map[string]any{"ok": true}, DedupKey: "i:1:0",
	})
	require.NoError(t, err)
	require.Equal(t, 1, fp.published)
}

func TestNewPublisherWithOptionsForwardsToInternal(t *testing.T) {
	fp := &fakePub{}

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	logger := slog.Default()

	pub := eventing.NewPublisher(fp,
		eventing.WithLogger(logger),
		eventing.WithTracerProvider(tp),
		eventing.WithMeterProvider(mp),
	)

	err := pub.Publish(t.Context(), kernel.OutboxEvent{
		Topic: "instance.started", Payload: map[string]any{"x": 1}, DedupKey: "i:2:0",
	})
	require.NoError(t, err)

	// At least one span should have been recorded with the custom tracer provider.
	spans := sr.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "eventing.publish", spans[0].Name())
}

func TestNewPublisherMarshalErrorPropagates(t *testing.T) {
	fp := &fakePub{}
	pub := eventing.NewPublisher(fp)

	// A channel cannot be JSON-marshalled; this exercises the marshal-error path.
	err := pub.Publish(t.Context(), kernel.OutboxEvent{
		Topic:   "instance.broken",
		Payload: map[string]any{"bad": make(chan int)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshal payload")
}

func TestNewGoChannelPublisherWithOptions(t *testing.T) {
	logger := slog.Default()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))

	pub, sub, closer := eventing.NewGoChannelPublisher(
		eventing.WithLogger(logger),
		eventing.WithTracerProvider(tp),
		eventing.WithMeterProvider(mp),
	)
	defer func() { _ = closer.Close() }()

	msgs, err := sub.Subscribe(t.Context(), "test.topic")
	require.NoError(t, err)

	_ = pub.Publish(t.Context(), kernel.OutboxEvent{
		Topic: "test.topic", Payload: map[string]any{"k": "v"}, DedupKey: "i:3:0",
	})
	msg := <-msgs
	require.NotNil(t, msg)
	msg.Ack()
}
