package runtime

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// DeliverFunc is the function the SignalBus uses to deliver a trigger to a
// specific process instance. The caller wires this to Runner.Deliver (with the
// definition already captured in a closure). It is also used by MessageBus for
// message correlation delivery.
type DeliverFunc func(ctx context.Context, instanceID string, trg engine.Trigger) error

// SignalBus fans out a named signal to every instance that is currently
// subscribed as a waiter for that signal name.
//
// Design (option a — subscription tracking):
//
//   - The bus maintains a map[signalName]set<instanceID> updated by the runtime
//     after each park (via Sync) or explicitly via Subscribe/Unsubscribe.
//
//   - Publish delivers engine.SignalReceived to every waiter for the given name
//     in sorted (deterministic) instance-ID order.
//
//   - The deliver function is injected at construction time as a [DeliverFunc]:
//     the caller typically wraps runner.Deliver with the definition pre-captured:
//
//     bus := runtime.NewSignalBus(clk, func(ctx context.Context, id string, trg engine.Trigger) error {
//     _, err := runner.Deliver(ctx, def, id, trg)
//     return err
//     })
//
// Concurrency: all internal state is protected by a mutex; the bus is safe for
// concurrent use from multiple goroutines (scheduler callbacks, HTTP handlers).
//
// Timestamp: Publish stamps each SignalReceived with the time from the injected
// [clock.Clock] (ADR-0003). Pass the same fake clock used by the Runner in tests
// so that downstream timers anchored to the signal timestamp are deterministic.
type SignalBus struct {
	clk     clock.Clock
	mu      sync.Mutex
	waiters map[string]map[string]struct{} // signalName → set of instanceIDs
	deliver DeliverFunc
}

// NewSignalBus constructs a SignalBus backed by the given clock and delivery
// function. clk is used to stamp SignalReceived triggers (ADR-0003 — never
// time.Now()). deliver is called once per registered waiter for each Publish,
// with the instance ID and the SignalReceived trigger.
func NewSignalBus(clk clock.Clock, deliver DeliverFunc) *SignalBus {
	return &SignalBus{
		clk:     clk,
		waiters: make(map[string]map[string]struct{}),
		deliver: deliver,
	}
}

// Subscribe registers instanceID as a waiter for signal signalName. Calling
// Subscribe with the same (instanceID, signalName) pair more than once is
// idempotent.
func (b *SignalBus) Subscribe(instanceID, signalName string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.waiters[signalName]; !ok {
		b.waiters[signalName] = make(map[string]struct{})
	}
	b.waiters[signalName][instanceID] = struct{}{}
}

// Unsubscribe removes instanceID from the waiter set for signalName. It is a
// no-op if the instance was not subscribed.
func (b *SignalBus) Unsubscribe(instanceID, signalName string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if set, ok := b.waiters[signalName]; ok {
		delete(set, instanceID)
		if len(set) == 0 {
			delete(b.waiters, signalName)
		}
	}
}

// Sync reconciles all signal subscriptions for instanceID so that the bus
// exactly reflects the set of signals the instance is currently awaiting.
// Signals not in awaitingNames are unsubscribed; new names are subscribed.
//
// Call Sync after each deliverLoop iteration so the bus tracks the up-to-date
// state of a parked instance.
func (b *SignalBus) Sync(instanceID string, awaitingNames []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Build target set.
	desired := make(map[string]struct{}, len(awaitingNames))
	for _, n := range awaitingNames {
		desired[n] = struct{}{}
	}

	// Remove from signal sets that are not in desired.
	for sig, set := range b.waiters {
		if _, want := desired[sig]; !want {
			delete(set, instanceID)
			if len(set) == 0 {
				delete(b.waiters, sig)
			}
		}
	}

	// Add to signal sets that are in desired but not already registered.
	for sig := range desired {
		if _, ok := b.waiters[sig]; !ok {
			b.waiters[sig] = make(map[string]struct{})
		}
		b.waiters[sig][instanceID] = struct{}{}
	}
}

// Publish broadcasts a SignalReceived trigger for name to every currently
// registered waiter, delivering them in sorted (deterministic) instance-ID
// order. The waiter's subscription is NOT automatically removed on delivery;
// it is the responsibility of the next deliverLoop call (via Sync) to reconcile.
//
// Delivery is best-effort: all registered waiters are attempted even if one
// fails. Individual delivery errors are accumulated and returned as a joined
// error via [errors.Join]. A nil return means all deliveries succeeded.
func (b *SignalBus) Publish(ctx context.Context, name string, payload map[string]any) error {
	// Snapshot the waiter set under lock so we hold the lock minimally and
	// allow concurrent Subscribe/Unsubscribe calls during delivery.
	b.mu.Lock()
	set, ok := b.waiters[name]
	var ids []string
	if ok && len(set) > 0 {
		ids = make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
	}
	b.mu.Unlock()

	if len(ids) == 0 {
		return nil
	}

	// Deterministic delivery order.
	sort.Strings(ids)

	trg := engine.NewSignalReceived(b.clk.Now(), name, payload)

	// Best-effort: attempt every waiter; accumulate errors.
	var errs []error
	for _, id := range ids {
		if err := b.deliver(ctx, id, trg); err != nil {
			errs = append(errs, &deliverError{signal: name, instanceID: id, err: err})
		}
	}
	return errors.Join(errs...)
}

// deliverError wraps a delivery failure with signal and instance context.
type deliverError struct {
	signal     string
	instanceID string
	err        error
}

func (e *deliverError) Error() string {
	return "runtime: SignalBus.Publish " + e.signal + " to " + e.instanceID + ": " + e.err.Error()
}

func (e *deliverError) Unwrap() error { return e.err }
