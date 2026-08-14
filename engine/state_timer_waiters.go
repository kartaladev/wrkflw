package engine

// TimerWaiter identifies one scheduled timer the instance is currently waiting
// on. A runtime or test harness correlates a TimerFired back to the instance
// using TimerID, and reads Kind to decide whether the timer is work the
// instance is waiting to do (see [TimerKind.walkScoped]).
//
// It is the timer analogue of [MessageWaiter]: a flat value struct, safe to
// copy, carrying no engine internals. Unlike a message waiter it needs a Kind,
// because timers of different kinds mean different things to a consumer, and a
// NodeID/TokenID pair, because a timer arm's owner is not recoverable from its
// id (ADR-0152 forbids parsing meaning out of an identity).
type TimerWaiter struct {
	// TimerID is the scheduled timer id, as emitted in [ScheduleTimer].
	TimerID string
	// Kind is the timer's purpose. Every arm-borne timer (boundary,
	// event-gateway, event sub-process, plain intermediate catch) is
	// [TimerIntermediate]; only a record in s.Timers carries another kind.
	Kind TimerKind
	// NodeID is the BPMN node that owns the arm: the boundary event, the
	// event-gateway catch node, the event sub-process, or the node the parked
	// token sits on.
	NodeID string
	// TokenID is the token the timer is attached to, or "" when the arm carries
	// no token — an event sub-process arm is keyed to its enclosing scope.
	TokenID string
}

// walkScoped reports whether a timer of this kind belongs to a compensation
// WALK rather than to the instance's forward work. A walk-scoped timer is not
// work the instance is waiting to do, so [InstanceState.HasArmedTimers]
// excludes it: firing a compensation-stall deadline manufactures the very
// incident the detection window exists to detect (ADR-0175).
//
// It is the one place the exclusion is defined, so a future walk-scoped kind is
// added here rather than at every predicate. ADR-0179's compensation-retry
// timer is the next such kind.
func (k TimerKind) walkScoped() bool {
	return k == TimerCompensationStall
}

// TimerRecordWaiters returns a waiter for every timer RECORD in s.Timers —
// deadline, in-wait/reminder, retry and compensation-stall timers, the four
// kinds the engine tracks in its own bookkeeping table. It is the only source
// whose entries carry a kind other than [TimerIntermediate], and the only one
// [InstanceState.HasArmedTimers] saw before ADR-0177.
//
// The result preserves s.Timers slice order (deterministic) and is nil when the
// instance holds no record.
func (s *InstanceState) TimerRecordWaiters() []TimerWaiter {
	var out []TimerWaiter
	for i := range s.Timers {
		tr := &s.Timers[i]
		out = append(out, TimerWaiter{
			TimerID: tr.TimerID,
			Kind:    tr.Kind,
			NodeID:  tr.NodeID,
			TokenID: tr.Token,
		})
	}
	return out
}

// TimerBoundaryWaiters returns a waiter for every armed TIMER boundary event on
// the instance. NodeID names the boundary event itself and TokenID the parked
// host activity token — nothing on that token names the arm, so the boundary is
// invisible to any predicate reading tokens alone.
//
// Signal and message boundary arms contribute no entries. The result preserves
// s.Boundaries slice order (deterministic) and is nil when no timer boundary is
// armed.
func (s *InstanceState) TimerBoundaryWaiters() []TimerWaiter {
	return timerWaitersOf(s.Boundaries, func(ba *boundaryArm) (string, string) {
		return ba.BoundaryNode, ba.HostToken
	})
}

// TimerArmedEventWaiters returns a waiter for every armed TIMER arm of an
// in-flight event-based gateway. NodeID names the catch node the arm was
// created for and TokenID the parked gateway token, which parks on an
// "evtgw:" sentinel rather than on the timer.
//
// Signal and message arms contribute no entries. The result preserves
// s.ArmedEvents slice order (deterministic) and is nil when no timer arm is
// armed.
func (s *InstanceState) TimerArmedEventWaiters() []TimerWaiter {
	return timerWaitersOf(s.ArmedEvents, func(ae *armedEvent) (string, string) {
		return ae.CatchNode, ae.GatewayToken
	})
}

// TimerEventSubprocessWaiters returns a waiter for every armed
// TIMER-triggered event sub-process arm. TokenID is always empty: such an arm
// is keyed to its enclosing scope, not to any token (ADR-0122/0123), so the
// instance can hold one while carrying no token at that node at all.
//
// Signal and message arms contribute no entries. The result preserves
// s.EventTriggeredSubprocesses slice order (deterministic) and is nil when no
// timer arm is armed.
func (s *InstanceState) TimerEventSubprocessWaiters() []TimerWaiter {
	return timerWaitersOf(s.EventTriggeredSubprocesses, func(ea *eventTriggeredSubprocessArm) (string, string) {
		return ea.EventSubprocessNode, ""
	})
}

// TimerTokenWaiters returns a waiter for every token parked on a plain TIMER
// intermediate catch event, identified by [Token.AwaitTimer]. It is the timer
// counterpart of the Token.AwaitSignal/AwaitMessage scans inside
// [InstanceState.SignalWaiters] and [InstanceState.MessageWaiters].
//
// ⚠ KNOWN LIMITATION (ADR-0177). A token parked on such a timer BEFORE
// AwaitTimer shipped has no value in its stored row, so after rehydration it
// yields no waiter until the arm is re-created. Backfilling it here would mean
// recognising a timer id by its shape, which ADR-0152 forbids. A downgrade to a
// build without the field has the same effect: persistence is whole-state
// json.Marshal, so an unknown field is silently dropped.
//
// The result preserves s.Tokens slice order (deterministic) and is nil when no
// token is parked on a timer.
func (s *InstanceState) TimerTokenWaiters() []TimerWaiter {
	var out []TimerWaiter
	for i := range s.Tokens {
		tok := &s.Tokens[i]
		if tok.AwaitTimer == "" {
			continue
		}
		out = append(out, TimerWaiter{
			TimerID: tok.AwaitTimer,
			Kind:    TimerIntermediate,
			NodeID:  tok.NodeID,
			TokenID: tok.ID,
		})
	}
	return out
}

// TimerWaiters returns EVERY timer the instance is currently waiting on: token
// timer-catch awaits ([Token.AwaitTimer]), timer boundaries,
// event-based-gateway timer arms, timer-triggered event sub-process arms, and
// the s.Timers record table. It is the single authority a runtime or harness
// mirrors — a future timer construct extends only this method, not every call
// site (the discipline ADR-0123 established for [InstanceState.MessageWaiters]
// and [InstanceState.SignalWaiters]).
//
// It enumerates EVERYTHING, including walk-scoped kinds; filtering is the
// caller's decision, exactly as SignalWaiters leaves de-duplication to its
// caller. [InstanceState.HasArmedTimers] is the filtered view.
//
// Order is deterministic: tokens, then boundaries, then gateway arms, then
// event-subs — the order the two siblings use — and then the record table, the
// fifth source timers alone have. The result is nil when the instance awaits no
// timer.
func (s *InstanceState) TimerWaiters() []TimerWaiter {
	var out []TimerWaiter
	out = append(out, s.TimerTokenWaiters()...)
	out = append(out, s.TimerBoundaryWaiters()...)
	out = append(out, s.TimerArmedEventWaiters()...)
	out = append(out, s.TimerEventSubprocessWaiters()...)
	return append(out, s.TimerRecordWaiters()...)
}
