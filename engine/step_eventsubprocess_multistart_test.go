package engine_test

// step_eventsubprocess_multistart_test.go — regression for ADR-0121 fix F3: an
// event sub-process arm must be built from the EVENT-TRIGGERED start of its
// nested definition, not from StartNodes()[0]. Once multi-start became legal,
// a manual start at index 0 would make the old code record a dead arm (empty
// trigger) that never matches a delivery, AND (fire path) place the entry token
// on the wrong start node.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// espManualBeforeEventStartDef builds a root-level interrupting ESP whose nested
// definition declares a MANUAL start (index 0) BEFORE its signal-triggered start
// (index 1). Its two starts flow to DISTINCT service tasks so the fired entry
// point is observable:
//
//	esp-manual-start        → esp-manual-svc(action "esp-manual-action") → esp-end
//	esp-evt-start (sig "cancel") → esp-svc (action "esp-action")         → esp-end
//
// The root process parks at a user task so the instance stays running while the
// ESP is armed. A correct arm records Signal "cancel" and, when fired, places the
// token on esp-evt-start → esp-svc (InvokeAction "esp-action").
func espManualBeforeEventStartDef() *model.ProcessDefinition {
	espInner := &model.ProcessDefinition{
		ID: "esp-multistart-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-manual-start"),
			event.NewStart("esp-evt-start", event.WithSignalName("cancel")),
			activity.NewServiceTask("esp-manual-svc", activity.WithTaskAction("esp-manual-action")),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "e1", Source: "esp-manual-start", Target: "esp-manual-svc"},
			{ID: "e2", Source: "esp-evt-start", Target: "esp-svc"},
			{ID: "e3", Source: "esp-manual-svc", Target: "esp-end"},
			{ID: "e4", Source: "esp-svc", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "root-esp-multistart", Version: 1,
		Nodes: []model.Node{
			event.NewStart("root-start"),
			activity.NewUserTask("root-user"),
			event.NewEnd("root-end"),
			activity.NewSubProcess("esp", espInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "root-start", Target: "root-user"},
			{ID: "f2", Source: "root-user", Target: "root-end"},
		},
	}
}

// TestEventSubprocessArmsFromEventStartNotIndexZero is the F3 regression: the arm
// is recorded from the event-triggered start (Signal "cancel"), and firing that
// signal enters the ESP at the event start's path, not the manual start's.
func TestEventSubprocessArmsFromEventStartNotIndexZero(t *testing.T) {
	at := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	def := espManualBeforeEventStartDef()

	// ---- Step 1: StartInstance → root-user parks; ESP arm recorded. ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-esp-multistart"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// The arm must be built from the EVENT start's trigger, not a dead empty arm.
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1, "expected one ESP arm recorded")
	arm := r1.State.EventTriggeredSubprocesses[0]
	assert.Equal(t, "cancel", arm.Signal,
		"ESP arm must carry the event-start's signal, not an empty (dead) trigger")

	// ---- Step 2: SignalReceived{"cancel"} → interrupting ESP fires. ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "cancel", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// The ESP must enter at the EVENT start's path (esp-svc), not the manual one.
	var espActionCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			assert.NotEqual(t, "esp-manual-action", ia.Name,
				"ESP must not enter at the manual start's path (esp-manual-svc)")
			if ia.Name == "esp-action" {
				espActionCmdID = ia.CommandID
			}
		}
	}
	require.NotEmpty(t, espActionCmdID,
		"expected InvokeAction for esp-action (ESP entered at the event-start path)")
}
