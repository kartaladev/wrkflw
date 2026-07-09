package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// inclusiveForkDef: start -> or -{a>0}-> ta ; -{b>0}-> tb ; -default-> tc ; each -> its end
func inclusiveForkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "or", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewInclusive("or"),
			activity.NewServiceTask("ta", activity.WithTaskAction("a")),
			activity.NewServiceTask("tb", activity.WithTaskAction("b")),
			activity.NewServiceTask("tc", activity.WithTaskAction("c")),
			event.NewEnd("ea"),
			event.NewEnd("eb"),
			event.NewEnd("ec"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "or"},
			{ID: "f2", Source: "or", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "or", Target: "tb", Condition: "b > 0"},
			{ID: "f4", Source: "or", Target: "tc", IsDefault: true},
			{ID: "f5", Source: "ta", Target: "ea"},
			{ID: "f6", Source: "tb", Target: "eb"},
			{ID: "f7", Source: "tc", Target: "ec"},
		},
	}
}

func actionNames(cmds []engine.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok {
			out = append(out, ia.Name)
		}
	}
	return out
}

func TestInclusiveForkTakesAllTrueBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, actionNames(res.Commands))
	require.Len(t, res.State.Tokens, 2)
}

func TestInclusiveForkSingleTrueBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 0}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a"}, actionNames(res.Commands))
	require.Len(t, res.State.Tokens, 1)
}

func TestInclusiveForkFallsBackToDefault(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 0, "b": 0}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"c"}, actionNames(res.Commands))
	require.Len(t, res.State.Tokens, 1)
}

// TestInclusiveForkUnconditionalFlowSuppressesDefault verifies that an outgoing
// flow with an empty condition (unconditional, non-default) is always selected and
// that the presence of such a taken flow suppresses the default.
func TestInclusiveForkUnconditionalFlowSuppressesDefault(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "or2", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewInclusive("or"),
			activity.NewServiceTask("ta", activity.WithTaskAction("a")), // unconditional (empty condition)
			activity.NewServiceTask("tb", activity.WithTaskAction("b")), // default
			event.NewEnd("ea"),
			event.NewEnd("eb"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "or"},
			{ID: "f2", Source: "or", Target: "ta", Condition: ""}, // empty = always taken
			{ID: "f3", Source: "or", Target: "tb", IsDefault: true},
			{ID: "f4", Source: "ta", Target: "ea"},
			{ID: "f5", Source: "tb", Target: "eb"},
		},
	}
	res, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	// Unconditional flow is taken; default must NOT be selected.
	assert.ElementsMatch(t, []string{"a"}, actionNames(res.Commands))
	require.Len(t, res.State.Tokens, 1)
}

// orDiamondDef: start -> orsplit -{a>0}-> ta ; -{b>0}-> tb ; -{c>0}-> tc ;
//
//	ta,tb,tc -> orjoin -> post -> end.
//
// post is a ServiceTask after the join so we can count how many times the join fires.
func orDiamondDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "ord", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewInclusive("orsplit"),
			activity.NewServiceTask("ta", activity.WithTaskAction("a")),
			activity.NewServiceTask("tb", activity.WithTaskAction("b")),
			activity.NewServiceTask("tc", activity.WithTaskAction("c")),
			gateway.NewInclusive("orjoin"),
			activity.NewServiceTask("post", activity.WithTaskAction("post")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "orsplit"},
			{ID: "f2", Source: "orsplit", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "orsplit", Target: "tb", Condition: "b > 0"},
			{ID: "f4", Source: "orsplit", Target: "tc", Condition: "c > 0"},
			{ID: "f5", Source: "ta", Target: "orjoin"},
			{ID: "f6", Source: "tb", Target: "orjoin"},
			{ID: "f7", Source: "tc", Target: "orjoin"},
			{ID: "f8", Source: "orjoin", Target: "post"},
			{ID: "f9", Source: "post", Target: "end"},
		},
	}
}

// Two of three branches active (a,b true; c false). The OR-join must fire after
// a and b complete and must NOT wait for c, which never received a token.
// The "post" task after the join must be invoked exactly ONCE (not once per branch).
func TestInclusiveJoinDoesNotWaitForUntakenBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := orDiamondDef()

	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 1, "c": 0}), engine.StepOptions{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a", "b"}, actionNames(r0.Commands))
	cmds := map[string]string{} // action name -> commandID
	for _, c := range r0.Commands {
		ia := c.(engine.InvokeAction)
		cmds[ia.Name] = ia.CommandID
	}

	// Complete a: token parks at the join; b can still reach the join, so no firing.
	// With forkInclusive (wrong behavior), the token would immediately proceed to "post"
	// and issue an InvokeAction{Name:"post"} — but the OR-join must NOT fire yet.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), cmds["a"], nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r1.Commands, "OR-join must not fire while b can still reach it")
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Complete b: now no token (other than those parked at the join) can reach the join.
	// It fires: consumes both parked tokens, places ONE token at "post" (not two).
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmds["b"], nil), engine.StepOptions{})
	require.NoError(t, err)
	// Exactly ONE InvokeAction for "post" (join fired once, not once-per-branch).
	require.Len(t, r2.Commands, 1)
	postCmd, ok := r2.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for post task")
	assert.Equal(t, "post", postCmd.Name)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
	// Exactly one token waiting at post.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "post", r2.State.Tokens[0].NodeID)
}

func TestNodesThatCanReachIsAccurate(t *testing.T) {
	// White-box-ish behavioral check via a single-branch OR-join that should fire
	// immediately once its only active branch arrives.
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := orDiamondDef()
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 0, "c": 0}), engine.StepOptions{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a"}, actionNames(r0.Commands))
	idA := r0.Commands[0].(engine.InvokeAction).CommandID

	// Only branch a is active; completing it must fire the join immediately,
	// advancing to the "post" task (one InvokeAction for "post").
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), idA, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	postCmd, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for post task")
	assert.Equal(t, "post", postCmd.Name)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)
}

func TestInclusiveForkNoMatchNoDefaultErrors(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "or", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewInclusive("or"),
			activity.NewServiceTask("ta", activity.WithTaskAction("a")),
			event.NewEnd("ea"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "or"},
			{ID: "f2", Source: "or", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "ta", Target: "ea"},
		},
	}
	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 0}), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrNoMatchingFlow)
}
