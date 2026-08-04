// Package engine is the pure token state machine (ADR-0002). Step maps
// (definition, state, Trigger) -> (state, []Command) with no I/O, no clock
// reads, and no goroutines.
package engine

import (
	"time"

	"github.com/kartaladev/wrkflw/authz"
)

// Trigger is the sealed set of things that drive the next step: initiating
// causes and returning results. The unexported marker keeps the set closed.
type Trigger interface {
	isTrigger()
	OccurredAt() time.Time
}

type baseTrigger struct{ at time.Time }

func (b baseTrigger) OccurredAt() time.Time { return b.at }
func (baseTrigger) isTrigger()              {}

// StartInstance begins a new process instance with initial variables.
type StartInstance struct {
	baseTrigger
	Vars map[string]any
	// StartNodeID is the start node to seed. Empty resolves the definition's
	// manual (trigger-less, caller-driven) start; non-empty seeds that specific
	// start node (ADR-0121). Set via NewStartInstanceAtNode.
	StartNodeID string
}

// ActionCompleted reports that a action.Action finished successfully.
type ActionCompleted struct {
	baseTrigger
	CommandID string
	Output    map[string]any
}

// ActionFailed reports that a action.Action failed.
type ActionFailed struct {
	baseTrigger
	CommandID string
	Err       string
	Retryable bool
	// JitterFraction is a value in [0,1) sampled by the runtime and applied to
	// the backoff duration in Step to spread retry storms across multiple workers.
	// Zero means no jitter (the default when constructed via NewActionFailed).
	JitterFraction float64
	// Cause is the original Go error from the live action invocation. It is
	// intentionally NOT persisted (json:"-") so that serialised/replayed
	// ActionFailed triggers remain JSON-serialisable. The engine is
	// snapshot-based: propagateError runs once per trigger and the resulting
	// InstanceState is committed; a replayed trigger never re-runs propagateError,
	// making a non-persisted live error deterministically safe. Consumed by
	// boundary-event error matching (errors.Is / errors.As).
	Cause error `json:"-"`
}

// NewStartInstance builds a StartInstance trigger stamped with the given time.
// StartNodeID is left empty, resolving the definition's sole manual (trigger-less,
// caller-driven) start event. Use NewStartInstanceAtNode to seed a specific start node.
func NewStartInstance(at time.Time, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars}
}

// NewStartInstanceAtNode builds a StartInstance trigger that seeds the given
// start node explicitly, instead of resolving the definition's manual start
// (ADR-0121). nodeID must identify one of the definition's start nodes.
func NewStartInstanceAtNode(at time.Time, nodeID string, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars, StartNodeID: nodeID}
}

// NewActionCompleted builds an ActionCompleted trigger reporting a successful service-action result.
func NewActionCompleted(at time.Time, commandID string, output map[string]any) ActionCompleted {
	return ActionCompleted{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Output: output}
}

// ActionFailedOption configures an ActionFailed trigger.
type ActionFailedOption func(*ActionFailed)

// WithJitter sets the backoff jitter fraction (fraction >= 0; the runtime samples
// it to spread concurrent retries across workers). Values <= 0 mean no jitter.
func WithJitter(fraction float64) ActionFailedOption {
	return func(a *ActionFailed) { a.JitterFraction = fraction }
}

// WithCause stores the original Go error on the non-persisted ActionFailed.Cause
// field so that boundary-event error-matching closures (WithBoundaryErrorCheck) can
// use errors.Is / errors.As against the live error. The field is tagged json:"-" and
// is never written to or read from the journal.
func WithCause(err error) ActionFailedOption {
	return func(a *ActionFailed) { a.Cause = err }
}

// NewActionFailed builds an ActionFailed trigger reporting a service-action error
// and whether it is retryable. JitterFraction defaults to 0; use WithJitter to set it.
func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool, opts ...ActionFailedOption) ActionFailed {
	af := ActionFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg, Retryable: retryable}
	for _, o := range opts {
		o(&af)
	}
	return af
}

// CompletionInput is the payload an actor SUBMITS when finishing a human task.
// It bundles the business disposition (Outcome), the actor's free-text remark
// (Note), and the output variables merged into the process (Output).
//
// It is the request side of the completion. The RECORD side — what the engine
// durably stamps on the task once the completion is accepted — is
// [humantask.Completion], which additionally carries the completing actor and
// the completion time. The two are deliberately named apart so a file importing
// both does not have to disambiguate, and so a reader can tell at a glance which
// one is being handled (ADR-0146 amendment 2).
//
// Outcome is recorded on the task's completion audit either way, and is
// validated against the user task's declared outcomes when the node declares
// any: a value outside the set fails with [ErrInvalidOutcome], and an EMPTY
// value fails with [ErrOutcomeRequired], because declaring a set makes the
// outcome mandatory. The zero value therefore completes a task only when its
// node declares no outcomes; Note and Output remain optional throughout.
type CompletionInput struct {
	// Outcome is the business outcome chosen by the actor (e.g. "approve").
	Outcome string
	// Note is the actor's free-text remark accompanying the completion.
	Note string
	// Output holds variables merged into the process instance on completion.
	Output map[string]any
}

// HumanCompleted reports that a human-task node was completed by an actor,
// carrying the actor's [CompletionInput] payload flattened onto the trigger.
type HumanCompleted struct {
	baseTrigger
	TaskID string
	// Outcome is the business outcome the actor chose; empty when none.
	Outcome string
	// Note is the actor's free-text remark; empty when none.
	Note   string
	Output map[string]any
	Actor  authz.Actor
}

// HumanCandidatesResolved reports the set of actors the runtime resolved as
// eligible for a human task, as of the trigger's occurrence time.
//
// The engine core cannot resolve candidates itself: expanding an
// [authz.AuthzSpec] into concrete actors is I/O (a group-membership lookup), and
// the core owns none. The runtime resolves and feeds the result back as a
// trigger — the same round trip a service action takes via [ActionCompleted] —
// so the resolved actors reach the committed snapshot the instance view renders.
//
// The trigger is not tied to task creation: applying it again re-states the
// candidate set, which is how a stale list is refreshed when the underlying
// actor registry changes.
type HumanCandidatesResolved struct {
	baseTrigger
	TaskID     string
	Candidates []authz.Actor
}

// HumanClaimed reports that an actor has claimed a human-task node.
type HumanClaimed struct {
	baseTrigger
	TaskID string
	Actor  authz.Actor
}

// HumanReassigned reports that a human-task node was reassigned from one actor
// to another by a third party (e.g. an admin).
type HumanReassigned struct {
	baseTrigger
	TaskID string
	From   string
	To     string
	By     authz.Actor
}

// NewHumanCompleted builds a HumanCompleted trigger stamped with the given time,
// carrying the actor's completion payload. Pass the zero [CompletionInput] to complete
// a task with no outcome, note, or output.
func NewHumanCompleted(at time.Time, taskID string, c CompletionInput, actor authz.Actor) HumanCompleted {
	return HumanCompleted{
		baseTrigger: baseTrigger{at: at},
		TaskID:      taskID,
		Outcome:     c.Outcome,
		Note:        c.Note,
		Output:      c.Output,
		Actor:       actor,
	}
}

// NewHumanCandidatesResolved builds a HumanCandidatesResolved trigger stamped
// with the given time. candidates replaces the task's current list wholesale.
func NewHumanCandidatesResolved(at time.Time, taskID string, candidates []authz.Actor) HumanCandidatesResolved {
	return HumanCandidatesResolved{baseTrigger: baseTrigger{at: at}, TaskID: taskID, Candidates: candidates}
}

// NewHumanClaimed builds a HumanClaimed trigger stamped with the given time.
func NewHumanClaimed(at time.Time, taskID string, actor authz.Actor) HumanClaimed {
	return HumanClaimed{baseTrigger: baseTrigger{at: at}, TaskID: taskID, Actor: actor}
}

// NewHumanReassigned builds a HumanReassigned trigger stamped with the given time.
// From is the previous assignee, To is the new assignee, By is the actor performing the reassignment.
func NewHumanReassigned(at time.Time, taskID, from, to string, by authz.Actor) HumanReassigned {
	return HumanReassigned{baseTrigger: baseTrigger{at: at}, TaskID: taskID, From: from, To: to, By: by}
}

// TimerFired reports that a previously scheduled timer has fired.
type TimerFired struct {
	baseTrigger
	TimerID string
}

// NewTimerFired builds a TimerFired trigger stamped with the given time.
func NewTimerFired(at time.Time, timerID string) TimerFired {
	return TimerFired{baseTrigger: baseTrigger{at: at}, TimerID: timerID}
}

// SignalReceived reports that a named signal has been broadcast. Every token in
// the instance awaiting that signal name will be resumed (broadcast semantics).
// Payload is optional additional data carried by the signal; it is merged into
// the instance variables before each resumed token drives forward.
type SignalReceived struct {
	baseTrigger
	Name    string
	Payload map[string]any
}

// NewSignalReceived builds a SignalReceived trigger stamped with the given time.
func NewSignalReceived(at time.Time, name string, payload map[string]any) SignalReceived {
	return SignalReceived{baseTrigger: baseTrigger{at: at}, Name: name, Payload: payload}
}

// MessageReceived reports that a named message has been delivered to this
// instance. The single token awaiting that message name and correlation key is
// resumed. If no token matches the trigger is a clean no-op.
type MessageReceived struct {
	baseTrigger
	Name           string
	CorrelationKey string
	Payload        map[string]any
}

// NewMessageReceived builds a MessageReceived trigger stamped with the given time.
func NewMessageReceived(at time.Time, name, correlationKey string, payload map[string]any) MessageReceived {
	return MessageReceived{baseTrigger: baseTrigger{at: at}, Name: name, CorrelationKey: correlationKey, Payload: payload}
}

// SubInstanceCompleted reports that a child process instance (started by a
// StartSubInstance command) has finished successfully. CommandID correlates
// this result back to the StartSubInstance command that spawned the child.
// Output carries any variables the child exported on completion.
//
// The engine resumes the parked parent token and merges Output into the parent
// instance variables.
type SubInstanceCompleted struct {
	baseTrigger
	// CommandID matches the StartSubInstance.CommandID that started the child.
	CommandID string
	// Output is the result variable map from the completed child instance.
	Output map[string]any
}

// NewSubInstanceCompleted builds a SubInstanceCompleted trigger stamped with
// the given time. at is the moment the child instance completed.
func NewSubInstanceCompleted(at time.Time, commandID string, output map[string]any) SubInstanceCompleted {
	return SubInstanceCompleted{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Output: output}
}

// SubInstanceFailed reports that a child process instance (started by a
// StartSubInstance command) has terminated with an error. CommandID correlates
// this result back to the StartSubInstance command. Err is a human-readable
// description of the failure reason, also used as the boundary-matching error
// code (mirrors ActionFailed.Err).
//
// The engine treats this as an error thrown at the call-activity node: when
// that node carries a boundary error event whose ErrorCode matches Err, the
// engine routes to it instead of failing the parent (ADR-0128). When no
// boundary matches, the engine marks the instance failed (StatusFailed) with a
// FailInstance command.
type SubInstanceFailed struct {
	baseTrigger
	// CommandID matches the StartSubInstance.CommandID that started the child.
	CommandID string
	// Err is the error message from the failed child instance; also used as
	// the error code for parent boundary-event matching (ADR-0128).
	Err string
}

// NewSubInstanceFailed builds a SubInstanceFailed trigger stamped with the
// given time. at is the moment the child instance failure was observed.
func NewSubInstanceFailed(at time.Time, commandID, errMsg string) SubInstanceFailed {
	return SubInstanceFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg}
}

// CompensateRequested is an admin/debug trigger that initiates reverse-order
// compensation rollback for a running process instance. The engine walks
// InstanceState.RootCompensations in reverse completion order, emitting one
// InvokeAction per record (down to and excluding ToNode), then resumes at
// ToNode (StatusRunning) or terminates (StatusTerminated) when ToNode is empty.
//
// ToNode: the BPMN node ID to roll back to (exclusive — that node's compensation
// is NOT re-run). An empty ToNode means "roll back everything" — the instance
// ends in StatusTerminated when all records are exhausted.
//
// Sub-process compensation (ADR-0013 → ADR-0039 → ADR-0162): every scope close
// archives its accumulated CompensationRecords by scope (keyed by the sub-process
// node ID) before closeScope is called — the normal sub-process exit, both
// event-sub-process exits, and the two abnormal teardowns (error boundary,
// interrupting event sub-process) via cancelScopeSubtree. Archived records reach
// RootCompensations via consolidateArchiveIntoRoot when a compensation walk
// begins. As a result, completed sub-process activities are rollback-able via
// this trigger — their records are folded into RootCompensations in completion
// order alongside root-level records, and the reverse walk reaches them
// naturally.
//
// Scope-targeted compensation (Compensate command) remains RESERVED for future use
// and is not yet emitted. It is intended for BPMN compensation boundary/throw event
// handling, which requires a producer not yet built. CompensateRequested is the
// only supported compensation entry point today.
//
// Delivering this trigger with a non-empty ToNode against an already-terminal
// instance is rejected with a workflow-engine error rather than resurrecting
// it (ADR-0164). A plain full rollback (ToNode and ReverseNode both empty) is
// still accepted.
type CompensateRequested struct {
	baseTrigger
	// ToNode is the rollback target node ID. Compensation runs from the most-recently
	// completed record back to (but not including) this node. Empty means full rollback.
	ToNode string
	// ReverseNode, when non-empty on a full walk (ToNode == ""), makes the walk resume
	// at this node with StatusRunning instead of terminating (ReverseInstance full
	// reverse). Empty for cancel/error/admin walks, which terminate on a full walk.
	ReverseNode string
	// ResetVars, when true, resets Variables to StartVariables on a ReverseNode resume.
	ResetVars bool
	// RestoreTargetVars, when true on a target reverse (ToNode non-empty), restores
	// Variables to ToNode's own start-of-visit snapshot — the Input captured on
	// ToNode's compensation record, i.e. the variables as they stood the moment
	// execution first arrived at ToNode, before ToNode ran. This is distinct from
	// ResetVars, which resets all the way back to StartVariables on a full
	// (ReverseNode) walk. RestoreTargetVars is opt-in so the raw admin
	// partial-rollback path (NewCompensateRequested) keeps its current-variables
	// behavior unchanged; only the ReverseInstance WithTargetNode facade path sets it
	// (via NewReverseToNode).
	RestoreTargetVars bool
}

// NewCompensateRequested builds a CompensateRequested trigger stamped with the
// given time. toNode is the rollback target (empty = full rollback). The reverse
// fields (ReverseNode, ResetVars) are left at their zero values; use
// NewReverseToStart to build a full-reverse-and-resume trigger instead.
//
// Delivering this trigger with a non-empty ToNode against an already-terminal
// instance is rejected with a workflow-engine error rather than resurrecting
// it (ADR-0164). A plain full rollback (ToNode and ReverseNode both empty) is
// still accepted.
func NewCompensateRequested(at time.Time, toNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ToNode: toNode}
}

// NewReverseToStart builds a CompensateRequested that compensates ALL records and,
// on finish, resumes at startNode (StatusRunning) with variables reset to
// StartVariables — the full-reverse form of ReverseInstance (does NOT terminate).
//
// Delivering this trigger against an already-terminal instance (StatusCompleted,
// StatusFailed, StatusTerminated) is rejected with a workflow-engine error rather
// than resurrecting it (ADR-0109 hardening) — this is a defense-in-depth guard
// behind the runtime facade's own terminal pre-check.
func NewReverseToStart(at time.Time, startNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ReverseNode: startNode, ResetVars: true}
}

// NewReverseToNode builds a CompensateRequested that compensates back to (but not
// including) toNode and, on finish, restores Variables to toNode's own
// start-of-visit snapshot — the target-reverse form of ReverseInstance's
// WithTargetNode option. ReverseNode and ResetVars are left at their zero values
// (this is not a full-reverse-to-start walk; see NewReverseToStart for that).
//
// Delivering this trigger against an already-terminal instance is rejected
// with a workflow-engine error rather than resurrecting it (ADR-0164): this
// constructor always sets a non-empty ToNode, so there is no plain-full-
// rollback carve-out reachable through it — see NewCompensateRequested for
// that.
func NewReverseToNode(at time.Time, toNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ToNode: toNode, RestoreTargetVars: true}
}

// CancelRequested is an admin trigger that immediately terminates a running
// process instance. The engine consumes all in-flight tokens, cancels any
// outstanding timers and boundary/gateway arms, sets Status to StatusTerminated,
// and emits FailInstance{Err:"cancelled"} as the terminal command.
//
// Behavior on an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated): the trigger is accepted without error; the status is
// overwritten to StatusTerminated (idempotent intent) and no harmful side effects
// occur since there are no live tokens or timers to cancel.
type CancelRequested struct {
	baseTrigger
}

// NewCancelRequested builds a CancelRequested trigger stamped with the given time.
func NewCancelRequested(at time.Time) CancelRequested {
	return CancelRequested{baseTrigger: baseTrigger{at: at}}
}

// ResolveIncident is an admin trigger that clears a parked incident, optionally
// grants additional retry attempts, and re-invokes the stalled action. The engine
// increments the token's remaining-attempts counter by AddAttempts before resuming.
type ResolveIncident struct {
	baseTrigger
	// IncidentID identifies the parked incident to resolve.
	IncidentID string
	// AddAttempts is the number of additional retry attempts granted when the
	// incident is cleared. Zero means resume with whatever attempts remain.
	AddAttempts int
}

// NewResolveIncident builds a ResolveIncident trigger stamped with the given time.
// incidentID is the parked incident to clear; addAttempts is the extra retry budget
// granted (may be zero).
func NewResolveIncident(at time.Time, incidentID string, addAttempts int) ResolveIncident {
	return ResolveIncident{baseTrigger: baseTrigger{at: at}, IncidentID: incidentID, AddAttempts: addAttempts}
}

// Compile-time assertions: CompensateRequested and CancelRequested must satisfy Trigger.
var _ Trigger = CompensateRequested{}
var _ Trigger = CancelRequested{}

// Compile-time assertion: ResolveIncident must satisfy Trigger.
var _ Trigger = ResolveIncident{}
