package eventing_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/eventing"
)

func TestNewMessageHandlerRoutesToDeliver(t *testing.T) {
	var gotName, gotKey string
	var gotPayload map[string]any
	deliver := func(_ context.Context, name, key string, payload map[string]any) error {
		gotName, gotKey, gotPayload = name, key, payload
		return nil
	}
	h := eventing.NewMessageHandler(deliver)

	body, _ := json.Marshal(map[string]any{
		"messageName":    "OrderPlaced",
		"correlationKey": "ord-7",
		"variables":      map[string]any{"amount": float64(10)},
	})
	msg := message.NewMessage("dedup-1", body)
	msg.Metadata.Set("topic", "message.OrderPlaced")

	require.NoError(t, h(msg))
	assert.Equal(t, "OrderPlaced", gotName)
	assert.Equal(t, "ord-7", gotKey)
	assert.Equal(t, map[string]any{"amount": float64(10)}, gotPayload)
}

func TestNewMessageHandlerAcksMalformedPayload(t *testing.T) {
	called := false
	h := eventing.NewMessageHandler(func(context.Context, string, string, map[string]any) error {
		called = true
		return nil
	})
	msg := message.NewMessage("dedup-2", []byte("{not json"))
	msg.Metadata.Set("topic", "message.X")
	require.NoError(t, h(msg)) // malformed → ack, no loop
	assert.False(t, called, "deliver must not be called for a malformed payload")
}
