package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p1", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewServiceTask("greet", "greet"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestStepStartInstanceReachesServiceTask(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()

	res, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"name": "Ada"}), engine.StepOptions{})
	require.NoError(t, err)

	// One InvokeAction emitted for the service task; one token parked on it.
	require.Len(t, res.Commands, 1)
	ia, ok := res.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "greet", ia.Name)
	assert.Equal(t, "Ada", ia.Input["name"])

	require.Len(t, res.State.Tokens, 1)
	tok := res.State.Tokens[0]
	assert.Equal(t, "greet", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, ia.CommandID, tok.AwaitCommand)
	assert.Equal(t, engine.StatusRunning, res.State.Status)
	assert.Equal(t, at, res.State.StartedAt)
}

func TestStepActionCompletedReachesEnd(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"name": "Ada"}), engine.StepOptions{})
	require.NoError(t, err)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdID, map[string]any{"greeting": "hi Ada"}),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r2.Commands, 1)
	_, ok := r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
	assert.Equal(t, "hi Ada", r2.State.Variables["greeting"])
	require.NotNil(t, r2.State.EndedAt)
}

func TestStepIsDeterministic(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()
	in := engine.InstanceState{InstanceID: "i1"}
	trg := engine.NewStartInstance(at, map[string]any{"name": "Ada"})

	a, err := engine.Step(def, in, trg, engine.StepOptions{})
	require.NoError(t, err)
	b, err := engine.Step(def, in, trg, engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, a.Commands, b.Commands)
	assert.Equal(t, a.State, b.State)
}

func TestStepDoesNotMutateInput(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()
	in := engine.InstanceState{
		InstanceID: "i1",
		Variables:  map[string]any{"name": "Ada"},
		Scopes: []engine.Scope{
			{
				ID:       "i1-s1",
				NodeID:   "sub",
				ParentID: "",
				Compensations: []engine.CompensationRecord{
					{NodeID: "svc", Action: "undo-svc"},
				},
			},
		},
	}

	_, err := engine.Step(def, in, engine.NewStartInstance(at, map[string]any{"extra": 1}), engine.StepOptions{})
	require.NoError(t, err)

	// Caller's state is untouched.
	assert.Empty(t, in.Tokens)
	assert.Equal(t, map[string]any{"name": "Ada"}, in.Variables)
	// Scopes must be untouched (deep-copy in cloneState protects them).
	require.Len(t, in.Scopes, 1)
	assert.Equal(t, "i1-s1", in.Scopes[0].ID)
	require.Len(t, in.Scopes[0].Compensations, 1)
	assert.Equal(t, "svc", in.Scopes[0].Compensations[0].NodeID)
	assert.Equal(t, "undo-svc", in.Scopes[0].Compensations[0].Action)
}

func TestStepActionFailedFailsInstance(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(at.Add(time.Second), cmdID, "boom", false), engine.StepOptions{})
	require.NoError(t, err)

	// Verify behavioral outcome: instance is failed and a FailInstance command is
	// present. We scan for it by type rather than checking the exact command count so
	// the test remains valid if terminal-cleanup commands (e.g. CancelTimer) are
	// legitimately added in the future.
	assert.Equal(t, engine.StatusFailed, r2.State.Status)
	require.NotNil(t, r2.State.EndedAt)

	var fi *engine.FailInstance
	for _, c := range r2.Commands {
		if v, ok := c.(engine.FailInstance); ok {
			vv := v
			fi = &vv
			break
		}
	}
	require.NotNil(t, fi, "FailInstance command must be present for unhandled ActionFailed")
	assert.Equal(t, "boom", fi.Err)
}

func TestStepActionCompletedUnknownCommandID(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	_, err = engine.Step(def, r1.State,
		engine.NewActionCompleted(at, "no-such-command", nil), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

