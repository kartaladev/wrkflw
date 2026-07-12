package engine_test

// step_subprocess_multistart_test.go — regression for ADR-0121 fix F2: an
// embedded sub-process must be entered INLINE at its MANUAL (trigger-less)
// start, even when an event-triggered start precedes it in the nested
// definition's node order. The old innerStarts[0] pick would enter the
// event-start's path once multi-start became legal in nested defs.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// subProcessManualAfterEventStartDef builds a sub-process whose nested
// definition declares an EVENT-triggered start (index 0) BEFORE its manual
// start (index 1). The two starts flow to DISTINCT service tasks so the entry
// point is observable from the emitted InvokeAction:
//
//	evt-start (signal "go")  → evt-svc   (action "evt-action")   → inner-end
//	manual-start             → manual-svc(action "manual-action")→ inner-end
//
// Correct entry places the inner token on manual-start → drives to manual-svc
// (InvokeAction "manual-action"). The old innerStarts[0] pick would enter
// evt-start → evt-svc (InvokeAction "evt-action").
func subProcessManualAfterEventStartDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-manual-after-event", Version: 1,
		Nodes: []model.Node{
			event.NewStart("evt-start", event.WithSignalName("go")),
			event.NewStart("manual-start"),
			activity.NewServiceTask("evt-svc", activity.WithTaskAction("evt-action")),
			activity.NewServiceTask("manual-svc", activity.WithTaskAction("manual-action")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "evt-start", Target: "evt-svc"},
			{ID: "if2", Source: "manual-start", Target: "manual-svc"},
			{ID: "if3", Source: "evt-svc", Target: "inner-end"},
			{ID: "if4", Source: "manual-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-manual-after-event", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestEmbeddedSubProcessEntersManualStartNotEventStart is the F2 regression.
func TestEmbeddedSubProcessEntersManualStartNotEventStart(t *testing.T) {
	at := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	def := subProcessManualAfterEventStartDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-manual-after-event"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Exactly one InvokeAction, and it must be for the MANUAL start's path.
	require.Len(t, r1.Commands, 1, "expected exactly one InvokeAction after sub-process entry")
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction, got %T", r1.Commands[0])
	assert.Equal(t, "manual-action", ia.Name,
		"embedded sub-process must enter at the MANUAL start (manual-svc), not the event-start (evt-svc)")

	// The single inner token must be parked at manual-svc, not evt-svc.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "manual-svc", r1.State.Tokens[0].NodeID)
}
