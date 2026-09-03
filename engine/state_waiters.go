package engine

// MessageWaiter identifies a (message name, correlation key) pair that the
// instance can be woken by. A runtime correlates a delivered message to the
// instance using these pairs. CorrelationKey is empty when the construct
// matches on message name alone.
type MessageWaiter struct {
	// Name is the message name the instance is awaiting.
	Name string
	// CorrelationKey is the resolved correlation key, or "" for name-only matching.
	CorrelationKey string
}

// MessageBoundaryWaiters returns the (message name, correlation key) pairs for
// every armed MESSAGE boundary event on the instance. A runtime registers these
// alongside message-catch tokens (Token.AwaitMessage) so a delivered message can
// be correlated to a parked instance even when the boundary's host token parks on
// a task/command rather than on the message itself.
//
// Timer and signal boundary arms contribute no entries. The result preserves
// s.Boundaries slice order (deterministic) and is nil when no message boundary
// is armed.
func (s *InstanceState) MessageBoundaryWaiters() []MessageWaiter {
	return messageWaitersOf(s.Boundaries)
}

// MessageArmedEventWaiters returns the (message name, correlation key) pairs for
// every armed MESSAGE arm of an in-flight event-based gateway. A runtime registers
// these alongside message-catch tokens (Token.AwaitMessage) and message-boundary
// waiters so a delivered message can be correlated to the parked instance even
// though an event-gateway arm is tracked as an armedEvent rather than a token
// carrying AwaitMessage.
//
// Timer and signal arms contribute no entries. The result preserves s.ArmedEvents
// slice order (deterministic) and is nil when no message arm is armed.
func (s *InstanceState) MessageArmedEventWaiters() []MessageWaiter {
	return messageWaitersOf(s.ArmedEvents)
}

// MessageEventSubprocessWaiters returns the (message name, correlation key) pairs
// for every armed MESSAGE-triggered event sub-process arm. A runtime registers
// these alongside message-catch tokens, message-boundary waiters, and
// event-based-gateway message arms so a delivered message can be correlated to a
// parked instance even though an event sub-process arm carries no token — the arm
// lives in s.EventTriggeredSubprocesses, not on a Token.AwaitMessage.
//
// Timer and signal arms contribute no entries. The result preserves
// s.EventTriggeredSubprocesses slice order (deterministic) and is nil when no
// message arm is armed.
func (s *InstanceState) MessageEventSubprocessWaiters() []MessageWaiter {
	return messageWaitersOf(s.EventTriggeredSubprocesses)
}

// SignalBoundaryNames returns the signal names of every armed SIGNAL boundary
// event on the instance. A runtime subscribes these in its SignalBus alongside
// signal-catch tokens (Token.AwaitSignal) so a broadcast can interrupt a host
// activity whose token parks on a task/command rather than on the signal itself
// — nothing sets Token.AwaitSignal for a boundary, so the name lives only here.
//
// Timer and message boundary arms contribute no entries. The result preserves
// s.Boundaries slice order (deterministic) and is nil when no signal boundary is
// armed.
func (s *InstanceState) SignalBoundaryNames() []string {
	return signalNamesOf(s.Boundaries)
}

// SignalArmedEventNames returns the signal names of every armed SIGNAL arm of an
// in-flight event-based gateway. A runtime subscribes these alongside
// signal-catch tokens and signal-boundary names so a broadcast can resolve an
// event-gateway race, even though such an arm is tracked as an armedEvent rather
// than a token carrying AwaitSignal.
//
// Timer and message arms contribute no entries. The result preserves
// s.ArmedEvents slice order (deterministic) and is nil when no signal arm is
// armed.
func (s *InstanceState) SignalArmedEventNames() []string {
	return signalNamesOf(s.ArmedEvents)
}

// SignalEventSubprocessNames returns the signal names of every armed
// SIGNAL-triggered event sub-process arm. A runtime subscribes these in its
// SignalBus alongside signal-catch tokens (Token.AwaitSignal) so a broadcast
// signal can wake an event sub-process arm, which — like a message event-sub arm
// — carries no token.
//
// Timer and message arms contribute no entries. The result preserves
// s.EventTriggeredSubprocesses slice order (deterministic) and is nil when no
// signal arm is armed.
func (s *InstanceState) SignalEventSubprocessNames() []string {
	return signalNamesOf(s.EventTriggeredSubprocesses)
}

// MessageWaiters returns EVERY (message name, correlation key) pair the instance
// can currently be woken by: token message-catch awaits (Token.AwaitMessage),
// armed message boundaries, event-based-gateway message arms, and
// message-triggered event sub-process arms. It is the single authority a runtime
// mirrors into its correlation table — a future message construct extends only
// this method, not every runtime call site. The scattered per-construct
// enumeration that this method centralizes is exactly what let event-sub arms be
// forgotten by the runtime in the first place.
//
// Order is deterministic: tokens (slice order), then boundaries, then gateway
// arms, then event-subs. The result is nil when the instance awaits no message.
func (s *InstanceState) MessageWaiters() []MessageWaiter {
	var out []MessageWaiter
	for i := range s.Tokens {
		tok := &s.Tokens[i]
		if tok.AwaitMessage != "" {
			out = append(out, MessageWaiter{Name: tok.AwaitMessage, CorrelationKey: tok.AwaitMessageKey})
		}
	}
	out = append(out, s.MessageBoundaryWaiters()...)
	out = append(out, s.MessageArmedEventWaiters()...)
	out = append(out, s.MessageEventSubprocessWaiters()...)
	return out
}

// SignalWaiters returns EVERY signal name the instance can currently be woken by:
// token signal-catch awaits (Token.AwaitSignal), armed signal boundaries,
// event-based-gateway signal arms, and signal-triggered event sub-process arms.
// It is the single authority a runtime mirrors into its SignalBus subscription
// set.
//
// It is the exact mirror of [InstanceState.MessageWaiters]: the two must
// enumerate the same four sources, because every construct that can await a
// message can equally await a signal. Omitting a source here does not fail
// loudly — the runtime simply never subscribes the name, and the instance parks
// forever.
//
// Order is deterministic: tokens (slice order), then boundaries, then gateway
// arms, then event-subs. The list may contain duplicates when two constructs
// await the same signal name; a set-based SignalBus.Sync collapses them, so no
// dedup is done here. The result is nil when the instance awaits no signal.
func (s *InstanceState) SignalWaiters() []string {
	var out []string
	for i := range s.Tokens {
		if s.Tokens[i].AwaitSignal != "" {
			out = append(out, s.Tokens[i].AwaitSignal)
		}
	}
	out = append(out, s.SignalBoundaryNames()...)
	out = append(out, s.SignalArmedEventNames()...)
	out = append(out, s.SignalEventSubprocessNames()...)
	return out
}
