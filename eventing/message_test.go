package eventing_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/eventing"
)

// TestNewMessageHandler is the ack/nack contract of NewMessageHandler: a
// returned error nacks (the broker re-delivers); nil acks.
func TestNewMessageHandler(t *testing.T) {
	t.Parallel()

	type delivered struct {
		name    string
		key     string
		payload map[string]any
		calls   int
	}

	type testCase struct {
		body       []byte
		deliverErr error
		assert     func(t *testing.T, got *delivered, err error)
	}

	validBody, err := json.Marshal(map[string]any{
		"messageName":    "OrderPlaced",
		"correlationKey": "ord-7",
		"variables":      map[string]any{"amount": float64(10)},
	})
	require.NoError(t, err)

	cases := map[string]testCase{
		"a decodable message routes to deliver": {
			body: validBody,
			assert: func(t *testing.T, got *delivered, err error) {
				require.NoError(t, err)
				assert.Equal(t, 1, got.calls)
				assert.Equal(t, "OrderPlaced", got.name)
				assert.Equal(t, "ord-7", got.key)
				assert.Equal(t, map[string]any{"amount": float64(10)}, got.payload)
			},
		},
		"a malformed payload is acked and never delivered": {
			body: []byte("{not json"),
			assert: func(t *testing.T, got *delivered, err error) {
				require.NoError(t, err, "malformed → ack, so the broker does not loop on poison")
				assert.Zero(t, got.calls, "deliver must not be called for a malformed payload")
			},
		},
		"an empty message name is acked and ignored": {
			body: []byte(`{"correlationKey":"ord-7"}`),
			assert: func(t *testing.T, got *delivered, err error) {
				require.NoError(t, err)
				assert.Zero(t, got.calls)
			},
		},
		"an empty body is acked and ignored": {
			body: nil,
			assert: func(t *testing.T, got *delivered, err error) {
				require.NoError(t, err)
				assert.Zero(t, got.calls)
			},
		},
		"a transient deliver failure is returned so the envelope is nacked": {
			body:       validBody,
			deliverErr: assert.AnError,
			assert: func(t *testing.T, got *delivered, err error) {
				require.ErrorIs(t, err, assert.AnError)
				assert.Equal(t, 1, got.calls)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := &delivered{}
			h := eventing.NewMessageHandler(func(_ context.Context, name, key string, payload map[string]any) error {
				got.calls++
				got.name, got.key, got.payload = name, key, payload
				return tc.deliverErr
			})

			err := h(t.Context(), eventing.Envelope{
				ID:       "dedup-1",
				Topic:    eventing.TopicMessagePrefix + "OrderPlaced",
				Metadata: map[string]string{eventing.MetaTopic: eventing.TopicMessagePrefix + "OrderPlaced"},
				Body:     tc.body,
			})
			tc.assert(t, got, err)
		})
	}
}
