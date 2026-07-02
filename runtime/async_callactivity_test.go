package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// asyncChildDef builds a child definition whose single task is a human task —
// so the child will park (StatusRunning) instead of completing.
//
//	child-start → child-human (KindUserTask) → child-end
func asyncChildDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "async-child",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("child-start"),
			model.NewUserTask("child-human", nil),
			model.NewEndEvent("child-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "acf1", Source: "child-start", Target: "child-human"},
			{ID: "acf2", Source: "child-human", Target: "child-end"},
		},
	}
}

// asyncParentDef builds a parent definition with a call activity that invokes asyncChildDef.
//
//	parent-start → call (KindCallActivity, DefRef:"async-child") → parent-end
func asyncParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "async-parent",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("parent-start"),
			model.NewCallActivity("call", "async-child"),
			model.NewEndEvent("parent-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "apf1", Source: "parent-start", Target: "call"},
			{ID: "apf2", Source: "call", Target: "parent-end"},
		},
	}
}

// TestAsyncCallActivityParentParks verifies that when WithCallLinkStore is configured:
//   - runner.Run(parent) returns StatusRunning (the parent parks, NOT errors)
//   - the child instance exists in the store and is StatusRunning
//   - cl.LookupChild(childID) returns the link with ParentCommandID == parent's call command ID
func TestAsyncCallActivityParentParks(t *testing.T) {
	ctx := t.Context()

	cl := runtime.NewMemCallLinkStore()
	store := mustMemStore(t, runtime.WithCallLinks(cl))

	child := asyncChildDef()
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"async-child": child,
	})

	// Wire human tasks so the child can reach AwaitHuman (parks there, StatusRunning).
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()

	runner := mustRunner(t, nil, store,
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	parent := asyncParentDef()
	const parentID = "async-parent-i1"
	st, err := runner.Run(ctx, parent, parentID, nil)
	require.NoError(t, err, "runner.Run must not return a hard error: parent should park")

	// Parent must be StatusRunning (parked at the call activity, child not yet done).
	assert.Equal(t, engine.StatusRunning, st.Status,
		"parent must be StatusRunning (parked) when child is async and parks")

	// Derive expected child instance ID using the existing scheme:
	// "<parentID>-sub-<suffix>" where suffix is the short command ID segment.
	// The first command in the parent will be something like "async-parent-i1-c1",
	// so suffix is "c1" and child ID is "async-parent-i1-sub-c1".
	childID := parentID + "-sub-c1"

	// The child instance must exist in the store and must be StatusRunning.
	childSt, _, loadErr := store.Load(ctx, childID)
	require.NoError(t, loadErr, "child instance must exist in the store")
	assert.Equal(t, engine.StatusRunning, childSt.Status,
		"child must be StatusRunning (parked at human task)")

	// The call link must be recorded with the correct parent command.
	link, ok, lookupErr := cl.LookupChild(ctx, childID)
	require.NoError(t, lookupErr)
	require.True(t, ok, "call link must be recorded for the child instance")
	assert.Equal(t, parentID, link.ParentInstanceID)
	assert.Equal(t, childID, link.ChildInstanceID)
	assert.Equal(t, 1, link.Depth, "first-level child must have depth 1")

	// ParentCommandID must be the command that triggered the child (used to resume parent later).
	assert.NotEmpty(t, link.ParentCommandID,
		"link.ParentCommandID must be set to the StartSubInstance command ID")

	// ParentDefID must reference the PARENT definition (not the child's def).
	assert.Equal(t, parent.ID, link.ParentDefID,
		"link.ParentDefID must be the parent definition ID")
	assert.Equal(t, parent.Version, link.ParentDefVersion,
		"link.ParentDefVersion must be the parent definition version")
}

// ── fixtures for child-terminal tests ────────────────────────────────────────

// asyncImmediateChildDef returns a child process definition with a service task
// named "complete-action" that succeeds immediately, so the child reaches
// StatusCompleted in the first burst.
//
//	child-start → child-work (KindServiceTask, Action:"complete-action") → child-end
func asyncImmediateChildDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "async-imm-child",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("child-start"),
			model.NewServiceTask("child-work", model.WithActionName("complete-action")),
			model.NewEndEvent("child-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "icf1", Source: "child-start", Target: "child-work"},
			{ID: "icf2", Source: "child-work", Target: "child-end"},
		},
	}
}

// asyncImmediateParentDef returns a parent that calls asyncImmediateChildDef.
func asyncImmediateParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "async-imm-parent",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("parent-start"),
			model.NewCallActivity("call", "async-imm-child"),
			model.NewEndEvent("parent-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "ipf1", Source: "parent-start", Target: "call"},
			{ID: "ipf2", Source: "call", Target: "parent-end"},
		},
	}
}

// asyncFailingChildDef returns a child process definition with a service task
// named "fail-action" that returns an error, so the child reaches StatusFailed.
//
//	child-start → child-work (KindServiceTask, Action:"fail-action") → child-end
func asyncFailingChildDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "async-fail-child",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("child-start"),
			model.NewServiceTask("child-work", model.WithActionName("fail-action")),
			model.NewEndEvent("child-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "fcf1", Source: "child-start", Target: "child-work"},
			{ID: "fcf2", Source: "child-work", Target: "child-end"},
		},
	}
}

// asyncFailingParentDef returns a parent that calls asyncFailingChildDef.
func asyncFailingParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "async-fail-parent",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("parent-start"),
			model.NewCallActivity("call", "async-fail-child"),
			model.NewEndEvent("parent-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "fpf1", Source: "parent-start", Target: "call"},
			{ID: "fpf2", Source: "call", Target: "parent-end"},
		},
	}
}

// successAction is a ServiceAction that returns a fixed output map.
type successAction struct{ out map[string]any }

func (a *successAction) Do(_ context.Context, _ map[string]any) (map[string]any, error) {
	return a.out, nil
}

// failAction is a ServiceAction that always returns an error.
type failAction struct{ msg string }

func (a *failAction) Do(_ context.Context, _ map[string]any) (map[string]any, error) {
	return nil, errors.New(a.msg)
}

// ── TestAsyncCallActivityChildTerminalFlipsLink ───────────────────────────────

// TestAsyncCallActivityChildTerminalFlipsLink verifies the deliverLoop terminal
// hook: when a child instance transitions into a terminal status the call link
// must be flipped to terminal (ClaimPending returns the PendingNotify).
//
// Sub-tests:
//
//   - Completed: child completes (StatusCompleted) → Outcome.Completed==true,
//     Outcome.Output carries the child's terminal variables.
//   - Failed: child fails (StatusFailed) → Outcome.Completed==false,
//     Outcome.Err non-empty.
func TestAsyncCallActivityChildTerminalFlipsLink(t *testing.T) {
	t.Run("Completed", func(t *testing.T) {
		ctx := t.Context()

		cl := runtime.NewMemCallLinkStore()
		store := mustMemStore(t, runtime.WithCallLinks(cl))

		childOutput := map[string]any{"result": "ok", "score": 42}
		cat := action.NewMapCatalog(map[string]action.ServiceAction{
			"complete-action": &successAction{out: childOutput},
		})

		child := asyncImmediateChildDef()
		parent := asyncImmediateParentDef()
		reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
			"async-imm-child": child,
		})

		runner := mustRunner(t, cat, store,
			runtime.WithCallLinkStore(cl),
			runtime.WithDefinitions(reg),
		)

		const parentID = "async-imm-p1"
		// Parent run: the parent calls the child; the child completes immediately
		// during the parent's first burst (runChild runs it synchronously).
		// The parent should park waiting for a SubInstanceCompleted notification.
		_, err := runner.Run(ctx, parent, parentID, nil)
		require.NoError(t, err, "runner.Run must not error")

		// The child instance's terminal commit must have flipped the call link.
		pending, claimErr := cl.ClaimPending(ctx, 10)
		require.NoError(t, claimErr)
		require.Len(t, pending, 1, "exactly one pending notify expected — the completed child")

		n := pending[0]
		childID := parentID + "-sub-c1"
		assert.Equal(t, childID, n.Link.ChildInstanceID)
		assert.True(t, n.Outcome.Completed, "Outcome.Completed must be true for a StatusCompleted child")
		assert.Equal(t, "ok", n.Outcome.Output["result"], "child output must be propagated")
		assert.Empty(t, n.Outcome.Err, "Outcome.Err must be empty on success")
	})

	t.Run("Failed", func(t *testing.T) {
		ctx := t.Context()

		cl := runtime.NewMemCallLinkStore()
		store := mustMemStore(t, runtime.WithCallLinks(cl))

		cat := action.NewMapCatalog(map[string]action.ServiceAction{
			"fail-action": &failAction{msg: "child service error"},
		})

		child := asyncFailingChildDef()
		parent := asyncFailingParentDef()
		reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
			"async-fail-child": child,
		})

		runner := mustRunner(t, cat, store,
			runtime.WithCallLinkStore(cl),
			runtime.WithDefinitions(reg),
		)

		const parentID = "async-fail-p1"
		_, err := runner.Run(ctx, parent, parentID, nil)
		require.NoError(t, err, "runner.Run must not error (parent parks; child failure is async)")

		pending, claimErr := cl.ClaimPending(ctx, 10)
		require.NoError(t, claimErr)
		require.Len(t, pending, 1, "exactly one pending notify expected — the failed child")

		n := pending[0]
		childID := parentID + "-sub-c1"
		assert.Equal(t, childID, n.Link.ChildInstanceID)
		assert.False(t, n.Outcome.Completed, "Outcome.Completed must be false for a StatusFailed child")
		assert.NotEmpty(t, n.Outcome.Err, "Outcome.Err must be set for a failed child")
		assert.Nil(t, n.Outcome.Output, "Outcome.Output must be nil on failure")
	})
}
