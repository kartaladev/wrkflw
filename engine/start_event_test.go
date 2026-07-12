package engine_test

// start_event_test.go — black-box tests for Task 2 (ADR-0121): the engine
// multi-start seam. StartInstance.StartNodeID tells handleStartInstance which
// start node to seed; empty resolves the definition's sole manual (trigger-less,
// caller-driven) start, and a definition with no manual start yields
// engine.ErrNoManualStart.

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

// oneManualStartLinearDef returns a linear definition with a single manual start.
//
//	Start → End
func oneManualStartLinearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-one-manual-start", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// twoStartsDef returns a definition with two start events — a manual start and
// a message-start — each leading to a distinct UserTask so a test can observe
// which start node was actually seeded by checking where the token parks.
//
//	start (none)    → ua (UserTask) → end
//	msgStart (msg)  → ub (UserTask) → end
func twoStartsDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-two-starts", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewStart("msgStart", event.WithMessageCorrelator("orderPlaced", "")),
			activity.NewUserTask("ua", activity.WithEligibleRoles("r")),
			activity.NewUserTask("ub", activity.WithEligibleRoles("r")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "ua"},
			{ID: "f2", Source: "ua", Target: "end"},
			{ID: "f3", Source: "msgStart", Target: "ub"},
			{ID: "f4", Source: "ub", Target: "end"},
		},
	}
}

// onlyMessageStartDef returns a definition whose only start event is a
// message-start — there is no manual (trigger-less, caller-driven) start to resolve.
//
//	msgStart (msg) → end
func onlyMessageStartDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-only-message-start", Version: 1,
		Nodes: []model.Node{
			event.NewStart("msgStart", event.WithMessageCorrelator("orderPlaced", "")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "msgStart", Target: "end"},
		},
	}
}

// TestHandleStartInstanceResolvesNode verifies that handleStartInstance
// resolves the start node to seed from StartInstance.StartNodeID: empty
// resolves the definition's sole manual start, a non-empty id seeds that
// specific start node, and empty against a definition with no manual start
// fails with engine.ErrNoManualStart.
func TestHandleStartInstanceResolvesNode(t *testing.T) {
	tests := map[string]struct {
		def    *model.ProcessDefinition
		nodeID string
		assert func(t *testing.T, out engine.StepResult, err error)
	}{
		"empty node id uses the manual start": {
			def: oneManualStartLinearDef(), nodeID: "",
			assert: func(t *testing.T, out engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, out.State.Status)
			},
		},
		"explicit node id seeds that start": {
			def: twoStartsDef(), nodeID: "msgStart",
			assert: func(t *testing.T, out engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusRunning, out.State.Status)
				require.Len(t, out.State.Tokens, 1)
				assert.Equal(t, "ub", out.State.Tokens[0].NodeID)
			},
		},
		"empty node id with only event starts errors": {
			def: onlyMessageStartDef(), nodeID: "",
			assert: func(t *testing.T, out engine.StepResult, err error) {
				assert.ErrorIs(t, err, engine.ErrNoManualStart)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := engine.Step(tc.def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstanceAtNode(time.Unix(0, 0), tc.nodeID, nil), engine.StepOptions{})
			tc.assert(t, out, err)
		})
	}
}

// TestNewStartInstance_DefaultsEmptyStartNodeID verifies that the existing
// two-arg NewStartInstance constructor keeps its current signature and leaves
// StartNodeID at its zero value ("") — the empty-node-id-resolves-manual-start
// path — preserving pre-ADR-0121 behavior for every existing caller.
func TestNewStartInstance_DefaultsEmptyStartNodeID(t *testing.T) {
	trg := engine.NewStartInstance(time.Unix(0, 0), nil)
	assert.Empty(t, trg.StartNodeID)
}
