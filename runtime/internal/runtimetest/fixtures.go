package runtimetest

import "github.com/zakyalvan/krtlwrkflw/model"

// SignalCatchDef returns: start → signal-catch(name) → end. The instance parks at
// the signal-catch node until a SignalReceived trigger arrives.
func SignalCatchDef(signalName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "signal-catch-" + signalName,
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewIntermediateCatchEvent("wait-signal", model.WithSignalName(signalName)),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-signal"},
			{ID: "f2", Source: "wait-signal", Target: "end"},
		},
	}
}

// TimerIntermediateDef returns: start → intermediate-catch(1h timer) →
// service("greet") → end.
func TimerIntermediateDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "timer-intermediate",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewIntermediateCatchEvent("wait1h", model.WithTimerDuration(`"1h"`)),
			model.NewServiceTask("greet", model.WithActionName("greet")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "greet"},
			{ID: "f3", Source: "greet", Target: "end"},
		},
	}
}

// ApprovalDef returns a minimal process: start → userTask("approve", role
// "manager") → end.
func ApprovalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "approval",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("approve", []string{"manager"}),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}
