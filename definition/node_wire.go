package definition

import (
	"encoding/json"
	"fmt"
)

// nodeWire is the flat JSON/JSONB representation of any node. It is the single
// serialization shape; previously stored definitions decode through it
// unchanged. Field names/order mirror the pre-interface Node struct.
type nodeWire struct {
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
func toWire(n Node) nodeWire {
	w := nodeWire{ID: n.ID(), Kind: n.Kind(), Name: n.Name()}
	switch v := n.(type) {
	case StartEvent:
		w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
	case ErrorEndEvent:
		w.ErrorCode = v.ErrorCode
	case ServiceTask:
		w.Action = v.Action
		applyActivityWire(&w, v.activityFields)
	case UserTask:
		w.CandidateRoles, w.EligibilityPrivileges, w.EligibilityExpr = v.CandidateRoles, v.EligibilityPrivileges, v.EligibilityExpr
		applyActivityWire(&w, v.activityFields)
	case ReceiveTask:
		w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
		applyActivityWire(&w, v.activityFields)
	case SendTask:
		w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
		applyActivityWire(&w, v.activityFields)
	case BusinessRuleTask:
		w.Action = v.Action
		applyActivityWire(&w, v.activityFields)
	case SubProcess:
		w.Subprocess = v.Subprocess
		applyActivityWire(&w, v.activityFields)
	case CallActivity:
		w.DefRef = v.DefRef
		applyActivityWire(&w, v.activityFields)
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

func applyActivityWire(w *nodeWire, a activityFields) {
	w.RetryPolicy, w.RecoveryFlow = a.RetryPolicy, a.RecoveryFlow
	w.CompensationAction, w.CancelHandler = a.CompensationAction, a.CancelHandler
	w.DeadlineDuration, w.DeadlineFlow, w.DeadlineAction = a.DeadlineDuration, a.DeadlineFlow, a.DeadlineAction
	w.ReminderEvery, w.ReminderAction = a.ReminderEvery, a.ReminderAction
}

func (w nodeWire) activity() activityFields {
	return activityFields{
		RetryPolicy:        w.RetryPolicy,
		RecoveryFlow:       w.RecoveryFlow,
		CompensationAction: w.CompensationAction,
		CancelHandler:      w.CancelHandler,
		DeadlineDuration:   w.DeadlineDuration,
		DeadlineFlow:       w.DeadlineFlow,
		DeadlineAction:     w.DeadlineAction,
		ReminderEvery:      w.ReminderEvery,
		ReminderAction:     w.ReminderAction,
	}
}

// fromWire reconstructs the concrete Node for w.Kind.
func fromWire(w nodeWire) (Node, error) {
	b := baseNode{id: w.ID, name: w.Name}
	switch w.Kind {
	case KindStartEvent:
		return StartEvent{baseNode: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}, nil
	case KindEndEvent:
		return EndEvent{b}, nil
	case KindTerminateEndEvent:
		return TerminateEndEvent{b}, nil
	case KindErrorEndEvent:
		return ErrorEndEvent{b, w.ErrorCode}, nil
	case KindServiceTask:
		return ServiceTask{baseNode: b, activityFields: w.activity(), Action: w.Action}, nil
	case KindUserTask:
		return UserTask{baseNode: b, activityFields: w.activity(), CandidateRoles: w.CandidateRoles, EligibilityPrivileges: w.EligibilityPrivileges, EligibilityExpr: w.EligibilityExpr}, nil
	case KindReceiveTask:
		return ReceiveTask{baseNode: b, activityFields: w.activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}, nil
	case KindSendTask:
		return SendTask{baseNode: b, activityFields: w.activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}, nil
	case KindBusinessRuleTask:
		return BusinessRuleTask{baseNode: b, activityFields: w.activity(), Action: w.Action}, nil
	case KindSubProcess:
		return SubProcess{baseNode: b, activityFields: w.activity(), Subprocess: w.Subprocess}, nil
	case KindCallActivity:
		return CallActivity{baseNode: b, activityFields: w.activity(), DefRef: w.DefRef}, nil
	case KindEventSubProcess:
		return EventSubProcess{baseNode: b, Subprocess: w.Subprocess, NonInterrupting: w.NonInterrupting}, nil
	case KindIntermediateCatchEvent:
		return IntermediateCatchEvent{
			baseNode:         b,
			TimerDuration:    w.TimerDuration,
			SignalName:       w.SignalName,
			MessageName:      w.MessageName,
			CorrelationKey:   w.CorrelationKey,
			DeadlineDuration: w.DeadlineDuration,
			DeadlineFlow:     w.DeadlineFlow,
			DeadlineAction:   w.DeadlineAction,
			ReminderEvery:    w.ReminderEvery,
			ReminderAction:   w.ReminderAction,
		}, nil
	case KindIntermediateThrowEvent:
		return IntermediateThrowEvent{baseNode: b, SignalName: w.SignalName, CompensateRef: w.CompensateRef}, nil
	case KindBoundaryEvent:
		return BoundaryEvent{
			baseNode:        b,
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
	Nodes         []nodeWire     `json:"nodes"`
	Flows         []SequenceFlow `json:"flows"`
	CancelActions []string       `json:"cancelActions,omitempty"`
}

// MarshalJSON serializes a ProcessDefinition to JSON using the flat nodeWire
// form so stored JSONB definitions remain backward-compatible.
func (d ProcessDefinition) MarshalJSON() ([]byte, error) {
	dw := definitionWire{
		ID:            d.ID,
		Version:       d.Version,
		Flows:         d.Flows,
		CancelActions: d.CancelActions,
	}
	dw.Nodes = make([]nodeWire, len(d.Nodes))
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
