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
