// Package service provides a transport-agnostic Service facade that unifies
// the workflow engine's core capabilities behind a single interface. The HTTP
// transport adapters depend exclusively on this package; they never import the
// engine core directly.
package service

import (
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
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
// correct instance by (Name, CorrelationKey) without needing the caller
// to know which instance is waiting.
type DeliverMessageRequest struct {
	// DefRef is the process-definition reference (id, or id:version); the
	// definition is resolved via the registry before calling
	// ProcessDriver.DeliverMessage. A zero Version selects the latest.
	DefRef model.Qualifier
	// Name is the message name.
	Name string
	// CorrelationKey is the value that routes the message to a specific instance.
	CorrelationKey string
	// Payload is an optional set of message variables.
	Payload map[string]any
}

// ClaimTaskRequest carries the parameters for claiming a human task.
type ClaimTaskRequest struct {
	// TaskToken is the opaque token that identifies the human task.
	TaskToken string
	// Actor is the principal claiming the task.
	Actor authz.Actor
}

// CompleteTaskRequest carries the parameters for completing a human task.
type CompleteTaskRequest struct {
	// TaskToken is the opaque token that identifies the human task.
	TaskToken string
	// Actor is the principal completing the task.
	Actor authz.Actor
	// Output is the set of output variables produced by the task.
	Output map[string]any
}

// ReassignTaskRequest carries the parameters for reassigning a human task
// from one actor to another.
type ReassignTaskRequest struct {
	// TaskToken is the opaque token that identifies the human task.
	TaskToken string
	// From is the actor ID of the current claimant.
	From string
	// To is the actor ID of the new claimant.
	To string
	// By is the principal performing the reassignment (must satisfy the
	// task's eligibility spec).
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
	// Values ≤ 0 are treated as 1 by the Engine implementation.
	AddAttempts int
}
