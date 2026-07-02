package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// recordingStore wraps an in-memory Store and captures every committed AppliedStep.
type recordingStore struct {
	runtime.Store
	steps []runtime.AppliedStep
}

func (s *recordingStore) Create(ctx context.Context, step runtime.AppliedStep) (runtime.Token, error) {
	s.steps = append(s.steps, step)
	return s.Store.Create(ctx, step)
}

func (s *recordingStore) Commit(ctx context.Context, expected runtime.Token, step runtime.AppliedStep) (runtime.Token, error) {
	s.steps = append(s.steps, step)
	return s.Store.Commit(ctx, expected, step)
}

// buildOrderPlacedSendTaskDef constructs: start → sendTask("OrderPlaced") → end.
func buildOrderPlacedSendTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-send-outbox", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewSendTask("send", "OrderPlaced"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "send"},
			{ID: "f2", Source: "send", Target: "end"},
		},
	}
}

// TestSendTaskCommitsMessageOutboxEvent asserts that a SendTask's message is written
// atomically as a message.<Name> outbox event in AppliedStep.Events (ADR-0067),
// and that Run succeeds without any MessageSink configured.
func TestSendTaskCommitsMessageOutboxEvent(t *testing.T) {
	def := buildOrderPlacedSendTaskDef()
	store := &recordingStore{Store: mustMemStore(t)}
	r := mustRunner(t, nil, store) // NO MessageSink — must not error
	_, err := r.Run(t.Context(), def, "i-1", map[string]any{"k": "v"})
	require.NoError(t, err)

	// Exactly one message.OrderPlaced event was committed in an AppliedStep.
	var msgEvents []runtime.OutboxEvent
	for _, step := range store.steps {
		for _, ev := range step.Events {
			if ev.Topic == "message.OrderPlaced" {
				msgEvents = append(msgEvents, ev)
			}
		}
	}
	require.Len(t, msgEvents, 1)
	assert.Equal(t, "i-1", msgEvents[0].InstanceID)
	assert.Equal(t, "OrderPlaced", msgEvents[0].Payload["messageName"])
}
