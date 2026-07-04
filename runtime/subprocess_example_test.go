package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// childDef builds a simple child definition:
//
//	child-start → child-svc (ServiceTask "set-output") → child-end
//
// "set-output" returns {"output": "from-child"}.
func childDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "child", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("child-start"),
			definition.NewServiceTask("child-svc", definition.WithActionName("set-output")),
			definition.NewEndEvent("child-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "cf1", Source: "child-start", Target: "child-svc"},
			{ID: "cf2", Source: "child-svc", Target: "child-end"},
		},
	}
}

// parentCallDef builds a parent definition:
//
//	parent-start → call (KindCallActivity, DefRef:"child") → parent-end
func parentCallDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "parent", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("parent-start"),
			definition.NewCallActivity("call", "child"),
			definition.NewEndEvent("parent-end"),
		},
		Flows: []definition.SequenceFlow{
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
	store := runtimetest.MustMemStore(t)

	// Build the definition registry with the child def.
	child := childDef()
	reg := kernel.NewMapDefinitionRegistry(map[string]*definition.ProcessDefinition{
		"child": child,
	})

	runner := runtimetest.MustRunner(t, cat, store, runtime.WithClock(clk), runtime.WithDefinitions(reg))

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
	events := store.Events()
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
	store := runtimetest.MustMemStore(t)

	// Child def uses a failing action.
	failingChild := &definition.ProcessDefinition{
		ID: "failing-child", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("child-start"),
			definition.NewServiceTask("child-svc", definition.WithActionName("failing-action")),
			definition.NewEndEvent("child-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "cf1", Source: "child-start", Target: "child-svc"},
			{ID: "cf2", Source: "child-svc", Target: "child-end"},
		},
	}

	// Parent def calls "failing-child".
	failingParent := &definition.ProcessDefinition{
		ID: "parent-fail", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("parent-start"),
			definition.NewCallActivity("call", "failing-child"),
			definition.NewEndEvent("parent-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "pf1", Source: "parent-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "parent-end"},
		},
	}

	reg := kernel.NewMapDefinitionRegistry(map[string]*definition.ProcessDefinition{
		"failing-child": failingChild,
	})

	runner := runtimetest.MustRunner(t, cat, store, runtime.WithClock(clk), runtime.WithDefinitions(reg))

	st, err := runner.Run(ctx, failingParent, "parent-fail-i1", nil)
	require.NoError(t, err)

	// Parent must have failed.
	assert.Equal(t, engine.StatusFailed, st.Status)
	require.NotNil(t, st.EndedAt)
}

// parkingChildDef builds a child definition that parks at a user task:
//
//	child-start → child-user (KindUserTask) → child-end
//
// Without a resolver wired, the runner cannot proceed and the child stays
// StatusRunning (parked). This is the definition for Fix 1 RED test.
func parkingChildDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "parking-child", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("child-start"),
			definition.NewUserTask("child-user", nil),
			definition.NewEndEvent("child-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "cf1", Source: "child-start", Target: "child-user"},
			{ID: "cf2", Source: "child-user", Target: "child-end"},
		},
	}
}

// parkingParentDef builds a parent that calls the parking child.
func parkingParentDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "parking-parent", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("parent-start"),
			definition.NewCallActivity("call", "parking-child"),
			definition.NewEndEvent("parent-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "pf1", Source: "parent-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "parent-end"},
		},
	}
}

// TestCallActivityParkedChildFailsParentWithClearError (Fix 1, TDD RED→GREEN):
//
// When the synchronous runner drives a child that parks (e.g. a user task),
// r.Run returns childSt.Status == StatusRunning. The runner must fail the parent
// with a CLEAR, diagnosable error message that:
//   - mentions the word "parked" or "does not support" so the limitation is obvious, and
//   - does NOT emit the misleading generic "did not complete" message.
//
// The child definition has a KindUserTask; WithHumanTasks is wired so the child
// successfully reaches AwaitHuman (resolver resolves, task is persisted), then
// returns nil/nil (parks). The child ends with StatusRunning.
//
// This test pins the explicit-parked-child contract so consumers get a
// meaningful error instead of a silent failure.
func TestCallActivityParkedChildFailsParentWithClearError(t *testing.T) {
	ctx := t.Context()

	clk := clock.System()
	store := runtimetest.MustMemStore(t)

	parkingChild := parkingChildDef()
	reg := kernel.NewMapDefinitionRegistry(map[string]*definition.ProcessDefinition{
		"parking-child": parkingChild,
	})

	// Wire human tasks so the child can park at the user task (StatusRunning).
	// The child will reach AwaitHuman, resolve candidates (empty list is fine),
	// persist the task, and return nil/nil — leaving childSt.Status == StatusRunning.
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	runner := runtimetest.MustRunner(t, nil, store,
		runtime.WithClock(clk),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	parent := parkingParentDef()
	st, err := runner.Run(ctx, parent, "parking-parent-i1", nil)
	require.NoError(t, err, "runner.Run must not return a hard error: the failure is a SubInstanceFailed trigger")

	// Parent must have failed (SubInstanceFailed causes parent failure).
	assert.Equal(t, engine.StatusFailed, st.Status, "parent must be StatusFailed when child parks")
	require.NotNil(t, st.EndedAt, "parent must have an EndedAt on failure")

	// The outbox must carry the failure event; check its error message is diagnosable.
	events := store.Events()
	var failEvent *kernel.OutboxEvent
	for i := range events {
		if events[i].Topic == "instance.failed" {
			e := events[i]
			failEvent = &e
			break
		}
	}
	require.NotNil(t, failEvent, "expected 'instance.failed' outbox event for parent")

	// Fix 1: the error message must explicitly name the limitation.
	errMsg, _ := failEvent.Payload["error"].(string)
	assert.True(t,
		contains(errMsg, "parked") || contains(errMsg, "does not support"),
		"error message must mention 'parked' or 'does not support', got: %q", errMsg,
	)
}

// contains is a simple substring helper to avoid importing strings in this file.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// selfRefDef builds a definition whose call-activity references itself
// (A → call[DefRef:"self-ref"] → end). Running it causes unbounded synchronous
// recursion in the current implementation. Fix 2 adds a depth guard that returns
// a descriptive error instead of stack-overflowing.
func selfRefDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "self-ref", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("sr-start"),
			definition.NewCallActivity("sr-call", "self-ref"),
			definition.NewEndEvent("sr-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "sf1", Source: "sr-start", Target: "sr-call"},
			{ID: "sf2", Source: "sr-call", Target: "sr-end"},
		},
	}
}

// TestCallActivityRecursionDepthLimited (Fix 2, TDD RED→GREEN):
//
// A definition whose call activity references itself causes unbounded synchronous
// recursion in r.Run. The depth guard must stop recursion at maxCallActivityDepth
// and return a clean SubInstanceFailed with a descriptive error that mentions the
// depth limit — NOT a stack overflow / panic.
//
// Observing RED: before the fix, running this test would either stackoverflow-crash
// the test binary or timeout. The test is written to expect the CLEAN error path;
// absence of that path (crash/panic) constitutes the RED state.
func TestCallActivityRecursionDepthLimited(t *testing.T) {
	ctx := t.Context()

	clk := clock.System()
	store := runtimetest.MustMemStore(t)

	def := selfRefDef()
	reg := kernel.NewMapDefinitionRegistry(map[string]*definition.ProcessDefinition{
		"self-ref": def,
	})

	runner := runtimetest.MustRunner(t, nil, store, runtime.WithClock(clk), runtime.WithDefinitions(reg))

	// This must not panic / stack-overflow. The depth guard must kick in and
	// fail the parent instance with a descriptive error.
	require.NotPanics(t, func() {
		st, err := runner.Run(ctx, def, "self-ref-i1", nil)
		require.NoError(t, err, "runner.Run must not return a hard error")
		assert.Equal(t, engine.StatusFailed, st.Status,
			"instance must be StatusFailed when call-activity depth limit is exceeded")
	}, "recursion must not cause a panic or stack overflow")

	// Check outbox for a diagnosable error.
	events := store.Events()
	var failMsg string
	for _, e := range events {
		if e.Topic == "instance.failed" {
			if m, ok := e.Payload["error"].(string); ok {
				failMsg = m
				break
			}
		}
	}
	assert.True(t,
		contains(failMsg, "depth") || contains(failMsg, "recursive") || contains(failMsg, "limit"),
		"failure message must mention depth/recursive/limit, got: %q", failMsg,
	)
}

// TestStartSubInstanceNoRegistry verifies that if StartSubInstance is performed
// without a registry configured, the runner returns a descriptive error rather
// than panicking.
func TestStartSubInstanceNoRegistry(t *testing.T) {
	ctx := t.Context()

	clk := clock.System()
	store := runtimetest.MustMemStore(t)

	// No WithDefinitions option.
	runner := runtimetest.MustRunner(t, nil, store, runtime.WithClock(clk))

	parent := parentCallDef()
	_, err := runner.Run(ctx, parent, "no-reg-i1", nil)
	require.Error(t, err, "expected error when no DefinitionRegistry is configured")
	assert.Contains(t, err.Error(), "registry", "error must mention registry")
}
