package engine

import (
	"fmt"
	"sort"
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// timerRecord is the engine's internal bookkeeping entry for a scheduled timer.
// It allows the engine to route a TimerFired back to the correct token and task
// without relying on the token's AwaitCommand (which is set to the TaskToken for
// user-task nodes, not the deadline timer ID).
//
// For intermediate-catch-event timers the TaskToken field is empty because the
// token parks on the TimerID itself and the tokenAwaiting lookup still works.
// Recording them here provides a single, unified dispatch table.
type timerRecord struct {
	// TimerID is the unique timer identifier emitted in ScheduleTimer.
	TimerID string
	// Kind discriminates intermediate, deadline, and in-wait timers.
	Kind TimerKind
	// Token is the ID of the parked engine token this timer guards.
	Token string
	// TaskToken is the human-task correlation token ("" for intermediate timers).
	TaskToken string
	// NodeID is the BPMN node that owns the timer (needed to resolve DeadlineFlow/DeadlineAction).
	NodeID string
	// ScopeID is the execution scope of the token that owns this timer. Empty
	// string means the root scope. Used to resolve the correct nested definition
	// when a deadline or reminder timer fires inside a sub-process.
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
	// (the default), true = non-interrupting.
	NonInterrupting bool
	// TimerID is the scheduled timer id for timer boundary events. Empty for
	// signal/message boundary events.
	TimerID string
	// Signal is the signal name for signal boundary events. Empty for
	// timer/message boundary events.
	Signal string
	// Message is the message name for message boundary events. Empty for
	// timer/signal boundary events.
	Message string
	// MessageKey is the resolved correlation key for message boundary events
	// (empty if no CorrelationKey was configured — match on message name alone).
	MessageKey string
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
	// NonInterrupting mirrors the event sub-process's non-interrupting flag (see
	// eventSubprocessNested — the legacy node's own flag, or the SubProcess-form
	// inner event-triggered start's flag). false = interrupting (the default);
	// true = non-interrupting.
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
	// removeEventTriggeredSubprocessArmsForScope (by TimerID), not by token lookup.
	TimerID string
	// Message is the message name for message-triggered event sub-processes.
	Message string
	// MessageKey is the resolved correlation key for message-triggered event sub-processes.
	MessageKey string
}

// CompensationRecord is a record of a completed compensable activity within a
// scope. Plan 8 (compensation/rollback) populates these records when an activity
// with a non-empty CompensateAction completes; it walks them in reverse
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

// String returns the canonical lowercase name of the status ("running",
// "completed", "failed", "compensating", "terminated"); out-of-range values map
// to "unknown". It implements [fmt.Stringer], so a Status formats correctly with
// %s/%v, and is the canonical source of the string form used by the runtime view
// DTOs (see runtime/view.StatusString).
func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCompensating:
		return "compensating"
	case StatusTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

// IsTerminal reports whether s is one of the instance's terminal statuses
// (StatusCompleted, StatusFailed, StatusTerminated) — a status from which no
// further trigger may resume normal execution. StatusRunning and
// StatusCompensating (mid-flight) are not terminal; an out-of-range Status
// value is also treated as not terminal.
//
// Used by stepCompensateRequested to reject a reverse trigger
// (CompensateRequested.ReverseNode != "") against an already-terminal
// instance (ADR-0109 hardening) — a defense-in-depth guard against the TOCTOU
// race where an instance completes between a caller's pre-check Load and the
// engine's own state.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusTerminated:
		return true
	default:
		return false
	}
}

// TokenState is the execution state of a single token.
type TokenState int

const (
	TokenActive TokenState = iota
	TokenWaitingCommand
	TokenAtJoin
	// TokenIncident marks a token that has exhausted its retry budget (or hit a
	// non-retryable error) and is now parked as an incident. The token remains in
	// this state until an operator resolves the incident (re-invoking the action
	// via ResolveIncident).
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
//     RootCompensations). Used to locate the correct record slice on each step
//     when ArchiveKey is empty.
//   - ArchiveKey: when non-empty, the walk is drawing records from
//     ArchivedCompensations[ArchiveKey] rather than a live scope or
//     RootCompensations. Set by the compensation throw producer (Phase 3);
//     empty for all beginCompensation (admin / cancel / error) walks.
//   - ResumeNode: when non-empty, stepCompensationFinish resumes execution at
//     this node (sets Status = StatusRunning, places a token here) instead of
//     applying the terminal FinalStatus. Used by the compensation throw walk to
//     continue past the throw event after the archived compensation records have
//     been run. Empty for all beginCompensation (admin / cancel / error) walks.
//   - ResumeScope: the ScopeID of the token that triggered the compensation
//     throw. Used alongside ResumeNode so placeTokenInScope restores the token
//     to the correct scope after the throw walk finishes. Empty means root scope.
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
//     rollback (ToNode == "" and ResumeNode == ""). Zero ⇒ StatusTerminated
//     (back-compat; admin path and pre-migration in-flight compensations).
//     StatusFailed for unhandled errors; StatusTerminated for cancel.
//   - FinalErr: when non-empty, stepCompensationFinish appends
//     FailInstance{Err: FinalErr} on the full-rollback branch. The admin path
//     leaves this empty.
//
// cloneState deep-copies this struct via value copy (all fields are plain
// scalars — no pointers or maps). No additional deep-copy code is needed.
type compensationCursor struct {
	// ScopeID identifies the scope being compensated ("" = root).
	// Ignored when ArchiveKey is non-empty (archive walk).
	ScopeID string
	// ArchiveKey is the ArchivedCompensations map key for a throw-walk
	// (non-empty). When set, cursorRecords reads from ArchivedCompensations[ArchiveKey]
	// instead of the live scope or RootCompensations. Empty for all
	// beginCompensation (admin / cancel / error) walks.
	ArchiveKey string
	// ResumeNode is the node to place a token at when the compensation throw
	// walk finishes (the throw event's single successor). When non-empty,
	// stepCompensationFinish sets Status = StatusRunning and places a token
	// here instead of applying FinalStatus. Empty for admin / cancel / error
	// walks (which always terminate).
	ResumeNode string
	// ResumeScope is the ScopeID of the scope in which the token should be
	// placed at ResumeNode after the throw walk finishes. Empty = root scope.
	// Populated by the compensation throw producer from the throw token's ScopeID.
	ResumeScope string
	// ToNode is the rollback target node ID (exclusive). Empty = full rollback.
	ToNode string
	// ReverseNode, when non-empty, makes the FULL-rollback finish resume at this
	// node (StatusRunning) instead of terminating — the ReverseInstance full-reverse
	// form (ADR-0109). Kept DISTINCT from ResumeNode (the compensation-throw resume)
	// so the throw-walk branch is never triggered by a reverse-to-start walk.
	ReverseNode string
	// ReverseResetVars, when true, resets Variables to StartVariables when the
	// full-rollback finish resumes at ReverseNode.
	ReverseResetVars bool
	// RestoreTargetVars, when true, makes the PARTIAL-rollback finish (ToNode
	// non-empty) restore Variables to ToNode's own start-of-visit snapshot — the
	// Input captured on ToNode's most-recent compensation record — instead of
	// leaving the current variables untouched (FU#1, ADR-0116). Carried on the
	// cursor (all-scalar) from the CompensateRequested trigger; the snapshot map
	// itself is resolved at finish time and lives on the transient finishPlan, so
	// the cursor stays value-copyable by cloneState with no map to deep-copy.
	// Zero (false) for every other walk — admin partial rollback keeps current
	// vars, matching ADR-0109's original WithTargetNode contract.
	RestoreTargetVars bool
	// StartRecordCount is the number of records present in the throwing scope
	// when a SCOPE-WIDE compensation throw walk started (ADR-0120). The walk
	// drains exactly the prefix [0 .. StartRecordCount-1] in reverse; on finish,
	// only that prefix is cleared (compensate-once), retaining any record a still-
	// running sibling appended mid-walk at index >= StartRecordCount (review A1).
	// Zero for every other walk (targeted throw, admin/cancel/error), which clear
	// their whole record source instead. Carried on the cursor (scalar) so
	// cloneState stays a plain value copy.
	StartRecordCount int
	// NextIndex is the index of the CompensationRecord currently in-flight
	// (most recently emitted). Counts DOWN from len(records)-1 to 0 as
	// compensation actions complete; the next record to emit is NextIndex-1.
	NextIndex int
	// ActiveCmdID is the CommandID of the compensation InvokeAction currently
	// in flight. Cleared when the step completes.
	ActiveCmdID string
	// FinalStatus is the Status the instance must enter when the full-rollback
	// branch of stepCompensationFinish fires (toNode == "" and ResumeNode == "").
	// The zero value (StatusRunning == 0) means UNSET: stepCompensationFinish
	// maps it to StatusTerminated (back-compat; admin full-rollback path and
	// pre-migration in-flight compensations deserialized from JSONB retain the
	// prior Terminated behaviour). Error/cancel paths that trigger compensation
	// set this explicitly: StatusFailed for unhandled errors, StatusTerminated
	// for cancel. This is always a terminal value at finish time — no caller of
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
	// StartVariables is an immutable copy of the variables the instance began with,
	// captured once on StartInstance. Used by a full ReverseInstance to restore a
	// fresh slate when resuming at the start node.
	StartVariables map[string]any
	Tokens         []Token
	StartedAt      time.Time
	EndedAt        *time.Time
	History        []NodeVisit

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

	// ArchivedCompensations holds completed sub-process compensation records keyed
	// by the sub-process node id. On normal scope exit, scope.Compensations are moved
	// here (instead of being hoisted to the parent) so scope identity survives for
	// scope-targeted compensation (ADR-0039). A root/instance walk consolidates these
	// into RootCompensations before traversal via consolidateArchiveIntoRoot.
	ArchivedCompensations map[string][]CompensationRecord

	// EventTriggeredSubprocesses holds the set of pending arms for in-flight
	// event sub-processes. One entry per event sub-process node (see
	// eventSubprocessNested) while its enclosing scope is active. Entries are
	// appended in definition-scan order (deterministic). Removed when the
	// trigger fires (one-shot) or when the enclosing scope closes.
	EventTriggeredSubprocesses []eventTriggeredSubprocessArm

	// Compensating tracks the in-flight reverse-order compensation walk, if any.
	// It is non-zero only while Status == StatusCompensating. The cursor is a
	// plain value struct (all scalar fields); cloneState copies it by value
	// automatically as part of the InstanceState struct copy — no extra
	// deep-copy code is required.
	Compensating compensationCursor

	// Incidents holds all open incident records for this instance. An incident is
	// created when a token transitions to [TokenIncident] (retry budget exhausted
	// or non-retryable error). Incidents are resolved (removed) when an operator
	// resolves the incident (re-invoking the failed action via ResolveIncident).
	Incidents []Incident

	// PendingCancel is set when a CancelRequested arrives while a compensation
	// THROW walk is in flight; the throw walk finishes, then runs a full cancel
	// over the remaining records and terminates instead of resuming — avoids
	// double-compensating the throw's in-flight records (ADR-0039 B1 fix).
	PendingCancel bool

	// DeferredCompensationThrows holds the token IDs of compensation-throw tokens
	// that were reached while a compensation walk was already in flight
	// (Compensating.ActiveCmdID != ""). The single-cursor model permits at most
	// one walk in flight, so concurrent throws (parallel branches processed in one
	// Macro drive pass) are SERIALIZED: the second+ throw tokens are parked
	// (TokenWaitingCommand, not consumed) and enqueued here. stepCompensationFinish
	// re-activates exactly one per finish, draining the queue one walk at a time
	// (ADR-0071). It is engine bookkeeping (persisted with the state, excluded from
	// the service.ProcessInstance JSON projection like Compensating/PendingCancel).
	DeferredCompensationThrows []string

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

// cancelOpenTasks marks every OPEN human task (Unclaimed or Claimed) Cancelled
// and returns an UpdateTask command for each, so the TaskStore projection is
// reconciled when the instance is terminated — otherwise a cancelled instance
// leaves its parked tasks visible in inbox queries (ClaimableBy / AssignedTo).
// Already-resolved tasks (Completed or Cancelled) are left untouched. Tasks are
// visited in slice order for deterministic command output. See ADR-0088.
func (s *InstanceState) cancelOpenTasks() []Command {
	var cmds []Command
	for i := range s.Tasks {
		if s.Tasks[i].IsOpen() {
			s.Tasks[i].State = humantask.Cancelled
			cmds = append(cmds, UpdateTask{Task: s.Tasks[i]})
		}
	}
	return cmds
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
// timers when a deadline breach or task completion supersedes them.
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

// cancelTimersForToken removes all timer records whose Token matches the given
// parked-token id (excluding excludeTimerID), returning their TimerIDs so the
// caller can emit CancelTimer commands. It is the token-keyed counterpart of
// cancelTimersByTaskToken, used to cancel a parked token's in-wait reminder when
// its wait resolves or its scope is interrupted (ReceiveTask / IntermediateCatchEvent
// have no human-task correlation token).
func (s *InstanceState) cancelTimersForToken(tokenID, excludeTimerID string) []string {
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.Token == tokenID && tr.TimerID != excludeTimerID {
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

// removeEventTriggeredSubprocessArm removes the single
// eventTriggeredSubprocessArm for the given (enclosingScopeID,
// eventSubprocessNode) pair. It is a no-op if no such entry exists.
func (s *InstanceState) removeEventTriggeredSubprocessArm(enclosingScopeID, espNode string) {
	out := make([]eventTriggeredSubprocessArm, 0, len(s.EventTriggeredSubprocesses))
	for _, ea := range s.EventTriggeredSubprocesses {
		if ea.EnclosingScopeID == enclosingScopeID && ea.EventSubprocessNode == espNode {
			continue
		}
		out = append(out, ea)
	}
	s.EventTriggeredSubprocesses = out
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

// archiveCompensations moves the Compensations of the scope identified by scopeID
// into ArchivedCompensations keyed by that scope's NodeID (the sub-process node
// that opened the scope). This replaces hoistCompensations on normal sub-process
// exit (ADR-0039): scope identity is preserved for scope-targeted compensation,
// and records are still reachable by the root/instance walk via
// consolidateArchiveIntoRoot. A sub-process entered more than once accumulates
// records in the same archive slot. No-op if the scope has no records or is not
// found.
func (s *InstanceState) archiveCompensations(scopeID string) {
	scope := s.scopeByID(scopeID)
	if scope == nil || len(scope.Compensations) == 0 {
		return
	}
	if s.ArchivedCompensations == nil {
		s.ArchivedCompensations = make(map[string][]CompensationRecord)
	}
	s.ArchivedCompensations[scope.NodeID] = append(s.ArchivedCompensations[scope.NodeID], scope.Compensations...)
	scope.Compensations = nil
}

// consolidateArchiveIntoRoot moves all ArchivedCompensations into RootCompensations,
// then stable-sorts the combined slice by (CompletedAt ascending, NodeID ascending
// tiebreak) for determinism, then sets ArchivedCompensations to nil. This is called
// by beginCompensation before the root/instance walk so the walk sees root + all
// archived sub-process records as one reverse-ordered sequence. Records are MOVED
// (single ownership: once consolidated, the archive is empty; stepCompensationFinish
// clears RootCompensations on walk finish, covering everything).
func (s *InstanceState) consolidateArchiveIntoRoot() {
	if len(s.ArchivedCompensations) == 0 {
		return
	}
	// Deterministic iteration: sort archive keys.
	keys := make([]string, 0, len(s.ArchivedCompensations))
	for k := range s.ArchivedCompensations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.RootCompensations = append(s.RootCompensations, s.ArchivedCompensations[k]...)
	}
	s.ArchivedCompensations = nil
	// Stable-sort combined slice by CompletedAt asc, NodeID asc tiebreak.
	sort.SliceStable(s.RootCompensations, func(i, j int) bool {
		if s.RootCompensations[i].CompletedAt.Equal(s.RootCompensations[j].CompletedAt) {
			return s.RootCompensations[i].NodeID < s.RootCompensations[j].NodeID
		}
		return s.RootCompensations[i].CompletedAt.Before(s.RootCompensations[j].CompletedAt)
	})
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
