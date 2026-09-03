package engine

import "slices"

// triggerMatch is the trigger-correlation quartet shared by the three arm
// families (armedEvent, boundaryArm, eventTriggeredSubprocessArm): what an
// incoming TimerFired/SignalReceived/MessageReceived trigger is matched
// against. Embedded ANONYMOUSLY in each arm type so field access/assignment
// (arm.TimerID, arm.Signal, ...) works unchanged via Go's field-promotion
// rules, and so json.Marshal/Unmarshal of the enclosing arm type is
// byte-identical to the pre-embed shape (an anonymous embedded struct's
// fields are promoted into the parent JSON object — see the parity test in
// state_arms_wire_test.go). At most one of the four fields is
// non-empty for a given arm (timer XOR signal XOR message) — EXCEPT an
// error-boundary arm (armBoundaries, step_boundaries.go:38-70), which carries
// none of the four fields set at all. The lookup helpers below (armByTimer /
// armBySignal / armByMessage) must therefore treat an empty identity key as
// matching NO arm, not as a wildcard for "the error-boundary arm".
type triggerMatch struct {
	// TimerID is the scheduled timer id for timer arms (empty for signal/message arms).
	TimerID string
	// Signal is the signal name for signal arms (empty for timer/message arms).
	Signal string
	// Message is the message name for message arms (empty for timer/signal arms).
	Message string
	// MessageKey is the resolved correlation key for message arms (empty if no key).
	MessageKey string
}

// armedEvent is the engine's bookkeeping entry for a single arm of an event-based
// gateway. When a KindEventBasedGateway is driven, one armedEvent is recorded for
// each outgoing catch-event node. The first arm to fire wins; its siblings are
// cancelled (CancelTimer for timer arms; drop for signal/message arms).
//
// Design:
//   - GatewayToken is the parked token ID on the gateway node. When an arm wins,
//     this token is moved to the winning arm's branch target.
//   - CatchNode is the BPMN node id of the catch-event (timer/signal/message). It
//     is used to look up the arm in O(n) on ArmedEvents — deterministic slice order.
//   - Flow is the sequence flow ID from the gateway to the catch node. The target of
//     this flow is the catch node itself; the token skips the catch node and is
//     directly routed to the catch node's single outgoing target (first-event-wins
//     routing: the catch node has already "fired" when its arm is selected).
//   - TimerID is non-empty for timer arms; it is the ID passed to ScheduleTimer and
//     is needed to emit CancelTimer for loser timer arms.
//   - Signal is non-empty for signal arms.
//   - Message / MessageKey are non-empty for message arms (MessageKey is the
//     resolved correlation key, empty if not configured on the node).
//
// All fields are plain strings (value type); cloneState copies the slice shallowly,
// which is correct because there are no pointer fields.
type armedEvent struct {
	// GatewayToken is the parked token ID on the event-based gateway.
	GatewayToken string
	// CatchNode is the BPMN node id of the catch-event arm.
	CatchNode string
	// Flow is the sequence flow ID from the gateway to the catch node.
	Flow string
	// triggerMatch carries TimerID/Signal/Message/MessageKey (promoted).
	triggerMatch
}

// boundaryArm is the engine's bookkeeping entry for a single armed boundary
// event attached to a parked host activity token. One entry exists per boundary
// event node while the host is parked; entries are removed when the boundary
// fires or when the host completes first.
//
// Flat value struct (no pointers): cloneState can copy the slice shallowly.
// Appended in definition-scan order so the slice is deterministic.
type boundaryArm struct {
	// HostToken is the ID of the parked host activity token.
	HostToken string
	// HostNode is the BPMN node id of the host activity.
	HostNode string
	// BoundaryNode is the BPMN node id of the boundary event.
	BoundaryNode string
	// Flow is the ID of the boundary event's outgoing sequence flow (the path
	// to take when the boundary fires).
	Flow string
	// NonInterrupting mirrors model.Node.NonInterrupting; false = interrupting
	// (the default), true = non-interrupting.
	NonInterrupting bool
	// triggerMatch carries TimerID/Signal/Message/MessageKey (promoted).
	triggerMatch
	// Action is the catalog action name to invoke (FireAndForget) when the
	// boundary fires, before routing. Empty means no action is emitted.
	Action string
}

// eventTriggeredSubprocessArm is the engine's bookkeeping entry for a single
// armed event sub-process that is waiting to be triggered. One entry is
// created per event sub-process node (see eventSubprocessNested — an
// event-triggered-start activity.SubProcess) when
// the enclosing scope opens (or on StartInstance for top-level event
// sub-processes). The entry is removed when the trigger fires (one-shot) or
// when the enclosing scope closes/completes normally.
//
// Design mirrors boundaryArm but is keyed to an enclosing SCOPE rather than a
// host activity token.
//
// Flat value struct (no pointers): cloneState copies the slice shallowly (correct
// because there are no pointer fields). Appended in definition-scan order so the
// slice is deterministic.
type eventTriggeredSubprocessArm struct {
	// EnclosingScopeID is the ID of the scope inside which this event sub-process
	// lives. Empty string means the root scope (top-level event sub-process).
	EnclosingScopeID string
	// EventSubprocessNode is the BPMN node ID of the event sub-process node (see
	// eventSubprocessNested) in the enclosing scope's definition.
	EventSubprocessNode string
	// NonInterrupting mirrors the event sub-process's non-interrupting flag — the
	// SubProcess-form inner event-triggered start's flag (see eventSubprocessNested).
	// false = interrupting (the default); true = non-interrupting.
	NonInterrupting bool
	// triggerMatch carries TimerID/Signal/Message/MessageKey (promoted).
	//
	// NOTE: the corresponding ScheduleTimer.Token is intentionally EMPTY for ESP
	// arms — the timer is keyed to the enclosing scope (EnclosingScopeID), not to
	// any individual token. Cancellation is performed via
	// removeEventTriggeredSubprocessArmsForScope (by TimerID), not by token lookup.
	triggerMatch
}

// cancelAllArmsAndBoundaries returns CancelTimer commands for every timer arm
// in s.ArmedEvents (event-gateway timer arms) and s.Boundaries (boundary timer
// arms) that has a non-empty TimerID, then clears both slices. Iteration is in
// slice order (ArmedEvents first, then Boundaries) for determinism.
//
// Cancelling these arms prevents gateway and boundary timer arms from leaking
// as orphaned scheduled tasks in the runtime scheduler. Terminal paths do not
// call this directly — they call cancelAllScheduledWork, which composes it with
// cancelAllTimers and the across-all-scopes event-sub-process sweep.
//
// This function deliberately does NOT drain s.EventTriggeredSubprocesses, which
// is why it remains separately callable: beginCompensation cancels in-flight
// tokens at compensation walk-START, where root-scope ESP arms must survive —
// the walk may still resume the instance (a ReverseNode target) rather than end
// it, so their timers must keep running. Any path that is genuinely terminating
// wants cancelAllScheduledWork instead.
func (s *InstanceState) cancelAllArmsAndBoundaries() []Command {
	var cmds []Command
	for _, ae := range s.ArmedEvents {
		if ae.TimerID != "" {
			cmds = append(cmds, CancelTimer{TimerID: ae.TimerID})
		}
	}
	s.ArmedEvents = nil
	for _, ba := range s.Boundaries {
		if ba.TimerID != "" {
			cmds = append(cmds, CancelTimer{TimerID: ba.TimerID})
		}
	}
	s.Boundaries = nil
	return cmds
}

// appendCancelTimers appends a CancelTimer command to cmds for each id in
// timerIDs and returns the result. It is the single place the state layer's
// []string timer-id removals are turned into commands; an empty timerIDs
// leaves cmds untouched (a nil accumulator stays nil).
func appendCancelTimers(cmds []Command, timerIDs []string) []Command {
	for _, id := range timerIDs {
		cmds = append(cmds, CancelTimer{TimerID: id})
	}
	return cmds
}

// cancelAllScheduledWork returns the CancelTimer commands that retire every
// piece of scheduler work the instance still owns — outstanding timer records,
// event-gateway and boundary timer arms, and event-sub-process arms across all
// scopes — and clears the corresponding state.
//
// It is the sweep for EVERY terminal transition — normal completion included.
// Every terminal site routes through endInstance, which calls it
// unconditionally, so a completed instance no longer carries live arms, timers
// or boundaries.
//
// ⚠ Do not restate that as a COUNT. This comment said "all eight terminal
// sites"; there were eight when it was written and ten on 2026-08-20, and
// nothing noticed for a long stretch of changes. The checkable property
// is that endInstance is the ONLY writer of a terminal Status — pinned by
// TestEndInstanceIsTheSoleTerminalStatusWriter, which re-derives the set from
// source on every run instead of trusting a number in prose.
//
// This deliberately narrows an earlier corollary: a repeatable non-interrupting
// root event-sub arm surviving into a terminal snapshot was reasoned harmless,
// because the runtime refuses to hold correlation waiters for terminal instances
// and the fire path is status-guarded; the arm is now retired at completion
// instead, so the condition never arises. The rule that non-interrupting arms
// are repeatable rather than one-shot is unaffected.
//
// Keeping it as one definition means a newly added arm family is retired across
// all the terminal paths at once.
func (s *InstanceState) cancelAllScheduledWork() []Command {
	cmds := s.cancelAllTimers()
	cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
	return appendCancelTimers(cmds, s.removeAllEventTriggeredSubprocessArms())
}

// armMatchable constrains a *T that exposes the arm's embedded triggerMatch,
// letting the generic scan/remove helpers (armByTimer/armBySignal/armByMessage,
// removeArmsWhere) operate uniformly over all three arm families. Each arm type
// satisfies it via the trivial matchPtr method below; PT is inferred from the
// slice element type at every call site (no explicit type args needed).
type armMatchable[T any] interface {
	*T
	matchPtr() *triggerMatch
}

func (a *armedEvent) matchPtr() *triggerMatch                  { return &a.triggerMatch }
func (b *boundaryArm) matchPtr() *triggerMatch                 { return &b.triggerMatch }
func (e *eventTriggeredSubprocessArm) matchPtr() *triggerMatch { return &e.triggerMatch }

// armByTimer returns a pointer to the first arm whose embedded TimerID equals
// timerID, or nil. An empty timerID matches no arm: arms of other
// kinds — and error-boundary arms, which carry NO non-empty match field at all
// (step_boundaries.go:38-70) — would otherwise all match.
func armByTimer[T any, PT armMatchable[T]](arms []T, timerID string) *T {
	if timerID == "" {
		return nil
	}
	for i := range arms {
		if PT(&arms[i]).matchPtr().TimerID == timerID {
			return &arms[i]
		}
	}
	return nil
}

// gatewayArmID identifies an armedEvent within an instance: the parked gateway
// token plus the catch node it arms. See armIDsBySignal for why identities are
// values rather than pointers.
type gatewayArmID struct {
	GatewayToken string
	CatchNode    string
}

// boundaryArmID identifies a boundaryArm within an instance: the parked host
// token plus the boundary node attached to it.
type boundaryArmID struct {
	HostToken    string
	BoundaryNode string
}

// eventSubArmID identifies an eventTriggeredSubprocessArm within an instance:
// the enclosing scope plus the event sub-process node.
//
// EnclosingScopeID == "" is the VALID root-scope identity, not a missing key —
// unlike the gateway and boundary owner fields, it must never be guarded as
// empty (see removeEventTriggeredSubprocessArmsForScope, which deliberately
// omits the empty-key early return its two sibling wrappers have). Guarding it
// would silently disable every top-level event sub-process.
type eventSubArmID struct {
	EnclosingScopeID    string
	EventSubprocessNode string
}

// armIDsBySignal returns the IDENTITY of every arm whose embedded signal name
// equals name, in slice (definition-scan) order, with duplicate identities
// collapsed to their FIRST occurrence. An empty name matches no arm —
// defence in depth, since validateTriggerKey already rejects one at Step entry.
//
// Three properties are load-bearing and each has a test:
//
//   - IDENTITIES, NOT POINTERS. removeArmsWhere reallocates the backing array and
//     the per-family wrappers assign it over the field, so a pointer captured
//     before an earlier arm fires addresses the DETACHED old array, where the
//     removed arm is still intact. Firing through such a pointer would dispatch
//     an arm that was already retired — a wrong-outcome bug, not a crash. The
//     detachment is bidirectional: a write through a pointer to a SURVIVING arm
//     is silently dropped too.
//
//   - NO SORT. Arms are emitted in declaration order. A per-family sort on
//     NonInterrupting was specified by an earlier draft and refuted by
//     execution in BOTH directions: whether an earlier arm's effects are
//     destroyable depends on the arm's BODY (parks / completes / terminates), not
//     on its interrupting flag, so no flag-based sort is correct in general.
//     Ordering is outcome-affecting and author-controlled.
//
//   - DE-DUPLICATION, FIRST WINS. Two arms can collide on one identity, so the
//     tie-break has to be defined. Do NOT justify this by enumerating what
//     model.Validate lets through: that list has already gone stale once, and it
//     is not the load-bearing reason anyway. The reason is that model.Validate is
//     not on this path — its only non-test caller is definitionCore.build(), so a
//     definition assembled as a struct literal (every fixture, and any consumer
//     that builds a ProcessDefinition directly) reaches the arm layer never having
//     been validated at all. The de-dup must hold for definitions no validator
//     ever saw.
//
//     For the record, the one shape a VALIDATED definition can still present is
//     two sequence flows between the same node pair (executed 2026-08-20:
//     model.Validate returns nil for it, and rejects duplicate node ids and
//     duplicate flow ids — the latter exempting blank ids, which are legal).
//     They are NOT interchangeable — colliding boundary arms have
//     been measured differing in NonInterrupting and Action, which decides whether
//     the delivery interrupts the host — so the tie-break is part of the contract:
//     the first in slice order wins, matching what the …ByID re-resolvers return.
//
// The linear de-dup scan is deliberate: the number of arms matching one signal
// name is small, and it avoids allocating a map on a hot dispatch path.
func armIDsBySignal[T any, PT armMatchable[T], ID comparable](arms []T, name string, identity func(*T) ID) []ID {
	if name == "" {
		return nil
	}
	var out []ID
	for i := range arms {
		if PT(&arms[i]).matchPtr().Signal != name {
			continue
		}
		id := identity(&arms[i])
		if slices.Contains(out, id) {
			continue
		}
		out = append(out, id)
	}
	return out
}

// armByID returns a pointer to the FIRST arm whose identity equals id, or nil
// when no arm carries it. It is the re-resolution half of
// snapshot-then-fire-each: a snapshotted identity is re-resolved immediately
// before its arm fires, and skipped when this returns nil.
//
// ⚠ EXISTENCE CHECK ONLY. Re-resolution must never be used to select the NEXT
// arm to fire. A non-interrupting arm deliberately stays armed after firing, so
// a loop that re-scanned for "the next match" would find the same arm forever —
// the non-terminating shape this design rejects.
//
// "First wins" is the same tie-break armIDsBySignal applies when de-duplicating,
// and the two must agree: colliding arms are NOT interchangeable (measured
// differing in NonInterrupting and Action, which decides whether the delivery
// interrupts the host).
func armByID[T any, ID comparable](arms []T, id ID, identity func(*T) ID) *T {
	for i := range arms {
		if identity(&arms[i]) == id {
			return &arms[i]
		}
	}
	return nil
}

// armByMessage returns a pointer to the first arm whose embedded Message equals
// name and MessageKey equals correlationKey, or nil. An empty name matches no
// arm. correlationKey is deliberately NOT guarded: an empty key means
// "uncorrelated" and must keep matching an arm whose MessageKey is also empty.
// See armByTimer for the pointer-aliasing contract.
func armByMessage[T any, PT armMatchable[T]](arms []T, name, correlationKey string) *T {
	if name == "" {
		return nil
	}
	for i := range arms {
		m := PT(&arms[i]).matchPtr()
		if m.Message == name && m.MessageKey == correlationKey {
			return &arms[i]
		}
	}
	return nil
}

// removeArmsWhere returns the subset of arms that do NOT satisfy owned, together
// with the TimerIDs of every removed timer arm (empty TimerIDs skipped) so the
// caller can emit CancelTimer commands. The kept slice is always non-nil (an
// empty, allocated slice when everything is removed), matching the per-family
// filters' prior behavior so the persisted JSON shape is unchanged. Slice order
// is preserved. owned keys on the arm's OWNER field (GatewayToken/HostToken/
// EnclosingScopeID) — the genuinely per-family part — supplied by each wrapper.
func removeArmsWhere[T any, PT armMatchable[T]](arms []T, owned func(*T) bool) (kept []T, cancelTimerIDs []string) {
	kept = make([]T, 0, len(arms))
	for i := range arms {
		if owned(&arms[i]) {
			if id := PT(&arms[i]).matchPtr().TimerID; id != "" {
				cancelTimerIDs = append(cancelTimerIDs, id)
			}
			continue
		}
		kept = append(kept, arms[i])
	}
	return kept, cancelTimerIDs
}

// messageWaitersOf returns the (name, correlation key) pair of every MESSAGE
// arm in arms, in slice order; nil when none is armed.
func messageWaitersOf[T any, PT armMatchable[T]](arms []T) []MessageWaiter {
	var out []MessageWaiter
	for i := range arms {
		if m := PT(&arms[i]).matchPtr(); m.Message != "" {
			out = append(out, MessageWaiter{Name: m.Message, CorrelationKey: m.MessageKey})
		}
	}
	return out
}

// timerWaitersOf returns a [TimerWaiter] for every TIMER arm in arms, in slice
// order; nil when none is armed.
//
// It is the timer sibling of messageWaitersOf/signalNamesOf, with one
// difference forced by the data: the shared triggerMatch embed carries no
// TimerKind, so the kind cannot be read off the arm. Every arm-borne timer is
// armed as [TimerIntermediate] (the three arm families' ScheduleTimer sites all
// pass that kind), which is why the constant is applied here rather than
// supplied per family. owner contributes the genuinely per-family part: the
// BPMN node that owns the arm and the token it is attached to, if any — an
// event-sub-process arm is keyed to a scope and carries no token.
func timerWaitersOf[T any, PT armMatchable[T]](arms []T, owner func(*T) (nodeID, tokenID string)) []TimerWaiter {
	var out []TimerWaiter
	for i := range arms {
		m := PT(&arms[i]).matchPtr()
		if m.TimerID == "" {
			continue
		}
		nodeID, tokenID := owner(&arms[i])
		out = append(out, TimerWaiter{
			TimerID: m.TimerID,
			Kind:    TimerIntermediate,
			NodeID:  nodeID,
			TokenID: tokenID,
		})
	}
	return out
}

// signalNamesOf returns the signal name of every SIGNAL arm in arms, in slice
// order; nil when none is armed.
func signalNamesOf[T any, PT armMatchable[T]](arms []T) []string {
	var out []string
	for i := range arms {
		if m := PT(&arms[i]).matchPtr(); m.Signal != "" {
			out = append(out, m.Signal)
		}
	}
	return out
}

// armedEventByTimer returns a pointer to the first armedEvent with the given
// timerID, or nil if none exists.
func (s *InstanceState) armedEventByTimer(timerID string) *armedEvent {
	return armByTimer(s.ArmedEvents, timerID)
}

// armedEventByID re-resolves a snapshotted gateway-arm identity. An empty
// GatewayToken names no arm, matching removeArmedEventsForGateway.
func (s *InstanceState) armedEventByID(id gatewayArmID) *armedEvent {
	if id.GatewayToken == "" {
		return nil
	}
	return armByID(s.ArmedEvents, id, func(ae *armedEvent) gatewayArmID {
		return gatewayArmID{GatewayToken: ae.GatewayToken, CatchNode: ae.CatchNode}
	})
}

// armedEventIDsBySignal returns the identity of every armedEvent armed on the
// given signal name, in declaration order. See armIDsBySignal.
func (s *InstanceState) armedEventIDsBySignal(name string) []gatewayArmID {
	return armIDsBySignal(s.ArmedEvents, name, func(ae *armedEvent) gatewayArmID {
		return gatewayArmID{GatewayToken: ae.GatewayToken, CatchNode: ae.CatchNode}
	})
}

// armedEventByMessage returns a pointer to the first armedEvent whose Message
// matches name and whose MessageKey matches correlationKey, or nil if none.
func (s *InstanceState) armedEventByMessage(name, correlationKey string) *armedEvent {
	return armByMessage(s.ArmedEvents, name, correlationKey)
}

// removeArmedEventsForGateway removes all armedEvent entries whose GatewayToken
// matches the given token ID, returning the TimerIDs of any timer-arm entries so
// the caller can emit CancelTimer commands for them.
func (s *InstanceState) removeArmedEventsForGateway(gatewayToken string) []string {
	// An empty gateway token names no arm.
	if gatewayToken == "" {
		return nil
	}
	kept, cancelTimerIDs := removeArmsWhere(s.ArmedEvents, func(ae *armedEvent) bool {
		return ae.GatewayToken == gatewayToken
	})
	s.ArmedEvents = kept
	return cancelTimerIDs
}

// boundaryArmByTimer returns a pointer to the first boundaryArm with the given
// timerID, or nil if none exists.
func (s *InstanceState) boundaryArmByTimer(timerID string) *boundaryArm {
	return armByTimer(s.Boundaries, timerID)
}

// boundaryArmByID re-resolves a snapshotted boundary-arm identity. An empty
// HostToken names no arm, matching removeBoundaryArmsForHost.
func (s *InstanceState) boundaryArmByID(id boundaryArmID) *boundaryArm {
	if id.HostToken == "" {
		return nil
	}
	return armByID(s.Boundaries, id, func(ba *boundaryArm) boundaryArmID {
		return boundaryArmID{HostToken: ba.HostToken, BoundaryNode: ba.BoundaryNode}
	})
}

// boundaryArmIDsBySignal returns the identity of every boundaryArm armed on the
// given signal name, in declaration order. See armIDsBySignal.
func (s *InstanceState) boundaryArmIDsBySignal(name string) []boundaryArmID {
	return armIDsBySignal(s.Boundaries, name, func(ba *boundaryArm) boundaryArmID {
		return boundaryArmID{HostToken: ba.HostToken, BoundaryNode: ba.BoundaryNode}
	})
}

// boundaryArmByMessage returns a pointer to the first boundaryArm whose Message
// matches name and whose MessageKey matches correlationKey, or nil if none.
func (s *InstanceState) boundaryArmByMessage(name, correlationKey string) *boundaryArm {
	return armByMessage(s.Boundaries, name, correlationKey)
}

// removeBoundaryArmsForHost removes all boundaryArm entries for the given
// hostToken, returning the TimerIDs of any timer-boundary arms so the caller
// can emit CancelTimer commands for them.
func (s *InstanceState) removeBoundaryArmsForHost(hostToken string) []string {
	// An empty host token names no arm.
	if hostToken == "" {
		return nil
	}
	kept, cancelTimerIDs := removeArmsWhere(s.Boundaries, func(ba *boundaryArm) bool {
		return ba.HostToken == hostToken
	})
	s.Boundaries = kept
	return cancelTimerIDs
}

// eventTriggeredSubprocessArmByID re-resolves a snapshotted event-sub-process
// identity.
//
// ⚠ It deliberately does NOT guard an empty EnclosingScopeID, unlike its two
// sibling re-resolvers. "" is the VALID root-scope identity; guarding it would
// silently disable every top-level event sub-process. This mirrors the existing
// asymmetry between removeEventTriggeredSubprocessArmsForScope (no guard) and
// removeArmedEventsForGateway / removeBoundaryArmsForHost (guarded).
func (s *InstanceState) eventTriggeredSubprocessArmByID(id eventSubArmID) *eventTriggeredSubprocessArm {
	return armByID(s.EventTriggeredSubprocesses, id, func(ea *eventTriggeredSubprocessArm) eventSubArmID {
		return eventSubArmID{EnclosingScopeID: ea.EnclosingScopeID, EventSubprocessNode: ea.EventSubprocessNode}
	})
}

// eventTriggeredSubprocessArmIDsBySignal returns the identity of every armed
// event sub-process on the given signal name, in declaration order. A ROOT-scope
// arm carries EnclosingScopeID == "" and is returned like any other — see
// eventSubArmID. See armIDsBySignal.
func (s *InstanceState) eventTriggeredSubprocessArmIDsBySignal(name string) []eventSubArmID {
	return armIDsBySignal(s.EventTriggeredSubprocesses, name, func(ea *eventTriggeredSubprocessArm) eventSubArmID {
		return eventSubArmID{EnclosingScopeID: ea.EnclosingScopeID, EventSubprocessNode: ea.EventSubprocessNode}
	})
}

// eventTriggeredSubprocessArmByTimer returns a pointer to the first
// eventTriggeredSubprocessArm with the given timerID, or nil if none exists.
func (s *InstanceState) eventTriggeredSubprocessArmByTimer(timerID string) *eventTriggeredSubprocessArm {
	return armByTimer(s.EventTriggeredSubprocesses, timerID)
}

// eventTriggeredSubprocessArmByMessage returns a pointer to the first
// eventTriggeredSubprocessArm whose Message matches name and whose MessageKey
// matches correlationKey, or nil.
func (s *InstanceState) eventTriggeredSubprocessArmByMessage(name, correlationKey string) *eventTriggeredSubprocessArm {
	return armByMessage(s.EventTriggeredSubprocesses, name, correlationKey)
}

// removeEventTriggeredSubprocessArmsForScope removes all
// eventTriggeredSubprocessArm entries whose EnclosingScopeID matches the given
// scopeID, returning the TimerIDs of any timer-armed entries so the caller can
// emit CancelTimer commands.
func (s *InstanceState) removeEventTriggeredSubprocessArmsForScope(scopeID string) []string {
	kept, cancelTimerIDs := removeArmsWhere(s.EventTriggeredSubprocesses, func(ea *eventTriggeredSubprocessArm) bool {
		return ea.EnclosingScopeID == scopeID
	})
	s.EventTriggeredSubprocesses = kept
	return cancelTimerIDs
}

// removeAllEventTriggeredSubprocessArms drains every armed event sub-process
// across ALL scopes (unlike removeEventTriggeredSubprocessArmsForScope, which
// is scoped to one EnclosingScopeID), returning the TimerIDs of any
// timer-armed entries so the caller can emit CancelTimer commands. It is the
// sweep-all used by terminal paths (terminate / immediate-cancel /
// immediate-fail) where no ESP arm should survive instance end. Iterates
// s.EventTriggeredSubprocesses in slice order for deterministic output.
func (s *InstanceState) removeAllEventTriggeredSubprocessArms() []string {
	var cancelTimerIDs []string
	for _, ea := range s.EventTriggeredSubprocesses {
		if ea.TimerID != "" {
			cancelTimerIDs = append(cancelTimerIDs, ea.TimerID)
		}
	}
	s.EventTriggeredSubprocesses = nil
	return cancelTimerIDs
}
