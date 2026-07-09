package runtimetest

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// SignalCatchDef returns: start → signal-catch(name) → end. The instance parks at
// the signal-catch node until a SignalReceived trigger arrives.
func SignalCatchDef(signalName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "signal-catch-" + signalName,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait-signal", event.WithCatchSignal(signalName)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
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
			event.NewStart("start"),
			event.NewIntermediateCatch("wait1h", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("greet", activity.WithTaskAction("greet")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "greet"},
			{ID: "f3", Source: "greet", Target: "end"},
		},
	}
}

// ApprovalWithReminderDef returns the ApprovalDef process whose user task
// carries a recurring in-wait reminder (Every waitEvery, action
// waitAction). The reminder is armed once with its recurring TriggerSpec and
// survives each fire; it is cancelled when the task completes. Used to exercise
// the recurrence-aware timer cancel in the runtime.
func ApprovalWithReminderDef(waitEvery time.Duration, waitAction string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "approval-reminder",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", []string{"manager"},
				activity.WithWaitReminder(schedule.Every(waitEvery), waitAction)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
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
			event.NewStart("start"),
			activity.NewUserTask("approve", []string{"manager"}),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}
