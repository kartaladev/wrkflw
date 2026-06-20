package engine

import (
	"time"

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
	// TimerSLA is a deadline timer that fires when an SLA is breached.
	TimerSLA
	// TimerInWait is a recurring in-wait timer (e.g. reminder) that fires
	// periodically during a wait period.
	TimerInWait
)

// String returns the name of the TimerKind for debugging/logging.
func (k TimerKind) String() string {
	switch k {
	case TimerIntermediate:
		return "TimerIntermediate"
	case TimerSLA:
		return "TimerSLA"
	case TimerInWait:
		return "TimerInWait"
	default:
		return "TimerKind(unknown)"
	}
}

// ScheduleTimer asks the runtime to schedule a timer that will deliver a
// TimerFired trigger at FireAt. Kind distinguishes intermediate, SLA, and
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

// InvokeAction asks the runtime to run a named ServiceAction. Its result
// returns as an ActionCompleted/ActionFailed trigger carrying the same CommandID.
type InvokeAction struct {
	CommandID string
	Name      string
	Input     map[string]any
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

func (InvokeAction) isCommand()    {}
func (CompleteInstance) isCommand() {}
func (FailInstance) isCommand()    {}
func (AwaitHuman) isCommand()      {}
func (UpdateTask) isCommand()      {}
func (ScheduleTimer) isCommand()   {}
func (CancelTimer) isCommand()     {}
