package runtimetest

import "github.com/zakyalvan/krtlwrkflw/definition"

// SignalCatchDef returns: start → signal-catch(name) → end. The instance parks at
// the signal-catch node until a SignalReceived trigger arrives.
func SignalCatchDef(signalName string) *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      "signal-catch-" + signalName,
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewIntermediateCatchEvent("wait-signal", definition.WithSignalName(signalName)),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-signal"},
			{ID: "f2", Source: "wait-signal", Target: "end"},
		},
	}
}

// TimerIntermediateDef returns: start → intermediate-catch(1h timer) →
// service("greet") → end.
func TimerIntermediateDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      "timer-intermediate",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewIntermediateCatchEvent("wait1h", definition.WithTimerDuration(`"1h"`)),
			definition.NewServiceTask("greet", definition.WithActionName("greet")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "greet"},
			{ID: "f3", Source: "greet", Target: "end"},
		},
	}
}

// ApprovalDef returns a minimal process: start → userTask("approve", role
// "manager") → end.
func ApprovalDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      "approval",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewUserTask("approve", []string{"manager"}),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}
