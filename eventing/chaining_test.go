package eventing_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/chain"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// capturingStarter is an ad-hoc InstanceStarter double that records calls and
// returns a configurable error.
type capturingStarter struct {
	mu    sync.Mutex
	ids   []string
	err   error
	state engine.InstanceState
}

func (s *capturingStarter) Drive(_ context.Context, _ *model.ProcessDefinition, id string, _ map[string]any) (engine.InstanceState, error) {
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

func chainCore(t *testing.T, starter chain.InstanceStarter, capture *[]chain.ChainEvent, mu *sync.Mutex) *chain.Chainer {
	t.Helper()
	policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		mu.Lock()
		*capture = append(*capture, ev)
		mu.Unlock()
		return chain.SuccessorDecision{Def: &model.ProcessDefinition{ID: "succ", Version: 1}, Vars: ev.Result}, true
	}
	c, err := chain.NewChainer(starter, policy)
	require.NoError(t, err)
	return c
}

// TestChainHandlerProjection is the ack/nack contract of NewChainHandler,
// row by row. A returned error nacks (the broker re-delivers); nil acks.
func TestChainHandlerProjection(t *testing.T) {
	t.Parallel()

	type testCase struct {
		topic      string
		body       string
		starterErr error
		assert     func(t *testing.T, err error, starter *capturingStarter, seen []chain.ChainEvent)
	}

	tests := map[string]testCase{
		"completed topic projects OutcomeCompleted with vars": {
			topic: eventing.TopicInstanceCompleted,
			body:  `{"orderID":"o-7"}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []chain.ChainEvent) {
				require.NoError(t, err)
				require.Len(t, seen, 1)
				assert.Equal(t, kernel.OutcomeCompleted, seen[0].Outcome)
				assert.Equal(t, "p1", seen[0].PredecessorID)
				assert.Equal(t, model.Version("approval", 1), seen[0].PredecessorDefinitionRef, "def metadata must project into PredecessorDefinitionRef")
				assert.Equal(t, map[string]any{"orderID": "o-7"}, seen[0].Result)
				assert.Equal(t, []string{"p1-next-completed"}, starter.startedIDs())
			},
		},
		"failed topic projects OutcomeFailed": {
			topic: eventing.TopicInstanceFailed,
			body:  `{"error":"boom"}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []chain.ChainEvent) {
				require.NoError(t, err)
				require.Len(t, seen, 1)
				assert.Equal(t, kernel.OutcomeFailed, seen[0].Outcome)
				assert.Equal(t, []string{"p1-next-failed"}, starter.startedIDs())
			},
		},
		"terminated topic projects OutcomeTerminated": {
			topic: eventing.TopicInstanceTerminated,
			body:  `{"error":"cancelled"}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []chain.ChainEvent) {
				require.NoError(t, err)
				require.Len(t, seen, 1)
				assert.Equal(t, kernel.OutcomeTerminated, seen[0].Outcome)
				assert.Equal(t, []string{"p1-next-terminated"}, starter.startedIDs())
			},
		},
		"unknown topic is acked without chaining": {
			topic: "instance.someotherthing",
			body:  `{}`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []chain.ChainEvent) {
				require.NoError(t, err, "unknown topic must ack, not error")
				assert.Empty(t, seen, "policy must not be consulted for a non-terminal topic")
				assert.Empty(t, starter.startedIDs())
			},
		},
		"malformed payload is acked without chaining": {
			topic: eventing.TopicInstanceCompleted,
			body:  `{not json`,
			assert: func(t *testing.T, err error, starter *capturingStarter, seen []chain.ChainEvent) {
				require.NoError(t, err, "poison payload must ack (no infinite re-delivery loop)")
				assert.Empty(t, seen)
			},
		},
		"a transient start failure is returned so the envelope is nacked": {
			topic:      eventing.TopicInstanceCompleted,
			body:       `{}`,
			starterErr: errors.New("db down"),
			assert: func(t *testing.T, err error, starter *capturingStarter, _ []chain.ChainEvent) {
				require.Error(t, err, "a transient start failure must return an error so the envelope is nacked")
				assert.NotEmpty(t, starter.startedIDs(), "the start was attempted before it failed")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			var seen []chain.ChainEvent
			starter := &capturingStarter{err: tc.starterErr}
			h := eventing.NewChainHandler(chainCore(t, starter, &seen, &mu))

			err := h(t.Context(), eventing.Envelope{
				ID:    "uuid-1",
				Topic: tc.topic,
				Metadata: map[string]string{
					eventing.MetaTopic:         tc.topic,
					eventing.MetaInstanceID:    "p1",
					eventing.MetaDefinitionRef: "approval:1",
				},
				Body: []byte(tc.body),
			})

			mu.Lock()
			seenCopy := append([]chain.ChainEvent(nil), seen...)
			mu.Unlock()
			tc.assert(t, err, starter, seenCopy)
		})
	}
}

// errSubscriber is an eventing.Subscriber whose Subscribe always fails.
type errSubscriber struct{ err error }

func (e errSubscriber) Subscribe(context.Context, string, eventing.Handler) error { return e.err }

// TestChainerRunSubscribeError asserts Run surfaces a Subscribe failure. Because
// Subscribe now BLOCKS and owns its own delivery loop, the old "subscribe every
// topic before starting any goroutine" shape is impossible; the property it
// bought — one failing subscription strands none of the others — is re-established
// by cancelling the sibling subscriptions and waiting for all of them to return.
// TestChainerRunLeaksNoGoroutines is the leak half of the same invariant.
func TestChainerRunSubscribeError(t *testing.T) {
	t.Parallel()

	policy := func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{}, false
	}
	core, err := chain.NewChainer(&capturingStarter{}, policy)
	require.NoError(t, err)
	cr := eventing.NewChainerRunner(core)

	sentinel := errors.New("broker unavailable")
	err = cr.Run(t.Context(), errSubscriber{err: sentinel})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// TestChainerRunStartsSuccessorEndToEnd drives the full subscription loop over a
// real in-process pub/sub + a real ProcessDriver + MemInstanceStore + MemChainLinkStore: a
// published instance.completed event starts the mapped successor exactly once.
func TestChainerRunStartsSuccessorEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	clk := clockwork.NewFakeClock()
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	links := kernel.NewMemChainLinkStore()
	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store), runtime.WithClock(clk))
	require.NoError(t, err)

	succ := &model.ProcessDefinition{
		ID: "fulfillment", Version: 1,
		Nodes: []model.Node{event.NewStart("s"), event.NewEnd("e")},
		Flows: []flow.SequenceFlow{{ID: "f", Source: "s", Target: "e"}},
	}
	policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{Def: succ, Vars: ev.Result}, true
	}
	core, err := chain.NewChainer(driver, policy, chain.WithChainLinks(links), chain.WithClock(clk))
	require.NoError(t, err)

	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer func() { require.NoError(t, closer.Close()) }()

	cr := eventing.NewChainerRunner(core)
	done := make(chan error, 1)
	go func() { done <- cr.Run(ctx, sub) }()

	// The bus is non-persistent: publishing before Run subscribes drops the
	// message. Republish on each tick until the (idempotent) chaining lands.
	require.Eventually(t, func() bool {
		_ = pub.Publish(ctx, kernel.OutboxEvent{
			Topic:      eventing.TopicInstanceCompleted,
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

// levelCountHandler is a slog.Handler that counts records per level, for asserting
// the level a message was logged at.
type levelCountHandler struct {
	mu     sync.Mutex
	counts map[slog.Level]int
}

func newLevelCountHandler() *levelCountHandler {
	return &levelCountHandler{counts: make(map[slog.Level]int)}
}

func (h *levelCountHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelCountHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.counts[r.Level]++
	return nil
}
func (h *levelCountHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelCountHandler) WithGroup(string) slog.Handler      { return h }
func (h *levelCountHandler) count(l slog.Level) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[l]
}

// TestChainerRunLogsBenignShutdownAtDebug asserts that when the chain handler fails
// with ErrDriverShuttingDown (driver draining), Run still nacks for redelivery but
// logs the benign case at DEBUG, not ERROR.
func TestChainerRunLogsBenignShutdownAtDebug(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var mu sync.Mutex
	var seen []chain.ChainEvent
	// Every successor start fails with a benign driver-shutdown error → nack + redeliver.
	starter := &capturingStarter{err: kernel.ErrDriverShuttingDown}
	core := chainCore(t, starter, &seen, &mu)

	rec := newLevelCountHandler()
	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer func() { require.NoError(t, closer.Close()) }()

	cr := eventing.NewChainerRunner(core, eventing.WithLogger(slog.New(rec)))
	done := make(chan error, 1)
	go func() { done <- cr.Run(ctx, sub) }()

	// The bus drops messages published before Run subscribes; republish until the
	// benign-shutdown DEBUG record appears (proving the handler ran and nacked).
	require.Eventually(t, func() bool {
		_ = pub.Publish(ctx, kernel.OutboxEvent{
			Topic:      eventing.TopicInstanceCompleted,
			Payload:    map[string]any{},
			InstanceID: "p1",
		})
		return rec.count(slog.LevelDebug) > 0
	}, 3*time.Second, 25*time.Millisecond)

	assert.Zero(t, rec.count(slog.LevelError),
		"a benign driver-shutdown nack must NOT be logged at ERROR")
	require.NotEmpty(t, starter.startedIDs(), "the handler must have attempted the successor start (then nacked)")

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}

// blockingSubscriber counts how many Subscribe calls are live. It blocks on ctx
// for every topic except failTopic, where it returns err immediately — the
// mid-flight failure P4 is about.
type blockingSubscriber struct {
	failTopic string
	err       error

	mu   sync.Mutex
	live int
	seen []string
}

func (b *blockingSubscriber) Subscribe(ctx context.Context, topic string, _ eventing.Handler) error {
	b.mu.Lock()
	b.seen = append(b.seen, topic)
	if topic == b.failTopic {
		b.mu.Unlock()
		return b.err
	}
	b.live++
	b.mu.Unlock()

	<-ctx.Done()

	b.mu.Lock()
	b.live--
	b.mu.Unlock()
	return ctx.Err()
}

func (b *blockingSubscriber) liveCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.live
}

func (b *blockingSubscriber) topics() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.seen...)
}

// TestChainerRunLeaksNoGoroutines is the leak half of the invariant the old
// "subscribe every topic before starting any goroutine" comment protected: with
// Subscribe now blocking, three subscriptions all start and all stop, and one
// failing mid-flight strands none of the others.
//
// Deliberately NOT parallel — goleak reads the whole process's goroutine set.
func TestChainerRunLeaksNoGoroutines(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, ignore)

	policy := func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{}, false
	}
	core, err := chain.NewChainer(&capturingStarter{}, policy)
	require.NoError(t, err)

	t.Run("an orderly cancellation stops all three", func(t *testing.T) {
		sub := &blockingSubscriber{}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- eventing.NewChainerRunner(core).Run(ctx, sub) }()

		require.Eventually(t, func() bool { return sub.liveCount() == 3 },
			3*time.Second, 5*time.Millisecond, "all three terminal topics must be subscribed")

		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
		assert.Zero(t, sub.liveCount(), "every subscription must have returned")
	})

	t.Run("one subscription failing strands none of the others", func(t *testing.T) {
		sentinel := errors.New("broker refused the topic")
		sub := &blockingSubscriber{failTopic: eventing.TopicInstanceTerminated, err: sentinel}

		done := make(chan error, 1)
		go func() { done <- eventing.NewChainerRunner(core).Run(t.Context(), sub) }()

		var err error
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			// Bounded so a Run that never cancels its siblings fails HERE, naming
			// the invariant, instead of deadlocking into "panic: test timed out"
			// with no assertion message at all.
			t.Fatal("Run did not return after one subscription failed; its siblings were never cancelled")
		}

		require.ErrorIs(t, err, sentinel)
		assert.ElementsMatch(t, chainTopicsForTest, sub.topics(),
			"every topic must have been attempted before Run returned")
		assert.Zero(t, sub.liveCount(),
			"the two healthy subscriptions must have been cancelled and joined, not stranded")
	})
}

// chainTopicsForTest names the three terminal topics Run must subscribe.
var chainTopicsForTest = []string{
	eventing.TopicInstanceCompleted,
	eventing.TopicInstanceFailed,
	eventing.TopicInstanceTerminated,
}
