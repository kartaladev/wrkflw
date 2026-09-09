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

	bus := eventing.NewInProcess()
	defer func() { require.NoError(t, bus.Close()) }()

	cr := eventing.NewChainerRunner(core)
	done := make(chan error, 1)
	go func() { done <- cr.Run(ctx, bus) }()

	// The bus is non-persistent: publishing before Run subscribes drops the
	// message. Republish on each tick until the (idempotent) chaining lands.
	require.Eventually(t, func() bool {
		_ = bus.Publish(ctx, kernel.OutboxEvent{
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
	bus := eventing.NewInProcess()
	defer func() { require.NoError(t, bus.Close()) }()

	cr := eventing.NewChainerRunner(core, eventing.WithLogger(slog.New(rec)))
	done := make(chan error, 1)
	go func() { done <- cr.Run(ctx, bus) }()

	// The bus drops messages published before Run subscribes; republish until the
	// benign-shutdown DEBUG record appears (proving the handler ran and nacked).
	require.Eventually(t, func() bool {
		_ = bus.Publish(ctx, kernel.OutboxEvent{
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

// gatedStarter wraps a Starter and holds every registration until release is
// closed. It is the whole of the readiness probe: with it, a caller that only
// *thinks* it has subscribed is separated from one that has, because the
// registration provably has not happened until the test allows it.
//
// It deliberately implements Start only. A Subscriber-shaped gate cannot express
// the same thing: Subscribe blocks and registers internally, so there is no
// moment at which the caller can be said to have finished registering.
type gatedStarter struct {
	inner   eventing.Starter
	release chan struct{}
}

func (g *gatedStarter) Start(ctx context.Context, topic string, h eventing.Handler) (func(), error) {
	<-g.release
	return g.inner.Start(ctx, topic, h)
}

// gatedSubscriber is the same gate over the blocking Subscriber shape, for the
// Run half of the comparison.
type gatedSubscriber struct {
	inner   eventing.Subscriber
	release chan struct{}
}

func (g *gatedSubscriber) Subscribe(ctx context.Context, topic string, h eventing.Handler) error {
	<-g.release
	return g.inner.Subscribe(ctx, topic, h)
}

// TestChainerStartIsReadyBeforeItReturns is THE RED for #134, and it is a
// genuine one: it fails against Run and passes against Start.
//
// The turnkey chaining path is driven by persistence.Relay, which publishes each
// outbox row EXACTLY ONCE and marks it published on a nil return. So the
// republish-until-it-lands loop that every other test in this file uses is not
// available to a real deployment — it is a test affordance papering over a
// missing readiness edge. This test therefore publishes ONCE, with no loop, and
// asserts the successor starts anyway.
//
// The gate is what makes it deterministic rather than a race that usually wins:
// registration cannot happen until the test closes release, and the test closes
// it only AFTER publishing. Against Run the envelope is provably already gone;
// against Start the publish cannot even be reached until all three topics are
// live.
//
// MEASURED BLIND SPOT, so nobody reads this test as covering more than it does:
// it publishes to chainTopicOrder[0], so a Start that brought up only the FIRST
// topic and returned would pass it. That mutation was run, and it is
// TestChainerStartStopIsIdempotentAndJoins and the partial-failure rows that
// catch it, by asserting over which topics the Starter actually saw. "All three
// are live" is asserted there, not here; this test asserts only that whatever
// Start brought up was live before it returned.
func TestChainerStartIsReadyBeforeItReturns(t *testing.T) {
	t.Parallel()

	newStack := func(t *testing.T) (*eventing.Chainer, *eventing.InProcess, kernel.InstanceStore) {
		t.Helper()
		clk := clockwork.NewFakeClock()
		store, err := kernel.NewMemInstanceStore()
		require.NoError(t, err)
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
		core, err := chain.NewChainer(driver, policy,
			chain.WithChainLinks(kernel.NewMemChainLinkStore()), chain.WithClock(clk))
		require.NoError(t, err)
		bus := eventing.NewInProcess()
		t.Cleanup(func() { require.NoError(t, bus.Close()) })
		return eventing.NewChainerRunner(core), bus, store
	}

	// publishOnce is the relay's shape: one publish, no retry, nil means done.
	publishOnce := func(t *testing.T, bus *eventing.InProcess, id string) {
		t.Helper()
		require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
			Topic: eventing.TopicInstanceCompleted, InstanceID: id,
			Payload: map[string]any{"orderID": "o-9"},
		}))
	}

	t.Run("Start returns only once every topic is live, so a single publish lands", func(t *testing.T) {
		t.Parallel()

		cr, bus, store := newStack(t)
		gate := &gatedStarter{inner: bus, release: make(chan struct{})}
		close(gate.release) // Start must do its own sequencing; the gate is open

		stop, err := cr.Start(t.Context(), gate)
		require.NoError(t, err)
		t.Cleanup(stop)

		publishOnce(t, bus, "p1")

		require.Eventually(t, func() bool {
			_, _, err := store.Load(t.Context(), "p1-next-completed")
			return err == nil
		}, 3*time.Second, 5*time.Millisecond,
			"a single relay-style publish after Start must reach the chainer; "+
				"Start returning means every terminal topic is live")
	})

	t.Run("Run has no readiness edge, so the same single publish is lost", func(t *testing.T) {
		t.Parallel()

		cr, bus, store := newStack(t)
		gate := &gatedSubscriber{inner: bus, release: make(chan struct{})}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- cr.Run(ctx, gate) }()

		// The gate proves the registration has NOT happened. This is what makes
		// the loss deterministic rather than a race the test usually wins.
		publishOnce(t, bus, "p2")
		close(gate.release)

		// A negative window: the envelope was dropped at fanout, so no amount of
		// waiting produces the successor. Paid on every green run, so it is short
		// (see docs/agents/test-deadlines.md).
		require.Never(t, func() bool {
			_, _, err := store.Load(t.Context(), "p2-next-completed")
			return err == nil
		}, 300*time.Millisecond, 25*time.Millisecond,
			"CHARACTERISATION of the defect #134 names: Run offers no edge to "+
				"sequence a publish against, so a relay-style single publish is lost")

		cancel()
		<-done
	})
}

// recordingStarter is a Starter double that records which topics were started
// and which of the returned stop functions were called, and can fail one topic.
type recordingStarter struct {
	failTopic string
	err       error

	mu      sync.Mutex
	started []string
	stopped []string
}

func (r *recordingStarter) Start(_ context.Context, topic string, _ eventing.Handler) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if topic == r.failTopic {
		return nil, r.err
	}
	r.started = append(r.started, topic)
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.stopped = append(r.stopped, topic)
	}, nil
}

func (r *recordingStarter) snapshot() (started, stopped []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.started...), append([]string(nil), r.stopped...)
}

// errStartRefused is the broker error every partial-failure row injects.
var errStartRefused = errors.New("broker refused the topic")

// requireStartFailed is the outcome assertion every partial-failure row shares:
// the caller learns which topic failed, and gets NO stop function — a non-nil
// stop beside an error is a handle to something that never attached.
func requireStartFailed(t *testing.T, stop func(), err error, failTopic string) {
	t.Helper()
	require.Error(t, err)
	assert.ErrorIs(t, err, errStartRefused, "the broker's error must reach the caller")
	assert.Contains(t, err.Error(), failTopic, "and it must name the topic that failed")
	assert.Nil(t, stop,
		"a failed Start must return no stop function, or a caller has something "+
			"to call that never attached")
}

// TestChainerStartStopIsIdempotentAndJoins is deliberately NOT folded into the
// table below, and this is the documented deviation
// (.claude/skills/table-test permits splitting where setup diverges): it
// exercises the SUCCESS path — Start returns, then stop is called twice —
// whereas every row below exercises a FAILED Start that returns no stop at all.
// There is no shared "call SUT, assert" shape to fold; the two differ in what
// they call after Start, not merely in inputs. Measured, and this is the reason
// that decided it: mutations S1 (only the first topic started), S3 (stop not
// idempotent) and S4 (stop does not join) are killed by THIS test, and S3 by
// this test ALONE — folding it in as a row risked losing a discriminator no
// other row carries.
//
// TestChainerStartStrandsNothingOnPartialFailure holds the invariant Start's doc
// promises and Run documents for itself: a failure on one subscription strands
// none of the others.
//
// Start establishes it BY CONSTRUCTION rather than by cancellation — it stops
// what it has already started before returning — so the failure mode it guards
// against is a leaked live subscription on a bus the caller believes it never
// successfully attached to, with no stop function to reach it by, since Start
// returned nil.
func TestChainerStartStrandsNothingOnPartialFailure(t *testing.T) {
	t.Parallel()

	policy := func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{}, false
	}
	core, err := chain.NewChainer(&capturingStarter{}, policy)
	require.NoError(t, err)

	type testCase struct {
		failTopic string
		// assert reads the outcome of the failed Start plus the topics the Starter
		// actually saw. Closure form, not want/wantStarted fields, per
		// .claude/skills/table-test: the rows differ in what they can say — the
		// first-topic row asserts an ABSENCE where the others assert a matched
		// pair — and a shared field could not express that difference.
		assert func(t *testing.T, stop func(), err error, started, stopped []string)
	}

	// requireUnwound is the assertion the last two rows share: whatever Start
	// brought up, it must have brought back down before returning the error.
	requireUnwound := func(t *testing.T, want []string, started, stopped []string) {
		t.Helper()
		assert.Equal(t, want, started,
			"Start must attempt the topics in chainTopicOrder and stop at the failure")
		assert.ElementsMatch(t, started, stopped,
			"every subscription Start brought up must be stopped again before it "+
				"returns the error — otherwise it is live with no way to reach it")
	}

	cases := map[string]testCase{
		"the first topic fails: nothing was started, nothing to strand": {
			failTopic: eventing.TopicInstanceCompleted,
			assert: func(t *testing.T, stop func(), err error, started, stopped []string) {
				requireStartFailed(t, stop, err, eventing.TopicInstanceCompleted)
				assert.Empty(t, started, "nothing may have been started")
				assert.Empty(t, stopped, "and so nothing may have needed stopping")
			},
		},
		"the middle topic fails: the one already live must be stopped": {
			failTopic: eventing.TopicInstanceFailed,
			assert: func(t *testing.T, stop func(), err error, started, stopped []string) {
				requireStartFailed(t, stop, err, eventing.TopicInstanceFailed)
				requireUnwound(t, []string{eventing.TopicInstanceCompleted}, started, stopped)
			},
		},
		"the last topic fails: both already live must be stopped": {
			failTopic: eventing.TopicInstanceTerminated,
			assert: func(t *testing.T, stop func(), err error, started, stopped []string) {
				requireStartFailed(t, stop, err, eventing.TopicInstanceTerminated)
				requireUnwound(t,
					[]string{eventing.TopicInstanceCompleted, eventing.TopicInstanceFailed},
					started, stopped)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sub := &recordingStarter{failTopic: tc.failTopic, err: errStartRefused}

			stop, err := eventing.NewChainerRunner(core).Start(t.Context(), sub)
			started, stopped := sub.snapshot()
			tc.assert(t, stop, err, started, stopped)
		})
	}
}

// TestChainerStartStopIsIdempotentAndJoins pins the rest of Start's stop
// contract: it ends every subscription, waits for the delivery loops, and is
// safe to call more than once.
func TestChainerStartStopIsIdempotentAndJoins(t *testing.T) {
	t.Parallel()

	policy := func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{}, false
	}
	core, err := chain.NewChainer(&capturingStarter{}, policy)
	require.NoError(t, err)

	sub := &recordingStarter{}
	stop, err := eventing.NewChainerRunner(core).Start(t.Context(), sub)
	require.NoError(t, err)

	started, stopped := sub.snapshot()
	assert.Equal(t, chainTopicsForTest, started, "all three terminal topics must be live")
	assert.Empty(t, stopped, "and none stopped while Start succeeded")

	stop()
	_, stopped = sub.snapshot()
	assert.ElementsMatch(t, chainTopicsForTest, stopped, "stop must end every subscription")

	assert.NotPanics(t, stop, "stop must be safe to call more than once")
	_, stoppedAgain := sub.snapshot()
	assert.Len(t, stoppedAgain, len(chainTopicsForTest),
		"a second stop must not stop anything twice")
}

// TestChainerStartStopJoinsTheDeliveryLoops is the precise half of Start's
// "a caller that defers it leaks nothing" promise, and it is the assertion the
// goleak test below structurally CANNOT make.
//
// MEASURED, and it is why both tests exist: goleak retries for a few hundred
// milliseconds, so a stop that returns WITHOUT joining is absorbed — the loops
// end on their own inside the retry window and goleak sees a clean process.
// Mutation S4 (`go stops[i]()`) survives the goleak test for exactly that
// reason. The negative window below is what fails on it, immediately: the
// chaining policy holds the delivery loop inside the handler, so a stop that
// returned early would return while a loop is provably still running.
//
// This mirrors TestInProcessStopJoinsItsDeliveryLoop, which owns the same
// property one layer down and records the same goleak limitation.
func TestChainerStartStopJoinsTheDeliveryLoops(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	policy := func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
		once.Do(func() { close(entered) })
		<-release // hold the delivery loop inside the handler
		return chain.SuccessorDecision{}, false
	}
	core, err := chain.NewChainer(&capturingStarter{}, policy)
	require.NoError(t, err)

	bus := eventing.NewInProcess()
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	stop, err := eventing.NewChainerRunner(core).Start(t.Context(), bus)
	require.NoError(t, err)

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
	}))
	<-entered // a delivery loop is now parked inside the handler

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()

	// A negative window: stop MUST still be blocked while that loop runs. Paid in
	// full on every green run, so it stays short (docs/agents/test-deadlines.md).
	select {
	case <-stopped:
		t.Fatal("stop returned while a chaining delivery loop was still inside the handler")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not return after the delivery loops finished")
	}
}

// TestChainerStartLeaksNoGoroutinesOverARealBus closes the one gap the round-1
// review named as its own weakest clear, and it is worth stating why the
// existing coverage did not reach it.
//
// Every other Start test drives a recordingStarter, whose stop functions have NO
// DELIVERY LOOP to join. Those tests therefore prove that Start CALLS its
// children's stops synchronously — they cannot see a failure to JOIN a real
// loop, because there is no loop. Start's doc promises "a caller that defers it
// leaks nothing", and over a real bus that promise is delegated entirely to
// InProcess.Start. This test is the end-to-end half.
//
// Deliberately NOT parallel: goleak reads the whole process's goroutine set, and
// Go runs a package's sequential tests before releasing its parallel ones, so
// this sees only what it created. It mirrors TestChainerRunLeaksNoGoroutines,
// which covers Run only.
func TestChainerStartLeaksNoGoroutinesOverARealBus(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, ignore)

	policy := func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{}, false
	}
	core, err := chain.NewChainer(&capturingStarter{}, policy)
	require.NoError(t, err)

	bus := eventing.NewInProcess()
	cr := eventing.NewChainerRunner(core)

	stop, err := cr.Start(t.Context(), bus)
	require.NoError(t, err)

	// Deliver something first, so the loops are demonstrably running rather than
	// parked having never started — a leak check over three loops that never
	// woke would be the weaker probe.
	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
	}))

	stop()

	// Close is registered as CLEANUP, not deferred, and the ordering is the whole
	// point: deferred functions run before t.Cleanup ones, so goleak's VerifyNone
	// (deferred first, therefore last of the defers) observes the process while
	// the bus is still OPEN — i.e. with nothing but stop() having ended the loops.
	//
	// Closing the bus inline here instead would make this test unable to fail:
	// Close ends every subscription itself, so a stop that never joined would be
	// covered for by Close and goleak would see a clean process. Measured — that
	// is exactly what the first version of this test did, and mutation S4 (stop
	// returns without joining) survived it.
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
}
