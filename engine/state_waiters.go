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
	var out []MessageWaiter
	for i := range s.Boundaries {
		ba := &s.Boundaries[i]
		if ba.Message != "" {
			out = append(out, MessageWaiter{Name: ba.Message, CorrelationKey: ba.MessageKey})
		}
	}
	return out
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
	var out []MessageWaiter
	for i := range s.ArmedEvents {
		ae := &s.ArmedEvents[i]
		if ae.Message != "" {
			out = append(out, MessageWaiter{Name: ae.Message, CorrelationKey: ae.MessageKey})
		}
	}
	return out
}

// MessageEventSubprocessWaiters returns the (message name, correlation key) pairs
// for every armed MESSAGE-triggered event sub-process arm. A runtime registers
// these alongside message-catch tokens, message-boundary waiters, and
// event-based-gateway message arms so a delivered message can be correlated to a
// parked instance even though an event sub-process arm carries no token — the arm
// lives in s.EventTriggeredSubprocesses, not on a Token.AwaitMessage
// (ADR-0122/0123).
//
// Timer and signal arms contribute no entries. The result preserves
// s.EventTriggeredSubprocesses slice order (deterministic) and is nil when no
// message arm is armed.
func (s *InstanceState) MessageEventSubprocessWaiters() []MessageWaiter {
	var out []MessageWaiter
	for i := range s.EventTriggeredSubprocesses {
		ea := &s.EventTriggeredSubprocesses[i]
		if ea.Message != "" {
			out = append(out, MessageWaiter{Name: ea.Message, CorrelationKey: ea.MessageKey})
		}
	}
	return out
}

// SignalEventSubprocessNames returns the signal names of every armed
// SIGNAL-triggered event sub-process arm. A runtime subscribes these in its
// SignalBus alongside signal-catch tokens (Token.AwaitSignal) so a broadcast
// signal can wake an event sub-process arm, which — like a message event-sub arm
// — carries no token (ADR-0123).
//
// Timer and message arms contribute no entries. The result preserves
// s.EventTriggeredSubprocesses slice order (deterministic) and is nil when no
// signal arm is armed.
func (s *InstanceState) SignalEventSubprocessNames() []string {
	var out []string
	for i := range s.EventTriggeredSubprocesses {
		ea := &s.EventTriggeredSubprocesses[i]
		if ea.Signal != "" {
			out = append(out, ea.Signal)
		}
	}
	return out
}

// MessageWaiters returns EVERY (message name, correlation key) pair the instance
// can currently be woken by: token message-catch awaits (Token.AwaitMessage),
// armed message boundaries, event-based-gateway message arms, and
// message-triggered event sub-process arms. It is the single authority a runtime
// mirrors into its correlation table — a future message construct extends only
// this method, not every runtime call site (ADR-0123). The scattered per-construct
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
// token signal-catch awaits (Token.AwaitSignal) and signal-triggered event
// sub-process arms. It is the single authority a runtime mirrors into its
// SignalBus subscription set (ADR-0123).
//
// Order is deterministic: token signals (slice order), then event-sub signals.
// The list may contain duplicates when a token and an event-sub await the same
// signal name; a set-based SignalBus.Sync collapses them, so no dedup is done
// here. The result is nil when the instance awaits no signal.
func (s *InstanceState) SignalWaiters() []string {
	var out []string
	for i := range s.Tokens {
		if s.Tokens[i].AwaitSignal != "" {
			out = append(out, s.Tokens[i].AwaitSignal)
		}
	}
	out = append(out, s.SignalEventSubprocessNames()...)
	return out
}
