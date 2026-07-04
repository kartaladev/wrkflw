// Package activity holds the BPMN activity node kinds — service, user, receive,
// send, and business-rule tasks, plus sub-process and call-activity — for the
// definition authoring layer. Import it to construct activities
// (activity.NewServiceTask, …) and, via its init, to register their
// (de)serialization with the definition package.
package activity

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
)

// --- concrete node types ---

// ServiceTask executes a named service action.
type ServiceTask struct {
	definition.Base
	definition.ActivityFields
	definition.TaskAction
}

// Kind returns definition.KindServiceTask.
func (ServiceTask) Kind() definition.NodeKind { return definition.KindServiceTask }

// UserTask waits for a human to complete a work item.
type UserTask struct {
	definition.Base
	definition.ActivityFields
	// CandidateRoles are the roles eligible to claim and complete this task.
	CandidateRoles []string
	// EligibilityPrivileges is a list of resource-privilege tokens (e.g. "finance-task claim")
	// evaluated by a casbin-backed Authorizer. Set via WithEligibilityPrivileges.
	EligibilityPrivileges []string
	// EligibilityExpr is an optional attribute predicate (expr) for fine-grained eligibility.
	EligibilityExpr string
}

// Kind returns definition.KindUserTask.
func (UserTask) Kind() definition.NodeKind { return definition.KindUserTask }

// ReceiveTask waits for an inbound message (signal or message correlation).
type ReceiveTask struct {
	definition.Base
	definition.ActivityFields
	// MessageName is the message reference for correlation.
	MessageName string
	// CorrelationKey is an expr expression evaluated at runtime to derive the correlation key.
	CorrelationKey string
}

// Kind returns definition.KindReceiveTask.
func (ReceiveTask) Kind() definition.NodeKind { return definition.KindReceiveTask }

// SendTask sends an outbound message.
type SendTask struct {
	definition.Base
	definition.ActivityFields
	// MessageName is the message reference to send.
	MessageName string
	// CorrelationKey is an optional expr expression evaluated at runtime to derive
	// the correlation key carried on the outbound message.
	CorrelationKey string
}

// Kind returns definition.KindSendTask.
func (SendTask) Kind() definition.NodeKind { return definition.KindSendTask }

// BusinessRuleTask executes a business rule action (by name or inline).
type BusinessRuleTask struct {
	definition.Base
	definition.ActivityFields
	definition.TaskAction
}

// Kind returns definition.KindBusinessRuleTask.
func (BusinessRuleTask) Kind() definition.NodeKind { return definition.KindBusinessRuleTask }

// SubProcess embeds a nested process definition executed as a scope.
type SubProcess struct {
	definition.Base
	definition.ActivityFields
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *definition.ProcessDefinition
}

// Kind returns definition.KindSubProcess.
func (SubProcess) Kind() definition.NodeKind { return definition.KindSubProcess }

// CallActivity delegates to a top-level process definition resolved by name.
type CallActivity struct {
	definition.Base
	definition.ActivityFields
	// DefRef is the name of the top-level process definition to call.
	DefRef string
}

// Kind returns definition.KindCallActivity.
func (CallActivity) Kind() definition.NodeKind { return definition.KindCallActivity }

// --- constructors ---

// NewServiceTask constructs a ServiceTask. Set the action with WithActionName
// (catalog reference) or WithAction/WithActionFunc (node-local inline); with
// neither, the action name defaults to the node id at execution time.
func NewServiceTask(id string, opts ...ServiceTaskOption) definition.Node {
	s := ServiceTask{Base: definition.NewBase(id, "")}
	for _, o := range opts {
		o.applyServiceTask(&s)
	}
	return s
}

// NewUserTask constructs a UserTask with the given id and candidate roles.
func NewUserTask(id string, roles []string, opts ...UserTaskOption) definition.Node {
	u := UserTask{Base: definition.NewBase(id, ""), CandidateRoles: roles}
	for _, o := range opts {
		o.applyUserTask(&u)
	}
	return u
}

// NewReceiveTask constructs a ReceiveTask with the given id and message name.
func NewReceiveTask(id, messageName string, opts ...ReceiveTaskOption) definition.Node {
	r := ReceiveTask{Base: definition.NewBase(id, ""), MessageName: messageName}
	for _, o := range opts {
		o.applyReceiveTask(&r)
	}
	return r
}

// NewSendTask constructs a SendTask with the given id and message name.
func NewSendTask(id, messageName string, opts ...SendTaskOption) definition.Node {
	s := SendTask{Base: definition.NewBase(id, ""), MessageName: messageName}
	for _, o := range opts {
		o.applySendTask(&s)
	}
	return s
}

// NewBusinessRuleTask constructs a BusinessRuleTask. Action configuration mirrors
// NewServiceTask (WithActionName / WithAction / WithActionFunc / default-by-id).
func NewBusinessRuleTask(id string, opts ...BusinessRuleOption) definition.Node {
	b := BusinessRuleTask{Base: definition.NewBase(id, "")}
	for _, o := range opts {
		o.applyBusinessRule(&b)
	}
	return b
}

// NewSubProcess constructs a SubProcess with the given id and nested definition.
func NewSubProcess(id string, sub *definition.ProcessDefinition, opts ...ActivityOption) definition.Node {
	n := SubProcess{Base: definition.NewBase(id, ""), Subprocess: sub}
	applyActivityOpts(&n.Base, &n.ActivityFields, opts)
	return n
}

// NewCallActivity constructs a CallActivity with the given id and definition reference.
func NewCallActivity(id, defRef string, opts ...ActivityOption) definition.Node {
	n := CallActivity{Base: definition.NewBase(id, ""), DefRef: defRef}
	applyActivityOpts(&n.Base, &n.ActivityFields, opts)
	return n
}

// --- action.Func adapter re-export convenience ---

// actionFunc adapts a plain function to action.ServiceAction for WithActionFunc.
func actionFunc(fn func(context.Context, map[string]any) (map[string]any, error)) action.ServiceAction {
	return action.Func(fn)
}

// --- serialization registration ---

func init() {
	definition.RegisterKind(definition.KindServiceTask, definition.NodeSpec{
		Name: "serviceTask",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return ServiceTask{Base: b, ActivityFields: w.Activity(), TaskAction: definition.TaskAction{Action: w.Action}}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(ServiceTask)
			w.Action = v.Action
			w.PutActivity(v.ActivityFields)
		},
	})
	definition.RegisterKind(definition.KindUserTask, definition.NodeSpec{
		Name: "userTask",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return UserTask{Base: b, ActivityFields: w.Activity(), CandidateRoles: w.CandidateRoles, EligibilityPrivileges: w.EligibilityPrivileges, EligibilityExpr: w.EligibilityExpr}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(UserTask)
			w.CandidateRoles, w.EligibilityPrivileges, w.EligibilityExpr = v.CandidateRoles, v.EligibilityPrivileges, v.EligibilityExpr
			w.PutActivity(v.ActivityFields)
		},
	})
	definition.RegisterKind(definition.KindReceiveTask, definition.NodeSpec{
		Name: "receiveTask",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return ReceiveTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(ReceiveTask)
			w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
			w.PutActivity(v.ActivityFields)
		},
	})
	definition.RegisterKind(definition.KindSendTask, definition.NodeSpec{
		Name: "sendTask",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return SendTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(SendTask)
			w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
			w.PutActivity(v.ActivityFields)
		},
	})
	definition.RegisterKind(definition.KindBusinessRuleTask, definition.NodeSpec{
		Name: "businessRuleTask",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return BusinessRuleTask{Base: b, ActivityFields: w.Activity(), TaskAction: definition.TaskAction{Action: w.Action}}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(BusinessRuleTask)
			w.Action = v.Action
			w.PutActivity(v.ActivityFields)
		},
	})
	definition.RegisterKind(definition.KindSubProcess, definition.NodeSpec{
		Name: "subProcess",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return SubProcess{Base: b, ActivityFields: w.Activity(), Subprocess: w.Subprocess}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(SubProcess)
			w.Subprocess = v.Subprocess
			w.PutActivity(v.ActivityFields)
		},
	})
	definition.RegisterKind(definition.KindCallActivity, definition.NodeSpec{
		Name: "callActivity",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return CallActivity{Base: b, ActivityFields: w.Activity(), DefRef: w.DefRef}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(CallActivity)
			w.DefRef = v.DefRef
			w.PutActivity(v.ActivityFields)
		},
	})
}
