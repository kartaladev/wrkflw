package definition

import (
	"encoding/json"
	"fmt"
)

// NodeWire is the flat JSON/JSONB representation of any node. It is the single
// serialization shape; previously stored definitions decode through it
// unchanged. Field names/order mirror the pre-interface Node struct.
type NodeWire struct {
	ID                    string             `json:"id"`
	Kind                  NodeKind           `json:"kind"`
	Name                  string             `json:"name,omitempty"`
	Action                string             `json:"action,omitempty"`
	CandidateRoles        []string           `json:"candidateRoles,omitempty"`
	EligibilityPrivileges []string           `json:"eligibilityPrivileges,omitempty"`
	EligibilityExpr       string             `json:"eligibilityExpr,omitempty"`
	TimerDuration         string             `json:"timerDuration,omitempty"`
	DeadlineDuration      string             `json:"deadlineDuration,omitempty"`
	DeadlineFlow          string             `json:"deadlineFlow,omitempty"`
	DeadlineAction        string             `json:"deadlineAction,omitempty"`
	ReminderEvery         string             `json:"reminderEvery,omitempty"`
	ReminderAction        string             `json:"reminderAction,omitempty"`
	RetryPolicy           *RetryPolicy       `json:"retryPolicy,omitempty"`
	RecoveryFlow          string             `json:"recoveryFlow,omitempty"`
	CompensationAction    string             `json:"compensationAction,omitempty"`
	CompensateRef         string             `json:"compensateRef,omitempty"`
	CancelHandler         string             `json:"cancelHandler,omitempty"`
	SignalName            string             `json:"signalName,omitempty"`
	MessageName           string             `json:"messageName,omitempty"`
	CorrelationKey        string             `json:"correlationKey,omitempty"`
	ErrorCode             string             `json:"errorCode,omitempty"`
	AttachedTo            string             `json:"attachedTo,omitempty"`
	NonInterrupting       bool               `json:"nonInterrupting,omitempty"`
	Subprocess            *ProcessDefinition `json:"subprocess,omitempty"`
	DefRef                string             `json:"defRef,omitempty"`
}

// toWire flattens a Node into its wire form.
func toWire(n Node) NodeWire {
	w := NodeWire{ID: n.ID(), Kind: n.Kind(), Name: n.Name()}
	switch v := n.(type) {
	case StartEvent:
		w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
	case ErrorEndEvent:
		w.ErrorCode = v.ErrorCode
	case ServiceTask:
		w.Action = v.Action
		w.PutActivity(v.ActivityFields)
	case UserTask:
		w.CandidateRoles, w.EligibilityPrivileges, w.EligibilityExpr = v.CandidateRoles, v.EligibilityPrivileges, v.EligibilityExpr
		w.PutActivity(v.ActivityFields)
	case ReceiveTask:
		w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
		w.PutActivity(v.ActivityFields)
	case SendTask:
		w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
		w.PutActivity(v.ActivityFields)
	case BusinessRuleTask:
		w.Action = v.Action
		w.PutActivity(v.ActivityFields)
	case SubProcess:
		w.Subprocess = v.Subprocess
		w.PutActivity(v.ActivityFields)
	case CallActivity:
		w.DefRef = v.DefRef
		w.PutActivity(v.ActivityFields)
	case EventSubProcess:
		w.Subprocess = v.Subprocess
		w.NonInterrupting = v.NonInterrupting
	case IntermediateCatchEvent:
		w.TimerDuration, w.SignalName, w.MessageName, w.CorrelationKey = v.TimerDuration, v.SignalName, v.MessageName, v.CorrelationKey
		w.DeadlineDuration, w.DeadlineFlow, w.DeadlineAction = v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
		w.ReminderEvery, w.ReminderAction = v.ReminderEvery, v.ReminderAction
	case IntermediateThrowEvent:
		w.SignalName, w.CompensateRef = v.SignalName, v.CompensateRef
	case BoundaryEvent:
		w.AttachedTo, w.NonInterrupting, w.ErrorCode = v.AttachedTo, v.NonInterrupting, v.ErrorCode
		w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
	}
	return w
}

// PutActivity projects the shared activity fields into the wire form. Leaf
// packages call it from their ToWire specs.
func (w *NodeWire) PutActivity(a ActivityFields) {
	w.RetryPolicy, w.RecoveryFlow = a.RetryPolicy, a.RecoveryFlow
	w.CompensationAction, w.CancelHandler = a.CompensationAction, a.CancelHandler
	w.DeadlineDuration, w.DeadlineFlow, w.DeadlineAction = a.DeadlineDuration, a.DeadlineFlow, a.DeadlineAction
	w.ReminderEvery, w.ReminderAction = a.ReminderEvery, a.ReminderAction
}

// Activity reconstructs the shared activity fields from the wire form. Leaf
// packages call it from their FromWire specs.
func (w NodeWire) Activity() ActivityFields {
	return ActivityFields{
		WaitFields: WaitFields{
			DeadlineDuration: w.DeadlineDuration,
			DeadlineFlow:     w.DeadlineFlow,
			DeadlineAction:   w.DeadlineAction,
			ReminderEvery:    w.ReminderEvery,
			ReminderAction:   w.ReminderAction,
		},
		RetryPolicy:        w.RetryPolicy,
		RecoveryFlow:       w.RecoveryFlow,
		CompensationAction: w.CompensationAction,
		CancelHandler:      w.CancelHandler,
	}
}

// Wait reconstructs the shared deadline+reminder fields from the wire form,
// for kinds (IntermediateCatchEvent) that carry WaitFields without the full
// ActivityFields.
func (w NodeWire) Wait() WaitFields {
	return WaitFields{
		DeadlineDuration: w.DeadlineDuration,
		DeadlineFlow:     w.DeadlineFlow,
		DeadlineAction:   w.DeadlineAction,
		ReminderEvery:    w.ReminderEvery,
		ReminderAction:   w.ReminderAction,
	}
}

// PutWait projects the shared deadline+reminder fields into the wire form.
func (w *NodeWire) PutWait(a WaitFields) {
	w.DeadlineDuration, w.DeadlineFlow, w.DeadlineAction = a.DeadlineDuration, a.DeadlineFlow, a.DeadlineAction
	w.ReminderEvery, w.ReminderAction = a.ReminderEvery, a.ReminderAction
}

// fromWire reconstructs the concrete Node for w.Kind.
func fromWire(w NodeWire) (Node, error) {
	b := Base{id: w.ID, name: w.Name}
	switch w.Kind {
	case KindStartEvent:
		return StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}, nil
	case KindEndEvent:
		return EndEvent{b}, nil
	case KindTerminateEndEvent:
		return TerminateEndEvent{b}, nil
	case KindErrorEndEvent:
		return ErrorEndEvent{b, w.ErrorCode}, nil
	case KindServiceTask:
		return ServiceTask{Base: b, ActivityFields: w.Activity(), TaskAction: TaskAction{Action: w.Action}}, nil
	case KindUserTask:
		return UserTask{Base: b, ActivityFields: w.Activity(), CandidateRoles: w.CandidateRoles, EligibilityPrivileges: w.EligibilityPrivileges, EligibilityExpr: w.EligibilityExpr}, nil
	case KindReceiveTask:
		return ReceiveTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}, nil
	case KindSendTask:
		return SendTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}, nil
	case KindBusinessRuleTask:
		return BusinessRuleTask{Base: b, ActivityFields: w.Activity(), TaskAction: TaskAction{Action: w.Action}}, nil
	case KindSubProcess:
		return SubProcess{Base: b, ActivityFields: w.Activity(), Subprocess: w.Subprocess}, nil
	case KindCallActivity:
		return CallActivity{Base: b, ActivityFields: w.Activity(), DefRef: w.DefRef}, nil
	case KindEventSubProcess:
		return EventSubProcess{Base: b, Subprocess: w.Subprocess, NonInterrupting: w.NonInterrupting}, nil
	case KindIntermediateCatchEvent:
		return IntermediateCatchEvent{
			Base:           b,
			WaitFields:     w.Wait(),
			TimerDuration:  w.TimerDuration,
			SignalName:     w.SignalName,
			MessageName:    w.MessageName,
			CorrelationKey: w.CorrelationKey,
		}, nil
	case KindIntermediateThrowEvent:
		return IntermediateThrowEvent{Base: b, SignalName: w.SignalName, CompensateRef: w.CompensateRef}, nil
	case KindBoundaryEvent:
		return BoundaryEvent{
			Base:            b,
			AttachedTo:      w.AttachedTo,
			NonInterrupting: w.NonInterrupting,
			ErrorCode:       w.ErrorCode,
			SignalName:      w.SignalName,
			MessageName:     w.MessageName,
			CorrelationKey:  w.CorrelationKey,
			TimerDuration:   w.TimerDuration,
		}, nil
	case KindExclusiveGateway:
		return ExclusiveGateway{b}, nil
	case KindParallelGateway:
		return ParallelGateway{b}, nil
	case KindInclusiveGateway:
		return InclusiveGateway{b}, nil
	case KindEventBasedGateway:
		return EventBasedGateway{b}, nil
	default:
		return nil, fmt.Errorf("workflow-definition: unknown node kind %q", w.Kind)
	}
}

// definitionWire mirrors ProcessDefinition with Nodes as wire forms.
type definitionWire struct {
	ID            string         `json:"id"`
	Version       int            `json:"version"`
	Nodes         []NodeWire     `json:"nodes"`
	Flows         []SequenceFlow `json:"flows"`
	CancelActions []string       `json:"cancelActions,omitempty"`
}

// MarshalJSON serializes a ProcessDefinition to JSON using the flat NodeWire
// form so stored JSONB definitions remain backward-compatible.
func (d ProcessDefinition) MarshalJSON() ([]byte, error) {
	dw := definitionWire{
		ID:            d.ID,
		Version:       d.Version,
		Flows:         d.Flows,
		CancelActions: d.CancelActions,
	}
	dw.Nodes = make([]NodeWire, len(d.Nodes))
	for i, n := range d.Nodes {
		dw.Nodes[i] = toWire(n)
	}
	return json.Marshal(dw)
}

// UnmarshalJSON deserializes a ProcessDefinition from JSON, reconstructing each
// node into its concrete type via the kind discriminator.
func (d *ProcessDefinition) UnmarshalJSON(data []byte) error {
	var dw definitionWire
	if err := json.Unmarshal(data, &dw); err != nil {
		return err
	}
	d.ID = dw.ID
	d.Version = dw.Version
	d.Flows = dw.Flows
	d.CancelActions = dw.CancelActions
	d.Nodes = make([]Node, len(dw.Nodes))
	for i, w := range dw.Nodes {
		n, err := fromWire(w)
		if err != nil {
			return err
		}
		d.Nodes[i] = n
	}
	return nil
}
