package engine

import (
	"slices"
	"time"

	"github.com/kartaladev/wrkflw/humantask"
)

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
	// TokenActive marks a token the engine may advance on the next step.
	TokenActive TokenState = iota
	// TokenWaiting marks a token parked on an external trigger — an outstanding
	// action command (AwaitCommand), a signal (AwaitSignal), a message
	// (AwaitMessage), or a human task. It is the general "parked, not consumed"
	// state; the Await* fields say what is awaited (ADR-0145).
	TokenWaiting
	// TokenJoining marks a token that has arrived at a join gateway and is
	// waiting for its sibling branches to arrive (ADR-0145).
	TokenJoining
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
	// TaskID links a user-task visit to the human task minted for it, so a
	// rendered history can resolve who claimed/completed it (and with which
	// outcome) from the task record instead of duplicating that audit on the
	// visit. Empty on every other node kind. See ADR-0145.
	TaskID string
	// CloseKind is why the visit closed, recorded only for an ABNORMAL close
	// (see the CloseKind* constants). A normal advance — the token completed the
	// node and moved on — leaves it empty. See ADR-0145.
	CloseKind CloseKind
}

// InstanceState is the authoritative snapshot of a running instance.
type InstanceState struct {
	// ids is the transient, per-Step id-generation seam (ADR-0149). It is
	// unexported: never serialized, never part of the durable snapshot, and
	// scrubbed by Step before the state is returned.
	ids idSource

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
	// (TokenWaiting, not consumed) and enqueued here. stepCompensationFinish
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

// TaskByID returns a pointer to the HumanTask with the given taskID, or
// nil if no such task exists in the state. An empty taskID names no task
// (ADR-0152).
func (s *InstanceState) TaskByID(taskID string) *humantask.HumanTask {
	if taskID == "" {
		return nil
	}
	for i := range s.Tasks {
		if s.Tasks[i].TaskID == taskID {
			return &s.Tasks[i]
		}
	}
	return nil
}

// removeIncidentsForToken drops every incident raised against tokenID. An empty
// tokenID matches nothing (ADR-0152: an empty key names nothing, and admitting
// it would make every token with a blank ID wipe the incident list). The
// remaining records keep their relative order so command output stays
// deterministic.
func (s *InstanceState) removeIncidentsForToken(tokenID string) {
	if tokenID == "" {
		return
	}
	s.Incidents = slices.DeleteFunc(s.Incidents, func(inc Incident) bool {
		return inc.TokenID == tokenID
	})
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
			// Clone before the record escapes: the command is handed to a
			// consumer-supplied TaskStore while the record it was built from is
			// committed as instance state, so a shallow copy would share the
			// Claim/Completion pointees, the Vars map and the actor slices
			// across that boundary (ADR-0163). HumanTask.Clone is the single
			// deep-copy definition for a task.
			cmds = append(cmds, UpdateTask{Task: s.Tasks[i].Clone()})
		}
	}
	return cmds
}

// Clone returns a deep copy of the InstanceState. All slice and map fields are
// independently allocated so that mutations to the returned state do not affect
// the receiver (and vice versa).
func (s InstanceState) Clone() InstanceState {
	return cloneState(s)
}
