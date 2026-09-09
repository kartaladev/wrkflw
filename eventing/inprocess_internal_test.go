package eventing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveSubscriptions and registeredTopics read the subscription registry the way
// only an INTERNAL test can. That is why this test is here and not beside the
// rest of the InProcess tests in inprocess_test.go, which is the external
// eventing_test package.
//
// MEASURED, and the reason this file exists at all: leaving a subscription in
// b.subs after its delivery loop has ended is INVISIBLE through the exported
// API. A stale entry is inert — its loop has returned, so it is never reached;
// fanout snapshots the slice and pushes to each subscription's own unbounded
// queue (inprocess.go:178, :124-132), so a stale entry cannot steal, delay,
// reorder or duplicate a live subscription's delivery; Publish returns nil
// regardless of how many subscriptions a topic has (:184); Close returns nil
// unconditionally and a second cancel of an already-cancelled context is a
// no-op (:276, :286); and the topic key is only ever read as b.subs[topic]
// (:178, :303, :310), never with comma-ok, so a deleted key and an empty slice
// are indistinguishable. The leak is real but it is a MEMORY leak with no
// exported reporter.
//
// The observable proxy an earlier draft proposed — end a subscription, publish,
// assert the dead handler is not reached and that a fresh subscription on the
// same topic gets exactly one copy — was written out in full and measured
// against both mutations. Both SURVIVED it: fanout pushes to every registered
// subscription independently, so the stale queue takes its own copy and the
// fresh subscription still receives exactly one. Shipping that proxy would have
// been a test that cannot fail for its stated reason.
func liveSubscriptions(b *InProcess, topic string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[topic])
}

func registeredTopics(b *InProcess) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// TestInProcessUnregistersOnEveryExitPath pins the deferred unregister on BOTH
// of the two paths a subscription can leave by — Subscribe returning on the
// caller's goroutine (inprocess.go:196) and Start's goroutine finishing
// (:225). Each is its own defer in its own function, so a test covering one
// says nothing about the other; the two rows are what make that explicit.
//
// The registry is the bus's only unbounded per-subscription structure. A leaked
// entry keeps its queue alive and fanout keeps appending to it forever, which is
// exactly the unbounded growth the InProcess doc warns about for a poison
// envelope (:68-74) — except with no handler left to blame.
func TestInProcessUnregistersOnEveryExitPath(t *testing.T) {
	t.Parallel()

	type testCase struct {
		// start registers a subscription on topic and returns only once it is
		// LIVE, along with an end func that ends it and returns only once the
		// delivery loop has finished. The lifecycle variation lives here rather
		// than in a ctx modifier because the teardown each row exercises is a
		// different API call, not a different context.
		start func(t *testing.T, b *InProcess, topic string, h Handler) (end func())
	}

	cases := map[string]testCase{
		"Subscribe returning on its context's cancellation": {
			start: func(t *testing.T, b *InProcess, topic string, h Handler) func() {
				ctx, cancel := context.WithCancel(t.Context())
				errCh := make(chan error, 1)
				go func() { errCh <- b.Subscribe(ctx, topic, h) }()

				require.Eventually(t, func() bool { return liveSubscriptions(b, topic) == 1 },
					3*time.Second, 5*time.Millisecond,
					"Subscribe must register its subscription")

				return func() {
					cancel()
					// A timeout, not a negative window: the deadline clause fails
					// the test and is paid only on failure, so it is generous
					// (docs/agents/test-deadlines.md).
					select {
					case err := <-errCh:
						require.ErrorIs(t, err, context.Canceled,
							"Subscribe returns its context's error on cancellation")
					case <-time.After(3 * time.Second):
						t.Fatal("Subscribe did not return after its context was cancelled")
					}
				}
			},
		},
		"Start's stop function joining the delivery loop": {
			start: func(t *testing.T, b *InProcess, topic string, h Handler) func() {
				stop, err := b.Start(t.Context(), topic, h)
				require.NoError(t, err)
				require.Equal(t, 1, liveSubscriptions(b, topic),
					"Start must return only once the subscription is live")
				return stop
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := NewInProcess()
			t.Cleanup(func() { require.NoError(t, b.Close()) })

			const topic = TopicInstanceCompleted
			end := tc.start(t, b, topic, func(context.Context, Envelope) error { return nil })
			end()

			assert.Zero(t, liveSubscriptions(b, topic),
				"a subscription whose delivery loop has ended must be unregistered, "+
					"or fanout keeps pushing onto a queue nobody drains")
			assert.Zero(t, registeredTopics(b),
				"the topic key must be deleted once its last subscription goes, "+
					"not left mapped to an empty slice")
		})
	}
}
