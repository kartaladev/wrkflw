package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// childDef builds a simple child definition:
//
//	child-start → child-svc (ServiceTask "set-output") → child-end
//
// "set-output" returns {"output": "from-child"}.
func childDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "child", Version: 1,
		Nodes: []model.Node{
			{ID: "child-start", Kind: model.KindStartEvent},
			{ID: "child-svc", Kind: model.KindServiceTask, Action: "set-output"},
			{ID: "child-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "cf1", Source: "child-start", Target: "child-svc"},
			{ID: "cf2", Source: "child-svc", Target: "child-end"},
		},
	}
}

// parentCallDef builds a parent definition:
//
//	parent-start → call (KindCallActivity, DefRef:"child") → parent-end
func parentCallDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "parent", Version: 1,
		Nodes: []model.Node{
			{ID: "parent-start", Kind: model.KindStartEvent},
			{ID: "call", Kind: model.KindCallActivity, DefRef: "child"},
			{ID: "parent-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "pf1", Source: "parent-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "parent-end"},
		},
	}
}

// setOutputAction is a test action that returns {"output": "from-child"}.
type setOutputAction struct{}

func (setOutputAction) Do(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{"output": "from-child"}, nil
}

// failingAction always returns an error.
type failingAction struct{}

func (failingAction) Do(_ context.Context, _ map[string]any) (map[string]any, error) {
	return nil, &actionError{msg: "child-action failed"}
}

type actionError struct{ msg string }

func (e *actionError) Error() string { return e.msg }

// TestCallActivityRunsChildAndResumesParent is the primary e2e test:
//
//  1. Build a parent def (parent-start → call [DefRef:"child"] → parent-end).
//  2. Build a child def (child-start → child-svc ["set-output"] → child-end).
//  3. Register the child in a MapDefinitionRegistry via WithDefinitions.
//  4. Run the parent → the runner performs StartSubInstance by running the child
//     to completion, then SubInstanceCompleted feeds back and the parent resumes.
//  5. Assert: parent StatusCompleted; child output ("output"="from-child") merged
//     into parent variables.
func TestCallActivityRunsChildAndResumesParent(t *testing.T) {
	ctx := t.Context()

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"set-output": setOutputAction{},
	})

	clk := clock.System()
	store := runtime.NewMemStateStore()
	jnl := runtime.NewMemJournal()
	out := runtime.NewMemOutbox()

	// Build the definition registry with the child def.
	child := childDef()
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"child": child,
	})

	runner := runtime.NewRunner(cat, clk, store, jnl, out, runtime.WithDefinitions(reg))

	parent := parentCallDef()
	st, err := runner.Run(ctx, parent, "parent-i1", map[string]any{"x": 42})
	require.NoError(t, err)

	// Parent must have completed.
	assert.Equal(t, engine.StatusCompleted, st.Status)
	require.NotNil(t, st.EndedAt)
	assert.Empty(t, st.Tokens, "all tokens must be consumed on completion")

	// Child output must be merged into parent variables.
	assert.Equal(t, "from-child", st.Variables["output"],
		"child's output variable must be merged into parent")
	assert.Equal(t, 42, st.Variables["x"],
		"original parent variable must be retained")

	// The child instance should also have been saved to the store (observable).
	// Child instance ID follows the "<parentID>-sub-<commandID>" scheme.
	// We check the parent completed and the outbox has the completion event.
	events := out.Events()
	require.NotEmpty(t, events, "at least one outbox event must be written")
	// Find the parent instance.completed event.
	found := false
	for _, e := range events {
		if e.Topic == "instance.completed" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected 'instance.completed' outbox event for parent")
}

// TestCallActivityChildFailureFailsParent verifies that when the child's action
// fails, the SubInstanceFailed trigger is delivered to the parent, causing the
// parent to reach StatusFailed.
func TestCallActivityChildFailureFailsParent(t *testing.T) {
	ctx := t.Context()

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"failing-action": failingAction{},
	})

	clk := clock.System()
	store := runtime.NewMemStateStore()
	jnl := runtime.NewMemJournal()
	out := runtime.NewMemOutbox()

	// Child def uses a failing action.
	failingChild := &model.ProcessDefinition{
		ID: "failing-child", Version: 1,
		Nodes: []model.Node{
			{ID: "child-start", Kind: model.KindStartEvent},
			{ID: "child-svc", Kind: model.KindServiceTask, Action: "failing-action"},
			{ID: "child-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "cf1", Source: "child-start", Target: "child-svc"},
			{ID: "cf2", Source: "child-svc", Target: "child-end"},
		},
	}

	// Parent def calls "failing-child".
	failingParent := &model.ProcessDefinition{
		ID: "parent-fail", Version: 1,
		Nodes: []model.Node{
			{ID: "parent-start", Kind: model.KindStartEvent},
			{ID: "call", Kind: model.KindCallActivity, DefRef: "failing-child"},
			{ID: "parent-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "pf1", Source: "parent-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "parent-end"},
		},
	}

	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"failing-child": failingChild,
	})

	runner := runtime.NewRunner(cat, clk, store, jnl, out, runtime.WithDefinitions(reg))

	st, err := runner.Run(ctx, failingParent, "parent-fail-i1", nil)
	require.NoError(t, err)

	// Parent must have failed.
	assert.Equal(t, engine.StatusFailed, st.Status)
	require.NotNil(t, st.EndedAt)
}

// TestStartSubInstanceNoRegistry verifies that if StartSubInstance is performed
// without a registry configured, the runner returns a descriptive error rather
// than panicking.
func TestStartSubInstanceNoRegistry(t *testing.T) {
	ctx := t.Context()

	clk := clock.System()
	store := runtime.NewMemStateStore()
	jnl := runtime.NewMemJournal()
	out := runtime.NewMemOutbox()

	// No WithDefinitions option.
	runner := runtime.NewRunner(nil, clk, store, jnl, out)

	parent := parentCallDef()
	_, err := runner.Run(ctx, parent, "no-reg-i1", nil)
	require.Error(t, err, "expected error when no DefinitionRegistry is configured")
	assert.Contains(t, err.Error(), "registry", "error must mention registry")
}
