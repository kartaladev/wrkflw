package engine

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// timerRecord is the engine's internal bookkeeping entry for a scheduled timer.
// It allows the engine to route a TimerFired back to the correct token and task
// without relying on the token's AwaitCommand (which is set to the TaskToken for
// user-task nodes, not the SLA timer ID).
//
// For intermediate-catch-event timers the TaskToken field is empty because the
// token parks on the TimerID itself and the tokenAwaiting lookup still works.
// Recording them here provides a single, unified dispatch table.
type timerRecord struct {
	// TimerID is the unique timer identifier emitted in ScheduleTimer.
	TimerID string
	// Kind discriminates intermediate, SLA, and in-wait timers.
	Kind TimerKind
	// Token is the ID of the parked engine token this timer guards.
	Token string
	// TaskToken is the human-task correlation token ("" for intermediate timers).
	TaskToken string
	// NodeID is the BPMN node that owns the timer (needed to resolve SLAFlow/SLAAction).
	NodeID string
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
	// TimerID is the scheduled timer id for timer arms (empty for signal/message arms).
	TimerID string
	// Signal is the signal name for signal arms (empty for timer/message arms).
	Signal string
	// Message is the message name for message arms (empty for timer/signal arms).
	Message string
	// MessageKey is the resolved correlation key for message arms (empty if no key).
	MessageKey string
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
	// (BPMN default), true = non-interrupting.
	NonInterrupting bool
	// TimerID is the scheduled timer id for timer boundary events. Empty for
	// signal boundary events.
	TimerID string
	// Signal is the signal name for signal boundary events. Empty for timer
	// boundary events.
	Signal string
}

// CompensationRecord is a minimal record of a completed compensable activity
// within a scope. Plan 8 (compensation/rollback) will populate and consume
// these records; they are kept minimal here — just enough to identify the
// activity and its compensating action.
//
// Fields:
//   - ActivityNode: the BPMN node ID of the completed compensable activity.
//   - Action: the name of the service action to invoke as compensation.
type CompensationRecord struct {
	// ActivityNode is the BPMN node ID of the completed compensable activity.
	ActivityNode string
	// Action is the name of the compensating service action (registered in the
	// service-action catalog) to run when this activity is rolled back.
	Action string
}

// Scope represents an active execution scope within a process instance. Scopes
// are created when a sub-process node is entered and removed when it exits.
// They form a tree via ParentID: the root scope has an empty ParentID.
//
// Fields:
//   - ID: unique scope identifier, assigned deterministically as
//     "<instanceID>-s<ScopeSeq>" (e.g. "inst-1-s1").
//   - NodeID: the BPMN node (typically a sub-process) that opened this scope.
//   - ParentID: the ID of the enclosing scope, or "" if this is a root scope.
//   - Compensations: ordered list of completed compensable activities inside
//     this scope, accumulated as activities finish. Plan 8 reads this list in
//     reverse order when rolling back the scope.
type Scope struct {
	// ID is the unique scope identifier (deterministic, no clock/random).
	ID string
	// NodeID is the BPMN node that opened this scope.
	NodeID string
	// ParentID is the enclosing scope's ID, or "" for a root scope.
	ParentID string
	// Compensations records completed compensable activities in entry order.
	// Plan 8 (compensation) populates and consumes this list.
	Compensations []CompensationRecord
}

// Status is the lifecycle state of a process instance.
type Status int

const (
	StatusRunning Status = iota
	StatusCompleted
	StatusFailed
	StatusCompensating
	StatusTerminated
)

// TokenState is the execution state of a single token.
type TokenState int

const (
	TokenActive TokenState = iota
	TokenWaitingCommand
	TokenAtJoin
)

// Token marks where execution currently sits and what it is waiting on.
type Token struct {
	ID           string
	NodeID       string
	ScopeID      string
	State        TokenState
	AwaitCommand string // CommandID this token is parked on, if any
	// AwaitSignal is the signal name this token is parked on (signal intermediate
	// catch event). The token resumes when a SignalReceived trigger with a matching
	// Name is delivered.
	AwaitSignal string
	// AwaitMessage is the message name this token is parked on (message
	// intermediate catch event). The token resumes when a MessageReceived trigger
	// with a matching Name (and AwaitMessageKey, if set) is delivered.
	AwaitMessage string
	// AwaitMessageKey is the resolved correlation key for a message catch event.
	// It is evaluated from model.Node.CorrelationKey against the instance variables
	// at park time. Empty means no key was configured — match on name alone.
	AwaitMessageKey string
	Payload         map[string]any
	EnteredAt       time.Time
}

// NodeVisit is one traversal of one node by one token (audit/history).
type NodeVisit struct {
	NodeID    string
	TokenID   string
	EnteredAt time.Time
	LeftAt    *time.Time
	ActorID   *string // who completed a human-task visit (later plans)
}

// InstanceState is the authoritative snapshot of a running instance.
type InstanceState struct {
	InstanceID string
	DefID      string
	DefVersion int
	Status     Status
	Variables  map[string]any
	Tokens     []Token
	StartedAt  time.Time
	EndedAt    *time.Time
	History    []NodeVisit

	// Tasks holds the in-flight human-task records for this instance.
	Tasks []humantask.HumanTask

	// Timers is the auxiliary bookkeeping table for all scheduled timers.
	// Keyed implicitly by index; looked up by TimerID. A timer record is removed
	// when the timer is consumed (fired and handled) or cancelled so that a
	// late/duplicate TimerFired is a clean no-op.
	// Appended in TimerSeq order; iteration is deterministic by construction.
	Timers []timerRecord

	// ArmedEvents holds the set of pending arms for in-flight event-based gateways.
	// Each entry corresponds to one catch-event arm of a parked gateway token.
	// Entries are appended in definition (outgoing-flow) order and removed in bulk
	// when any arm wins (all arms for that gateway are removed together).
	// A late trigger for a removed arm finds no matching armedEvent and is a no-op.
	ArmedEvents []armedEvent

	// Boundaries holds the set of pending arms for in-flight boundary events
	// attached to parked host activity tokens. One entry per boundary event
	// node while the host is parked. Entries are appended in definition-scan
	// order (deterministic). Removed when the boundary fires or the host
	// completes first (cancellation).
	Boundaries []boundaryArm

	// Scopes holds all currently open execution scopes (sub-process nodes in
	// flight). Each scope is opened when a sub-process node is entered and
	// removed when it exits. Scopes form a tree via Scope.ParentID.
	// Iteration is in openScope (ScopeSeq) order, which is deterministic.
	Scopes []Scope

	// Deterministic ID counters (never randomness or the clock).
	CmdSeq   int
	TokenSeq int
	TaskSeq  int
	TimerSeq int
	// ScopeSeq is the monotonic counter used to generate deterministic scope
	// IDs of the form "<instanceID>-s<ScopeSeq>".
	ScopeSeq int
}

// TaskByToken returns a pointer to the HumanTask with the given taskToken, or
// nil if no such task exists in the state.
func (s *InstanceState) TaskByToken(taskToken string) *humantask.HumanTask {
	for i := range s.Tasks {
		if s.Tasks[i].TaskToken == taskToken {
			return &s.Tasks[i]
		}
	}
	return nil
}

// timerByID returns a pointer to the timerRecord with the given timerID, or nil
// if no such record exists.
func (s *InstanceState) timerByID(timerID string) *timerRecord {
	for i := range s.Timers {
		if s.Timers[i].TimerID == timerID {
			return &s.Timers[i]
		}
	}
	return nil
}

// removeTimer removes the timerRecord with the given timerID from the Timers
// slice. It is a no-op if no record with that timerID exists.
func (s *InstanceState) removeTimer(timerID string) {
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TimerID != timerID {
			out = append(out, tr)
		}
	}
	s.Timers = out
}

// cancelTimersByTaskToken removes all timer records associated with the given
// taskToken (excluding the one already being handled), returning their TimerIDs
// so the caller can emit CancelTimer commands. Used to cancel in-wait/reminder
// timers when an SLA breach or task completion supersedes them.
func (s *InstanceState) cancelTimersByTaskToken(taskToken, excludeTimerID string) []string {
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TaskToken == taskToken && tr.TimerID != excludeTimerID {
			toCancel = append(toCancel, tr.TimerID)
			continue
		}
		out = append(out, tr)
	}
	s.Timers = out
	return toCancel
}

// cancelAllTimers returns a CancelTimer command for every outstanding timer
// record in s.Timers (in deterministic slice order) and empties s.Timers.
// Call this on any terminal-failure path to avoid orphaned timers in the
// scheduler.
//
// NOTE: A comprehensive sweep across ALL terminal transitions (not just
// ActionFailed) is deferred to the errors/compensation plan (Plan 8).
func (s *InstanceState) cancelAllTimers() []Command {
	if len(s.Timers) == 0 {
		return nil
	}
	cmds := make([]Command, 0, len(s.Timers))
	for _, tr := range s.Timers {
		cmds = append(cmds, CancelTimer{TimerID: tr.TimerID})
	}
	s.Timers = nil
	return cmds
}

// cancelAllArmsAndBoundaries returns CancelTimer commands for every timer arm
// in s.ArmedEvents (event-gateway timer arms) and s.Boundaries (boundary timer
// arms) that has a non-empty TimerID, then clears both slices. Iteration is in
// slice order (ArmedEvents first, then Boundaries) for determinism.
//
// This is called alongside cancelAllTimers on the ActionFailed terminal path to
// prevent gateway and boundary timer arms from leaking as orphaned scheduled
// tasks in the runtime scheduler.
//
// NOTE: A comprehensive sweep across ALL terminal transitions (not just
// ActionFailed) and multi-token scenarios is deferred to the errors/compensation
// plan (Plan 8). This covers ActionFailed specifically, consistent with the
// Plan-5 precedent for cancelAllTimers.
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

// removeBoundaryArm removes the single boundaryArm entry with the given
// (hostToken, boundaryNode) pair. It is a no-op if no such entry exists.
func (s *InstanceState) removeBoundaryArm(hostToken, boundaryNode string) {
	out := make([]boundaryArm, 0, len(s.Boundaries))
	for _, ba := range s.Boundaries {
		if ba.HostToken == hostToken && ba.BoundaryNode == boundaryNode {
			continue
		}
		out = append(out, ba)
	}
	s.Boundaries = out
}

// openScope creates a new Scope for the given nodeID nested inside
// parentScopeID (empty string for a root scope). The scope is assigned a
// deterministic ID of the form "<instanceID>-s<ScopeSeq>" using s.ScopeSeq,
// which is incremented before use. The new scope is appended to s.Scopes.
// Returns the new scope's ID.
func (s *InstanceState) openScope(nodeID, parentScopeID string) string {
	s.ScopeSeq++
	id := fmt.Sprintf("%s-s%d", s.InstanceID, s.ScopeSeq)
	s.Scopes = append(s.Scopes, Scope{
		ID:       id,
		NodeID:   nodeID,
		ParentID: parentScopeID,
	})
	return id
}

// tokensInScope counts the number of tokens in s.Tokens whose ScopeID equals
// scopeID. Returns 0 if no tokens match.
func (s *InstanceState) tokensInScope(scopeID string) int {
	count := 0
	for i := range s.Tokens {
		if s.Tokens[i].ScopeID == scopeID {
			count++
		}
	}
	return count
}

// closeScope removes the Scope with the given scopeID from s.Scopes. It is a
// no-op if no scope with that ID exists. Child scopes (those whose ParentID
// equals scopeID) are NOT automatically removed — callers are responsible for
// closing or reparenting children before closing a parent. This is intentionally
// minimal; Plan 8 (compensation/rollback) will add the richer cascading logic.
func (s *InstanceState) closeScope(scopeID string) {
	out := make([]Scope, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		if sc.ID != scopeID {
			out = append(out, sc)
		}
	}
	s.Scopes = out
}

// scopeByID returns a pointer to the Scope with the given id, or nil if no
// such scope exists in s.Scopes. The pointer is into the slice element; callers
// must not hold it across mutations to s.Scopes.
func (s *InstanceState) scopeByID(id string) *Scope {
	for i := range s.Scopes {
		if s.Scopes[i].ID == id {
			return &s.Scopes[i]
		}
	}
	return nil
}

// Clone returns a deep copy of the InstanceState. All slice and map fields are
// independently allocated so that mutations to the returned state do not affect
// the receiver (and vice versa).
func (s InstanceState) Clone() InstanceState {
	return cloneState(s)
}
