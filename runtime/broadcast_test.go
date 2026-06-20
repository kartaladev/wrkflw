package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

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

	bus := runtime.NewSignalBus(rec.deliver)
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
	bus := runtime.NewSignalBus(rec.deliver)

	err := bus.Publish(ctx, "nonexistent", nil)
	require.NoError(t, err)
	assert.Empty(t, rec.instanceIDs())
}

// TestSignalBusUnsubscribeRemovesWaiter verifies that Unsubscribe removes an
// instance from the waiter set so it no longer receives the signal.
func TestSignalBusUnsubscribeRemovesWaiter(t *testing.T) {
	ctx := context.Background()
	rec := &deliverRecord{}
	bus := runtime.NewSignalBus(rec.deliver)

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
	bus := runtime.NewSignalBus(rec.deliver)

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

	bus := runtime.NewSignalBus(func(_ context.Context, _ string, _ engine.Trigger) error {
		return errDeliver
	})
	bus.Subscribe("inst-a", "approved")

	err := bus.Publish(ctx, "approved", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, errDeliver)
}

// TestSignalBusIsSafeForConcurrentUse verifies that Subscribe/Unsubscribe/Publish
// can be called concurrently without data races (run with -race).
func TestSignalBusIsSafeForConcurrentUse(t *testing.T) {
	ctx := context.Background()
	rec := &deliverRecord{}
	bus := runtime.NewSignalBus(rec.deliver)

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
