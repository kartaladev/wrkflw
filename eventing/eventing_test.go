package eventing_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime"
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
		// publishes is how many times the same event is published; 0 means once.
		publishes int
		assert    func(t *testing.T, sent []eventing.Envelope, spans tracetest.SpanStubs, err error)
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
				// The routing keys are golden; the trace-context keys the
				// propagator adds are the subject of
				// TestPublishInjectsTraceContext and are dropped here so this row
				// stays an exact comparison rather than a subset check.
				routing := maps.Clone(sent[0].Metadata)
				delete(routing, "traceparent")
				delete(routing, "tracestate")
				assert.Equal(t, map[string]string{
					"topic":          "instance.completed",
					"instance_id":    "order-42",
					"definition_ref": "order-flow:1",
				}, routing)
				assert.Equal(t, []byte(`{"status":"completed"}`), sent[0].Body)
			},
		},
		"an event with no dedup key gets a freshly minted, unique id": {
			event: kernel.OutboxEvent{
				Topic:      "instance.failed",
				InstanceID: "order-43",
				Payload:    map[string]any{"error": "boom"},
			},
			publishes: 2,
			assert: func(t *testing.T, sent []eventing.Envelope, _ tracetest.SpanStubs, err error) {
				require.NoError(t, err)
				require.Len(t, sent, 2)
				assert.NotEmpty(t, sent[0].ID, "an empty dedup key must still yield an id")
				assert.NotEqual(t, sent[0].ID, sent[1].ID,
					"two events with no dedup key must not collide on the same envelope id")
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

			publishes := max(tc.publishes, 1)
			var err error
			for range publishes {
				err = pub.Publish(t.Context(), tc.event)
			}

			require.NoError(t, tp.ForceFlush(t.Context()))
			tc.assert(t, sent, tracetest.SpanStubsFromReadOnlySpans(sr.Ended()), err)
		})
	}
}

// TestTopicConstantsMatchWhatTheRuntimeWrites checks the exported topic
// constants against the literals runtime/outbox.go actually commits, by driving
// real instances to each terminal state and reading the outbox events the store
// recorded. Asserting the constants against themselves would pass by
// construction; asserting them against the runtime's own writes is what makes a
// drift on either side fail here.
func TestTopicConstantsMatchWhatTheRuntimeWrites(t *testing.T) {
	t.Parallel()

	type testCase struct {
		drive  func(t *testing.T, ctx context.Context, store *kernel.MemInstanceStore)
		assert func(t *testing.T, topics []string)
	}

	startEnd := func(id string) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: id, Version: 1,
			Nodes: []model.Node{event.NewStart("start"), event.NewEnd("end")},
			Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
		}
	}

	cases := map[string]testCase{
		"a completed instance writes TopicInstanceCompleted": {
			drive: func(t *testing.T, ctx context.Context, store *kernel.MemInstanceStore) {
				driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
				require.NoError(t, err)
				st, err := driver.Drive(ctx, startEnd("done-flow"), "i-done", nil)
				require.NoError(t, err)
				require.Equal(t, engine.StatusCompleted, st.Status)
			},
			assert: func(t *testing.T, topics []string) {
				assert.Contains(t, topics, eventing.TopicInstanceCompleted)
			},
		},
		"a failed instance writes TopicInstanceFailed": {
			drive: func(t *testing.T, ctx context.Context, store *kernel.MemInstanceStore) {
				cat := action.NewCatalog(map[string]action.Action{
					"boom": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
						return nil, errors.New("action failed")
					}),
				})
				def := &model.ProcessDefinition{
					ID: "fail-flow", Version: 1,
					Nodes: []model.Node{
						event.NewStart("start"),
						activity.NewServiceTask("task", activity.WithTaskAction("boom")),
						event.NewEnd("end"),
					},
					Flows: []flow.SequenceFlow{
						{ID: "f1", Source: "start", Target: "task"},
						{ID: "f2", Source: "task", Target: "end"},
					},
				}
				driver, err := runtime.NewProcessDriver(
					runtime.WithInstanceStore(store), runtime.WithActionCatalog(cat))
				require.NoError(t, err)
				st, err := driver.Drive(ctx, def, "i-fail", nil)
				require.NoError(t, err)
				require.Equal(t, engine.StatusFailed, st.Status)
			},
			assert: func(t *testing.T, topics []string) {
				assert.Contains(t, topics, eventing.TopicInstanceFailed)
			},
		},
		"a cancelled instance writes TopicInstanceTerminated": {
			drive: func(t *testing.T, ctx context.Context, store *kernel.MemInstanceStore) {
				def := &model.ProcessDefinition{
					ID: "park-flow", Version: 1,
					Nodes: []model.Node{
						event.NewStart("start"),
						activity.NewReceiveTask("await", "Nudge"),
						event.NewEnd("end"),
					},
					Flows: []flow.SequenceFlow{
						{ID: "f1", Source: "start", Target: "await"},
						{ID: "f2", Source: "await", Target: "end"},
					},
				}
				driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
				require.NoError(t, err)
				st, err := driver.Drive(ctx, def, "i-park", nil)
				require.NoError(t, err)
				require.Equal(t, engine.StatusRunning, st.Status)

				st, err = driver.CancelInstance(ctx, def, "i-park")
				require.NoError(t, err)
				require.Equal(t, engine.StatusTerminated, st.Status)
			},
			assert: func(t *testing.T, topics []string) {
				assert.Contains(t, topics, eventing.TopicInstanceTerminated)
			},
		},
		"a SendTask writes TopicMessagePrefix + the message name": {
			drive: func(t *testing.T, ctx context.Context, store *kernel.MemInstanceStore) {
				def := &model.ProcessDefinition{
					ID: "send-flow", Version: 1,
					Nodes: []model.Node{
						event.NewStart("start"),
						activity.NewSendTask("send", "OrderPlaced"),
						event.NewEnd("end"),
					},
					Flows: []flow.SequenceFlow{
						{ID: "f1", Source: "start", Target: "send"},
						{ID: "f2", Source: "send", Target: "end"},
					},
				}
				driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
				require.NoError(t, err)
				st, err := driver.Drive(ctx, def, "i-send", nil)
				require.NoError(t, err)
				require.Equal(t, engine.StatusCompleted, st.Status)
			},
			assert: func(t *testing.T, topics []string) {
				assert.Contains(t, topics, eventing.TopicMessagePrefix+"OrderPlaced")
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store, err := kernel.NewMemInstanceStore()
			require.NoError(t, err)
			tc.drive(t, t.Context(), store)

			var topics []string
			for _, ev := range store.Events() {
				topics = append(topics, ev.Topic)
			}
			require.NotEmpty(t, topics, "the runtime must have committed at least one outbox event")
			tc.assert(t, topics)
		})
	}
}

// TestPublishInjectsTraceContext pins P1 of this refactor: the trace context an
// Envelope carries is written by THIS package's propagator, defaulting to
// propagation.TraceContext{}, and never by otel.GetTextMapPropagator().
//
// The distinction is not cosmetic. The default global propagator is a no-op —
// measured on otel v1.46.0, its Fields() is empty and it injects nothing — so a
// publisher that reaches for the global writes no traceparent at all unless the
// deployment happened to call otel.SetTextMapPropagator. A test that sets the
// global would pass while production silently lost the header. Nothing in this
// test touches the global; the traceparent below can only come from the
// package's own default.
func TestPublishInjectsTraceContext(t *testing.T) {
	t.Parallel()

	type testCase struct {
		opts   func() []eventing.Option
		assert func(t *testing.T, env eventing.Envelope, published trace.SpanContext)
	}

	cases := map[string]testCase{
		"the default propagator writes a W3C traceparent naming the publish span": {
			opts: func() []eventing.Option { return nil },
			assert: func(t *testing.T, env eventing.Envelope, published trace.SpanContext) {
				require.NotEmpty(t, env.Metadata["traceparent"],
					"the default propagator must write traceparent without any global being set")

				carried := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(
					context.Background(), propagation.MapCarrier(env.Metadata)))
				require.True(t, carried.IsValid(), "the injected traceparent must parse back")
				assert.Equal(t, published.TraceID(), carried.TraceID())
				assert.Equal(t, published.SpanID(), carried.SpanID(),
					"the carried span must be the publish span, so a consumer parents onto it")
			},
		},
		"an explicit propagator replaces the default": {
			opts: func() []eventing.Option {
				return []eventing.Option{eventing.WithPropagator(stubPropagator{})}
			},
			assert: func(t *testing.T, env eventing.Envelope, _ trace.SpanContext) {
				assert.Equal(t, "stub-value", env.Metadata["stub-key"])
				assert.NotContains(t, env.Metadata, "traceparent",
					"WithPropagator must replace the default, not compose with it")
			},
		},
		"a propagator that writes nothing leaves only the routing keys": {
			opts: func() []eventing.Option {
				return []eventing.Option{
					eventing.WithPropagator(propagation.NewCompositeTextMapPropagator()),
				}
			},
			assert: func(t *testing.T, env eventing.Envelope, _ trace.SpanContext) {
				assert.Equal(t, map[string]string{
					"topic":          "instance.completed",
					"instance_id":    "order-42",
					"definition_ref": "",
				}, env.Metadata)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got eventing.Envelope
			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

			opts := append([]eventing.Option{eventing.WithTracerProvider(tp)}, tc.opts()...)
			pub := eventing.NewPublisher(func(_ context.Context, env eventing.Envelope) error {
				got = env
				return nil
			}, opts...)

			require.NoError(t, pub.Publish(t.Context(), kernel.OutboxEvent{
				Topic:      eventing.TopicInstanceCompleted,
				InstanceID: "order-42",
				Payload:    map[string]any{"ok": true},
			}))
			require.NoError(t, tp.ForceFlush(t.Context()))

			spans := sr.Ended()
			require.Len(t, spans, 1)
			tc.assert(t, got, spans[0].SpanContext())
		})
	}
}

// stubPropagator is a TextMapPropagator that writes one fixed key, standing in
// for a deployment's own propagator (B3, Jaeger, a composite). It proves
// WithPropagator is actually consulted rather than ignored.
type stubPropagator struct{}

func (stubPropagator) Inject(_ context.Context, carrier propagation.TextMapCarrier) {
	carrier.Set("stub-key", "stub-value")
}

func (stubPropagator) Extract(ctx context.Context, _ propagation.TextMapCarrier) context.Context {
	return ctx
}

func (stubPropagator) Fields() []string { return []string{"stub-key"} }
