// Package service provides a transport-agnostic Service facade that unifies
// the workflow engine's core capabilities behind a single interface. The HTTP
// transport adapters depend exclusively on this package; they never import the
// engine core directly.
package service

import (
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// StartInstanceRequest carries the parameters for starting a new process instance.
type StartInstanceRequest struct {
	// DefRef is the process-definition reference (id, or id:version) used to look
	// up the definition in the registry. A zero Version selects the latest.
	DefRef model.Qualifier
	// Vars is the initial set of process variables.
	Vars map[string]any
}

// DeliverSignalRequest carries the parameters for delivering a signal to a
// running process instance.
type DeliverSignalRequest struct {
	// InstanceID identifies the target process instance.
	InstanceID string
	// Signal is the name of the signal to deliver (e.g. "approved").
	Signal string
	// Payload is an optional map of variables attached to the signal.
	Payload map[string]any
}

// DeliverMessageRequest carries the parameters for delivering a message.
// The driver's internal message-waiter table routes the message to the
// correct instance by (Name, CorrelationKey), or starts a new instance from a
// unique message-start event when none is waiting — without needing
// the caller to know which instance is waiting or which definition to use.
type DeliverMessageRequest struct {
	// Name is the message name.
	Name string
	// CorrelationKey is the value that routes the message to a specific instance.
	CorrelationKey string
	// Payload is an optional set of message variables.
	Payload map[string]any
}

// ClaimTaskRequest carries the parameters for claiming a human task.
type ClaimTaskRequest struct {
	// TaskID is the opaque token that identifies the human task.
	TaskID string
	// Actor is the principal claiming the task.
	Actor authz.Actor
}

// CompleteTaskRequest carries the parameters for completing a human task.
type CompleteTaskRequest struct {
	// TaskID is the opaque token that identifies the human task.
	TaskID string
	// Actor is the principal completing the task.
	Actor authz.Actor
	// Outcome is the business outcome the actor chose (e.g. "approve"). It is
	// recorded on the task's completion audit and, when the user-task node
	// declares an outcome set, validated against it — a value outside the set is
	// rejected with [engine.ErrInvalidOutcome] and an EMPTY value with
	// [engine.ErrOutcomeRequired], since declaring a set makes the outcome
	// mandatory. Empty means no outcome, which is valid only when the node
	// declares none.
	Outcome string
	// Note is the actor's free-text remark accompanying the completion; optional.
	Note string
	// Output is the set of output variables produced by the task.
	Output map[string]any
}

// ReassignTaskRequest carries the parameters for reassigning a human task
// from one actor to another.
type ReassignTaskRequest struct {
	// TaskID is the opaque token that identifies the human task.
	TaskID string
	// From is the actor ID of the current claimant.
	From string
	// To is the actor ID of the new claimant.
	To string
	// By is the principal performing the reassignment (must satisfy the
	// task's eligibility spec).
	By authz.Actor
}

// RefreshTaskCandidatesRequest carries the parameters for re-resolving the
// candidate actors of an open human task.
type RefreshTaskCandidatesRequest struct {
	// TaskID is the opaque token that identifies the human task.
	TaskID string
	// By is the principal requesting the refresh. It must satisfy the task's
	// eligibility spec — the same policy ReassignTaskRequest.By is held to.
	By authz.Actor
}

// CancelInstanceRequest carries the parameters for cancelling a process instance.
type CancelInstanceRequest struct {
	// InstanceID identifies the process instance to cancel.
	InstanceID string
}

// ResolveIncidentRequest carries the parameters for resolving an open incident
// on a process instance that has exhausted its automatic retry budget.
type ResolveIncidentRequest struct {
	// InstanceID identifies the process instance that owns the incident.
	InstanceID string
	// IncidentID is the unique identifier of the incident to resolve.
	IncidentID string
	// AddAttempts is the number of additional execution attempts to grant the
	// failing node before the operator considers the incident resolved.
	// Values ≤ 0 are treated as 1 by the ProcessEngine implementation.
	AddAttempts int
}

// ResolveCompensationStallRequest carries an operator's escape from a
// compensation walk whose dispatched action stopped reporting back.
type ResolveCompensationStallRequest struct {
	// InstanceID identifies the process instance whose walk is stalled.
	InstanceID string
	// CommandID is the stalled compensation dispatch. REQUIRED, and it must match
	// the walk's in-flight command id — read it from ProcessInstance's
	// `compensating.active_command_id`.
	//
	// The match is what makes every verb idempotent: a replayed request finds the
	// cursor already moved on and is refused rather than acting on whatever is in
	// flight now.
	CommandID string
	// IncidentID optionally names the stall incident being cleared. Empty targets
	// the walk in flight — the normal case, since stall detection is off by
	// default and there may be no incident to name. A non-empty id naming no open
	// stall incident is an error, not a silent walk-wide action.
	IncidentID string
	// Disposition selects retry, skip or abandon.
	//
	// ⚠ abandon is destructive and irreversible, and is accepted only on a walk
	// that terminates; retry re-executes a named remote action. Gate them
	// accordingly.
	Disposition engine.CompensationDisposition
}
