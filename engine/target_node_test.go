package engine

// target_node_test.go — white-box tests for TargetNode. Uses package engine
// (not engine_test) so the table can build/inspect InstanceState directly and
// assert on unexported plumbing where useful.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// startStubStrategy is a no-op validate.ValidationStrategy used to prove that
// TargetNode returns the specific start node that carries InputValidation
// (so runtime validateInput runs against it) rather than starts[0].
type startStubStrategy struct{}

func (startStubStrategy) NewValidator() (validate.Validator, error) { return startStubValidator{}, nil }

type startStubValidator struct{}

func (startStubValidator) Validate(context.Context, map[string]any) error { return nil }

// multiStartDef builds a definition with TWO start events: a manual
// (trigger-less) start at index 0 and a message-triggered start at index 1 that
// carries InputValidation. Used to prove TargetNode honours StartNodeID /
// manual-start resolution instead of the old len(starts)==1 short-circuit.
func multiStartDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "multi-start", Version: 1,
		Nodes: []model.Node{
			event.NewStart("manual-start"),
			event.NewStart("msg-start",
				event.WithMessageCorrelator("go-msg", ""),
				event.WithInputValidation(startStubStrategy{})),
			activity.NewServiceTask("svc", activity.WithTaskAction("a")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "manual-start", Target: "svc"},
			{ID: "f2", Source: "msg-start", Target: "svc"},
			{ID: "f3", Source: "svc", Target: "end"},
		},
	}
}

// nestedUserTaskDef builds: start -> sub{ inner-start -> approve(UserTask) ->
// inner-end } -> end. Used to reproduce the nested-completion regression: the
// old flat def.Node lookup could not find "approve" because it lives in the
// sub-process's nested definition, not the top-level one.
func nestedUserTaskDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-approve", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("approve"),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "approve"},
			{ID: "if2", Source: "approve", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-approve", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "end"},
		},
	}
}

// nestedReceiveTaskDef builds: start -> sub{ inner-start -> recv(ReceiveTask
// "m") -> inner-end } -> end. Used for the nested message-delivery tier.
func nestedReceiveTaskDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-recv", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewReceiveTask("recv", "m"),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "recv"},
			{ID: "if2", Source: "recv", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-recv", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "end"},
		},
	}
}

// TestTargetNode drives each fixture through Step to reach the parked state
// under test (more faithful than hand-building InstanceState), then asserts
// TargetNode resolves the scope-correct node for the given trigger.
func TestTargetNode(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	// ---- Drive fixture 1 to a parked nested UserTask ("approve"). ----
	userTaskDef := nestedUserTaskDef()
	userTaskResult, err := Step(userTaskDef, InstanceState{InstanceID: "i-approve"},
		NewStartInstance(at, nil), StepOptions{})
	require.NoError(t, err)
	require.Len(t, userTaskResult.State.Tokens, 1, "expected one parked token at the nested approve task")
	taskToken := userTaskResult.State.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken, "parked user-task token must carry AwaitCommand")

	// ---- Drive fixture 2 to a parked nested ReceiveTask ("recv"). ----
	receiveTaskDef := nestedReceiveTaskDef()
	receiveTaskResult, err := Step(receiveTaskDef, InstanceState{InstanceID: "i-recv"},
		NewStartInstance(at, nil), StepOptions{})
	require.NoError(t, err)
	require.Len(t, receiveTaskResult.State.Tokens, 1, "expected one parked token at the nested recv task")

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		st     InstanceState
		trg    Trigger
		assert func(t *testing.T, node model.Node, ok bool)
	}

	cases := []testCase{
		{
			name: "completion nested regression: resolves the nested UserTask, not unfindable via flat lookup",
			def:  userTaskDef,
			st:   userTaskResult.State,
			trg:  NewHumanCompleted(at, taskToken, nil, authz.Actor{ID: "user1"}),
			assert: func(t *testing.T, node model.Node, ok bool) {
				require.True(t, ok)
				require.NotNil(t, node)
				assert.Equal(t, "approve", node.ID())
				assert.Equal(t, model.KindUserTask, node.Kind())
			},
		},
		{
			name: "message nested tier-4: resolves the nested ReceiveTask via the tokenAwaitingMessage tier",
			def:  receiveTaskDef,
			st:   receiveTaskResult.State,
			trg:  NewMessageReceived(at, "m", "", nil),
			assert: func(t *testing.T, node model.Node, ok bool) {
				require.True(t, ok)
				require.NotNil(t, node)
				assert.Equal(t, "recv", node.ID())
				assert.Equal(t, model.KindReceiveTask, node.Kind())
			},
		},
		{
			name: "start: resolves the single top-level start node from a fresh state",
			def:  userTaskDef,
			st:   InstanceState{InstanceID: "fresh"},
			trg:  NewStartInstance(at, nil),
			assert: func(t *testing.T, node model.Node, ok bool) {
				require.True(t, ok)
				require.NotNil(t, node)
				assert.Equal(t, "start", node.ID())
			},
		},
		{
			name: "event-started: StartNodeID targets the message-start (carrying InputValidation), not starts[0]",
			def:  multiStartDef(),
			st:   InstanceState{InstanceID: "evt-started"},
			trg:  NewStartInstanceAtNode(at, "msg-start", nil),
			assert: func(t *testing.T, node model.Node, ok bool) {
				require.True(t, ok)
				require.NotNil(t, node)
				assert.Equal(t, "msg-start", node.ID())
				se, isSE := node.(event.StartEvent)
				require.True(t, isSE, "resolved node must be a StartEvent, got %T", node)
				assert.NotNil(t, se.InputValidation,
					"the resolved start must carry InputValidation so validateInput runs")
			},
		},
		{
			name: "manual start resolved when StartNodeID empty on a multi-start def",
			def:  multiStartDef(),
			st:   InstanceState{InstanceID: "manual-started"},
			trg:  NewStartInstance(at, nil),
			assert: func(t *testing.T, node model.Node, ok bool) {
				require.True(t, ok)
				require.NotNil(t, node)
				assert.Equal(t, "manual-start", node.ID())
			},
		},
		{
			name: "StartNodeID targets manual start explicitly on a 2-start def returns that node, not starts[0] semantics",
			def:  multiStartDef(),
			st:   InstanceState{InstanceID: "explicit-manual"},
			trg:  NewStartInstanceAtNode(at, "manual-start", nil),
			assert: func(t *testing.T, node model.Node, ok bool) {
				require.True(t, ok)
				require.NotNil(t, node)
				assert.Equal(t, "manual-start", node.ID())
			},
		},
		{
			name: "non-input trigger: returns (nil,false) for a trigger kind TargetNode does not resolve",
			def:  userTaskDef,
			st:   userTaskResult.State,
			trg:  NewTimerFired(at, "tm-1"),
			assert: func(t *testing.T, node model.Node, ok bool) {
				assert.False(t, ok)
				assert.Nil(t, node)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node, ok := TargetNode(tc.def, tc.st, tc.trg)
			tc.assert(t, node, ok)
		})
	}
}
