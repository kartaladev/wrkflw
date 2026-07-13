package engine

// triggerMatch is the trigger-correlation quartet shared by the three arm
// families (armedEvent, boundaryArm, eventTriggeredSubprocessArm): what an
// incoming TimerFired/SignalReceived/MessageReceived trigger is matched
// against. Embedded ANONYMOUSLY in each arm type so field access/assignment
// (arm.TimerID, arm.Signal, ...) works unchanged via Go's field-promotion
// rules, and so json.Marshal/Unmarshal of the enclosing arm type is
// byte-identical to the pre-embed shape (an anonymous embedded struct's
// fields are promoted into the parent JSON object — see ADR-0131 and the
// parity test in state_arms_wire_test.go). At most one of the four fields is
// non-empty for a given arm (timer XOR signal XOR message).
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
// This is called alongside cancelAllTimers on ALL terminal paths to prevent
// gateway and boundary timer arms from leaking as orphaned scheduled tasks in
// the runtime scheduler. Callers:
//   - handleCancelRequested (admin cancel, no compensation records → StatusTerminated)
//   - handleSubInstanceFailed (child instance failed → parent StatusFailed)
//   - propagateError (unhandled-error, no compensation records → terminal path)
//   - beginCompensation (cancel in-flight tokens at compensation walk-start)
//   - applyTerminate (compensation walk finish, reached via stepCompensationFinish
//     → applyFinish, when the walk ends the instance rather than resuming it)
//
// This function does NOT drain s.EventTriggeredSubprocesses. beginCompensation
// invokes it at walk-START, where root-scope ESP arms must survive — the walk
// may still resume the instance (a ReverseNode target) rather than end it, so
// their timers must keep running. The other four callers above run once the
// instance is genuinely terminating and each additionally drains ESP arms
// itself, via removeAllEventTriggeredSubprocessArms (a sweep across ALL
// scopes) — not through this function.
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

// armByTimer returns a pointer to the first arm in arms whose embedded timer id
// equals timerID, or nil if none exists. Slice order is preserved (first match
// wins) and the returned pointer aliases the slice element so callers may mutate
// it in place.
func armByTimer[T any, PT armMatchable[T]](arms []T, timerID string) *T {
	for i := range arms {
		if PT(&arms[i]).matchPtr().TimerID == timerID {
			return &arms[i]
		}
	}
	return nil
}

// armBySignal returns a pointer to the first arm whose embedded signal name
// equals name, or nil. See armByTimer for the pointer-aliasing contract.
func armBySignal[T any, PT armMatchable[T]](arms []T, name string) *T {
	for i := range arms {
		if PT(&arms[i]).matchPtr().Signal == name {
			return &arms[i]
		}
	}
	return nil
}

// armByMessage returns a pointer to the first arm whose embedded Message equals
// name and MessageKey equals correlationKey, or nil. See armByTimer for the
// pointer-aliasing contract.
func armByMessage[T any, PT armMatchable[T]](arms []T, name, correlationKey string) *T {
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

// armedEventByTimer returns a pointer to the first armedEvent with the given
// timerID, or nil if none exists.
func (s *InstanceState) armedEventByTimer(timerID string) *armedEvent {
	return armByTimer(s.ArmedEvents, timerID)
}

// armedEventBySignal returns a pointer to the first armedEvent with the given
// signal name, or nil if none exists.
func (s *InstanceState) armedEventBySignal(name string) *armedEvent {
	return armBySignal(s.ArmedEvents, name)
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

// boundaryArmBySignal returns a pointer to the first boundaryArm with the given
// signal name, or nil if none exists.
func (s *InstanceState) boundaryArmBySignal(name string) *boundaryArm {
	return armBySignal(s.Boundaries, name)
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
	kept, cancelTimerIDs := removeArmsWhere(s.Boundaries, func(ba *boundaryArm) bool {
		return ba.HostToken == hostToken
	})
	s.Boundaries = kept
	return cancelTimerIDs
}

// eventTriggeredSubprocessArmBySignal returns a pointer to the first
// eventTriggeredSubprocessArm with the given signal name, or nil if none exists.
func (s *InstanceState) eventTriggeredSubprocessArmBySignal(name string) *eventTriggeredSubprocessArm {
	return armBySignal(s.EventTriggeredSubprocesses, name)
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
