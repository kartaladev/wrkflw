package eventing_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/goleak"

	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// collector records the envelopes a Handler was given, safely across the
// subscription goroutines the bus delivers on.
type collector struct {
	mu   sync.Mutex
	envs []eventing.Envelope
}

func (c *collector) handle(_ context.Context, env eventing.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.envs = append(c.envs, env)
	return nil
}

func (c *collector) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.envs)
}

func (c *collector) snapshot() []eventing.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]eventing.Envelope(nil), c.envs...)
}

// TestInProcessFansOutToEverySubscriber asserts the bus is pub/SUB, not a queue:
// two subscriptions on one topic each receive every envelope, rather than
// competing for them. That is the property NewChainerRunner depends on when a
// consumer runs it alongside their own subscription on the same terminal topic.
func TestInProcessFansOutToEverySubscriber(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess()
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	first, second := &collector{}, &collector{}
	stopFirst, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted, first.handle)
	require.NoError(t, err)
	defer stopFirst()
	stopSecond, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted, second.handle)
	require.NoError(t, err)
	defer stopSecond()

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic:      eventing.TopicInstanceCompleted,
		InstanceID: "p1",
		DedupKey:   "p1:1:0",
		Payload:    map[string]any{"orderID": "o-9"},
	}))

	require.Eventually(t, func() bool {
		return first.len() == 1 && second.len() == 1
	}, 3*time.Second, 10*time.Millisecond,
		"both subscriptions on one topic must receive the envelope")

	for _, got := range [][]eventing.Envelope{first.snapshot(), second.snapshot()} {
		assert.Equal(t, "p1:1:0", got[0].ID)
		assert.Equal(t, eventing.TopicInstanceCompleted, got[0].Topic)
		assert.Equal(t, "p1", got[0].Metadata[eventing.MetaInstanceID])
		assert.JSONEq(t, `{"orderID":"o-9"}`, string(got[0].Body))
	}
}

// TestInProcessDeliversOnlyToTheMatchingTopic asserts an envelope never reaches a
// subscription on a different topic.
func TestInProcessDeliversOnlyToTheMatchingTopic(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess()
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	completed, failed := &collector{}, &collector{}
	stopCompleted, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted, completed.handle)
	require.NoError(t, err)
	defer stopCompleted()
	stopFailed, err := bus.Start(t.Context(), eventing.TopicInstanceFailed, failed.handle)
	require.NoError(t, err)
	defer stopFailed()

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
	}))

	require.Eventually(t, func() bool { return completed.len() == 1 },
		3*time.Second, 10*time.Millisecond)
	assert.Zero(t, failed.len(), "a subscription on another topic must receive nothing")
}

// TestInProcessRedeliversNackedEnvelopesAfterBackoff asserts at-least-once
// delivery: a handler that returns an error is handed THE SAME envelope again
// after the redelivery backoff, until it acks — and not once more after that.
func TestInProcessRedeliversNackedEnvelopesAfterBackoff(t *testing.T) {
	t.Parallel()

	const backoff = 20 * time.Millisecond

	bus := eventing.NewInProcess(eventing.WithRedeliveryBackoff(backoff))
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	var (
		mu       sync.Mutex
		attempts []time.Time
		ids      []string
	)
	record := func(id string) int {
		mu.Lock()
		defer mu.Unlock()
		attempts = append(attempts, time.Now())
		ids = append(ids, id)
		return len(attempts)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(attempts)
	}

	stop, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted,
		func(_ context.Context, env eventing.Envelope) error {
			if record(env.ID) < 3 {
				return errors.New("transient handler failure")
			}
			return nil
		})
	require.NoError(t, err)
	defer stop()

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic:      eventing.TopicInstanceCompleted,
		InstanceID: "p1",
		DedupKey:   "p1:1:0",
		Payload:    map[string]any{},
	}))

	require.Eventually(t, func() bool { return count() >= 3 }, 3*time.Second, 5*time.Millisecond,
		"a nacked envelope must be redelivered until the handler acks")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, attempts, 3, "exactly two nacks then one ack — no delivery after the ack")
	assert.Equal(t, []string{"p1:1:0", "p1:1:0", "p1:1:0"}, ids,
		"redelivery must hand back the same envelope, not a new one")
	assert.GreaterOrEqual(t, attempts[1].Sub(attempts[0]), backoff,
		"the first redelivery must wait for the backoff")
	assert.GreaterOrEqual(t, attempts[2].Sub(attempts[1]), backoff,
		"every redelivery must wait for the backoff, not just the first")
}

// TestInProcessCloseEndsEverySubscription asserts Close is a real shutdown: every
// blocking Subscribe returns, not just the first, and not only the ones that
// happen to be idle.
func TestInProcessCloseEndsEverySubscription(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess()

	const subscriptions = 3
	live := make([]*collector, subscriptions)
	done := make(chan error, subscriptions)
	for i := range live {
		live[i] = &collector{}
		go func(c *collector) {
			done <- bus.Subscribe(t.Context(), eventing.TopicInstanceCompleted, c.handle)
		}(live[i])
	}

	// Republish until every subscription has seen a probe: that is what proves all
	// three are registered, which is the precondition the assertion below reads.
	// The bus drops publishes that land before a Subscribe registers.
	require.Eventually(t, func() bool {
		// Not require.NoError: a testify condition runs on its own goroutine, and
		// t.FailNow from there is undefined behaviour per the testing docs. A
		// publish error surfaces as this wait timing out with its message.
		_ = bus.Publish(t.Context(), kernel.OutboxEvent{
			Topic: eventing.TopicInstanceCompleted, InstanceID: "probe", Payload: map[string]any{},
		})
		for _, c := range live {
			if c.len() == 0 {
				return false
			}
		}
		return true
	}, 3*time.Second, 10*time.Millisecond, "all subscriptions must go live")

	// Then wait for every subscription to go IDLE, not merely live. Delivery is
	// FIFO per subscription, so a sentinel published after the probe loop is seen
	// only once every earlier probe has been consumed — at which point each
	// subscription is parked on an empty queue.
	//
	// This is the difference between the two ways a loop can notice Close, and
	// only one of them is the interesting one. A subscription with a backlog
	// notices between envelopes; a parked one has to be woken. Closing while
	// backlogged tests the first and says nothing about the second — measured:
	// deleting the Close case from the parked select leaves this test green
	// unless it waits for idleness first.
	// Then wait for every subscription to go IDLE, not merely live — and assert
	// that WITHOUT depending on delivery order. Publishing has stopped, so each
	// queue is finite and draining; two consecutive samples that agree, a tick
	// apart, mean the loops are parked on an empty queue whatever order they
	// drained in.
	//
	// This is the difference between the two ways a loop can notice Close, and
	// only one of them is the interesting one. A subscription with a backlog
	// notices between envelopes; a parked one has to be woken. Closing while
	// backlogged tests the first and says nothing about the second.
	//
	// An earlier version proved idleness with a sentinel envelope asserted to
	// arrive last, which silently assumed FIFO: under LIFO the sentinel arrives
	// FIRST, the wait passes with a backlog still queued, and the test reverts to
	// the weak form it was rewritten to escape. Measured — a LIFO mutation left
	// that version green. Sampling for quiescence has no such assumption.
	prev := make([]int, len(live))
	for i, c := range live {
		prev[i] = c.len()
	}
	require.Eventually(t, func() bool {
		stable := true
		for i, c := range live {
			n := c.len()
			if n != prev[i] {
				stable = false
			}
			prev[i] = n
		}
		return stable
	}, 3*time.Second, 50*time.Millisecond,
		"all subscriptions must drain to idle once publishing stops")

	// MEASURED LIMIT OF THIS TEST, so nobody over-reads a green run: it pins the
	// CONTRACT (every Subscribe returns) deterministically — 6/6 green on correct
	// code — but it does NOT reliably pin WHICH exit the loop takes. Deleting the
	// Close case from the parked select above kills it only 6 times in 12,
	// because a loop still working through a backlog leaves via the
	// between-envelopes check instead and that is a legitimate exit too.
	// Forcing the parked path needs to observe the park, which no exported API
	// allows; that gap is tracked separately.
	// TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff is the
	// deterministic parked-park counterpart — a 30s backoff cannot be raced.

	require.NoError(t, bus.Close())

	for range subscriptions {
		select {
		case err := <-done:
			assert.NoError(t, err, "Close is an orderly stop, not a subscription failure")
		case <-time.After(2 * time.Second):
			t.Fatal("a subscription did not return after Close")
		}
	}

	assert.ErrorIs(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, Payload: map[string]any{},
	}), eventing.ErrBusClosed, "a closed bus must refuse publishes rather than drop them silently")

	assert.NoError(t, bus.Close(), "Close must be idempotent")
}

// TestInProcessStopJoinsItsDeliveryLoop is the precise half of the leak
// invariant: Start's stop function must not return until the delivery loop it
// started has finished, including a handler still in flight.
//
// goleak alone does not establish this. goleak retries for a few hundred
// milliseconds, so a loop that exits promptly after an unjoined cancel is
// absorbed — measured: deleting the join from stop leaves the goleak test below
// green. The wait here is what fails on that mutation, immediately.
func TestInProcessStopJoinsItsDeliveryLoop(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess()
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	entered := make(chan struct{})
	release := make(chan struct{})
	stop, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted,
		func(context.Context, eventing.Envelope) error {
			close(entered)
			<-release // hold the delivery loop inside the handler
			return nil
		})
	require.NoError(t, err)

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
	}))
	<-entered

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()

	// A negative window: stop MUST still be blocked while the handler runs. Paid
	// in full on every green run, so it stays short (see docs/agents/test-deadlines.md).
	select {
	case <-stopped:
		t.Fatal("stop returned while the delivery loop was still inside the handler")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return after the delivery loop finished")
	}
}

// TestInProcessLeaksNoGoroutines asserts the bus owns every goroutine it starts.
// Deliberately NOT parallel: goleak reads the whole process's goroutine set, and
// Go runs the sequential tests of a package before it releases the parallel ones,
// so this sees only what this test created.
func TestInProcessLeaksNoGoroutines(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, ignore)

	bus := eventing.NewInProcess()

	// A Start whose stop function is called must leave nothing behind …
	first := &collector{}
	stop, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted, first.handle)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
	}))
	require.Eventually(t, func() bool { return first.len() == 1 }, 3*time.Second, 10*time.Millisecond)
	stop()

	// … and so must one left running when the bus is closed instead.
	second := &collector{}
	stopSecond, err := bus.Start(t.Context(), eventing.TopicInstanceFailed, second.handle)
	require.NoError(t, err)
	require.NoError(t, bus.Close())
	stopSecond()
}

// publisherOnlyKey marks a value put in the PUBLISHER's context. A handler must
// never see it: that is what "the trace context travels in the envelope" means
// concretely, as opposed to a Go context being smuggled across the delivery.
type publisherOnlyKey struct{}

// TestPublishSpanParentsHandlerSpan is the end-to-end trace requirement: a span
// a handler starts is a CHILD of the eventing.publish span, and it gets there
// through Envelope.Metadata rather than through a shared context.
//
// The distinction is the whole point. The library this replaced carried the
// publisher's live context OBJECT to the handler, which works only while
// publisher and consumer share a process — put a real broker in the middle and
// the context is gone, because a broker hands a consumer bytes and headers. So
// the test asserts both halves: the handler's span parents onto the publish
// span, AND a value placed in the publisher's context does NOT reach the
// handler. Deleting the propagator Extract from InProcess.dispatch turns the
// first half red.
func TestPublishSpanParentsHandlerSpan(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	bus := eventing.NewInProcess(eventing.WithTracerProvider(tp))
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	tracer := tp.Tracer("consumer")
	sawPublisherValue := make(chan bool, 1)
	handled := make(chan struct{})

	stop, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted,
		func(hctx context.Context, _ eventing.Envelope) error {
			sawPublisherValue <- hctx.Value(publisherOnlyKey{}) != nil
			_, span := tracer.Start(hctx, "consumer.handle")
			span.End()
			close(handled)
			return nil
		})
	require.NoError(t, err)
	defer stop()

	publishCtx := context.WithValue(t.Context(), publisherOnlyKey{}, "publisher-only")
	require.NoError(t, bus.Publish(publishCtx, kernel.OutboxEvent{
		Topic:      eventing.TopicInstanceCompleted,
		InstanceID: "p1",
		Payload:    map[string]any{},
	}))

	<-handled
	assert.False(t, <-sawPublisherValue,
		"the handler must not inherit the publisher's context; a broker would never carry it")

	require.NoError(t, tp.ForceFlush(t.Context()))
	spans := tracetest.SpanStubsFromReadOnlySpans(sr.Ended())

	var publish, handle tracetest.SpanStub
	for _, s := range spans {
		switch s.Name {
		case "eventing.publish":
			publish = s
		case "consumer.handle":
			handle = s
		}
	}
	require.NotEmpty(t, publish.Name, "the publish span must have been recorded")
	require.NotEmpty(t, handle.Name, "the handler span must have been recorded")

	assert.Equal(t, publish.SpanContext.TraceID(), handle.SpanContext.TraceID(),
		"the handler span must be in the publish span's trace")
	assert.Equal(t, publish.SpanContext.SpanID(), handle.Parent.SpanID(),
		"the handler span's parent must be the publish span itself")
}

// TestInProcessGivesEachSubscriptionItsOwnEnvelope asserts the isolation the
// InProcess doc promises: two subscriptions on one topic each get their OWN copy
// of the metadata map AND the body bytes, so neither can observe the other's
// mutations.
//
// A real broker gives this for free — each consumer reads its own bytes off the
// wire — so a bus that shared them would let a handler pass here and misbehave
// the moment a Kafka client replaced it, which is precisely the difference the
// Envelope seam exists to erase. The gate makes the test deterministic rather
// than delivery-order dependent: the second handler reads only after the first
// has mutated.
func TestInProcessGivesEachSubscriptionItsOwnEnvelope(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess()
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	mutated := make(chan struct{})
	stopFirst, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted,
		func(_ context.Context, env eventing.Envelope) error {
			env.Body[0] = 'X'
			env.Metadata[eventing.MetaInstanceID] = "clobbered-by-the-first-handler"
			close(mutated)
			return nil
		})
	require.NoError(t, err)
	defer stopFirst()

	second := make(chan eventing.Envelope, 1)
	stopSecond, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted,
		func(_ context.Context, env eventing.Envelope) error {
			<-mutated // read only after the first handler has scribbled on its copy
			second <- env
			return nil
		})
	require.NoError(t, err)
	defer stopSecond()

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic:      eventing.TopicInstanceCompleted,
		InstanceID: "p1",
		Payload:    map[string]any{"k": "v"},
	}))

	got := <-second
	assert.Equal(t, byte('{'), got.Body[0],
		"the body must be cloned per subscription, not shared")
	assert.JSONEq(t, `{"k":"v"}`, string(got.Body))
	assert.Equal(t, "p1", got.Metadata[eventing.MetaInstanceID],
		"the metadata map must be cloned per subscription, not shared")
}

// TestInProcessCloseCancelsTheContextsItCreated asserts Close honours its own
// doc — "ends every live subscription" — for the context registration, not just
// the goroutine.
//
// Before this, cancel was reachable only from inside stop's sync.Once, so a bus
// Closed without calling stop left the derived context uncancelled and still
// attached to its parent: the loop exited, but the registration lived until the
// PARENT was cancelled. go vet's lostcancel structurally cannot see it, because
// cancel escapes into a closure.
func TestInProcessCloseCancelsTheContextsItCreated(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess()

	captured := make(chan context.Context, 1)
	stop, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted,
		func(hctx context.Context, _ eventing.Envelope) error {
			captured <- hctx
			return nil
		})
	require.NoError(t, err)
	defer stop()

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
	}))

	handlerCtx := <-captured
	require.NoError(t, handlerCtx.Err(), "the handler's context must be live during delivery")

	require.NoError(t, bus.Close())

	require.Eventually(t, func() bool { return handlerCtx.Err() != nil },
		3*time.Second, 5*time.Millisecond,
		"Close must cancel the context Start derived, or its registration on the parent is stranded")
	assert.ErrorIs(t, handlerCtx.Err(), context.Canceled)
}

// TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff is the LIVENESS
// half of Close: a handler that nacks forever leaves its subscription asleep in
// the backoff, not in the queue wait, and Close has to reach it there too.
//
// The backoff below is deliberately far longer than the test's own patience, so
// a Close that failed to interrupt it could not be mistaken for a slow one.
func TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess(eventing.WithRedeliveryBackoff(30 * time.Second))

	var attempts atomic.Int64
	done := make(chan error, 1)
	go func() {
		done <- bus.Subscribe(t.Context(), eventing.TopicInstanceCompleted,
			func(context.Context, eventing.Envelope) error {
				attempts.Add(1)
				return errors.New("this handler never acks")
			})
	}()

	require.Eventually(t, func() bool {
		_ = bus.Publish(t.Context(), kernel.OutboxEvent{
			Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
		})
		return attempts.Load() > 0
	}, 3*time.Second, 10*time.Millisecond, "the handler must have nacked at least once")

	require.NoError(t, bus.Close())

	select {
	case err := <-done:
		assert.NoError(t, err, "Close is an orderly stop, not a subscription failure")
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not end a subscription parked in the redelivery backoff")
	}
}

// TestPublishToAClosedBusLogsAtDebugNotError is the outbound twin of
// TestChainerRunLogsBenignShutdownAtDebug. A relay draining while the bus closes
// is what a graceful shutdown looks like, so the refusal is still reported and
// still returned as an error — but at DEBUG, because an operator paging on
// ERROR should not be woken by a clean shutdown.
func TestPublishToAClosedBusLogsAtDebugNotError(t *testing.T) {
	t.Parallel()

	rec := newLevelCountHandler()
	bus := eventing.NewInProcess(eventing.WithLogger(slog.New(rec)))
	require.NoError(t, bus.Close())

	err := bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "p1", Payload: map[string]any{},
	})

	require.ErrorIs(t, err, eventing.ErrBusClosed,
		"a closed bus must refuse rather than silently drop, so the relay leaves the row pending")
	assert.Positive(t, rec.count(slog.LevelDebug), "the refusal must still be recorded, at DEBUG")
	assert.Zero(t, rec.count(slog.LevelError),
		"a publish refused by a closing bus must NOT be logged at ERROR")
}

// TestInProcessGivesEachDeliveryAttemptItsOwnEnvelope is the REDELIVERY half of
// isolation, and the half a per-subscriber clone does not cover.
//
// dispatch holds one Envelope and loops over it, re-extracting the handler
// context from env.Metadata on every attempt. So a handler that writes to the
// envelope it was handed corrupts its OWN next attempt — including the trace
// context that attempt is built from — even when each subscriber already has a
// private copy. watermill's gochannel cloned inside its retry loop for exactly
// this reason, naming retries in the comment; per delivery ATTEMPT is where the
// copy belongs.
func TestInProcessGivesEachDeliveryAttemptItsOwnEnvelope(t *testing.T) {
	t.Parallel()

	bus := eventing.NewInProcess(eventing.WithRedeliveryBackoff(5 * time.Millisecond))
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	type received struct {
		body       string
		instanceID string
	}
	var (
		mu       sync.Mutex
		attempts []received
	)

	stop, err := bus.Start(t.Context(), eventing.TopicInstanceCompleted,
		func(_ context.Context, env eventing.Envelope) error {
			mu.Lock()
			attempts = append(attempts, received{string(env.Body), env.Metadata[eventing.MetaInstanceID]})
			n := len(attempts)
			mu.Unlock()

			// Scribble on the envelope we were handed, exactly as a handler doing
			// an in-place decode or a bytes.Replace would.
			env.Body[0] = 'X'
			env.Metadata[eventing.MetaInstanceID] = "clobbered-on-the-first-attempt"

			if n == 1 {
				return errors.New("nack once, so the same envelope comes back")
			}
			return nil
		})
	require.NoError(t, err)
	defer stop()

	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic:      eventing.TopicInstanceCompleted,
		InstanceID: "p1",
		Payload:    map[string]any{"k": "v"},
	}))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(attempts) >= 2
	}, 3*time.Second, 5*time.Millisecond, "the nacked envelope must be redelivered")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, attempts, 2, "exactly one nack then one ack")
	assert.Equal(t, `{"k":"v"}`, attempts[0].body)
	assert.Equal(t, "p1", attempts[0].instanceID)
	assert.Equal(t, `{"k":"v"}`, attempts[1].body,
		"a redelivered envelope must be a fresh copy, not the one the last attempt scribbled on")
	assert.Equal(t, "p1", attempts[1].instanceID,
		"metadata too: the handler context of attempt 2 is extracted from this map")
}
