package signal_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// mustSignalBus builds a SignalBus or fails the test.
func mustSignalBus(t *testing.T, deliver signal.DeliverFunc, opts ...signal.SignalBusOption) *signal.SignalBus {
	t.Helper()
	bus, err := signal.NewSignalBus(deliver, opts...)
	require.NoError(t, err)
	return bus
}

// deliverRecord tracks what was delivered to which instance.
type deliverRecord struct {
	mu      sync.Mutex
	entries []deliverEntry
}

type deliverEntry struct {
	instanceID string
	trg        engine.Trigger
}

func (r *deliverRecord) deliver(_ context.Context, instanceID string, trg engine.Trigger) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, deliverEntry{instanceID: instanceID, trg: trg})
	return nil
}

func (r *deliverRecord) instanceIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, len(r.entries))
	for i, e := range r.entries {
		ids[i] = e.instanceID
	}
	return ids
}

// TestSignalBusPublishDeliversToAllSubscribers verifies that Publish fans out to
// all subscribed instances in deterministic (sorted) order.
func TestSignalBusPublishDeliversToAllSubscribers(t *testing.T) {
	ctx := context.Background()
	rec := &deliverRecord{}

	bus := mustSignalBus(t, rec.deliver, signal.WithSignalBusClock(clockwork.NewFakeClock()))
	bus.Subscribe("inst-b", "approved")
	bus.Subscribe("inst-a", "approved")
	bus.Subscribe("inst-c", "approved")

	err := bus.Publish(ctx, "approved", map[string]any{"decision": "yes"})
	require.NoError(t, err)

	ids := rec.instanceIDs()
	assert.Equal(t, []string{"inst-a", "inst-b", "inst-c"}, ids,
		"delivery order must be deterministic (sorted instance IDs)")

	// Verify the trigger type and payload.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, e := range rec.entries {
		sig, ok := e.trg.(engine.SignalReceived)
		require.True(t, ok, "trigger must be SignalReceived")
		assert.Equal(t, "approved", sig.Name)
		assert.Equal(t, "yes", sig.Payload["decision"])
	}
}

// TestSignalBusPublishNoWaitersIsNoop verifies that publishing to a signal with
// no subscribers is a clean no-op (no error, no deliveries).
func TestSignalBusPublishNoWaitersIsNoop(t *testing.T) {
	ctx := context.Background()
	rec := &deliverRecord{}
	bus := mustSignalBus(t, rec.deliver, signal.WithSignalBusClock(clockwork.NewFakeClock()))

	err := bus.Publish(ctx, "nonexistent", nil)
	require.NoError(t, err)
	assert.Empty(t, rec.instanceIDs())
}

// TestSignalBusUnsubscribeRemovesWaiter verifies that Unsubscribe removes an
// instance from the waiter set so it no longer receives the signal.
func TestSignalBusUnsubscribeRemovesWaiter(t *testing.T) {
	ctx := context.Background()
	rec := &deliverRecord{}
	bus := mustSignalBus(t, rec.deliver, signal.WithSignalBusClock(clockwork.NewFakeClock()))

	bus.Subscribe("inst-a", "approved")
	bus.Subscribe("inst-b", "approved")
	bus.Unsubscribe("inst-a", "approved")

	err := bus.Publish(ctx, "approved", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"inst-b"}, rec.instanceIDs())
}

// TestSignalBusSyncReconciles verifies Sync replaces the complete set of signal
// subscriptions for an instance (new signals added, old signals removed).
func TestSignalBusSyncReconciles(t *testing.T) {
	ctx := context.Background()
	rec := &deliverRecord{}
	bus := mustSignalBus(t, rec.deliver, signal.WithSignalBusClock(clockwork.NewFakeClock()))

	// Initial subscriptions.
	bus.Subscribe("inst-a", "sig-old")
	bus.Subscribe("inst-a", "sig-keep")

	// Sync: inst-a now waits on sig-keep and sig-new (no longer sig-old).
	bus.Sync("inst-a", []string{"sig-keep", "sig-new"})

	// Publishing sig-old must NOT reach inst-a.
	err := bus.Publish(ctx, "sig-old", nil)
	require.NoError(t, err)
	assert.Empty(t, rec.instanceIDs(), "inst-a must not receive sig-old after Sync")

	// Publishing sig-new MUST reach inst-a.
	err = bus.Publish(ctx, "sig-new", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"inst-a"}, rec.instanceIDs())
}

// TestSignalBusPublishDeliverErrorPropagates verifies that a delivery error
// from one waiter is returned from Publish.
func TestSignalBusPublishDeliverErrorPropagates(t *testing.T) {
	ctx := context.Background()
	errDeliver := errors.New("deliver: forced failure")

	bus := mustSignalBus(t, func(_ context.Context, _ string, _ engine.Trigger) error {
		return errDeliver
	}, signal.WithSignalBusClock(clockwork.NewFakeClock()))
	bus.Subscribe("inst-a", "approved")

	err := bus.Publish(ctx, "approved", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, errDeliver)
}

// TestSignalBusPublishBestEffortDeliversAll verifies that a delivery failure for
// one waiter does not block delivery to subsequent waiters (best-effort semantics).
// All waiters are attempted; errors are joined and returned at the end.
func TestSignalBusPublishBestEffortDeliversAll(t *testing.T) {
	ctx := context.Background()
	errFirst := errors.New("deliver: first instance failed")

	delivered := make(map[string]bool)
	bus := mustSignalBus(t, func(_ context.Context, instanceID string, _ engine.Trigger) error {
		if instanceID == "inst-a" {
			return errFirst
		}
		delivered[instanceID] = true
		return nil
	}, signal.WithSignalBusClock(clockwork.NewFakeClock()))
	bus.Subscribe("inst-a", "sig") // will fail
	bus.Subscribe("inst-b", "sig") // must still be delivered
	bus.Subscribe("inst-c", "sig") // must still be delivered

	err := bus.Publish(ctx, "sig", nil)
	require.Error(t, err, "joined error must be returned")
	assert.ErrorIs(t, err, errFirst, "original error must be unwrappable")
	assert.True(t, delivered["inst-b"], "inst-b must be delivered despite inst-a failure")
	assert.True(t, delivered["inst-c"], "inst-c must be delivered despite inst-a failure")
}

// TestSignalBusIsSafeForConcurrentUse verifies that Subscribe/Unsubscribe/Publish
// can be called concurrently without data races (run with -race).
func TestSignalBusIsSafeForConcurrentUse(t *testing.T) {
	ctx := context.Background()
	rec := &deliverRecord{}
	fc := clockwork.NewFakeClock()
	bus := mustSignalBus(t, rec.deliver, signal.WithSignalBusClock(fc))

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "inst-" + string(rune('a'+n%26))
			bus.Subscribe(id, "sig")
			_ = bus.Publish(ctx, "sig", nil)
			bus.Unsubscribe(id, "sig")
		}(i)
	}
	wg.Wait()
}

// TestSignalBusPublishStampsViaClock verifies that SignalReceived triggers
// produced by Publish use the injected clock.Clock timestamp, not wall-clock
// time.Now(). This is required by ADR-0003 for fake-clock determinism in tests.
func TestSignalBusPublishStampsViaClock(t *testing.T) {
	knownTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(knownTime)

	var captured engine.Trigger
	bus := mustSignalBus(t, func(_ context.Context, _ string, trg engine.Trigger) error {
		captured = trg
		return nil
	}, signal.WithSignalBusClock(fc))
	bus.Subscribe("inst-x", "pay")

	err := bus.Publish(context.Background(), "pay", nil)
	require.NoError(t, err)
	require.NotNil(t, captured)

	sig, ok := captured.(engine.SignalReceived)
	require.True(t, ok, "trigger must be SignalReceived")
	assert.Equal(t, knownTime, sig.OccurredAt(),
		"OccurredAt must equal the fake clock's time, not wall-clock time.Now()")
}

func TestNewSignalBusDefaultUsesSystemClock(t *testing.T) {
	var got time.Time
	deliver := func(_ context.Context, _ string, trg engine.Trigger) error {
		got = trg.OccurredAt()
		return nil
	}
	bus := mustSignalBus(t, deliver)
	bus.Subscribe("inst-1", "sig")
	before := time.Now()
	require.NoError(t, bus.Publish(t.Context(), "sig", nil))
	after := time.Now()
	assert.False(t, got.Before(before) || got.After(after), "SignalReceived should be stamped from the system clock")
}

func TestNewSignalBusWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	var got time.Time
	deliver := func(_ context.Context, _ string, trg engine.Trigger) error {
		got = trg.OccurredAt()
		return nil
	}
	bus := mustSignalBus(t, deliver, signal.WithSignalBusClock(fake))
	bus.Subscribe("inst-1", "sig")
	require.NoError(t, bus.Publish(t.Context(), "sig", nil))
	assert.Equal(t, time.Unix(1000, 0).UTC(), got.UTC())
}

func TestNewSignalBusFailsFast(t *testing.T) {
	t.Parallel()

	deliver := func(_ context.Context, _ string, _ engine.Trigger) error { return nil }
	type testCase struct {
		name    string
		deliver signal.DeliverFunc
		assert  func(t *testing.T, bus *signal.SignalBus, err error)
	}
	cases := []testCase{
		{
			name:    "nil deliver",
			deliver: nil,
			assert: func(t *testing.T, bus *signal.SignalBus, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, bus)
			},
		},
		{
			name:    "valid deliver",
			deliver: deliver,
			assert: func(t *testing.T, bus *signal.SignalBus, err error) {
				require.NoError(t, err)
				require.NotNil(t, bus)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bus, err := signal.NewSignalBus(tc.deliver)
			tc.assert(t, bus, err)
		})
	}
}
