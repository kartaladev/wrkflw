// Package activity holds the workflow activity node kinds — service, user, receive,
// send, and business-rule tasks, plus sub-process and call-activity — for the
// definition authoring layer. Import it to construct activities
// (activity.NewServiceTask, …) and, via its init, to register their
// (de)serialization with the definition package.
package activity

import (
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// --- concrete node types ---

// ServiceTask executes a named service action.
type ServiceTask struct {
	model.Base
	model.ActivityFields
	model.TaskAction
}

// Kind returns model.KindServiceTask.
func (ServiceTask) Kind() model.NodeKind { return model.KindServiceTask }

// UserTask waits for a human to complete a work item.
type UserTask struct {
	model.Base
	model.ActivityFields
	// CandidateRoles are the roles eligible to claim and complete this task.
	CandidateRoles []string
	// EligibilityPrivileges is a list of resource-privilege tokens (e.g. "finance-task claim")
	// evaluated by a casbin-backed Authorizer. Set via WithEligibilityPrivileges.
	EligibilityPrivileges []string
	// EligibilityExpr is an optional attribute predicate (expr) for fine-grained eligibility.
	EligibilityExpr string
	// CompletionValidation, when set, validates the task's completion output
	// before it is applied to the process instance's variables. Nil = no
	// validation. Set via WithCompletionValidation.
	CompletionValidation validate.ValidationStrategy
	// Manual, when true, marks this UserTask as a manual task: it completes on a
	// bare trigger (no payload/form-data) and must not carry CompletionValidation.
	// Eligibility is still honored if set. See ADR-0118. This deliberately
	// diverges from strict BPMN Manual Task (which has no execution semantics /
	// auto-completes): a manual task here keeps a durable "someone confirmed this"
	// checkpoint.
	Manual bool
}

// Kind returns model.KindUserTask.
func (UserTask) Kind() model.NodeKind { return model.KindUserTask }

// ReceiveTask waits for an inbound message (signal or message correlation).
type ReceiveTask struct {
	model.Base
	model.ActivityFields
	// MessageName is the message reference for correlation.
	MessageName string
	// CorrelationKey is an expr expression evaluated at runtime to derive the correlation key.
	CorrelationKey string
	// PayloadValidation, when set, validates the inbound message payload before
	// it is applied to the process instance's variables. Nil = no validation.
	// Set via WithPayloadValidation.
	PayloadValidation validate.ValidationStrategy
}

// Kind returns model.KindReceiveTask.
func (ReceiveTask) Kind() model.NodeKind { return model.KindReceiveTask }

// SendTask sends an outbound message.
type SendTask struct {
	model.Base
	model.ActivityFields
	// MessageName is the message reference to send.
	MessageName string
	// CorrelationKey is an optional expr expression evaluated at runtime to derive
	// the correlation key carried on the outbound message.
	CorrelationKey string
}

// Kind returns model.KindSendTask.
func (SendTask) Kind() model.NodeKind { return model.KindSendTask }

// BusinessRuleTask executes a named business rule action.
type BusinessRuleTask struct {
	model.Base
	model.ActivityFields
	model.TaskAction
}

// Kind returns model.KindBusinessRuleTask.
func (BusinessRuleTask) Kind() model.NodeKind { return model.KindBusinessRuleTask }

// SubProcess embeds a nested process definition executed as a scope.
type SubProcess struct {
	model.Base
	model.ActivityFields
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *model.ProcessDefinition
}

// Kind returns model.KindSubProcess.
func (SubProcess) Kind() model.NodeKind { return model.KindSubProcess }

// CallActivity delegates to a top-level process definition resolved by
// reference (id, or id:version).
type CallActivity struct {
	model.Base
	model.ActivityFields
	// DefRef is the reference (id, or id:version) of the top-level process
	// definition to call. A zero Version selects the latest.
	DefRef model.Qualifier
}

// Kind returns model.KindCallActivity.
func (CallActivity) Kind() model.NodeKind { return model.KindCallActivity }

// --- constructors ---

// NewServiceTask constructs a ServiceTask. Set the action with WithTaskAction
// (catalog reference); with no name, the action name defaults to the node id
// at execution time.
func NewServiceTask(id string, opts ...ServiceTaskOption) model.Node {
	s := ServiceTask{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyServiceTask(&s)
	}
	return s
}

// NewUserTask constructs a UserTask. Eligibility is optional and set via
// WithCandidateRoles / WithEligibilityPrivileges / WithEligibilityExpr; with
// none set, the engine gate is open (authorization defers to the transport
// layer). See ADR-0117.
func NewUserTask(id string, opts ...UserTaskOption) model.Node {
	u := UserTask{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyUserTask(&u)
	}
	return u
}

// NewReceiveTask constructs a ReceiveTask with the given id and message name.
func NewReceiveTask(id, messageName string, opts ...ReceiveTaskOption) model.Node {
	r := ReceiveTask{Base: model.NewBase(id, ""), MessageName: messageName}
	for _, o := range opts {
		o.applyReceiveTask(&r)
	}
	return r
}

// NewSendTask constructs a SendTask with the given id and message name.
func NewSendTask(id, messageName string, opts ...SendTaskOption) model.Node {
	s := SendTask{Base: model.NewBase(id, ""), MessageName: messageName}
	for _, o := range opts {
		o.applySendTask(&s)
	}
	return s
}

// NewBusinessRuleTask constructs a BusinessRuleTask. Action configuration mirrors
// NewServiceTask (WithTaskAction / default-by-id).
func NewBusinessRuleTask(id string, opts ...BusinessRuleOption) model.Node {
	b := BusinessRuleTask{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyBusinessRule(&b)
	}
	return b
}

// NewSubProcess constructs a SubProcess with the given id and nested model.
func NewSubProcess(id string, sub *model.ProcessDefinition, opts ...ActivityOption) model.Node {
	n := SubProcess{Base: model.NewBase(id, ""), Subprocess: sub}
	applyActivityOpts(&n.Base, &n.ActivityFields, opts)
	return n
}

// NewCallActivity constructs a CallActivity with the given id and definition
// reference (id, or id:version — a zero Version selects the latest).
func NewCallActivity(id string, ref model.Qualifier, opts ...ActivityOption) model.Node {
	n := CallActivity{Base: model.NewBase(id, ""), DefRef: ref}
	applyActivityOpts(&n.Base, &n.ActivityFields, opts)
	return n
}

// parseOrZero parses a CallActivity def-ref wire string into a model.Qualifier,
// returning the zero Qualifier on a parse error or empty input. FromWire has no
// error return, so an invalid/empty ref hydrates to the zero value and is
// rejected by definition validation (the call-activity def-ref required check).
func parseOrZero(s string) model.Qualifier {
	q, _ := model.ParseQualifier(s)
	return q
}

// --- serialization registration ---

func init() {
	model.RegisterKind(model.KindServiceTask, model.NodeSpec{
		Name: "serviceTask",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return ServiceTask{Base: b, ActivityFields: w.Activity(), TaskAction: model.TaskAction{Action: w.Action}}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(ServiceTask)
			w.Action = v.Action
			w.PutActivity(v.ActivityFields)
		},
	})
	model.RegisterKind(model.KindUserTask, model.NodeSpec{
		Name: "userTask",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			n := UserTask{Base: b, ActivityFields: w.Activity(), CandidateRoles: w.CandidateRoles, EligibilityPrivileges: w.EligibilityPrivileges, EligibilityExpr: w.EligibilityExpr, Manual: w.Manual}
			if w.Validation != nil {
				n.CompletionValidation = model.PendingValidation(*w.Validation)
			}
			return n
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(UserTask)
			w.CandidateRoles, w.EligibilityPrivileges, w.EligibilityExpr = v.CandidateRoles, v.EligibilityPrivileges, v.EligibilityExpr
			w.Manual = v.Manual
			w.PutActivity(v.ActivityFields)
			w.Validation = model.PutValidation(v.CompletionValidation)
		},
		ValidationGet: func(n model.Node) validate.ValidationStrategy { return n.(UserTask).CompletionValidation },
		ValidationSet: func(n model.Node, s validate.ValidationStrategy) model.Node {
			v := n.(UserTask)
			v.CompletionValidation = s
			return v
		},
	})
	model.RegisterKind(model.KindReceiveTask, model.NodeSpec{
		Name: "receiveTask",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			n := ReceiveTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
			if w.Validation != nil {
				n.PayloadValidation = model.PendingValidation(*w.Validation)
			}
			return n
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(ReceiveTask)
			w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
			w.PutActivity(v.ActivityFields)
			w.Validation = model.PutValidation(v.PayloadValidation)
		},
		ValidationGet: func(n model.Node) validate.ValidationStrategy { return n.(ReceiveTask).PayloadValidation },
		ValidationSet: func(n model.Node, s validate.ValidationStrategy) model.Node {
			v := n.(ReceiveTask)
			v.PayloadValidation = s
			return v
		},
	})
	model.RegisterKind(model.KindSendTask, model.NodeSpec{
		Name: "sendTask",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return SendTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(SendTask)
			w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
			w.PutActivity(v.ActivityFields)
		},
	})
	model.RegisterKind(model.KindBusinessRuleTask, model.NodeSpec{
		Name: "businessRuleTask",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return BusinessRuleTask{Base: b, ActivityFields: w.Activity(), TaskAction: model.TaskAction{Action: w.Action}}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(BusinessRuleTask)
			w.Action = v.Action
			w.PutActivity(v.ActivityFields)
		},
	})
	model.RegisterKind(model.KindSubProcess, model.NodeSpec{
		Name: "subProcess",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return SubProcess{Base: b, ActivityFields: w.Activity(), Subprocess: w.Subprocess}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(SubProcess)
			w.Subprocess = v.Subprocess
			w.PutActivity(v.ActivityFields)
		},
	})
	model.RegisterKind(model.KindCallActivity, model.NodeSpec{
		Name: "callActivity",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return CallActivity{Base: b, ActivityFields: w.Activity(), DefRef: parseOrZero(w.DefRef)}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(CallActivity)
			w.DefRef = v.DefRef.String()
			w.PutActivity(v.ActivityFields)
		},
	})
}
