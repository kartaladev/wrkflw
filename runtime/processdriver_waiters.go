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
func (driver *ProcessDriver) syncWaiters(st engine.InstanceState) {
	driver.syncSignalBus(st)
	driver.syncMsgWaiters(st)
}

// syncSignalBus reconciles st's signal awaits with the SignalBus, if one is
// configured. The authoritative set of signal names the instance can be woken by
// — token AwaitSignal catches AND signal-triggered event sub-process arms — comes
// from the engine's st.SignalWaiters() (ADR-0123), so the runtime never has to
// know which constructs contribute. This is a no-op when driver.sigbus is nil.
func (driver *ProcessDriver) syncSignalBus(st engine.InstanceState) {
	if driver.sigbus == nil {
		return
	}
	var awaiting []string
	if !isTerminal(st.Status) {
		// A terminal instance awaits nothing. A repeatable non-interrupting root
		// event-sub arm can still be present in a terminal snapshot (ADR-0124), so
		// leaving its subscription would misroute a later broadcast to a dead
		// instance; drop all subscriptions by syncing an empty set.
		awaiting = st.SignalWaiters()
	}
	driver.sigbus.Sync(st.InstanceID, awaiting)
}

// syncMsgWaiters reconciles the runner's internal message-waiter table with the
// current state of st. It removes stale entries for the instance, then
// re-registers every (name, key) the instance can be woken by, as reported by the
// engine's single authority st.MessageWaiters() — token message-catch awaits,
// armed message boundaries (host parks on a task, not the message — ADR-0053),
// event-based-gateway message arms, and message-triggered event sub-process arms
// (ADR-0123). Consolidating the per-construct enumeration in the engine is what
// keeps a future message construct from being silently forgotten here.
func (driver *ProcessDriver) syncMsgWaiters(st engine.InstanceState) {
	driver.msgMu.Lock()
	defer driver.msgMu.Unlock()

	// Remove all existing entries for this instance.
	for k, id := range driver.msgWaiters {
		if id == st.InstanceID {
			delete(driver.msgWaiters, k)
		}
	}

	// A terminal instance awaits nothing: registering a waiter for it would
	// misroute a later delivery (e.g. swallow a message that should start a fresh
	// message-start instance). A repeatable non-interrupting root event-sub arm can
	// still be present in the terminal snapshot, so this guard is required
	// (ADR-0124); it also retroactively closes the same gap for a never-fired arm.
	if isTerminal(st.Status) {
		return
	}

	// Re-register from the engine's authoritative union of message awaits.
	for _, w := range st.MessageWaiters() {
		driver.msgWaiters[msgKey{Name: w.Name, CorrelationKey: w.CorrelationKey}] = st.InstanceID
	}
}

// findMessageWaiter returns the instance ID that is currently waiting for a
// message with the given name and correlation key, and whether one was found.
func (driver *ProcessDriver) findMessageWaiter(name, correlationKey string) (string, bool) {
	driver.msgMu.Lock()
	defer driver.msgMu.Unlock()
	id, ok := driver.msgWaiters[msgKey{Name: name, CorrelationKey: correlationKey}]
	return id, ok
}
