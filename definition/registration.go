package definition

// This file registers the serialization spec for every node kind. In Phase 3 of
// the relocation each block moves into the leaf package that owns the kind
// (event, gateway, activity), so that adding a kind is a single-site change. For
// now every kind is registered here so the package (de)serializes on its own.

func init() {
	// --- events ---
	RegisterKind(KindStartEvent, NodeSpec{
		Name: "startEvent",
		FromWire: func(b Base, w NodeWire) Node {
			return StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(StartEvent)
			w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
		},
	})
	RegisterKind(KindEndEvent, NodeSpec{
		Name:     "endEvent",
		FromWire: func(b Base, _ NodeWire) Node { return EndEvent{b} },
		ToWire:   func(Node, *NodeWire) {},
	})
	RegisterKind(KindTerminateEndEvent, NodeSpec{
		Name:     "terminateEndEvent",
		FromWire: func(b Base, _ NodeWire) Node { return TerminateEndEvent{b} },
		ToWire:   func(Node, *NodeWire) {},
	})
	RegisterKind(KindErrorEndEvent, NodeSpec{
		Name:     "errorEndEvent",
		FromWire: func(b Base, w NodeWire) Node { return ErrorEndEvent{b, w.ErrorCode} },
		ToWire:   func(n Node, w *NodeWire) { w.ErrorCode = n.(ErrorEndEvent).ErrorCode },
	})
	RegisterKind(KindIntermediateCatchEvent, NodeSpec{
		Name: "intermediateCatchEvent",
		FromWire: func(b Base, w NodeWire) Node {
			return IntermediateCatchEvent{Base: b, WaitFields: w.Wait(), TimerDuration: w.TimerDuration, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(IntermediateCatchEvent)
			w.TimerDuration, w.SignalName, w.MessageName, w.CorrelationKey = v.TimerDuration, v.SignalName, v.MessageName, v.CorrelationKey
			w.PutWait(v.WaitFields)
		},
	})
	RegisterKind(KindIntermediateThrowEvent, NodeSpec{
		Name: "intermediateThrowEvent",
		FromWire: func(b Base, w NodeWire) Node {
			return IntermediateThrowEvent{Base: b, SignalName: w.SignalName, CompensateRef: w.CompensateRef}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(IntermediateThrowEvent)
			w.SignalName, w.CompensateRef = v.SignalName, v.CompensateRef
		},
	})
	RegisterKind(KindBoundaryEvent, NodeSpec{
		Name: "boundaryEvent",
		FromWire: func(b Base, w NodeWire) Node {
			return BoundaryEvent{Base: b, AttachedTo: w.AttachedTo, NonInterrupting: w.NonInterrupting, ErrorCode: w.ErrorCode, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(BoundaryEvent)
			w.AttachedTo, w.NonInterrupting, w.ErrorCode = v.AttachedTo, v.NonInterrupting, v.ErrorCode
			w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
		},
	})
	RegisterKind(KindEventSubProcess, NodeSpec{
		Name: "eventSubProcess",
		FromWire: func(b Base, w NodeWire) Node {
			return EventSubProcess{Base: b, Subprocess: w.Subprocess, NonInterrupting: w.NonInterrupting}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(EventSubProcess)
			w.Subprocess, w.NonInterrupting = v.Subprocess, v.NonInterrupting
		},
	})

	// --- gateways ---
	RegisterKind(KindExclusiveGateway, NodeSpec{
		Name:     "exclusiveGateway",
		FromWire: func(b Base, _ NodeWire) Node { return ExclusiveGateway{b} },
		ToWire:   func(Node, *NodeWire) {},
	})
	RegisterKind(KindParallelGateway, NodeSpec{
		Name:     "parallelGateway",
		FromWire: func(b Base, _ NodeWire) Node { return ParallelGateway{b} },
		ToWire:   func(Node, *NodeWire) {},
	})
	RegisterKind(KindInclusiveGateway, NodeSpec{
		Name:     "inclusiveGateway",
		FromWire: func(b Base, _ NodeWire) Node { return InclusiveGateway{b} },
		ToWire:   func(Node, *NodeWire) {},
	})
	RegisterKind(KindEventBasedGateway, NodeSpec{
		Name:     "eventBasedGateway",
		FromWire: func(b Base, _ NodeWire) Node { return EventBasedGateway{b} },
		ToWire:   func(Node, *NodeWire) {},
	})

	// --- activities ---
	RegisterKind(KindServiceTask, NodeSpec{
		Name: "serviceTask",
		FromWire: func(b Base, w NodeWire) Node {
			return ServiceTask{Base: b, ActivityFields: w.Activity(), TaskAction: TaskAction{Action: w.Action}}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(ServiceTask)
			w.Action = v.Action
			w.PutActivity(v.ActivityFields)
		},
	})
	RegisterKind(KindUserTask, NodeSpec{
		Name: "userTask",
		FromWire: func(b Base, w NodeWire) Node {
			return UserTask{Base: b, ActivityFields: w.Activity(), CandidateRoles: w.CandidateRoles, EligibilityPrivileges: w.EligibilityPrivileges, EligibilityExpr: w.EligibilityExpr}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(UserTask)
			w.CandidateRoles, w.EligibilityPrivileges, w.EligibilityExpr = v.CandidateRoles, v.EligibilityPrivileges, v.EligibilityExpr
			w.PutActivity(v.ActivityFields)
		},
	})
	RegisterKind(KindReceiveTask, NodeSpec{
		Name: "receiveTask",
		FromWire: func(b Base, w NodeWire) Node {
			return ReceiveTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(ReceiveTask)
			w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
			w.PutActivity(v.ActivityFields)
		},
	})
	RegisterKind(KindSendTask, NodeSpec{
		Name: "sendTask",
		FromWire: func(b Base, w NodeWire) Node {
			return SendTask{Base: b, ActivityFields: w.Activity(), MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(SendTask)
			w.MessageName, w.CorrelationKey = v.MessageName, v.CorrelationKey
			w.PutActivity(v.ActivityFields)
		},
	})
	RegisterKind(KindBusinessRuleTask, NodeSpec{
		Name: "businessRuleTask",
		FromWire: func(b Base, w NodeWire) Node {
			return BusinessRuleTask{Base: b, ActivityFields: w.Activity(), TaskAction: TaskAction{Action: w.Action}}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(BusinessRuleTask)
			w.Action = v.Action
			w.PutActivity(v.ActivityFields)
		},
	})
	RegisterKind(KindSubProcess, NodeSpec{
		Name: "subProcess",
		FromWire: func(b Base, w NodeWire) Node {
			return SubProcess{Base: b, ActivityFields: w.Activity(), Subprocess: w.Subprocess}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(SubProcess)
			w.Subprocess = v.Subprocess
			w.PutActivity(v.ActivityFields)
		},
	})
	RegisterKind(KindCallActivity, NodeSpec{
		Name: "callActivity",
		FromWire: func(b Base, w NodeWire) Node {
			return CallActivity{Base: b, ActivityFields: w.Activity(), DefRef: w.DefRef}
		},
		ToWire: func(n Node, w *NodeWire) {
			v := n.(CallActivity)
			w.DefRef = v.DefRef
			w.PutActivity(v.ActivityFields)
		},
	})
}
