// Package engine is the pure token state machine (ADR-0002). Step maps
// (definition, state, Trigger) -> (state, []Command) with no I/O, no clock
// reads, and no goroutines.
package engine

import (
	"time"

	"github.com/kartaladev/wrkflw/authz"
)

// terminalPolicy reports how [Step] treats a trigger delivered to an instance
// that has already reached a terminal status (StatusCompleted, StatusFailed,
// StatusTerminated). It is declared per trigger and enforced once, in dispatch,
// rather than hand-copied into individual handlers — the sprawl that shape
// produced is what ADR-0165 replaced.
//
// StatusCompensating is NOT terminal, so an in-flight compensation walk is
// unaffected by any of these.
type terminalPolicy int

const (
	// rejectSilently logs and returns the state unchanged, with no error. For
	// triggers delivered asynchronously by the engine's own machinery, whose
	// caller cannot tell a no-op from success and must not retry — notably
	// calllink.CallNotifier and signalbus.Publish, which fan out to many targets
	// and would abort or retry a whole batch on an error from one dead instance.
	//
	// It takes iota so the zero value is the safe one: a policy left unset rejects
	// rather than resurrects.
	rejectSilently terminalPolicy = iota
	// rejectWithError returns ErrInstanceTerminal. For triggers originating in a
	// synchronous external API call, whose caller must not be allowed to believe
	// it succeeded.
	rejectWithError
	// allowOnTerminal runs the handler anyway. For triggers that deliberately
	// operate on a terminal instance — today only a plain full compensation
	// rollback, which is a legitimate admin action against a finished instance.
	allowOnTerminal
)

// Trigger is the sealed set of things that drive the next step: initiating
// causes and returning results. The unexported markers keep the set closed —
// no consumer outside this package can implement it.
//
// Every trigger declares how it behaves when it is delivered to an instance that
// has already reached a terminal status (StatusCompleted, StatusFailed,
// StatusTerminated), and [Step] applies that declaration in one place, before
// any handler runs. Three outcomes are possible, and each concrete trigger's own
// doc comment states which one applies to it:
//
//   - Dropped silently — [Step] returns the state unchanged, with no commands
//     and no error. Triggers relayed by the engine's own asynchronous machinery
//     take this outcome, because an error reaches a relay rather than a caller
//     waiting on an answer, and the relay can only do harm with it: the
//     call-link notifier leaves the link claimable and redelivers it drain after
//     drain, and a signal broadcast joins its per-target errors, so one dead
//     instance would fail the publish for every live one.
//   - Refused with [ErrInstanceTerminal] — [Step] returns that error and the
//     zero [StepResult], so the caller keeps the state it already held. Triggers
//     originating in a synchronous external API call take this outcome, because
//     such a caller must not be shown success for a request that did nothing.
//   - Passed through to the handler — today only a plain full compensation
//     rollback, which is a legitimate admin action against a finished instance.
//     See [CompensateRequested]; its handler applies one further, state-dependent
//     refusal that the type-level outcome cannot express.
//
// The two rejecting outcomes both log a warn record, carrying the instance id,
// the trigger type, the status, which of the two was taken, and — when the
// trigger carries an identity key, such as a timer or task id — that key too. A
// silent drop leaves no other trace, so that record is the only way to answer
// "why did my trigger do nothing".
//
// StatusCompensating is not terminal (see [Status.IsTerminal]), so an in-flight
// compensation walk is unaffected by any of this. See ADR-0165.
type Trigger interface {
	isTrigger()
	OccurredAt() time.Time
	// terminalPolicy states this trigger's behaviour on an already-terminal
	// instance. It is deliberately NOT implemented on baseTrigger: every trigger
	// type must declare its own, so a new trigger fails to compile until its
	// author has made the decision. That compile error is the mechanism. Do not
	// add a default. See ADR-0165.
	terminalPolicy() terminalPolicy
}

type baseTrigger struct{ at time.Time }

func (b baseTrigger) OccurredAt() time.Time { return b.at }
func (baseTrigger) isTrigger()              {}

// Deliberately absent: func (baseTrigger) terminalPolicy() terminalPolicy.
// See the Trigger interface doc — the omission is load-bearing.

// StartInstance begins a new process instance with initial variables.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is refused with [ErrInstanceTerminal] rather than
// restarting it: starting an instance is a synchronous external API call, and
// its caller must not be told a start succeeded when none happened. See
// [Trigger] for the three terminal outcomes.
type StartInstance struct {
	baseTrigger
	Vars map[string]any
	// StartNodeID is the start node to seed. Empty resolves the definition's
	// manual (trigger-less, caller-driven) start; non-empty seeds that specific
	// start node (ADR-0121). Set via NewStartInstanceAtNode.
	StartNodeID string
}

// terminalPolicy: starting an instance is a synchronous external API call, and a
// terminal instance that accepted one would flip back to StatusRunning with
// EndedAt still set, seed a second start token, and re-mint its tasks. The caller
// must be told. See ADR-0165.
func (StartInstance) terminalPolicy() terminalPolicy { return rejectWithError }

// ActionCompleted reports that a action.Action finished successfully.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is dropped silently — [Step] returns the state unchanged,
// with no commands and no error. The result arrives asynchronously from a worker
// the engine itself dispatched, so a result for an instance that died while the
// action ran has no caller that could act on an error — only the engine, which
// dispatched the action in the first place. See [Trigger] for the three terminal
// outcomes.
type ActionCompleted struct {
	baseTrigger
	CommandID string
	Output    map[string]any
}

// terminalPolicy: an action result arrives from a worker the engine dispatched.
// The worker cannot distinguish a no-op from success and would retry an error,
// so a result for an instance that died while the action ran is swallowed.
// See ADR-0165.
//
// The route this closes, migrated here from the per-handler guard ADR-0165
// deleted: three terminal paths keep s.Tokens (handleUnhandledError's
// immediate-fail branch, handleSubInstanceFailed's tail, and applyTerminate —
// only forceTerminate and handleCancelRequested's immediate branch nil them), so
// a surviving sibling still carries its AwaitCommand, and tokenAwaiting matches
// on AwaitCommand alone with no status check. drive has no status guard either,
// so that token could run to an end event and flip a FAILED instance to
// Completed. ADR-0161's liveAwaiters filter does not cover it: that drops
// commands emitted in the SAME step, whereas this one was dispatched earlier and
// is already in flight. Guarding inside handleActionCompleted's `tok == nil`
// branch could not see the case at all, which is why the check has to precede
// any lookup — and dispatch precedes all of them.
func (ActionCompleted) terminalPolicy() terminalPolicy { return rejectSilently }

// ActionFailed reports that a action.Action failed.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is dropped silently, for the same reason as
// [ActionCompleted]: an instance that is already dead has nothing left to fail,
// and the report arrives from the engine's own dispatch path rather than from a
// caller. See [Trigger] for the three terminal outcomes.
type ActionFailed struct {
	baseTrigger
	CommandID string
	Err       string
	Retryable bool
	// JitterFraction is a value in [0,1) sampled by the runtime and used to SCALE
	// the retry backoff in Step, spreading retry storms across multiple workers.
	//
	// Zero — the value NewActionFailed leaves unless [WithJitter] is passed —
	// means NO JITTER: the retry is armed at the policy's full backoff interval.
	// It does not mean a zero delay. (It once did, which made an unjittered
	// retry immediate; see the retry-backoff regression test.)
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

// terminalPolicy: same asynchronous-worker reasoning as ActionCompleted — a
// failure report for an instance that is already dead has nothing left to fail.
// See ADR-0165.
//
// The route, migrated here from the deleted per-handler guard, is the symmetric
// twin of ActionCompleted's and is real rather than theoretical. An ActionFailed
// whose token outlived a terminal transition is dispatched normally: with an
// error boundary on its node, propagateError routes to the recovery path, which
// can run to a normal end as the last token and flip a FAILED instance to
// Completed; without one, it emits a DUPLICATE FailInstance and re-runs
// endInstance on an already-terminal instance.
func (ActionFailed) terminalPolicy() terminalPolicy { return rejectSilently }

// NewStartInstance builds a StartInstance trigger stamped with the given time.
// StartNodeID is left empty, resolving the definition's sole manual (trigger-less,
// caller-driven) start event. Use NewStartInstanceAtNode to seed a specific start node.
//
// Delivering the result to an already-terminal instance is refused with
// [ErrInstanceTerminal]; see [StartInstance].
func NewStartInstance(at time.Time, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars}
}

// NewStartInstanceAtNode builds a StartInstance trigger that seeds the given
// start node explicitly, instead of resolving the definition's manual start
// (ADR-0121). nodeID must identify one of the definition's start nodes.
//
// Delivering the result to an already-terminal instance is refused with
// [ErrInstanceTerminal]; see [StartInstance].
func NewStartInstanceAtNode(at time.Time, nodeID string, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars, StartNodeID: nodeID}
}

// NewActionCompleted builds an ActionCompleted trigger reporting a successful service-action result.
//
// Delivering the result to an already-terminal instance is a silent no-op; see
// [ActionCompleted].
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
//
// Delivering the result to an already-terminal instance is a silent no-op; see
// [ActionFailed].
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
//
// It is refused on two independent keys, both of which report an error because
// the actor submitting a completion form is a synchronous external caller who
// must not be shown success:
//
//   - The instance is already terminal (StatusCompleted, StatusFailed,
//     StatusTerminated) — [ErrInstanceTerminal]. See [Trigger] for the three
//     terminal outcomes.
//   - The task itself is already Completed or Cancelled — [ErrTaskNotOpen].
//     ADR-0163 closes a task while the instance keeps running, so a closed task
//     on a live instance is reachable and the instance-status key cannot see it.
//
// A TaskID that names no task record at all reports humantask.ErrTaskNotFound,
// which is what the layer above the engine already reported for an unknown id.
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

// terminalPolicy: a human submitting a completion form is a synchronous external
// caller who must not be shown success. Left unguarded this was the worst of the
// reproduced routes — a surviving token whose AwaitCommand still matched drove
// on and appended a post-mortem history visit, and on a single-token instance it
// flipped StatusFailed to StatusCompleted whose second terminal event was
// suppressed. See ADR-0165.
func (HumanCompleted) terminalPolicy() terminalPolicy { return rejectWithError }

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
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is dropped silently — [Step] returns the state unchanged,
// with no commands and no error. So is a resolution for a task that is already
// Completed or Cancelled: a closed task's candidate list is part of its audit
// record and stays frozen, so a late refresh is pointless rather than a caller
// error. That second case is a deliberate asymmetry with [HumanClaimed],
// [HumanReassigned] and [HumanCompleted], which report [ErrTaskNotOpen] for a
// closed task. See [Trigger] for the three terminal outcomes.
type HumanCandidatesResolved struct {
	baseTrigger
	TaskID     string
	Candidates []authz.Actor
}

// terminalPolicy: the runtime feeds a resolved candidate set back on its own
// schedule, the same round trip a service action takes. Restating candidates on
// a dead instance is pointless, not an error the resolver can act on.
// See ADR-0165.
//
// The route, migrated here from the deleted per-handler guard: it is reached
// when a sibling branch terminates the instance in the same step that opened
// this task, or when a caller delivers a stale refresh. Unguarded, the handler
// rewrites the task's candidate list and emits an UpdateTask, versioning a dead
// instance and re-publishing its task to the store.
func (HumanCandidatesResolved) terminalPolicy() terminalPolicy { return rejectSilently }

// HumanClaimed reports that an actor has claimed a human-task node.
//
// Like [HumanCompleted] it is refused on two independent keys, both with an
// error, because an actor clicking "claim" is a synchronous external caller:
// [ErrInstanceTerminal] when the instance is already terminal (StatusCompleted,
// StatusFailed, StatusTerminated), and [ErrTaskNotOpen] when the task itself is
// already Completed or Cancelled. See [Trigger] for the three terminal outcomes.
type HumanClaimed struct {
	baseTrigger
	TaskID string
	Actor  authz.Actor
}

// terminalPolicy: an actor clicking "claim" is a synchronous external caller.
// Left unguarded this re-opened a task the terminal transition had already
// cancelled, flipping its state back to Claimed and emitting an UpdateTask
// against a dead instance. See ADR-0165.
func (HumanClaimed) terminalPolicy() terminalPolicy { return rejectWithError }

// HumanReassigned reports that a human-task node was reassigned from one actor
// to another by a third party (e.g. an admin).
//
// It is refused on the same two keys as [HumanClaimed], and for the same reason —
// the admin performing the reassignment is a synchronous external caller:
// [ErrInstanceTerminal] on an already-terminal instance (StatusCompleted,
// StatusFailed, StatusTerminated), [ErrTaskNotOpen] on a task that is already
// Completed or Cancelled. See [Trigger] for the three terminal outcomes.
type HumanReassigned struct {
	baseTrigger
	TaskID string
	From   string
	To     string
	By     authz.Actor
}

// terminalPolicy: same route and same reasoning as HumanClaimed — an admin
// reassigning is a synchronous external caller, and the unguarded handler forged
// a Claim on a cancelled task. See ADR-0165.
func (HumanReassigned) terminalPolicy() terminalPolicy { return rejectWithError }

// NewHumanCompleted builds a HumanCompleted trigger stamped with the given time,
// carrying the actor's completion payload. Pass the zero [CompletionInput] to complete
// a task with no outcome, note, or output.
//
// Delivering the result is refused with [ErrInstanceTerminal] on an
// already-terminal instance and with [ErrTaskNotOpen] on a closed task; see
// [HumanCompleted].
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
//
// Delivering the result to an already-terminal instance, or for a closed task,
// is a silent no-op; see [HumanCandidatesResolved].
func NewHumanCandidatesResolved(at time.Time, taskID string, candidates []authz.Actor) HumanCandidatesResolved {
	return HumanCandidatesResolved{baseTrigger: baseTrigger{at: at}, TaskID: taskID, Candidates: candidates}
}

// NewHumanClaimed builds a HumanClaimed trigger stamped with the given time.
//
// Delivering the result is refused with [ErrInstanceTerminal] on an
// already-terminal instance and with [ErrTaskNotOpen] on a closed task; see
// [HumanClaimed].
func NewHumanClaimed(at time.Time, taskID string, actor authz.Actor) HumanClaimed {
	return HumanClaimed{baseTrigger: baseTrigger{at: at}, TaskID: taskID, Actor: actor}
}

// NewHumanReassigned builds a HumanReassigned trigger stamped with the given time.
// From is the previous assignee, To is the new assignee, By is the actor performing the reassignment.
//
// Delivering the result is refused with [ErrInstanceTerminal] on an
// already-terminal instance and with [ErrTaskNotOpen] on a closed task; see
// [HumanReassigned].
func NewHumanReassigned(at time.Time, taskID, from, to string, by authz.Actor) HumanReassigned {
	return HumanReassigned{baseTrigger: baseTrigger{at: at}, TaskID: taskID, From: from, To: to, By: by}
}

// TimerFired reports that a previously scheduled timer has fired.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is dropped silently — [Step] returns the state unchanged,
// with no commands and no error. The scheduler fires on its own schedule, so a
// timer armed before the instance died can land after it; this is the engine's
// own machinery reporting in, not a caller to inform. See [Trigger] for the three
// terminal outcomes.
type TimerFired struct {
	baseTrigger
	TimerID string
}

// terminalPolicy: the scheduler fires timers on its own schedule and a timer
// armed before the instance died can always land after it. This is the engine's
// own machinery reporting in, not a caller to inform. See ADR-0165.
//
// The route, migrated here from the deleted per-handler guard: an unhandled
// error can fail an instance without sweeping its sibling boundary, deadline, or
// event-sub arms, so a late timer must not fire any of them on a terminal
// instance.
func (TimerFired) terminalPolicy() terminalPolicy { return rejectSilently }

// NewTimerFired builds a TimerFired trigger stamped with the given time.
//
// Delivering the result to an already-terminal instance is a silent no-op; see
// [TimerFired].
func NewTimerFired(at time.Time, timerID string) TimerFired {
	return TimerFired{baseTrigger: baseTrigger{at: at}, TimerID: timerID}
}

// SignalReceived reports that a named signal has been broadcast. Every token in
// the instance awaiting that signal name will be resumed (broadcast semantics).
// Payload is optional additional data carried by the signal; it is merged into
// the instance variables before each resumed token drives forward.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is dropped silently — [Step] returns the state unchanged,
// with no commands and no error, and in particular Payload is NOT merged into a
// dead instance's variables. A signal is a broadcast fanned out to every instance
// awaiting the name, so an error from one dead target must not fail the publish
// for the live ones. See [Trigger] for the three terminal outcomes.
type SignalReceived struct {
	baseTrigger
	Name    string
	Payload map[string]any
}

// terminalPolicy: a signal is a broadcast. signalbus.Publish fans one out to
// every instance awaiting the name, so an error from a single dead target must
// not fail the publish for the live ones. Left unguarded this merged the
// signal's Payload into a dead instance's Variables and drove a surviving token
// to a post-mortem end event. See ADR-0165.
func (SignalReceived) terminalPolicy() terminalPolicy { return rejectSilently }

// NewSignalReceived builds a SignalReceived trigger stamped with the given time.
//
// Delivering the result to an already-terminal instance is a silent no-op; see
// [SignalReceived].
func NewSignalReceived(at time.Time, name string, payload map[string]any) SignalReceived {
	return SignalReceived{baseTrigger: baseTrigger{at: at}, Name: name, Payload: payload}
}

// MessageReceived reports that a named message has been delivered to this
// instance. The single token awaiting that message name and correlation key is
// resumed. If no token matches the trigger is a clean no-op.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is dropped silently in the same way — [Step] returns the
// state unchanged, with no commands and no error, and Payload is NOT merged into
// a dead instance's variables. A message that correlates to no live token is
// already a clean no-op, so one that correlates to a token on a dead instance is
// treated the same way. See [Trigger] for the three terminal outcomes.
type MessageReceived struct {
	baseTrigger
	Name           string
	CorrelationKey string
	Payload        map[string]any
}

// terminalPolicy: a message that correlates to no live token is already
// documented as a clean no-op, so a message that correlates to a token on a dead
// instance must be one too — the delivering broker has nothing to retry.
// Reproduced identically to SignalReceived. See ADR-0165.
func (MessageReceived) terminalPolicy() terminalPolicy { return rejectSilently }

// NewMessageReceived builds a MessageReceived trigger stamped with the given time.
//
// Delivering the result to an already-terminal instance is a silent no-op; see
// [MessageReceived].
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
//
// Delivered to an already-terminal PARENT instance (StatusCompleted,
// StatusFailed, StatusTerminated) it is dropped silently — [Step] returns the
// state unchanged, with no commands and no error. The child's result is relayed
// by the engine's own call-link notifier, for which a parent that died while the
// child ran is a normal race rather than a fault; returning no error is what lets
// the notifier mark the link notified, instead of leaving it claimable for a
// later drain to retry. See [Trigger] for the three terminal outcomes.
type SubInstanceCompleted struct {
	baseTrigger
	// CommandID matches the StartSubInstance.CommandID that started the child.
	CommandID string
	// Output is the result variable map from the completed child instance.
	Output map[string]any
}

// terminalPolicy: the child's completion is relayed by calllink.CallNotifier,
// which marks the link notified and must not retry — its idempotency branch keys
// on ErrTokenNotFound, and a parent that died while the child ran is a normal
// race, not a fault. See ADR-0165.
//
// The route, migrated here from the deleted per-handler guard: CallNotifier.DrainOnce
// performs no parent-status check, so the parent can have gone terminal at any
// point since the child was started, and the call-activity token survives the
// three token-keeping terminal paths with its AwaitCommand intact. tokenAwaiting
// hands it straight back, drive has no status guard, and the token can reach an
// end event as the last one — flipping a FAILED instance to Completed while
// terminalOutboxEvent suppresses the second event. Returning nil rather than
// ErrTokenNotFound is safe for the notifier: its idempotency branch marks the
// link notified on success OR ErrTokenNotFound alike, so a no-op still retires
// the link instead of retrying forever. Pinned by runtime/calllink's
// terminal-parent test.
func (SubInstanceCompleted) terminalPolicy() terminalPolicy { return rejectSilently }

// NewSubInstanceCompleted builds a SubInstanceCompleted trigger stamped with
// the given time. at is the moment the child instance completed.
//
// Delivering the result to an already-terminal parent instance is a silent
// no-op; see [SubInstanceCompleted].
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
//
// Delivered to an already-terminal parent instance (StatusCompleted,
// StatusFailed, StatusTerminated) it is dropped silently, for the same reason as
// [SubInstanceCompleted]: the same call-link notifier relays it, and a parent
// that is already dead has no boundary left to route to. See [Trigger] for the
// three terminal outcomes.
type SubInstanceFailed struct {
	baseTrigger
	// CommandID matches the StartSubInstance.CommandID that started the child.
	CommandID string
	// Err is the error message from the failed child instance; also used as
	// the error code for parent boundary-event matching (ADR-0128).
	Err string
}

// terminalPolicy: same relay and same reasoning as SubInstanceCompleted — a
// child failure reported to an already-dead parent has no boundary left to route
// to. See ADR-0165.
//
// The route, migrated here from the deleted per-handler guard: with a matching
// error boundary the unguarded handler routes to recovery and drives, flipping a
// FAILED instance to Completed; with no boundary it re-runs handleSubInstanceFailed's
// endInstance tail on an already-terminal instance, overwriting EndedAt with a
// later timestamp and emitting a DUPLICATE FailInstance.
func (SubInstanceFailed) terminalPolicy() terminalPolicy { return rejectSilently }

// NewSubInstanceFailed builds a SubInstanceFailed trigger stamped with the
// given time. at is the moment the child instance failure was observed.
//
// Delivering the result to an already-terminal parent instance is a silent
// no-op; see [SubInstanceFailed].
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
// On an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) the outcome depends on the trigger's own payload, and is
// three-way rather than a single accept-or-refuse rule:
//
//   - A rollback carrying RESUME intent — a targeted walk (ToNode non-empty) or
//     a reverse-and-resume (ReverseNode non-empty) — is refused with
//     [ErrInstanceTerminal], because completing it would leave the instance
//     running again (ADR-0109 hardening, ADR-0164).
//   - A PLAIN full rollback (ToNode and ReverseNode both empty) with
//     compensation records still to walk is allowed through and compensates
//     normally. Rolling back a finished instance whose records survive is a
//     legitimate admin action, and this is the case ADR-0164's carve-out exists
//     to protect.
//   - A plain full rollback with nothing left to compensate is refused with
//     [ErrInstanceTerminal]. There is no compensation to gain, and the walk
//     would finish immediately through the terminal transition again —
//     re-stamping the status, dropping any token that survived the original
//     transition, and overwriting EndedAt.
//
// The records that count are the root ones and any archived by a closed
// sub-process scope, since a walk consolidates the archive before it starts: an
// empty RootCompensations alone does not mean there is nothing to compensate.
//
// On a non-terminal instance none of the above applies. See [Trigger] for the
// three terminal outcomes and ADR-0165 for why the second condition is decided
// by the handler rather than by the trigger's type.
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

// terminalPolicy is the one policy that reads its receiver rather than just its
// type, and that property is what decided the mechanism: a per-type table cannot
// express it.
//
// A rollback carrying RESUME intent — a targeted walk (ToNode) or a
// reverse-and-resume (ReverseNode) — would leave the instance running again, so
// it is refused on a terminal instance (ADR-0109 hardening, ADR-0164). A PLAIN
// full rollback (both empty) must still walk: internal cancel/error paths
// re-deliver one against an already-terminal instance, and compensating a
// finished instance whose records are still present is a legitimate admin
// action.
//
// Rejecting resume intent HERE — that is, in dispatch, ahead of the handler —
// preserves a property the guard this replaced called out explicitly:
// stepCompensateRequested refused a resume BEFORE beginCompensation, because
// guarding at the resume site instead would let the rollback's InvokeActions
// fire first. dispatch runs earlier still, so the property is strictly
// stronger, not merely preserved.
//
// One condition stays in stepCompensateRequested rather than here, deliberately:
// a plain full rollback on a terminal instance is refused when there is nothing
// left to compensate. That predicate reads STATE, not the trigger, and giving
// this method an *InstanceState would stop the policy being a property of the
// trigger. See ADR-0165 Decision 5 and hasCompensationRecordsToWalk.
//
// ⚠ Decision 5's predicate as written in the ADR was inverted — it refused the
// walk when records SURVIVE and admitted it when they do not. Measurement showed
// the opposite: with records the walk is real and is ADR-0164 carve-out #1;
// without them the immediate finish re-stamps the terminal transition, discards
// any surviving token and overwrites EndedAt, compensating nothing. The
// decision's intent stands and is implemented with the corrected predicate.
// So the full contract on a terminal instance is: resume-shaped rollbacks are
// refused here, a plain full rollback with anything to compensate walks and
// returns no error, and a plain full rollback with nothing to compensate is
// refused by the handler — both REFUSALS carrying ErrInstanceTerminal.
func (t CompensateRequested) terminalPolicy() terminalPolicy {
	if t.ReverseNode != "" || t.ToNode != "" {
		return rejectWithError
	}
	return allowOnTerminal
}

// NewCompensateRequested builds a CompensateRequested trigger stamped with the
// given time. toNode is the rollback target (empty = full rollback). The reverse
// fields (ReverseNode, ResetVars) are left at their zero values; use
// NewReverseToStart to build a full-reverse-and-resume trigger instead.
//
// On an already-terminal instance the outcome depends on toNode and on what is
// left to compensate: a non-empty toNode is refused with [ErrInstanceTerminal],
// while an empty one (a plain full rollback) walks when records survive and is
// refused with the same error when none do. See [CompensateRequested] for the
// full three-way contract.
func NewCompensateRequested(at time.Time, toNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ToNode: toNode}
}

// NewReverseToStart builds a CompensateRequested that compensates ALL records and,
// on finish, resumes at startNode (StatusRunning) with variables reset to
// StartVariables — the full-reverse form of ReverseInstance (does NOT terminate).
//
// Delivering this trigger against an already-terminal instance (StatusCompleted,
// StatusFailed, StatusTerminated) is refused with [ErrInstanceTerminal] rather
// than resurrecting it (ADR-0109 hardening) — a defense-in-depth guard behind the
// runtime facade's own terminal pre-check. The plain-full-rollback case described
// on [CompensateRequested] is not reachable through this constructor: a non-empty
// startNode makes the trigger resume-shaped, and an empty one produces a
// malformed trigger (ResetVars without ReverseNode) that is rejected as such.
// See [NewCompensateRequested] for a plain full rollback.
func NewReverseToStart(at time.Time, startNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ReverseNode: startNode, ResetVars: true}
}

// NewReverseToNode builds a CompensateRequested that compensates back to (but not
// including) toNode and, on finish, restores Variables to toNode's own
// start-of-visit snapshot — the target-reverse form of ReverseInstance's
// WithTargetNode option. ReverseNode and ResetVars are left at their zero values
// (this is not a full-reverse-to-start walk; see NewReverseToStart for that).
//
// Delivering this trigger against an already-terminal instance is refused with
// [ErrInstanceTerminal] rather than resurrecting it (ADR-0164). The
// plain-full-rollback case described on [CompensateRequested] is not reachable
// through this constructor: a non-empty toNode makes the trigger resume-shaped,
// and an empty one produces a malformed trigger (RestoreTargetVars without
// ToNode) that is rejected as such. See [NewCompensateRequested] for a plain
// full rollback.
func NewReverseToNode(at time.Time, toNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ToNode: toNode, RestoreTargetVars: true}
}

// CancelRequested is an admin trigger that terminates a running process
// instance. It emits an InvokeCancelAction for each of the definition's
// CancelActions and for each active token whose node declares one, then takes
// one of two termination routes depending on what is left to undo:
//
//   - With no compensation records, it terminates immediately: all in-flight
//     tokens are consumed, outstanding timers and boundary/gateway arms are
//     cancelled, Status becomes StatusTerminated and FailInstance{Err:"cancelled"}
//     is the terminal command.
//   - With compensation records present, it compensates FIRST (ADR-0034): Status
//     becomes StatusCompensating and the walk runs to its end, which is where the
//     same StatusTerminated / FailInstance{Err:"cancelled"} terminal is emitted.
//
// A cancel arriving while a compensation walk is already in flight never starts a
// second walk. If that walk would otherwise resume the instance, the cancel is
// recorded as pending and consumed when the walk finishes; otherwise it is a
// no-op, because the in-flight walk already drives the instance to its own end.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is dropped silently — [Step] returns the state unchanged,
// with no commands and no error. Cancel is idempotent in intent and the paths
// that deliver it re-deliver freely, so there is no caller to report to; but the
// instance is left alone rather than re-terminated, and in particular no
// InvokeCancelAction is emitted. That last point is why tolerating the trigger
// was not harmless: the handler emits the definition's cancel actions before it
// inspects any token, and a terminal transition does not necessarily leave the
// instance without tokens or compensation records, so an accepted late cancel
// could re-run side-effecting actions against an instance that had already
// finished. See [Trigger] for the three terminal outcomes.
type CancelRequested struct {
	baseTrigger
}

// terminalPolicy: cancel is idempotent in intent, and the paths that deliver it
// re-deliver freely — propagateCancel's child loop logs and swallows every error
// it gets, so rejectSilently satisfies that loop exactly as the previous
// tolerate-it behaviour did, without the damage.
//
// It was reclassified from allowOnTerminal by the rule-#9 audit. Left tolerant
// it kept a live resurrection route: forceTerminate never clears
// RootCompensations, so the handler set StatusCompensating on an already-
// Completed instance, re-fired every compensation InvokeAction against a dead
// instance, overwrote the terminal status and EndedAt, and published a SECOND
// terminal event — terminalOutboxEvent suppresses only when the previous status
// was terminal, and by then it was Compensating. See ADR-0165.
func (CancelRequested) terminalPolicy() terminalPolicy { return rejectSilently }

// NewCancelRequested builds a CancelRequested trigger stamped with the given time.
//
// Delivering the result to an already-terminal instance is a silent no-op; see
// [CancelRequested].
func NewCancelRequested(at time.Time) CancelRequested {
	return CancelRequested{baseTrigger: baseTrigger{at: at}}
}

// ResolveIncident is an admin trigger that clears a parked incident, optionally
// grants additional retry attempts, and re-invokes the stalled action. The engine
// increments the token's remaining-attempts counter by AddAttempts before resuming.
//
// Delivered to an already-terminal instance (StatusCompleted, StatusFailed,
// StatusTerminated) it is refused with [ErrInstanceTerminal]. An incident can
// outlive the instance that raised it — ADR-0164 deliberately keeps an incident
// whose token survived a terminal transition — so an admin can reach one and try
// to clear it. The refusal is reported rather than silent because an admin told
// nothing would reasonably conclude the process had resumed. Because
// [ErrInstanceTerminal] wraps [ErrInvalidTransition], it reaches an HTTP admin
// through the existing conflict mapping. See [Trigger] for the three terminal
// outcomes.
type ResolveIncident struct {
	baseTrigger
	// IncidentID identifies the parked incident to resolve.
	IncidentID string
	// AddAttempts is the number of additional retry attempts granted when the
	// incident is cleared. Zero means resume with whatever attempts remain.
	AddAttempts int
}

// terminalPolicy: an admin clearing an incident is a synchronous external
// caller, and this is the one policy that is a deliberate behaviour choice
// rather than a reproduced defect. The handler already refused a terminal
// instance — silently. An admin who resolves an incident on a dead instance and
// is told nothing will reasonably believe the process resumed, so the refusal is
// now visible. It reaches them as HTTP 422 through machinery that already
// exists, because the sentinel wraps ErrInvalidTransition. See ADR-0165.
//
// The route, migrated here from the deleted per-handler guard — and the reason
// the refusal must happen BEFORE the incident is removed. ADR-0164 Decision 3's
// removeOrphanedIncidents sweep is what makes it reachable: it deliberately
// KEEPS an incident whose token survived a terminal transition, which is exactly
// the state (StatusFailed, a live TokenIncident token, a live incident) an admin
// might still try to address. Unguarded, handleResolveIncident deletes the
// incident, flips the token back to TokenActive and re-invokes the stalled
// service action, so a SIDE-EFFECTING action really runs against a dead
// instance; the resulting ActionCompleted is then swallowed by ActionCompleted's
// own rejectSilently policy, stranding the token TokenActive on a terminal
// instance with its incident already gone — neither re-raisable nor
// re-resolvable.
//
// ⚠ The standing lesson this route carries: ADR-0164 Decision 3's rationale
// enumerated only the READ consumers of s.Incidents, and this is the WRITE
// consumer. When a decision changes a data structure's lifetime, enumerate its
// WRITERS.
func (ResolveIncident) terminalPolicy() terminalPolicy { return rejectWithError }

// NewResolveIncident builds a ResolveIncident trigger stamped with the given time.
// incidentID is the parked incident to clear; addAttempts is the extra retry budget
// granted (may be zero).
//
// Delivering the result to an already-terminal instance is refused with
// [ErrInstanceTerminal]; see [ResolveIncident].
func NewResolveIncident(at time.Time, incidentID string, addAttempts int) ResolveIncident {
	return ResolveIncident{baseTrigger: baseTrigger{at: at}, IncidentID: incidentID, AddAttempts: addAttempts}
}

// Compile-time assertions: CompensateRequested and CancelRequested must satisfy Trigger.
var _ Trigger = CompensateRequested{}
var _ Trigger = CancelRequested{}

// Compile-time assertion: ResolveIncident must satisfy Trigger.
var _ Trigger = ResolveIncident{}

// CompensationDisposition selects which of the three operator escapes a
// [ResolveCompensationStall] carries.
type CompensationDisposition int

const (
	// CompensationRetry re-dispatches the stalled record under a fresh command
	// id. It is the verb for a compensation that never actually ran — and it
	// assumes the action is idempotent, since the original dispatch may yet
	// arrive at a worker.
	CompensationRetry CompensationDisposition = iota
	// CompensationSkip gives up on the stalled record and advances the walk,
	// exactly as a returned ActionFailed does (ADR-0034 Decision 4's best-effort
	// skip). It is the only verb accepted on a resuming walk.
	CompensationSkip
	// CompensationAbandon ends the walk and terminates the instance, retaining
	// the records the walk never dispatched. Accepted ONLY on a walkAdmin walk —
	// see handleResolveCompensationStall.
	CompensationAbandon
)

// String returns the name of the CompensationDisposition for logging.
func (d CompensationDisposition) String() string {
	switch d {
	case CompensationRetry:
		return "retry"
	case CompensationSkip:
		return "skip"
	case CompensationAbandon:
		return "abandon"
	default:
		return "unknown"
	}
}

// ResolveCompensationStall is the operator's escape from a compensation walk
// whose dispatched action stopped reporting back (ADR-0175).
//
// Such a walk is stuck AND invisible: it advances only on a trigger carrying the
// cursor's command id, and — for a walk started by beginCompensation — the
// prologue has already cancelled every token and every timer, so measured on
// main the state had tokens=0, timers=0, incidents=0 and both waiter sets empty.
// No clock and no scheduler can move it.
//
// CommandID is REQUIRED and must equal the cursor's ActiveCmdID. That match is
// what makes all three verbs naturally idempotent — a replay finds the cursor
// already moved on and is a clean no-op — and it is the evidence of intent a
// bare "act on whatever is in flight" verb lacks: a 500 ms-old healthy dispatch
// also satisfies "a walk is in flight", so without it skip could silently drop a
// compensation that was about to succeed.
type ResolveCompensationStall struct {
	baseTrigger
	// CommandID is the compensation command being disposed of. REQUIRED: it must
	// equal InstanceState.Compensating.ActiveCmdID or the trigger is refused.
	CommandID string
	// IncidentID optionally names the stall incident being cleared. Empty targets
	// the in-flight walk, which is the normal case: detection defaults to OFF, so
	// with CompensationStallAfter unset there is no incident to name.
	//
	// A NON-empty value naming no open IncidentCompensationStall is an ERROR, not
	// the idempotent no-op ResolveIncident uses — an operator who mistypes an id
	// must not silently get a walk-wide action instead.
	IncidentID string
	// Disposition selects retry, skip or abandon.
	Disposition CompensationDisposition
}

// terminalPolicy: every disposition is a synchronous operator action, so a
// refusal must be visible. An operator who abandons a walk on an already-dead
// instance and is told nothing would reasonably believe it worked. Mirrors
// ResolveIncident, and reaches an HTTP admin as a conflict because
// ErrInstanceTerminal wraps ErrInvalidTransition (ADR-0165).
func (ResolveCompensationStall) terminalPolicy() terminalPolicy { return rejectWithError }

// NewResolveCompensationStall builds the escape trigger for an explicitly
// supplied disposition. It is the constructor the LAYERED surface uses: the
// runtime, service and HTTP layers each receive the verb as data from their
// caller, so none of them can name one of the three constructors below without
// re-deriving a switch.
//
// Prefer the named constructors in engine-embedding code, where the verb is
// known at the call site and reads better.
func NewResolveCompensationStall(at time.Time, commandID, incidentID string, disposition CompensationDisposition) ResolveCompensationStall {
	return ResolveCompensationStall{
		baseTrigger: baseTrigger{at: at},
		CommandID:   commandID, IncidentID: incidentID, Disposition: disposition,
	}
}

// NewRetryStalledCompensation re-dispatches the stalled compensation record
// under a fresh command id and re-arms the stall guard.
//
// commandID must be the stalled dispatch's id (see [ResolveCompensationStall]);
// incidentID is optional and may be empty.
func NewRetryStalledCompensation(at time.Time, commandID, incidentID string) ResolveCompensationStall {
	return ResolveCompensationStall{
		baseTrigger: baseTrigger{at: at},
		CommandID:   commandID, IncidentID: incidentID, Disposition: CompensationRetry,
	}
}

// NewSkipStalledCompensation gives up on the stalled record and advances the
// walk — the same path a returned ActionFailed takes.
func NewSkipStalledCompensation(at time.Time, commandID, incidentID string) ResolveCompensationStall {
	return ResolveCompensationStall{
		baseTrigger: baseTrigger{at: at},
		CommandID:   commandID, IncidentID: incidentID, Disposition: CompensationSkip,
	}
}

// NewAbandonCompensationWalk ends the walk and terminates the instance,
// retaining the records it never dispatched so a later full rollback
// (NewCompensateRequested with an empty ToNode) can still run them.
//
// Accepted only on an admin/cancel/error walk. On a compensation-THROW, partial
// or reverse walk it returns an error naming skip instead — those walks finish
// by RESUMING, and abandon was measured destroying un-run records there.
func NewAbandonCompensationWalk(at time.Time, commandID, incidentID string) ResolveCompensationStall {
	return ResolveCompensationStall{
		baseTrigger: baseTrigger{at: at},
		CommandID:   commandID, IncidentID: incidentID, Disposition: CompensationAbandon,
	}
}

// Compile-time assertion: ResolveCompensationStall must satisfy Trigger.
var _ Trigger = ResolveCompensationStall{}
