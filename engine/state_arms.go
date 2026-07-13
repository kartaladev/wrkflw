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

// armedEventByTimer returns a pointer to the first armedEvent with the given
// timerID, or nil if none exists.
func (s *InstanceState) armedEventByTimer(timerID string) *armedEvent {
	for i := range s.ArmedEvents {
		if s.ArmedEvents[i].TimerID == timerID {
			return &s.ArmedEvents[i]
		}
	}
	return nil
}

// armedEventBySignal returns a pointer to the first armedEvent with the given
// signal name, or nil if none exists.
func (s *InstanceState) armedEventBySignal(name string) *armedEvent {
	for i := range s.ArmedEvents {
		if s.ArmedEvents[i].Signal == name {
			return &s.ArmedEvents[i]
		}
	}
	return nil
}

// armedEventByMessage returns a pointer to the first armedEvent whose Message
// matches name and whose MessageKey matches correlationKey, or nil if none.
func (s *InstanceState) armedEventByMessage(name, correlationKey string) *armedEvent {
	for i := range s.ArmedEvents {
		ae := &s.ArmedEvents[i]
		if ae.Message == name && ae.MessageKey == correlationKey {
			return ae
		}
	}
	return nil
}

// removeArmedEventsForGateway removes all armedEvent entries whose GatewayToken
// matches the given token ID, returning the TimerIDs of any timer-arm entries so
// the caller can emit CancelTimer commands for them.
func (s *InstanceState) removeArmedEventsForGateway(gatewayToken string) []string {
	var cancelTimerIDs []string
	out := make([]armedEvent, 0, len(s.ArmedEvents))
	for _, ae := range s.ArmedEvents {
		if ae.GatewayToken == gatewayToken {
			if ae.TimerID != "" {
				cancelTimerIDs = append(cancelTimerIDs, ae.TimerID)
			}
			continue
		}
		out = append(out, ae)
	}
	s.ArmedEvents = out
	return cancelTimerIDs
}

// boundaryArmByTimer returns a pointer to the first boundaryArm with the given
// timerID, or nil if none exists.
func (s *InstanceState) boundaryArmByTimer(timerID string) *boundaryArm {
	for i := range s.Boundaries {
		if s.Boundaries[i].TimerID == timerID {
			return &s.Boundaries[i]
		}
	}
	return nil
}

// boundaryArmBySignal returns a pointer to the first boundaryArm with the given
// signal name, or nil if none exists.
func (s *InstanceState) boundaryArmBySignal(name string) *boundaryArm {
	for i := range s.Boundaries {
		if s.Boundaries[i].Signal == name {
			return &s.Boundaries[i]
		}
	}
	return nil
}

// boundaryArmByMessage returns a pointer to the first boundaryArm whose Message
// matches name and whose MessageKey matches correlationKey, or nil if none.
func (s *InstanceState) boundaryArmByMessage(name, correlationKey string) *boundaryArm {
	for i := range s.Boundaries {
		ba := &s.Boundaries[i]
		if ba.Message == name && ba.MessageKey == correlationKey {
			return ba
		}
	}
	return nil
}

// removeBoundaryArmsForHost removes all boundaryArm entries for the given
// hostToken, returning the TimerIDs of any timer-boundary arms so the caller
// can emit CancelTimer commands for them.
func (s *InstanceState) removeBoundaryArmsForHost(hostToken string) []string {
	var cancelTimerIDs []string
	out := make([]boundaryArm, 0, len(s.Boundaries))
	for _, ba := range s.Boundaries {
		if ba.HostToken == hostToken {
			if ba.TimerID != "" {
				cancelTimerIDs = append(cancelTimerIDs, ba.TimerID)
			}
			continue
		}
		out = append(out, ba)
	}
	s.Boundaries = out
	return cancelTimerIDs
}

// eventTriggeredSubprocessArmBySignal returns a pointer to the first
// eventTriggeredSubprocessArm with the given signal name, or nil if none exists.
func (s *InstanceState) eventTriggeredSubprocessArmBySignal(name string) *eventTriggeredSubprocessArm {
	for i := range s.EventTriggeredSubprocesses {
		if s.EventTriggeredSubprocesses[i].Signal == name {
			return &s.EventTriggeredSubprocesses[i]
		}
	}
	return nil
}

// eventTriggeredSubprocessArmByTimer returns a pointer to the first
// eventTriggeredSubprocessArm with the given timerID, or nil if none exists.
func (s *InstanceState) eventTriggeredSubprocessArmByTimer(timerID string) *eventTriggeredSubprocessArm {
	for i := range s.EventTriggeredSubprocesses {
		if s.EventTriggeredSubprocesses[i].TimerID == timerID {
			return &s.EventTriggeredSubprocesses[i]
		}
	}
	return nil
}

// eventTriggeredSubprocessArmByMessage returns a pointer to the first
// eventTriggeredSubprocessArm whose Message matches name and whose MessageKey
// matches correlationKey, or nil.
func (s *InstanceState) eventTriggeredSubprocessArmByMessage(name, correlationKey string) *eventTriggeredSubprocessArm {
	for i := range s.EventTriggeredSubprocesses {
		ea := &s.EventTriggeredSubprocesses[i]
		if ea.Message == name && ea.MessageKey == correlationKey {
			return ea
		}
	}
	return nil
}

// removeEventTriggeredSubprocessArmsForScope removes all
// eventTriggeredSubprocessArm entries whose EnclosingScopeID matches the given
// scopeID, returning the TimerIDs of any timer-armed entries so the caller can
// emit CancelTimer commands.
func (s *InstanceState) removeEventTriggeredSubprocessArmsForScope(scopeID string) []string {
	var cancelTimerIDs []string
	out := make([]eventTriggeredSubprocessArm, 0, len(s.EventTriggeredSubprocesses))
	for _, ea := range s.EventTriggeredSubprocesses {
		if ea.EnclosingScopeID == scopeID {
			if ea.TimerID != "" {
				cancelTimerIDs = append(cancelTimerIDs, ea.TimerID)
			}
			continue
		}
		out = append(out, ea)
	}
	s.EventTriggeredSubprocesses = out
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
