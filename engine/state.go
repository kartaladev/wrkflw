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
	// ScopeID is the execution scope of the token that owns this timer. Empty
	// string means the root scope. Used to resolve the correct nested definition
	// when a SLA or reminder timer fires inside a sub-process.
	ScopeID string
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

// eventSubprocessArm is the engine's bookkeeping entry for a single armed event
// sub-process that is waiting to be triggered. One entry is created per
// KindEventSubProcess node when the enclosing scope opens (or on StartInstance
// for top-level event sub-processes). The entry is removed when the trigger fires
// (one-shot) or when the enclosing scope closes/completes normally.
//
// Design mirrors boundaryArm but is keyed to an enclosing SCOPE rather than a
// host activity token.
//
// Flat value struct (no pointers): cloneState copies the slice shallowly (correct
// because there are no pointer fields). Appended in definition-scan order so the
// slice is deterministic.
type eventSubprocessArm struct {
	// EnclosingScopeID is the ID of the scope inside which this event sub-process
	// lives. Empty string means the root scope (top-level event sub-process).
	EnclosingScopeID string
	// EventSubprocessNode is the BPMN node ID of the KindEventSubProcess node in
	// the enclosing scope's definition.
	EventSubprocessNode string
	// NonInterrupting mirrors model.Node.NonInterrupting on the KindEventSubProcess
	// node. false = interrupting (BPMN default); true = non-interrupting.
	NonInterrupting bool
	// Signal is the signal name from the event sub-process's nested start event.
	// Non-empty for signal-triggered event sub-processes.
	Signal string
	// TimerID is the scheduled timer ID for timer-triggered event sub-processes.
	// Non-empty for timer-triggered event sub-processes.
	//
	// NOTE: the corresponding ScheduleTimer.Token is intentionally EMPTY for ESP
	// arms — the timer is keyed to the enclosing scope (EnclosingScopeID), not to
	// any individual token. Cancellation is performed via
	// removeEventSubprocessArmsForScope (by TimerID), not by token lookup.
	TimerID string
	// Message is the message name for message-triggered event sub-processes.
	Message string
	// MessageKey is the resolved correlation key for message-triggered event sub-processes.
	MessageKey string
}

// CompensationRecord is a record of a completed compensable activity within a
// scope. Plan 8 (compensation/rollback) populates these records when an activity
// with a non-empty CompensationAction completes; it walks them in reverse
// completion order when rolling back the scope.
//
// Fields:
//   - NodeID: the BPMN node ID of the completed compensable activity.
//   - Action: the name of the service action to invoke as compensation.
//   - CompletedAt: the time the activity completed (from the trigger's OccurredAt;
//     never time.Now() — deterministic and clock-free).
//   - Input: a snapshot of the instance variables at the moment the activity was
//     invoked (i.e. the same map passed to InvokeAction). Taken before merging the
//     activity's output so the compensation action receives the original inputs.
//     Deep-copied in cloneState so mutations to the clone do not affect the original.
type CompensationRecord struct {
	// NodeID is the BPMN node ID of the completed compensable activity.
	NodeID string
	// Action is the name of the compensating service action (registered in the
	// service-action catalog) to run when this activity is rolled back.
	Action string
	// CompletedAt is the time the activity completed (from the trigger's OccurredAt).
	CompletedAt time.Time
	// Input is a snapshot of the instance variables at invocation time.
	Input map[string]any
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
	// TokenIncident marks a token that has exhausted its retry budget (or hit a
	// non-retryable error) and is now parked as an incident. The token remains in
	// this state until an operator resolves the incident (e.g. via retry or skip).
	TokenIncident
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

	// RetryAttempts is the number of execution attempts already made for this
	// token's current node (0 = first attempt has not started yet, 1 = one
	// attempt has completed or failed, etc.).
	RetryAttempts int
	// RetryStartedAt is the wall-clock time when the first retry attempt was
	// initiated. It serves as the anchor for MaxElapsed budget calculations.
	// Zero value means the token is not currently retrying.
	RetryStartedAt time.Time
}

// Incident records a token that has exhausted its retry budget (or encountered a
// non-retryable error) and is now parked awaiting operator intervention. An
// incident is created when the engine transitions a token to [TokenIncident].
type Incident struct {
	// ID is the unique identifier for this incident, generated deterministically
	// from InstanceState.IncidentSeq.
	ID string
	// TokenID is the ID of the token that encountered the error.
	TokenID string
	// NodeID is the node where the failure occurred.
	NodeID string
	// ScopeID is the execution scope of the failed token ("" = root scope).
	ScopeID string
	// CommandID is the ID of the command that triggered the failure (e.g. the
	// InvokeAction command whose response was ActionFailed).
	CommandID string
	// Error is the error message or error code reported by the failing action.
	Error string
	// Attempts is the total number of execution attempts made before the incident
	// was opened (includes the initial attempt plus all retries).
	Attempts int
	// CreatedAt is the time the incident was created.
	CreatedAt time.Time
}

// NodeVisit is one traversal of one node by one token (audit/history).
type NodeVisit struct {
	NodeID    string
	TokenID   string
	EnteredAt time.Time
	LeftAt    *time.Time
	ActorID   *string // who completed a human-task visit (later plans)
}

// compensationCursor tracks progress through an in-flight reverse-order
// compensation walk. It is set when a CompensateRequested trigger arrives and
// cleared when the walk completes (all targeted records processed).
//
// Fields:
//   - ScopeID: the scope whose records are being walked ("" = root scope /
//     RootCompensations). Used to locate the correct record slice on each step.
//   - ToNode: the rollback target — compensation walks back to (but not
//     including) this node. Empty means "roll back everything".
//   - NextIndex: the index into the relevant CompensationRecord slice of the
//     record currently in-flight (most recently emitted). The walk proceeds
//     from len(records)-1 down to 0 (reverse order). Initially set to
//     len(records)-1 (the most-recently completed record). Decremented by one
//     after each ActionCompleted while Status == StatusCompensating. The active
//     InvokeAction's CommandID is tracked in ActiveCmdID so ActionCompleted can
//     distinguish a compensation response from a normal one.
//   - ActiveCmdID: the CommandID of the in-flight compensation InvokeAction.
//     When ActionCompleted arrives with this CommandID and Status ==
//     StatusCompensating, the engine advances the cursor to the next record
//     rather than doing normal token routing.
//   - FinalStatus: the Status applied by stepCompensationFinish on a full
//     rollback (ToNode == ""). Zero ⇒ StatusTerminated (back-compat; admin path
//     and pre-migration in-flight compensations). StatusFailed for unhandled
//     errors; StatusTerminated for cancel.
//   - FinalErr: when non-empty, stepCompensationFinish appends
//     FailInstance{Err: FinalErr} on the full-rollback branch. The admin path
//     leaves this empty.
//
// cloneState deep-copies this struct via value copy (all fields are plain
// scalars — no pointers or maps). No additional deep-copy code is needed.
type compensationCursor struct {
	// ScopeID identifies the scope being compensated ("" = root).
	ScopeID string
	// ToNode is the rollback target node ID (exclusive). Empty = full rollback.
	ToNode string
	// NextIndex is the index of the CompensationRecord currently in-flight
	// (most recently emitted). Counts DOWN from len(records)-1 to 0 as
	// compensation actions complete; the next record to emit is NextIndex-1.
	NextIndex int
	// ActiveCmdID is the CommandID of the compensation InvokeAction currently
	// in flight. Cleared when the step completes.
	ActiveCmdID string
	// FinalStatus is the Status the instance must enter when the full-rollback
	// branch of stepCompensationFinish fires (toNode == ""). The zero value
	// (StatusRunning == 0) means UNSET: stepCompensationFinish maps it to
	// StatusTerminated (back-compat; admin full-rollback path and pre-migration
	// in-flight compensations deserialized from JSONB retain the prior
	// Terminated behaviour). Error/cancel paths that trigger compensation set
	// this explicitly: StatusFailed for unhandled errors, StatusTerminated for
	// cancel. This is always a terminal value at finish time — no caller of
	// beginCompensation ever wants a non-terminal final status here.
	FinalStatus Status
	// FinalErr is the error string passed to a FailInstance command when the
	// full-rollback branch completes. When non-empty, stepCompensationFinish
	// appends FailInstance{Err: FinalErr} to the result commands before clearing
	// the cursor. The admin path leaves this empty (no FailInstance emitted).
	FinalErr string
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

	// RootCompensations records completed compensable activities at the ROOT
	// (top-level) scope. It is the compensation list for the implicit root
	// execution scope — the counterpart of Scope.Compensations for sub-process
	// scopes, but stored directly on InstanceState so that s.Scopes remains
	// clean (containing ONLY currently-open sub-process scopes, as expected by
	// existing tests that assert on len(s.Scopes)).
	//
	// Plan 8 (compensation rollback) reads this in reverse order when rolling
	// back top-level compensable activities.
	RootCompensations []CompensationRecord

	// EventSubprocesses holds the set of pending arms for in-flight event
	// sub-processes. One entry per KindEventSubProcess node while its enclosing
	// scope is active. Entries are appended in definition-scan order (deterministic).
	// Removed when the trigger fires (one-shot) or when the enclosing scope closes.
	EventSubprocesses []eventSubprocessArm

	// Compensating tracks the in-flight reverse-order compensation walk, if any.
	// It is non-zero only while Status == StatusCompensating. The cursor is a
	// plain value struct (all scalar fields); cloneState copies it by value
	// automatically as part of the InstanceState struct copy — no extra
	// deep-copy code is required.
	Compensating compensationCursor

	// Incidents holds all open incident records for this instance. An incident is
	// created when a token transitions to [TokenIncident] (retry budget exhausted
	// or non-retryable error). Incidents are resolved (removed) when an operator
	// retries or skips the failed node.
	Incidents []Incident

	// Deterministic ID counters (never randomness or the clock).
	CmdSeq   int
	TokenSeq int
	TaskSeq  int
	TimerSeq int
	// ScopeSeq is the monotonic counter used to generate deterministic scope
	// IDs of the form "<instanceID>-s<ScopeSeq>".
	ScopeSeq int
	// IncidentSeq is the monotonic counter used to generate deterministic incident
	// IDs of the form "<instanceID>-inc<IncidentSeq>".
	IncidentSeq int
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
// This is called alongside cancelAllTimers on ALL terminal paths to prevent
// gateway and boundary timer arms from leaking as orphaned scheduled tasks in
// the runtime scheduler. Callers include:
//   - ActionFailed (unhandled error → StatusFailed)
//   - CancelRequested (admin cancel → StatusTerminated)
//   - SubInstanceFailed (child instance failed → parent StatusFailed)
//   - propagateError's unhandled-error terminal path
//   - stepCompensateRequested (cancels all in-flight tokens before compensating)
//
// EventSubprocesses arms are also drained on the terminal and compensation paths
// via removeEventSubprocessArmsForScope — this function does NOT drain them, so
// callers that need to cover ESP arms call removeEventSubprocessArmsForScope
// separately.
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

// eventSubprocessArmBySignal returns a pointer to the first eventSubprocessArm
// with the given signal name, or nil if none exists.
func (s *InstanceState) eventSubprocessArmBySignal(name string) *eventSubprocessArm {
	for i := range s.EventSubprocesses {
		if s.EventSubprocesses[i].Signal == name {
			return &s.EventSubprocesses[i]
		}
	}
	return nil
}

// eventSubprocessArmByTimer returns a pointer to the first eventSubprocessArm
// with the given timerID, or nil if none exists.
func (s *InstanceState) eventSubprocessArmByTimer(timerID string) *eventSubprocessArm {
	for i := range s.EventSubprocesses {
		if s.EventSubprocesses[i].TimerID == timerID {
			return &s.EventSubprocesses[i]
		}
	}
	return nil
}

// eventSubprocessArmByMessage returns a pointer to the first eventSubprocessArm
// whose Message matches name and whose MessageKey matches correlationKey, or nil.
func (s *InstanceState) eventSubprocessArmByMessage(name, correlationKey string) *eventSubprocessArm {
	for i := range s.EventSubprocesses {
		ea := &s.EventSubprocesses[i]
		if ea.Message == name && ea.MessageKey == correlationKey {
			return ea
		}
	}
	return nil
}

// removeEventSubprocessArm removes the single eventSubprocessArm for the given
// (enclosingScopeID, eventSubprocessNode) pair. It is a no-op if no such entry exists.
func (s *InstanceState) removeEventSubprocessArm(enclosingScopeID, espNode string) {
	out := make([]eventSubprocessArm, 0, len(s.EventSubprocesses))
	for _, ea := range s.EventSubprocesses {
		if ea.EnclosingScopeID == enclosingScopeID && ea.EventSubprocessNode == espNode {
			continue
		}
		out = append(out, ea)
	}
	s.EventSubprocesses = out
}

// removeEventSubprocessArmsForScope removes all eventSubprocessArm entries
// whose EnclosingScopeID matches the given scopeID, returning the TimerIDs of
// any timer-armed entries so the caller can emit CancelTimer commands.
func (s *InstanceState) removeEventSubprocessArmsForScope(scopeID string) []string {
	var cancelTimerIDs []string
	out := make([]eventSubprocessArm, 0, len(s.EventSubprocesses))
	for _, ea := range s.EventSubprocesses {
		if ea.EnclosingScopeID == scopeID {
			if ea.TimerID != "" {
				cancelTimerIDs = append(cancelTimerIDs, ea.TimerID)
			}
			continue
		}
		out = append(out, ea)
	}
	s.EventSubprocesses = out
	return cancelTimerIDs
}

// recordCompensation appends a CompensationRecord to the scope identified by
// scopeID. If scopeID is "" (root-level token), the record is appended to
// s.RootCompensations — the root-scope compensation list that is stored directly
// on the InstanceState rather than in a Scope entry. This keeps s.Scopes clean
// (containing only currently-open sub-process scopes) so that existing tests that
// assert on len(s.Scopes) are unaffected.
//
// If scopeID is non-empty and the scope is not found (defensive: should not occur
// in a well-formed state), the call is a no-op.
func (s *InstanceState) recordCompensation(scopeID, nodeID, action string, completedAt time.Time, input map[string]any) {
	rec := CompensationRecord{
		NodeID:      nodeID,
		Action:      action,
		CompletedAt: completedAt,
		Input:       input,
	}
	if scopeID == "" {
		s.RootCompensations = append(s.RootCompensations, rec)
		return
	}
	scope := s.scopeByID(scopeID)
	if scope == nil {
		return // defensive no-op
	}
	scope.Compensations = append(scope.Compensations, rec)
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

// hoistCompensations moves childID's accumulated compensation records into its
// parent (parentID), appended in completion order, so they remain rollback-able
// after the child scope closes. parentID "" targets RootCompensations. The
// child's own slice is cleared. No-op if the child has no records or is not found.
func (s *InstanceState) hoistCompensations(childID, parentID string) {
	child := s.scopeByID(childID)
	if child == nil || len(child.Compensations) == 0 {
		return
	}
	if parentID == "" {
		s.RootCompensations = append(s.RootCompensations, child.Compensations...)
	} else if parent := s.scopeByID(parentID); parent != nil {
		// The parent always exists here by construction: scopes close child-first,
		// so a closing scope's parent is either root ("") or a still-open ancestor.
		parent.Compensations = append(parent.Compensations, child.Compensations...)
	}
	child.Compensations = nil
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
