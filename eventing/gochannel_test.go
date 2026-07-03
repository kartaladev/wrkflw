package eventing_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestGoChannelPublisherRoundTrip(t *testing.T) {
	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer func() { require.NoError(t, closer.Close()) }()

	msgs, err := sub.Subscribe(t.Context(), "instance.completed")
	require.NoError(t, err)

	require.NoError(t, pub.Publish(t.Context(), kernel.OutboxEvent{
		Topic:      "instance.completed",
		Payload:    map[string]any{"order": "A-1"},
		DedupKey:   "inst-1:1:0",
		InstanceID: "inst-1",
	}))

	msg := <-msgs
	require.Equal(t, "inst-1:1:0", msg.UUID)
	require.Equal(t, "inst-1", msg.Metadata.Get("instance_id"))
	require.JSONEq(t, `{"order":"A-1"}`, string(msg.Payload))
	msg.Ack()
}
