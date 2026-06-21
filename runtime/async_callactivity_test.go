package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
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
			{ID: "child-start", Kind: model.KindStartEvent},
			{ID: "child-human", Kind: model.KindUserTask},
			{ID: "child-end", Kind: model.KindEndEvent},
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
			{ID: "parent-start", Kind: model.KindStartEvent},
			{ID: "call", Kind: model.KindCallActivity, DefRef: "async-child"},
			{ID: "parent-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "apf1", Source: "parent-start", Target: "call"},
			{ID: "apf2", Source: "call", Target: "parent-end"},
		},
	}
}

// TestAsyncCallActivityParentParks verifies that when WithCallLinks is configured:
//   - runner.Run(parent) returns StatusRunning (the parent parks, NOT errors)
//   - the child instance exists in the store and is StatusRunning
//   - cl.LookupChild(childID) returns the link with ParentCommandID == parent's call command ID
func TestAsyncCallActivityParentParks(t *testing.T) {
	ctx := t.Context()

	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	child := asyncChildDef()
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"async-child": child,
	})

	// Wire human tasks so the child can reach AwaitHuman (parks there, StatusRunning).
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()

	runner := runtime.NewRunner(nil, clock.System(), store,
		runtime.WithCallLinks(cl),
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
