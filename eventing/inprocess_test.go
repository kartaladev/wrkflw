package eventing_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
			Topic: eventing.TopicInstanceCompleted, InstanceID: "probe", Payload: map[string]any{},
		}))
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
	const sentinelID = "sentinel:1:0"
	require.NoError(t, bus.Publish(t.Context(), kernel.OutboxEvent{
		Topic: eventing.TopicInstanceCompleted, InstanceID: "sentinel",
		DedupKey: sentinelID, Payload: map[string]any{},
	}))
	require.Eventually(t, func() bool {
		for _, c := range live {
			envs := c.snapshot()
			if len(envs) == 0 || envs[len(envs)-1].ID != sentinelID {
				return false
			}
		}
		return true
	}, 3*time.Second, 10*time.Millisecond, "all subscriptions must drain to idle")

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
