package eventing_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// capturingStarter is an ad-hoc InstanceStarter double that records calls and
// returns a configurable error.
type capturingStarter struct {
	mu    sync.Mutex
	ids   []string
	err   error
	state engine.InstanceState
}

func (s *capturingStarter) Run(_ context.Context, _ *model.ProcessDefinition, id string, _ map[string]any) (engine.InstanceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, id)
	return s.state, s.err
}

func (s *capturingStarter) startedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ids...)
}

func chainCore(t *testing.T, starter runtime.InstanceStarter, capture *[]runtime.ChainEvent, mu *sync.Mutex) *runtime.Chainer {
	t.Helper()
	policy := func(_ context.Context, ev runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
		mu.Lock()
		*capture = append(*capture, ev)
		mu.Unlock()
		return runtime.SuccessorDecision{Def: &model.ProcessDefinition{ID: "succ", Version: 1}, Vars: ev.Result}, true
	}
	return runtime.NewChainer(starter, policy)
}

func TestChainHandlerProjection(t *testing.T) {
	tests := map[string]struct {
		topic   string
		body    string
		assert  func(t *testing.T, err error, starter *capturingStarter, seen []runtime.ChainEvent)
	}{
		"completed topic projects OutcomeCompleted with vars": {
			topic: "instance.completed",
			body:  `{"orderID":"o-7"}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []runtime.ChainEvent) {
				require.NoError(t, err)
				require.Len(t, seen, 1)
				assert.Equal(t, runtime.OutcomeCompleted, seen[0].Outcome)
				assert.Equal(t, "p1", seen[0].PredecessorID)
				assert.Equal(t, "approval:1", seen[0].PredecessorDef, "def metadata must project into PredecessorDef (ADR-0047)")
				assert.Equal(t, map[string]any{"orderID": "o-7"}, seen[0].Result)
				assert.Equal(t, []string{"p1-next-completed"}, starter.startedIDs())
			},
		},
		"failed topic projects OutcomeFailed": {
			topic: "instance.failed",
			body:  `{"error":"boom"}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []runtime.ChainEvent) {
				require.NoError(t, err)
				require.Len(t, seen, 1)
				assert.Equal(t, runtime.OutcomeFailed, seen[0].Outcome)
				assert.Equal(t, []string{"p1-next-failed"}, starter.startedIDs())
			},
		},
		"terminated topic projects OutcomeTerminated": {
			topic: "instance.terminated",
			body:  `{"error":"cancelled"}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []runtime.ChainEvent) {
				require.NoError(t, err)
				require.Len(t, seen, 1)
				assert.Equal(t, runtime.OutcomeTerminated, seen[0].Outcome)
				assert.Equal(t, []string{"p1-next-terminated"}, starter.startedIDs())
			},
		},
		"unknown topic is acked without chaining": {
			topic: "instance.someotherthing",
			body:  `{}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []runtime.ChainEvent) {
				require.NoError(t, err, "unknown topic must ack, not error")
				assert.Empty(t, seen, "policy must not be consulted for a non-terminal topic")
				assert.Empty(t, starter.startedIDs())
			},
		},
		"malformed payload is acked without chaining": {
			topic: "instance.completed",
			body:  `{not json`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []runtime.ChainEvent) {
				require.NoError(t, err, "poison payload must ack (no infinite re-delivery loop)")
				assert.Empty(t, seen)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []runtime.ChainEvent
			starter := &capturingStarter{}
			h := eventing.NewChainHandler(chainCore(t, starter, &seen, &mu))

			msg := message.NewMessage("uuid-1", []byte(tc.body))
			msg.Metadata.Set("topic", tc.topic)
			msg.Metadata.Set("instance_id", "p1")
			msg.Metadata.Set("def", "approval:1")
			err := h(msg)

			mu.Lock()
			seenCopy := append([]runtime.ChainEvent(nil), seen...)
			mu.Unlock()
			tc.assert(t, err, starter, seenCopy)
		})
	}
}

// TestChainHandlerTransientErrorNacks asserts a transient start failure is
// returned (so the broker re-delivers), not swallowed.
func TestChainHandlerTransientErrorNacks(t *testing.T) {
	var mu sync.Mutex
	var seen []runtime.ChainEvent
	starter := &capturingStarter{err: errors.New("db down")}
	h := eventing.NewChainHandler(chainCore(t, starter, &seen, &mu))

	msg := message.NewMessage("uuid-1", []byte(`{}`))
	msg.Metadata.Set("topic", "instance.completed")
	msg.Metadata.Set("instance_id", "p1")
	require.Error(t, h(msg), "a transient start failure must return an error so the message is nacked")
}

// errSubscriber is a message.Subscriber whose Subscribe always fails.
type errSubscriber struct{ err error }

func (e errSubscriber) Subscribe(context.Context, string) (<-chan *message.Message, error) {
	return nil, e.err
}
func (e errSubscriber) Close() error { return nil }

// TestChainerRunSubscribeError asserts Run surfaces a Subscribe failure (and, by
// subscribing all topics before starting any goroutine, does not leak workers).
func TestChainerRunSubscribeError(t *testing.T) {
	policy := func(context.Context, runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
		return runtime.SuccessorDecision{}, false
	}
	core := runtime.NewChainer(&capturingStarter{}, policy)
	cr := eventing.NewChainerRunner(core)

	sentinel := errors.New("broker unavailable")
	err := cr.Run(t.Context(), errSubscriber{err: sentinel})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// TestChainerRunStartsSuccessorEndToEnd drives the full subscription loop over a
// real GoChannel pub/sub + a real Runner + MemStore + MemChainLinkStore: a
// published instance.completed event starts the mapped successor exactly once.
func TestChainerRunStartsSuccessorEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	clk := clockwork.NewFakeClock()
	store := runtime.NewMemStore()
	links := runtime.NewMemChainLinkStore()
	runner := runtime.NewRunner(action.NewMapCatalog(nil), clk, store)

	succ := &model.ProcessDefinition{
		ID: "fulfillment", Version: 1,
		Nodes: []model.Node{model.NewStartEvent("s"), model.NewEndEvent("e")},
		Flows: []model.SequenceFlow{{ID: "f", Source: "s", Target: "e"}},
	}
	policy := func(_ context.Context, ev runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
		return runtime.SuccessorDecision{Def: succ, Vars: ev.Result}, true
	}
	core := runtime.NewChainer(runner, policy, runtime.WithChainLinks(links), runtime.WithChainClock(clk))

	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer func() { require.NoError(t, closer.Close()) }()

	cr := eventing.NewChainerRunner(core)
	done := make(chan error, 1)
	go func() { done <- cr.Run(ctx, sub) }()

	// GoChannel is non-persistent: publishing before Run subscribes drops the
	// message. Republish on each tick until the (idempotent) chaining lands.
	require.Eventually(t, func() bool {
		_ = pub.Publish(ctx, runtime.OutboxEvent{
			Topic:      "instance.completed",
			Payload:    map[string]any{"orderID": "o-9"},
			InstanceID: "p1",
		})
		_, _, err := store.Load(ctx, "p1-next-completed")
		return err == nil
	}, 3*time.Second, 25*time.Millisecond)

	st, _, err := store.Load(ctx, "p1-next-completed")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)

	got, ok, err := links.LookupBySuccessor(ctx, "p1-next-completed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "p1", got.PredecessorID)

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}
