package engine

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// Command is the sealed set of side effects the core asks the runtime to
// perform. The unexported marker keeps the set closed.
type Command interface {
	isCommand()
}

// TimerKind discriminates the purpose of a scheduled timer.
type TimerKind int

const (
	// TimerIntermediate is a timer on an intermediate catch event node.
	TimerIntermediate TimerKind = iota
	// TimerDeadline is a deadline timer that fires when a deadline is breached.
	TimerDeadline
	// TimerInWait is a recurring in-wait timer (e.g. reminder) that fires
	// periodically during a wait period.
	TimerInWait
	// TimerRetry is a one-shot timer that the runtime schedules to re-invoke
	// a failed action after its backoff (plus optional jitter) has elapsed.
	TimerRetry
)

// String returns the name of the TimerKind for debugging/logging.
func (k TimerKind) String() string {
	switch k {
	case TimerIntermediate:
		return "TimerIntermediate"
	case TimerDeadline:
		return "TimerDeadline"
	case TimerInWait:
		return "TimerInWait"
	case TimerRetry:
		return "TimerRetry"
	default:
		return "TimerKind(unknown)"
	}
}

// ScheduleTimer asks the runtime to schedule a timer that will deliver a
// TimerFired trigger at FireAt. Kind distinguishes intermediate, deadline, and
// in-wait timers so the runtime can apply the right scheduling policy.
type ScheduleTimer struct {
	TimerID string
	Token   string
	FireAt  time.Time
	Kind    TimerKind
}

// CancelTimer asks the runtime to cancel a previously scheduled timer.
type CancelTimer struct {
	TimerID string
}

// InvokeAction asks the runtime to run a ServiceAction. Its result returns as
// ActionCompleted/ActionFailed with the same CommandID.
//
// Name is the lookup key: the explicit action name, or the node id when the node
// declared no name (default-by-id).
//
// Inline and Scoped are set by the engine for MAIN-action invocations only
// (KindServiceTask / KindBusinessRuleTask entry and retry re-invocation). The
// engine holds the exact scope-effective definition and node at those sites, so
// it resolves both tiers from the CORRECT scope — which is essential for nodes
// nested inside a sub-process, where a runtime-side flat node lookup against the
// top-level definition would miss (or, with repeated ids across sub-processes,
// match the wrong node).
//
//   - Inline is the node-local inline action (WithAction/WithActionFunc), or nil.
//     When non-nil the runtime runs it directly, bypassing name resolution.
//   - Scoped is the scope-effective definition's scoped catalog, or nil. The
//     runtime resolves Name against it before the global catalog.
//
// SECONDARY invocations (compensation, deadline, reminder, throw-compensation) leave
// both nil; the runtime falls back to the top-level definition's scoped catalog
// + global for them. That is a documented limitation: secondary actions resolve
// against the ROOT definition's scoped catalog + global, not nested scoped catalogs.
type InvokeAction struct {
	CommandID string
	Name      string
	Inline    action.ServiceAction
	Scoped    action.Catalog
	Input     map[string]any
	// FireAndForget marks an action the engine runs for its side effect only
	// (deadline-breach and reminder actions). No token awaits its result, so the
	// runtime must run it WITHOUT feeding an ActionCompleted/ActionFailed back into
	// the engine — otherwise handleActionCompleted would report ErrTokenNotFound for
	// a command no token ever parked on. CommandID is still set for tracing/metrics.
	// (Distinct from InvokeCancelAction, which is cancel-specific and carries no
	// CommandID.)
	FireAndForget bool
}

// CompleteInstance marks the instance complete with a result.
type CompleteInstance struct {
	Result map[string]any
}

// FailInstance marks the instance failed.
type FailInstance struct {
	Err string
}

// AwaitHuman asks the runtime to create a human-task record and park the engine
// until a HumanCompleted trigger arrives. Eligibility describes who may act.
type AwaitHuman struct {
	TaskToken   string
	Eligibility authz.AuthzSpec
}

// UpdateTask asks the runtime to persist an updated [humantask.HumanTask] record
// (e.g. after a claim or reassignment).
type UpdateTask struct {
	Task humantask.HumanTask
}

// ThrowSignal asks the runtime to broadcast a named signal to all interested
// subscribers (other process instances or external listeners). It is emitted
// when execution passes through a KindIntermediateThrowEvent node whose
// SignalName is set. The runtime is responsible for delivering the signal;
// the engine continues past the throw node without waiting for delivery.
type ThrowSignal struct {
	Name    string
	Payload map[string]any
}

// StartSubInstance asks the runtime to start a new child process instance for
// the given process definition reference. The result is correlated back to the
// parent via CommandID: a SubInstanceCompleted or SubInstanceFailed trigger
// carrying the same CommandID will resume the waiting parent token.
//
// Input is the initial variables passed into the child instance. The parent
// engine state must not be mutated after emitting this command (the calling
// convention of Step guarantees this via cloneState).
//
// Note: the drive-behavior (parking the parent token and routing the result
// back) is implemented in Task 3+. This command type is defined here so that
// the sealed Command set is extended atomically with the trigger types.
type StartSubInstance struct {
	// CommandID is a unique identifier generated by the engine (via CmdSeq)
	// used to correlate the child's completion/failure back to this command.
	CommandID string
	// DefRef is the process definition reference (ID, or "ID:version") for the
	// child process to start. Resolution is left to the runtime.
	DefRef string
	// Input is the initial variable map passed to the child instance. May be nil.
	Input map[string]any
}

// SendMessage asks the runtime to emit an outbound message through its
// consumer-wired message sink. It is emitted when execution passes through a
// KindSendTask node. The engine treats it as fire-and-forget: it does not wait
// for delivery and the token auto-advances past the send node in the same Step.
//
// The sink decides how to route the message (intra-engine delivery to a parked
// ReceiveTask via DeliverMessage, publication to an external broker / the
// eventing outbox, or both). Payload carries a copy of the instance variables.
type SendMessage struct {
	// Name is the message reference to send (the SendTask's MessageName).
	Name string
	// CorrelationKey is the resolved correlation key (empty when the SendTask
	// declared no correlation-key expression).
	CorrelationKey string
	// Payload is a copy of the sending instance's variables. May be nil.
	Payload map[string]any
}

// InvokeCancelAction asks the runtime to run a named ServiceAction as a
// best-effort side effect during cancellation. Unlike InvokeAction it carries no
// CommandID and its result is never fed back into the engine (the instance is
// already terminal). The runtime logs a failure; it never fails the cancel.
type InvokeCancelAction struct {
	Name  string
	Input map[string]any
}

// Compensate is RESERVED for future scope-targeted compensation. It is part of
// the sealed Command set so that the command space is extended atomically, but it
// is NOT YET EMITTED or wired into any Step path.
//
// Intended use (not yet implemented): an engine-internal cancel/error path could
// emit Compensate to compensate a specific scope before terminating — for example,
// compensating only the records accumulated inside one sub-process scope rather than
// the whole instance. ScopeID identifies the target scope ("" = root); FromNode is
// the most recently completed node to start the reverse walk from ("" = all records
// in scope).
//
// Current state of compensation:
//   - The admin entry point is the CompensateRequested trigger (a Trigger, not a
//     Command), which walks InstanceState.RootCompensations in reverse order.
//   - When a sub-process scope closes normally, its accumulated CompensationRecords
//     are ARCHIVED into InstanceState.ArchivedCompensations keyed by the sub-process
//     node ID via archiveCompensations before closeScope is called (ADR-0039).
//     Archived records are merged into RootCompensations by consolidateArchiveIntoRoot
//     when a compensation walk begins (CompensateRequested / cancel / error path).
//   - Compensate{ScopeID, FromNode} remains RESERVED for future scope-targeted
//     compensation, which requires a producer (e.g. a BPMN compensation boundary
//     event or throw event) not yet built. Do not rely on this command being emitted.
//
// NOTE: FromNode is currently unused. There is no shared "compensationWalk"
// function; the CompensateRequested path (stepCompensateRequested in step.go) and
// any future Compensate-command path will share the same cursor-based logic once
// this command is wired.
type Compensate struct {
	// ScopeID identifies the execution scope to compensate. Empty = root scope.
	ScopeID string
	// FromNode is the BPMN node ID to start the reverse walk from.
	// Empty means compensate ALL records in the scope.
	// NOTE: Not yet used — Compensate is not yet emitted.
	FromNode string
}

func (InvokeAction) isCommand()       {}
func (InvokeCancelAction) isCommand() {}
func (CompleteInstance) isCommand()   {}
func (FailInstance) isCommand()       {}
func (AwaitHuman) isCommand()         {}
func (UpdateTask) isCommand()         {}
func (ScheduleTimer) isCommand()      {}
func (CancelTimer) isCommand()        {}
func (ThrowSignal) isCommand()        {}
func (SendMessage) isCommand()        {}
func (StartSubInstance) isCommand()   {}
func (Compensate) isCommand()         {}

// Compile-time assertions: each command must satisfy Command.
var _ Command = InvokeCancelAction{}
var _ Command = Compensate{}
