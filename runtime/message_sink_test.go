package runtime_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// recordingSink captures every OutboundMessage handed to it.
type recordingSink struct {
	mu   sync.Mutex
	msgs []runtime.OutboundMessage
}

func (s *recordingSink) Send(_ context.Context, msg runtime.OutboundMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
	return nil
}

func (s *recordingSink) recorded() []runtime.OutboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runtime.OutboundMessage, len(s.msgs))
	copy(out, s.msgs)
	return out
}

// sendTaskRuntimeDef: start → send(msg "shipment.notify", corr orderID) → end.
func sendTaskRuntimeDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-send-rt", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewSendTask("send", "shipment.notify", model.WithCorrelationKey("orderID")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "send"},
			{ID: "f2", Source: "send", Target: "end"},
		},
	}
}

// TestRunnerSendTaskInvokesMessageSink asserts that a SendTask routed through a
// Runner configured WithMessageSink delivers the OutboundMessage (name,
// correlation key, payload, instance ID) to the sink and completes.
func TestRunnerSendTaskInvokesMessageSink(t *testing.T) {
	fc := clockwork.NewFakeClock()
	sink := &recordingSink{}
	r := runtime.NewRunner(nil, runtime.NewMemStore(), runtime.WithRunnerClock(fc), runtime.WithMessageSink(sink))

	st, err := r.Run(t.Context(), sendTaskRuntimeDef(), "i1", map[string]any{"orderID": "o-7"})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)

	got := sink.recorded()
	require.Len(t, got, 1, "the SendTask must deliver exactly one outbound message")
	assert.Equal(t, "i1", got[0].InstanceID)
	assert.Equal(t, "shipment.notify", got[0].Name)
	assert.Equal(t, "o-7", got[0].CorrelationKey)
	assert.Equal(t, "o-7", got[0].Payload["orderID"])
}

// TestRunnerSendTaskNoSinkErrors asserts that reaching a SendTask without a
// configured MessageSink returns a descriptive error rather than silently
// dropping the message.
func TestRunnerSendTaskNoSinkErrors(t *testing.T) {
	fc := clockwork.NewFakeClock()
	r := runtime.NewRunner(nil, runtime.NewMemStore(), runtime.WithRunnerClock(fc))

	_, err := r.Run(t.Context(), sendTaskRuntimeDef(), "i1", map[string]any{"orderID": "o-7"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MessageSink")
}
