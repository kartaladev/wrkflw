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
// It is the key [Step] tests before dispatching any trigger to its handler: each
// [Trigger] declares what it does on a terminal instance, and that declaration is
// applied in one place rather than re-checked per handler (ADR-0165). The check
// is also the defence against the TOCTOU race in which an instance reaches a
// terminal status between a caller's own pre-check load and the engine's state —
// a caller-side check can only ever be advisory.
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
	// AwaitTimer is the scheduled timer id this token is parked on (plain timer
	// intermediate catch event). It is an ENUMERATION MARKER, not a dispatch key:
	// the same id is written to AwaitCommand, which is what handleTimerFired
	// still routes on, so this field is purely additive (ADR-0177).
	//
	// It exists because AwaitCommand is overloaded — measured holding human-task
	// ids, event-gateway sentinels, action command ids, timer ids and "" — so a
	// timer park is not identifiable from state alone without a dedicated field.
	// [InstanceState.TimerTokenWaiters] is its only reader.
	//
	// ⚠ It must be CLEARED wherever AwaitCommand is cleared; see
	// [Token.clearAwait], which is the only writer of the empty value. Left set,
	// [InstanceState.HasArmedTimers] reports true forever for a token that is
	// waiting on nothing.
	AwaitTimer string
	Payload    map[string]any
	EnteredAt  time.Time

	// RetryAttempts is the number of execution attempts already made for this
	// token's current node (0 = first attempt has not started yet, 1 = one
	// attempt has completed or failed, etc.).
	RetryAttempts int
	// RetryStartedAt is the wall-clock time when the first retry attempt was
	// initiated. It serves as the anchor for MaxElapsed budget calculations.
	// Zero value means the token is not currently retrying.
	RetryStartedAt time.Time
}

// clearAwait drops the await markers a token stops holding the moment it is
// resumed: the command/task/timer id it parked on ([Token.AwaitCommand]) and
// the timer-catch enumeration marker ([Token.AwaitTimer]).
//
// It exists so the two can never drift. AwaitTimer is written alongside
// AwaitCommand at the plain timer intermediate-catch arm site, and left set it
// makes [InstanceState.TimerWaiters] report an arm the scheduler no longer
// holds — the inverted-purpose defect ADR-0177's audit caught. Every site that
// clears AwaitCommand calls this instead; that is the invariant, not an
// optimisation.
//
// It deliberately leaves AwaitSignal/AwaitMessage alone: those are cleared (or
// not) by the signal/message resume paths on their own terms, and folding them
// in here would change behaviour rather than preserve it.
func (t *Token) clearAwait() {
	t.AwaitCommand = ""
	t.AwaitTimer = ""
}

// IncidentKind discriminates what an [Incident] is about, so a reader can tell
// a failed action apart from a compensation walk that stopped reporting back.
//
// Its zero value is IncidentAction, which is what every incident raised before
// ADR-0175 means — so an existing record keeps its meaning without migration.
type IncidentKind int

const (
	// IncidentAction is a service action that failed and exhausted its retries.
	// It names a token, and resolving it re-invokes that token's action.
	IncidentAction IncidentKind = iota
	// IncidentCompensationStall is a dispatched compensation action that has not
	// reported back within StepOptions.CompensationStallAfter (ADR-0175). It is
	// walk-scoped: TokenID is empty, because a stalled walk holds no tokens.
	//
	// It must NOT be resolved through ResolveIncident — see handleResolveIncident,
	// which refuses it and names the three escape verbs instead.
	IncidentCompensationStall
)

// String returns the name of the IncidentKind for debugging/logging.
func (k IncidentKind) String() string {
	switch k {
	case IncidentAction:
		return "IncidentAction"
	case IncidentCompensationStall:
		return "IncidentCompensationStall"
	default:
		return "IncidentKind(unknown)"
	}
}

// Incident records work that has stopped and needs operator attention: a token
// that exhausted its retry budget or hit a non-retryable error (IncidentAction,
// created as the engine moves that token to [TokenIncident]), or a compensation
// walk whose dispatched action stopped reporting back (IncidentCompensationStall,
// ADR-0175). See [IncidentKind] — the two are resolved by different operations.
type Incident struct {
	// ID is the unique identifier for this incident, generated deterministically
	// from InstanceState.IncidentSeq.
	ID string
	// Kind discriminates what this incident is about. The zero value,
	// IncidentAction, is what every pre-ADR-0175 record means.
	//
	// ⚠ Kind enters the persisted snapshot. An OLD build round-trips a NEW
	// snapshot with Kind dropped, degrading an IncidentCompensationStall into a
	// resolvable IncidentAction that the shipped resolve-incident endpoint will
	// then delete — the exact data loss the refusal exists to prevent. Do not run
	// pre-0175 and post-0175 builds against the same instance store.
	Kind IncidentKind
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

	// Timers is the auxiliary bookkeeping table for the timers the engine
	// dispatches by RECORD: deadline, in-wait/reminder, retry and
	// compensation-stall. It is NOT every scheduled timer — the arm-borne
	// sources (timer boundaries, event-gateway timer arms, event-sub-process
	// timer arms and plain timer intermediate catch events) arm a timer without
	// writing a record here, so a len(Timers) test is not a test for "any armed
	// timer". [InstanceState.TimerWaiters] is the authority spanning all five
	// sources (ADR-0177).
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
	// It is non-zero only while Status == StatusCompensating. Its scalar fields
	// are carried by the InstanceState struct copy in cloneState; its one
	// non-scalar field — the pinned Records source added by ADR-0171 — is
	// deep-copied there explicitly.
	Compensating compensationCursor

	// Incidents holds all open incident records for this instance. An incident is
	// created when a token transitions to [TokenIncident] (retry budget exhausted
	// or non-retryable error). Incidents are resolved (removed) when an operator
	// resolves the incident (re-invoking the failed action via ResolveIncident).
	Incidents []Incident

	// PendingCancel is set when a CancelRequested — or, since ADR-0170, an
	// unhandled error — arrives while a resuming compensation walk is in flight;
	// that walk finishes, then runs a full walk over the remaining records and
	// terminates instead of resuming, avoiding double-compensation of the records
	// the in-flight walk already consumed (ADR-0039 B1 fix).
	PendingCancel bool

	// PendingFinalStatus / PendingFinalErr are the terminal outcome the deferred
	// termination described by PendingCancel must apply when the in-flight walk
	// finishes. They are read by applyFinish and cleared as they are consumed.
	//
	// BOTH deferral sites stamp them EXPLICITLY: handleCancelRequested writes
	// StatusTerminated + "cancelled" (the ADR-0039 cancel outcome) and
	// handleUnhandledError writes StatusFailed + the uncaught error code
	// (ADR-0170), so whichever trigger arrives last owns the outcome. They did
	// not always: the cancel path once set PendingCancel alone and leaned on
	// applyFinish's zero-value default, which a preceding deferred error had
	// already displaced — measured, a cancel then terminated the instance
	// `failed` carrying the superseded error's code.
	//
	// The ZERO value (StatusRunning == 0, "") consequently no longer accompanies
	// a SET PendingCancel in any state this engine writes — applyFinish zeroes
	// the pair, but only as it clears PendingCancel with it. applyFinish still
	// maps that combination to the cancel outcome, for exactly one input: a
	// snapshot persisted by an older build, whose PendingCancel came from the
	// pre-fix cancel path.
	//
	// Both are scalars, so cloneState's struct copy carries them and the
	// persisted JSON snapshot round-trips them with no extra code. Engine
	// bookkeeping: like Compensating and PendingCancel they are deliberately
	// absent from the service.ProcessInstance projection.
	PendingFinalStatus Status
	PendingFinalErr    string

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

// removeOrphanedIncidents drops every incident whose TokenID names a token that
// is no longer present, and keeps the rest in slice order so command output
// stays deterministic. It is the terminal-site counterpart of
// removeIncidentsForToken (ADR-0163): the two terminal paths that drop every
// token — forceTerminate and handleCancelRequested's immediate branch — never
// route through cancelTokenWaits, so without this an incident outlives the token
// it describes.
//
// Deliberately NOT s.Incidents = nil. A terminal instance whose tokens survive
// (handleUnhandledError's immediate branch, handleSubInstanceFailed's tail)
// keeps its incidents, because runtime/outbox.go's terminalEventErr, the
// service/ audit view and incident_count all read them after the instance is
// terminal (ADR-0164 Decision 3).
//
// An incident with an empty TokenID is KEPT, mirroring removeIncidentsForToken:
// an empty key names nothing (ADR-0152), so such a record names no token to be
// orphaned FROM. Leaving it to tokenByID would invert that rule — tokenByID("")
// reports nil, which this predicate would read as "the token is gone" and
// delete an incident the keep-token sites are supposed to keep.
//
// ⚠ This was written as "a guard against the next terminal site rather than a
// live defect", because no production path built an empty-TokenID incident.
// ADR-0175 IS that next site: handleCompensationStallFired raises a walk-scoped
// incident with TokenID "". The keep is now load-bearing rather than
// speculative — which is also why retiring a stall incident needs its own sweep
// (retireCompensationStallIncidents) instead of falling out of this one.
func (s *InstanceState) removeOrphanedIncidents() {
	s.Incidents = slices.DeleteFunc(s.Incidents, func(inc Incident) bool {
		return inc.TokenID != "" && s.tokenByID(inc.TokenID) == nil
	})
}

// retireCompensationStallIncidents removes every open IncidentCompensationStall
// raised against commandID (ADR-0175). An empty commandID names no incident and
// retires nothing, mirroring ADR-0152.
//
// It is called wherever a walk moves on — stepCompensationAdvance, and the
// escape verbs before they delegate to a finish — plus a final sweep in
// endInstance. Without it, a walk that recovered on its own carries a stale
// "compensation action stalled" incident into its terminal state, where
// runtime/outbox.go's terminalEventErr and runtime/processdriver_action.go's
// terminalErr both read Incidents[0] unconditionally and would publish it as the
// instance's cause of death — to the outbox, and to a call-activity parent.
func (s *InstanceState) retireCompensationStallIncidents(commandID string) {
	if commandID == "" {
		return
	}
	s.Incidents = slices.DeleteFunc(s.Incidents, func(inc Incident) bool {
		return inc.Kind == IncidentCompensationStall && inc.CommandID == commandID
	})
}

// endInstance performs the terminal transition: status, EndedAt, a cleared
// compensation cursor, the orphaned-incident sweep, and the projection sweeps
// every terminal path owes.
//
// Clearing s.Compensating makes beginCompensation's documented invariant
// ("s.Compensating is the zero cursor here") true by construction.
//
// Call it at the site's existing terminal position. The two sites that drop
// tokens do so BEFORE this call, which is what lets removeOrphanedIncidents
// retire their incidents; hoisting it above the token drop would silently
// retain them.
//
// The terminal command is threaded through rather than appended by the caller so
// the emitted order stays [task cancels…, terminal, scheduled-work cancels…] —
// exactly what applyTerminate, handleUnhandledError, forceTerminate and
// handleCancelRequested emit today. Pass nil where a site emits no terminal
// command of its own.
func (s *InstanceState) endInstance(status Status, at time.Time, terminal Command) []Command {
	s.Status = status
	ended := at
	s.EndedAt = &ended
	// Harvest BEFORE the cursor clear, and before the scopes are dropped. Both halves
	// of that ordering are load-bearing (ADR-0174):
	//
	//   - Before the CLEAR, because partitionForLiveWalk drops a live scope-wide
	//     walk's already-dispatched records, and forceTerminate reaches here with such
	//     a cursor still live — it clears no cursor and closes no scope. Harvesting
	//     after the clear re-archives them and a later rollback re-runs them: measured
	//     archived=[undoA undoB undoC] and a rollback re-dispatching [undoC undoB undoA]
	//     where only [undoA] was owed.
	//   - Before s.Scopes = nil, or there is nothing left to harvest from.
	s.harvestOpenScopeCompensations()
	// Retire any stall incident still open, BEFORE the cursor clear takes away the
	// ActiveCmdID that names it (ADR-0175). The walk ends here, so an incident
	// saying it is stalled is stale by construction — and leaving one behind makes
	// it Incidents[0] on a terminal instance, which runtime/outbox.go's
	// terminalEventErr and runtime/processdriver_action.go's terminalErr publish
	// unconditionally as the cause of death.
	//
	// This is the REMAINDER sweep. The routes that move a walk on retire their own
	// incident first — a late ActionCompleted, a late ActionFailed and the skip
	// verb all through stepCompensationAdvance, and retry and abandon explicitly —
	// so what reaches here is a walk killed mid-flight. The route this is measured
	// on is a force-termination end event; see
	// TestForceTerminationSweepsOpenStallIncident.
	s.retireCompensationStallIncidents(s.Compensating.ActiveCmdID)
	s.Compensating = compensationCursor{}
	s.removeOrphanedIncidents()
	cmds := s.cancelOpenTasks()
	if terminal != nil {
		cmds = append(cmds, terminal)
	}
	cmds = append(cmds, s.cancelAllScheduledWork()...)
	// Close every scope the instance still had open. Before ADR-0174 a terminal
	// snapshot could carry one — ADR-0162 deferred closing them to ADR-0164, and 0164
	// shipped without it. nil rather than an empty slice: it matches the
	// normalization ADR-0173 chose for ArchivedCompensations, and no reader
	// distinguishes them (every one uses len/range, and no reader exists outside this
	// package). Note this changes the persisted shape from "Scopes":[] to
	// "Scopes":null on EVERY terminal transition, ordinary completions included.
	s.Scopes = nil
	return cmds
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

// spawnsNewWork reports whether the instance may still START new work — open a
// scope, place a token, dispatch an action. ADR-0172: a dying instance spawns no
// new work, whichever scope an arm belongs to.
//
// ⚠ It is an ALLOW-LIST, and that is load-bearing. Status.IsTerminal treats an
// out-of-range Status as NOT terminal (see its doc comment), so a deny-list
// predicate — "return false if terminal or dying" — starts FIRING arms on an
// unrecognised status. Measured: an armed root event-sub-process arm is silenced
// on an out-of-range status today, and a deny-list formulation fires it. Any new
// Status constant must be added here explicitly; until it is, it fails closed.
func (s *InstanceState) spawnsNewWork() bool {
	switch s.Status {
	case StatusRunning:
		return true
	case StatusCompensating:
		// A rollback that will RESUME (compensation throw, partial rollback) is a
		// legitimately running instance; one that will END it is not.
		return !s.Compensating.walkTerminates(s.PendingCancel)
	default:
		// Terminal, or out of range.
		return false
	}
}

// hasOpenStallIncident reports whether an open IncidentCompensationStall with
// the given id exists. An empty id names no incident (ADR-0152).
func (s *InstanceState) hasOpenStallIncident(incidentID string) bool {
	if incidentID == "" {
		return false
	}
	for _, inc := range s.Incidents {
		if inc.ID == incidentID && inc.Kind == IncidentCompensationStall {
			return true
		}
	}
	return false
}
