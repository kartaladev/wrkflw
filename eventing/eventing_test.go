package eventing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TestPublisherMapsOutboxEventToEnvelope pins the wire mapping NewPublisher
// performs: one kernel.OutboxEvent in, one eventing.Envelope out, handed to the
// PublishFunc. The first row is a GOLDEN envelope — every field of the mapping
// stated as a literal, so a change to the id, the topic, any metadata key, or
// the body bytes has to be made deliberately here.
func TestPublisherMapsOutboxEventToEnvelope(t *testing.T) {
	t.Parallel()

	type testCase struct {
		event      kernel.OutboxEvent
		publishErr error
		assert     func(t *testing.T, sent []eventing.Envelope, spans tracetest.SpanStubs, err error)
	}

	sentinel := errors.New("broker unavailable")

	cases := map[string]testCase{
		"golden envelope for a fully populated event": {
			event: kernel.OutboxEvent{
				Topic:         "instance.completed",
				InstanceID:    "order-42",
				DefinitionRef: model.Version("order-flow", 1),
				DedupKey:      "order-42:3:0",
				Payload:       map[string]any{"status": "completed"},
			},
			assert: func(t *testing.T, sent []eventing.Envelope, _ tracetest.SpanStubs, err error) {
				require.NoError(t, err)
				require.Len(t, sent, 1)
				assert.Equal(t, "order-42:3:0", sent[0].ID, "the dedup key is the envelope id")
				assert.Equal(t, "instance.completed", sent[0].Topic)
				assert.Equal(t, map[string]string{
					"topic":          "instance.completed",
					"instance_id":    "order-42",
					"definition_ref": "order-flow:1",
				}, sent[0].Metadata)
				assert.Equal(t, []byte(`{"status":"completed"}`), sent[0].Body)
			},
		},
		"an event with no dedup key gets a generated uuid": {
			event: kernel.OutboxEvent{
				Topic:      "instance.failed",
				InstanceID: "order-43",
				Payload:    map[string]any{"error": "boom"},
			},
			assert: func(t *testing.T, sent []eventing.Envelope, _ tracetest.SpanStubs, err error) {
				require.NoError(t, err)
				require.Len(t, sent, 1)
				_, parseErr := uuid.Parse(sent[0].ID)
				assert.NoError(t, parseErr, "an empty dedup key must yield a parseable UUID, not an empty id")
				assert.Equal(t, "", sent[0].Metadata["definition_ref"],
					"a zero DefinitionRef still writes the key, empty")
			},
		},
		"an unmarshalable payload fails before the PublishFunc runs": {
			event: kernel.OutboxEvent{
				Topic:   "instance.broken",
				Payload: map[string]any{"bad": make(chan int)},
			},
			assert: func(t *testing.T, sent []eventing.Envelope, spans tracetest.SpanStubs, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "marshal payload")
				assert.Empty(t, sent, "nothing may reach the broker when the payload cannot be encoded")
				require.Len(t, spans, 1)
				assert.Equal(t, "eventing.publish", spans[0].Name)
			},
		},
		"a PublishFunc failure is wrapped with the topic and recorded on the span": {
			event: kernel.OutboxEvent{
				Topic:      "instance.completed",
				InstanceID: "order-44",
				Payload:    map[string]any{"ok": true},
			},
			publishErr: sentinel,
			assert: func(t *testing.T, sent []eventing.Envelope, spans tracetest.SpanStubs, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, sentinel)
				assert.Contains(t, err.Error(), `topic="instance.completed"`)
				assert.Len(t, sent, 1, "the PublishFunc was still called")
				require.Len(t, spans, 1)
				assert.NotEmpty(t, spans[0].Events, "the error must be recorded on the span")
			},
		},
		"the span carries the destination and instance attributes": {
			event: kernel.OutboxEvent{
				Topic:      "instance.terminated",
				InstanceID: "order-45",
				Payload:    map[string]any{},
			},
			assert: func(t *testing.T, _ []eventing.Envelope, spans tracetest.SpanStubs, err error) {
				require.NoError(t, err)
				require.Len(t, spans, 1, "the injected TracerProvider must be the one used")
				assert.Equal(t, "eventing.publish", spans[0].Name)
				attrs := map[string]string{}
				for _, a := range spans[0].Attributes {
					attrs[string(a.Key)] = a.Value.Emit()
				}
				assert.Equal(t, "instance.terminated", attrs["messaging.destination"])
				assert.Equal(t, "order-45", attrs["wrkflw.instance_id"])
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var sent []eventing.Envelope
			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
			mp := metric.NewMeterProvider(metric.WithReader(metric.NewManualReader()))

			pub := eventing.NewPublisher(
				func(_ context.Context, env eventing.Envelope) error {
					sent = append(sent, env)
					return tc.publishErr
				},
				eventing.WithTracerProvider(tp),
				eventing.WithMeterProvider(mp),
			)

			err := pub.Publish(t.Context(), tc.event)

			require.NoError(t, tp.ForceFlush(t.Context()))
			tc.assert(t, sent, tracetest.SpanStubsFromReadOnlySpans(sr.Ended()), err)
		})
	}
}
