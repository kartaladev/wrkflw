// Package engine is the pure token state machine (ADR-0002). Step maps
// (definition, state, Trigger) -> (state, []Command) with no I/O, no clock
// reads, and no goroutines.
package engine

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
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
}

// ActionCompleted reports that a ServiceAction finished successfully.
type ActionCompleted struct {
	baseTrigger
	CommandID string
	Output    map[string]any
}

// ActionFailed reports that a ServiceAction failed.
type ActionFailed struct {
	baseTrigger
	CommandID string
	Err       string
	Retryable bool
}

// NewStartInstance builds a StartInstance trigger stamped with the given time.
func NewStartInstance(at time.Time, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars}
}

// NewActionCompleted builds an ActionCompleted trigger reporting a successful service-action result.
func NewActionCompleted(at time.Time, commandID string, output map[string]any) ActionCompleted {
	return ActionCompleted{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Output: output}
}

// NewActionFailed builds an ActionFailed trigger reporting a service-action error and whether it is retryable.
func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool) ActionFailed {
	return ActionFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg, Retryable: retryable}
}

// HumanCompleted reports that a human-task node was completed by an actor.
type HumanCompleted struct {
	baseTrigger
	TaskToken string
	Output    map[string]any
	Actor     authz.Actor
}

// HumanClaimed reports that an actor has claimed a human-task node.
type HumanClaimed struct {
	baseTrigger
	TaskToken string
	Actor     authz.Actor
}

// HumanReassigned reports that a human-task node was reassigned from one actor
// to another by a third party (e.g. an admin).
type HumanReassigned struct {
	baseTrigger
	TaskToken string
	From      string
	To        string
	By        authz.Actor
}

// NewHumanCompleted builds a HumanCompleted trigger stamped with the given time.
func NewHumanCompleted(at time.Time, taskToken string, output map[string]any, actor authz.Actor) HumanCompleted {
	return HumanCompleted{baseTrigger: baseTrigger{at: at}, TaskToken: taskToken, Output: output, Actor: actor}
}

// NewHumanClaimed builds a HumanClaimed trigger stamped with the given time.
func NewHumanClaimed(at time.Time, taskToken string, actor authz.Actor) HumanClaimed {
	return HumanClaimed{baseTrigger: baseTrigger{at: at}, TaskToken: taskToken, Actor: actor}
}

// NewHumanReassigned builds a HumanReassigned trigger stamped with the given time.
// From is the previous assignee, To is the new assignee, By is the actor performing the reassignment.
func NewHumanReassigned(at time.Time, taskToken, from, to string, by authz.Actor) HumanReassigned {
	return HumanReassigned{baseTrigger: baseTrigger{at: at}, TaskToken: taskToken, From: from, To: to, By: by}
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
// Task 3+ implements the engine's handling of this trigger (resume the parked
// parent token and merge Output into the parent instance variables).
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
// description of the failure reason.
//
// Task 3+ implements the engine's handling of this trigger (mark the parent
// token/instance failed or route to an error boundary if one is configured).
type SubInstanceFailed struct {
	baseTrigger
	// CommandID matches the StartSubInstance.CommandID that started the child.
	CommandID string
	// Err is the error message from the failed child instance.
	Err string
}

// NewSubInstanceFailed builds a SubInstanceFailed trigger stamped with the
// given time. at is the moment the child instance failure was observed.
func NewSubInstanceFailed(at time.Time, commandID, errMsg string) SubInstanceFailed {
	return SubInstanceFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg}
}
