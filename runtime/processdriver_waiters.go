package runtime

import "github.com/zakyalvan/krtlwrkflw/engine"

// msgKey is the composite key used to look up a message waiter by name+correlation.
type msgKey struct {
	Name           string
	CorrelationKey string
}

// syncWaiters reconciles both the SignalBus subscriptions and the internal
// message-waiter table for st after each deliverLoop save. It calls
// syncSignalBus (if a bus is configured) and syncMsgWaiters so both are
// always consistent with the current parked state of the instance.
func (r *ProcessDriver) syncWaiters(st engine.InstanceState) {
	r.syncSignalBus(st)
	r.syncMsgWaiters(st)
}

// syncSignalBus reconciles st's AwaitSignal tokens with the SignalBus, if one
// is configured. This is a no-op when r.sigbus is nil.
func (r *ProcessDriver) syncSignalBus(st engine.InstanceState) {
	if r.sigbus == nil {
		return
	}
	var awaiting []string
	for _, tok := range st.Tokens {
		if tok.AwaitSignal != "" {
			awaiting = append(awaiting, tok.AwaitSignal)
		}
	}
	r.sigbus.Sync(st.InstanceID, awaiting)
}

// syncMsgWaiters reconciles the runner's internal message-waiter table with the
// current state of st. It registers new message-awaiting tokens and removes
// entries that are no longer waiting.
func (r *ProcessDriver) syncMsgWaiters(st engine.InstanceState) {
	r.msgMu.Lock()
	defer r.msgMu.Unlock()

	// Remove all existing entries for this instance.
	for k, id := range r.msgWaiters {
		if id == st.InstanceID {
			delete(r.msgWaiters, k)
		}
	}

	// Re-register from current tokens (message-catch intermediate events / receive tasks).
	for _, tok := range st.Tokens {
		if tok.AwaitMessage != "" {
			k := msgKey{Name: tok.AwaitMessage, CorrelationKey: tok.AwaitMessageKey}
			r.msgWaiters[k] = st.InstanceID
		}
	}

	// Re-register from armed message BOUNDARY events. Their host token parks on a
	// task/command (not on the message), so they are not covered by the token loop
	// above; DeliverMessage must still be able to correlate a delivered message to
	// this instance to fire the boundary (ADR-0053).
	for _, w := range st.MessageBoundaryWaiters() {
		k := msgKey{Name: w.Name, CorrelationKey: w.CorrelationKey}
		r.msgWaiters[k] = st.InstanceID
	}
}

// findMessageWaiter returns the instance ID that is currently waiting for a
// message with the given name and correlation key, and whether one was found.
func (r *ProcessDriver) findMessageWaiter(name, correlationKey string) (string, bool) {
	r.msgMu.Lock()
	defer r.msgMu.Unlock()
	id, ok := r.msgWaiters[msgKey{Name: name, CorrelationKey: correlationKey}]
	return id, ok
}
