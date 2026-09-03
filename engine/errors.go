package engine

import (
	"errors"
	"fmt"
)

// ErrInvalidTransition classifies a trigger that cannot be applied because the
// targeted instance/token is not in a state that accepts it. The instance exists —
// this is a conflict, not a "not found". Consumers classify wrong-state transitions
// with errors.Is(err, ErrInvalidTransition); the service layer maps it to ErrConflict
// and transports map it to HTTP 422.
var ErrInvalidTransition = errors.New("workflow-engine: invalid state transition")

var (
	// ErrUnknownTrigger is returned when a trigger type has no handler. It is an
	// infrastructure/programming error, not a wrong-state transition.
	ErrUnknownTrigger = errors.New("workflow-engine: unknown trigger")

	// ErrTokenNotFound is returned when a trigger targets a command/task id that
	// is not awaiting. It is one kind of invalid transition and wraps
	// [ErrInvalidTransition] so errors.Is holds for both sentinels.
	ErrTokenNotFound = fmt.Errorf("workflow-engine: no token awaiting command: %w", ErrInvalidTransition)

	// ErrInstanceTerminal is returned when a trigger that requires a live instance
	// is delivered to one that has already reached a terminal status
	// (StatusCompleted, StatusFailed, StatusTerminated). It is the error half of
	// the terminal contract every [Trigger] declares and [Step] applies: a trigger
	// whose sender is a synchronous external caller fails with this, while one
	// delivered by the engine's own asynchronous machinery is dropped silently
	// instead, returning the state unchanged and no error at all.
	// [CompensateRequested] takes either outcome — or neither — depending on its
	// payload and on what is left to compensate; each trigger type's own doc
	// comment states which applies.
	//
	// It wraps [ErrInvalidTransition] so errors.Is holds for both sentinels, which
	// is what makes the service layer classify it as its ErrConflict and the HTTP
	// transports map it to 422 with no change to either layer. An UNWRAPPED
	// sentinel here would instead fall through the transports' error classifier to
	// a 500 with an empty body. Those layers live outside this package and are not
	// imported by it; the coupling is the wrapping, nothing more.
	//
	// It is deliberately a sibling of, not a parent of, [ErrTokenNotFound]: callers
	// such as the call-link notifier key their idempotency branch on
	// [ErrTokenNotFound] and must not swallow a terminal-instance rejection.
	ErrInstanceTerminal = fmt.Errorf("workflow-engine: instance is terminal: %w", ErrInvalidTransition)

	// ErrTaskNotOpen is returned when a trigger that requires an open human task is
	// delivered to one already Completed or Cancelled. This is a second key the
	// instance-status guard cannot see: a task can close while the instance keeps
	// running, so a closed task on a live instance is reachable.
	//
	// Among the triggers, [HumanClaimed], [HumanReassigned] and [HumanCompleted]
	// report it, because each of them answers a synchronous external caller.
	// [HumanCandidatesResolved] deliberately does not: a closed task's candidate
	// list is part of its audit record and stays frozen, so restating it is
	// pointless rather than a caller error, and the trigger is a silent no-op
	// there instead.
	//
	// It is deliberately distinct from [humantask.ErrTaskNotFound], which means the
	// record is absent: a closed task is present, and a caller must be able to tell
	// "no such task" from "too late". It wraps [ErrInvalidTransition] for the same
	// classification reason as [ErrInstanceTerminal]. runtime/task aliases this, so
	// errors.Is(err, task.ErrTaskNotOpen) and errors.Is(err, engine.ErrTaskNotOpen)
	// both hold.
	ErrTaskNotOpen = fmt.Errorf("workflow-engine: human task is not open: %w", ErrInvalidTransition)

	// ErrInstanceAlreadyStarted is returned when [StartInstance] is delivered to an
	// instance that has already been started.
	//
	// Without it the start is accepted on ANY non-terminal instance and
	// SUPERIMPOSES a second start on the live one: measured, tokens 1 → 3 and human
	// tasks 1 → 2 in a single step, with the old parked token, its task and its
	// armed timers all surviving beside the new ones, [InstanceState.StartVariables]
	// — the audit record of what the process was started with — overwritten, and a
	// compensation cursor left naming an ActiveCmdID on an instance whose status the
	// restart flipped back to running, so the worker still executing that
	// compensation is answered with [ErrTokenNotFound].
	//
	// The guard is deliberately NOT a status test: StatusRunning is the zero value,
	// so a never-started state already reads as running and a status-keyed guard
	// would refuse every legitimate start. It is not a StartedAt test alone either —
	// [Step] is public API and the caller supplies the trigger's OccurredAt, so a
	// zero one leaves StartedAt.IsZero() true on an instance that HAS started
	// (measured: the naive guard let a second start through, tokens 1 → 2). The
	// predicate reads every witness of a prior start: StartedAt, Tokens and History.
	//
	// It wraps [ErrInvalidTransition] for the same classification reason as
	// [ErrInstanceTerminal] — the instance exists and is in the wrong state for this
	// trigger.
	ErrInstanceAlreadyStarted = fmt.Errorf("workflow-engine: instance has already been started: %w", ErrInvalidTransition)

	// ErrCancelNotApplicable is returned when a [CancelRequested] is DROPPED: a
	// terminal or admin partial-rollback compensation walk is already in flight, so
	// re-entering compensation would double-run the records it is still consuming,
	// and the cancel therefore does nothing at all.
	//
	// It reports an outcome; it does not abort anything. The engine's answer here
	// used to be err=<nil> with zero commands and a state byte-identical to the one
	// it was given, after which a partial rollback resumed and the "cancelled"
	// instance went back to running while its operator held an HTTP 200. Nothing
	// about that behaviour changes — only the honesty of the answer.
	//
	// ⚠ Callers must treat it as a REPORTING outcome, never as a propagation-halting
	// error. runtime.ProcessDriver.CancelInstance therefore excludes it from its
	// early return and re-reports it AFTER cancelling the instance's async children,
	// and propagateCancel's child loop recurses into a dropped child's own subtree
	// rather than skipping it. Measured with the naive shape (return on any error):
	// a terminated parent kept a permanently RUNNING child and grandchild — strictly
	// worse than the silent nil it replaces.
	//
	// It is deliberately NOT returned by the DEFERRED site (PendingCancel = true on
	// a RESUMING walk), which keeps returning nil: that cancel recorded its intent
	// and the instance really will terminate. "Will not terminate at all" and "will
	// terminate later" are different answers.
	//
	// It wraps [ErrInvalidTransition] for the same classification reason as
	// [ErrInstanceTerminal], so the service layer classifies it as its ErrConflict
	// and the HTTP transports answer 422 rather than a 500 with an empty body.
	ErrCancelNotApplicable = fmt.Errorf("workflow-engine: cancel is not applicable while a compensation walk is in flight: %w", ErrInvalidTransition)

	// ErrNoCompensationWalk is returned when a [ResolveCompensationStall] escape
	// verb arrives with no compensation walk in flight — the instance is not
	// compensating, or its cursor holds no dispatched command.
	//
	// An error rather than a no-op: every verb answers a synchronous operator, and
	// an operator told nothing would reasonably believe the escape worked. It
	// wraps [ErrInvalidTransition] for the same classification reason as
	// [ErrInstanceTerminal], so it reaches an HTTP admin as a conflict.
	ErrNoCompensationWalk = fmt.Errorf("workflow-engine: no compensation walk in flight: %w", ErrInvalidTransition)

	// ErrCompensationCommandMismatch is returned when a [ResolveCompensationStall]
	// names a CommandID that is not the walk's current ActiveCmdID.
	//
	// This is what makes the verbs idempotent: a replayed escape finds the cursor
	// already moved on and is refused rather than acting on whatever is in flight
	// NOW. Without the match a compensation action was measured running TWICE,
	// with the original completion then rejected as "no token awaiting command" —
	// which an at-least-once action transport turns into a redelivery loop.
	ErrCompensationCommandMismatch = fmt.Errorf("workflow-engine: compensation command id does not match the walk in flight: %w", ErrInvalidTransition)

	// ErrStallIncidentNotFound is returned when a [ResolveCompensationStall]
	// carries a non-empty IncidentID naming no open [IncidentCompensationStall].
	//
	// Deliberately NOT the idempotent no-op [ResolveIncident] uses for an unknown
	// id: an operator who mistypes an incident id must not silently receive a
	// walk-wide action they did not ask for.
	ErrStallIncidentNotFound = fmt.Errorf("workflow-engine: no open compensation-stall incident with that id: %w", ErrInvalidTransition)

	// ErrCompensationWalkResumes is returned when abandon is attempted on a
	// compensation walk that finishes by RESUMING — a compensation throw, a
	// partial rollback, or a full reverse.
	//
	// stepCompensationFinish selects its plan from the cursor's walk mode, so such
	// a walk takes a resume plan and a "terminate instead" override never applies.
	// Measured, abandoning a targeted throw DESTROYED its un-run stalled record
	// and left the instance running; on a scope-wide throw it cleared the whole
	// drained prefix. Skip is the verb that works on these walks — it drains them
	// to their natural resume — so no escape is lost.
	ErrCompensationWalkResumes = fmt.Errorf("workflow-engine: cannot abandon a compensation walk that resumes; use skip: %w", ErrInvalidTransition)

	// ErrIncidentNotResolvable is returned when [ResolveIncident] names an
	// incident whose kind it cannot act on. handleResolveIncident whitelists a
	// single kind — [IncidentAction] — so it refuses every other kind: today
	// [IncidentCompensationStall] and [IncidentCompensationFailed], and any kind
	// appended after them inherits the refusal.
	//
	// The refusal exists to prevent data loss, not merely to be tidy.
	// handleResolveIncident removes the incident BEFORE looking up its token and
	// returns no commands when the token is nil; both walk-scoped kinds carry
	// TokenID "", so unguarded they are silently EATEN (measured on a stall:
	// err=<nil>, cmds=[], incidents=0). An operator hitting the already-shipped
	// resolve-incident endpoint would delete the only record that the walk had
	// stalled — or that a compensation action had failed — making it invisible as
	// well as unresolved. The message names the verbs that do work.
	ErrIncidentNotResolvable = fmt.Errorf(
		"workflow-engine: this incident is not resolvable with resolve-incident; "+
			"use the compensation-walk verbs (retry, skip, abandon): %w", ErrInvalidTransition)

	// ErrNoMatchingFlow is returned when a gateway has no matching/default outgoing
	// flow. It is a definition/data error, not a wrong-state transition.
	ErrNoMatchingFlow = errors.New("workflow-engine: no matching outgoing flow")

	// ErrManualTaskPayload is returned when a wait-mode manual UserTask is completed
	// with a non-empty output, outcome, or note. A manual task is a form-less
	// checkpoint; supplying a payload is a caller error. Immediate-mode manual tasks
	// never take a trigger.
	ErrManualTaskPayload = errors.New("workflow-engine: manual user task cannot carry a completion payload")

	// ErrInvalidOutcome is returned when a UserTask completion carries an outcome
	// the node does not declare. Validation fails closed: once a node declares
	// outcomes, only those are accepted. A node declaring none is unconstrained —
	// any outcome at all completes it.
	ErrInvalidOutcome = errors.New("workflow-engine: completion outcome is not declared by the user task")

	// ErrOutcomeRequired is returned when a UserTask that declares outcomes is
	// completed without one. A declared set is a closed, mandatory value domain:
	// the outcome typically routes a downstream exclusive gateway, so accepting a
	// blank one would publish no routing variable and fail the step later with
	// [ErrNoMatchingFlow]. It is deliberately distinct from [ErrInvalidOutcome] so
	// a caller can tell "you sent nothing" from "you sent a value I do not accept".
	// A manual UserTask is exempt — it is forbidden from declaring outcomes
	// ([model.ErrManualTaskOutcome]) and completes on a bare trigger.
	ErrOutcomeRequired = errors.New("workflow-engine: user task requires a completion outcome")

	// ErrEmptyTriggerKey is returned when an inbound trigger's identity key is
	// empty. An identity key names one specific record; the empty string names
	// none, so the trigger cannot be dispatched.
	//
	// It is deliberately NOT wrapped in [ErrInvalidTransition]: the instance state is
	// irrelevant here, the trigger itself is malformed. Transports classify it 400,
	// alongside the other caller-correctable input sentinels.
	ErrEmptyTriggerKey = errors.New("workflow-engine: trigger identity key is empty")

	// ErrEmptyReassignTarget reports a HumanReassigned trigger whose To names no
	// actor. Reassignment moves a task's claim from one actor to another; the empty
	// string names none, so the result would be a Claimed task nobody holds —
	// invisible to AssignedTo (no claimant to match) and to ClaimableBy (not
	// Unclaimed), i.e. reachable only by ID.
	//
	// Deliberately NOT [ErrEmptyTriggerKey]: that sentinel is documented as an
	// *identity key* naming one specific record, and To is a required field rather
	// than the trigger's identity — TaskID already is. Like it, this is not wrapped
	// in [ErrInvalidTransition]: the instance state is irrelevant, the trigger
	// itself is malformed. Transports classify it 400.
	ErrEmptyReassignTarget = errors.New("workflow-engine: reassignment target is empty")
)
