package watermill_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/require"
	watermillpub "github.com/zakyalvan/krtlwrkflw/internal/eventing/watermill"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// fakePub captures the topic and messages of the last Publish call.
type fakePub struct {
	topic string
	msgs  []*message.Message
	err   error
}

func (f *fakePub) Publish(topic string, msgs ...*message.Message) error {
	if f.err != nil {
		return f.err
	}
	f.topic = topic
	f.msgs = msgs
	return nil
}

func (f *fakePub) Close() error { return nil }

func TestPublishMapsEventToMessage(t *testing.T) {
	tests := map[string]struct {
		event  runtime.OutboxEvent
		assert func(t *testing.T, fp *fakePub, err error)
	}{
		"dedup key becomes the message UUID and payload is JSON": {
			event: runtime.OutboxEvent{
				Topic:      "instance.completed",
				Payload:    map[string]any{"ok": true},
				DedupKey:   "inst-1:3:0",
				InstanceID: "inst-1",
			},
			assert: func(t *testing.T, fp *fakePub, err error) {
				require.NoError(t, err)
				require.Equal(t, "instance.completed", fp.topic)
				require.Len(t, fp.msgs, 1)
				require.Equal(t, "inst-1:3:0", fp.msgs[0].UUID)
				require.JSONEq(t, `{"ok":true}`, string(fp.msgs[0].Payload))
				require.Equal(t, "inst-1", fp.msgs[0].Metadata.Get("instance_id"))
				require.Equal(t, "instance.completed", fp.msgs[0].Metadata.Get("topic"))
			},
		},
		"empty dedup key gets a generated non-empty UUID": {
			event: runtime.OutboxEvent{Topic: "instance.failed", Payload: map[string]any{"error": "boom"}},
			assert: func(t *testing.T, fp *fakePub, err error) {
				require.NoError(t, err)
				require.NotEmpty(t, fp.msgs[0].UUID)
			},
		},
		"publisher error is wrapped and returned": {
			event: runtime.OutboxEvent{Topic: "instance.completed", Payload: map[string]any{}},
			assert: func(t *testing.T, _ *fakePub, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "instance.completed")
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fp := &fakePub{}
			if name == "publisher error is wrapped and returned" {
				fp.err = errors.New("broker down")
			}
			pub := watermillpub.NewPublisher(fp)
			err := pub.Publish(t.Context(), tc.event)
			tc.assert(t, fp, err)
		})
	}
}

func TestPublisherImplementsRuntimePublisher(t *testing.T) {
	var _ runtime.Publisher = (*watermillpub.Publisher)(nil)
}

func TestPublishMarshalErrorPropagates(t *testing.T) {
	// A channel cannot be JSON-marshalled; this exercises the marshal-error path.
	pub := watermillpub.NewPublisher(&fakePub{})
	err := pub.Publish(t.Context(), runtime.OutboxEvent{
		Topic:   "instance.broken",
		Payload: map[string]any{"bad": make(chan int)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshal payload")
}

func TestNewPublisherWithLogger(t *testing.T) {
	// Verify WithLogger option is accepted and a Publish call still works.
	fp := &fakePub{}
	pub := watermillpub.NewPublisher(fp, watermillpub.WithLogger(slog.Default()))
	err := pub.Publish(t.Context(), runtime.OutboxEvent{
		Topic: "instance.started", Payload: map[string]any{"ok": true}, DedupKey: "i:4:0",
	})
	require.NoError(t, err)
	require.Equal(t, "instance.started", fp.topic)
}
