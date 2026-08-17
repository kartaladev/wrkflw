package engine

// TimerWaiter identifies one scheduled timer the instance is currently waiting
// on. A runtime or test harness correlates a TimerFired back to the instance
// using TimerID, and reads Kind to decide whether the timer is work the
// instance is waiting to do (see [TimerKind.detectionOnly]).
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

// firesOnDyingInstance reports whether a timer of this kind must still be
// delivered to an instance that spawns no new work. It is the exemption
// ADR-0178's dying-instance guard consults: a timer belonging to a compensation
// WALK is not forward work the guard exists to suppress, and the walks that
// TERMINATE are exactly the ones an operator most needs to see wedged
// (ADR-0175) or rolled back to completion (ADR-0179).
//
// It is the one place that exemption is defined, so a future walk-scoped kind is
// added here rather than at the guard.
//
// ⚠ It is NOT the same question as [TimerKind.detectionOnly], though the two
// coincided while [TimerCompensationStall] was the only walk-scoped kind. See
// that method for the axis the two split on.
func (k TimerKind) firesOnDyingInstance() bool {
	return k == TimerCompensationStall || k == TimerCompensationRetry
}

// detectionOnly reports whether a timer of this kind exists purely to OBSERVE
// that something has not happened, as opposed to being work the instance is
// waiting to do. [InstanceState.HasArmedTimers] excludes such a timer: firing a
// compensation-stall deadline manufactures the very incident the detection
// window exists to detect (ADR-0175), so a harness must not treat it as
// drivable.
//
// ⚠ Walk-scoped does not imply detection-only, which is why this is a separate
// predicate from [TimerKind.firesOnDyingInstance] rather than one boolean
// (ADR-0179). Both compensation kinds belong to the walk, but the retry timer is
// forward work — it exists to RE-DISPATCH the failed compensation action, not
// merely to notice it failed. Answering this question with the walk-scoped one
// would hide a live backoff from every consumer's test harness, which then
// reports the park as unhandled instead of firing the timer.
func (k TimerKind) detectionOnly() bool {
	return k == TimerCompensationStall
}

// TimerRecordWaiters returns a waiter for every timer RECORD in s.Timers —
// deadline, in-wait/reminder, retry, compensation-stall and compensation-retry
// timers, the five kinds the engine tracks in its own bookkeeping table
// (ADR-0179 added the fifth). It is the only source whose entries carry a kind
// other than [TimerIntermediate], and the only one
// [InstanceState.HasArmedTimers] saw before ADR-0177.
//
// It reports every record unfiltered, INCLUDING the detection-only stall kind:
// filtering is HasArmedTimers' job, not this one's.
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
