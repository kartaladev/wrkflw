// Package engine is the pure token state machine (ADR-0002). Step maps
// (definition, state, Trigger) -> (state, []Command) with no I/O, no clock
// reads, and no goroutines.
package engine

import "time"

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

func NewStartInstance(at time.Time, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars}
}

func NewActionCompleted(at time.Time, commandID string, output map[string]any) ActionCompleted {
	return ActionCompleted{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Output: output}
}

func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool) ActionFailed {
	return ActionFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg, Retryable: retryable}
}
