package engine

import (
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// Command is the sealed set of side effects the core asks the runtime to
// perform. The unexported marker keeps the set closed.
type Command interface {
	isCommand()
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

func (InvokeAction) isCommand()     {}
func (CompleteInstance) isCommand() {}
func (FailInstance) isCommand()     {}
func (AwaitHuman) isCommand()       {}
func (UpdateTask) isCommand()       {}
