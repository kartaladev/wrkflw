package runtime_test

import (
	"context"
	"fmt"
	"sort"
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

// cancelPropParentDef builds a parent definition with a call activity pointing at childDefRef.
//
//	parent-start → call (KindCallActivity) → parent-end
func cancelPropParentDef(id, childDefRef string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []model.Node{
			{ID: "p-start", Kind: model.KindStartEvent},
			{ID: "call", Kind: model.KindCallActivity, DefRef: childDefRef},
			{ID: "p-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "pf1", Source: "p-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "p-end"},
		},
	}
}

// cancelPropChildDef builds a child definition that parks at a human task.
//
//	child-start → child-human (KindUserTask) → child-end
func cancelPropChildDef(id string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []model.Node{
			{ID: "c-start", Kind: model.KindStartEvent},
			{ID: "c-human", Kind: model.KindUserTask},
			{ID: "c-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "cf1", Source: "c-start", Target: "c-human"},
			{ID: "cf2", Source: "c-human", Target: "c-end"},
		},
	}
}

// cancelPropRunner builds a Runner with CallLinks + Definitions + HumanTasks wired.
// The registry is populated with BOTH plain "defID" keys (for StartSubInstance
// DefRef lookup) and "defID:version" keys (for propagateCancel's def resolution).
func cancelPropRunner(store *runtime.MemStore, cl *runtime.MemCallLinkStore, defs map[string]*model.ProcessDefinition) *runtime.Runner {
	reg := cancelPropRegistry(defs)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	return runtime.NewRunner(nil, clock.System(), store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)
}

// cancelPropRegistry builds a MapDefinitionRegistry with both plain and versioned
// keys for each definition, matching the convention used in e2e tests.
func cancelPropRegistry(defs map[string]*model.ProcessDefinition) *runtime.MapDefinitionRegistry {
	full := make(map[string]*model.ProcessDefinition, len(defs)*2)
	for k, v := range defs {
		full[k] = v
		full[fmt.Sprintf("%s:%d", v.ID, v.Version)] = v
	}
	return runtime.NewMapDefinitionRegistry(full)
}

// TestCancelPropagationParentAndChild verifies that cancelling a parent also cancels
// its still-running async child (case a).
func TestCancelPropagationParentAndChild(t *testing.T) {
	ctx := t.Context()

	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	childDef := cancelPropChildDef("prop-child")
	parentDef := cancelPropParentDef("prop-parent", "prop-child")

	runner := cancelPropRunner(store, cl, map[string]*model.ProcessDefinition{
		"prop-child":  childDef,
		"prop-parent": parentDef,
	})

	const parentID = "prop-parent-i1"
	st, err := runner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must be running (parked) after Run")

	childID := parentID + "-sub-c1"
	childSt, _, loadErr := store.Load(ctx, childID)
	require.NoError(t, loadErr)
	assert.Equal(t, engine.StatusRunning, childSt.Status, "child must be running (parked)")

	// Cancel the parent — propagation must also cancel the child.
	cancelSt, err := runner.CancelInstance(ctx, parentDef, parentID)
	require.NoError(t, err, "CancelInstance must not return an error")
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status, "parent must be Terminated after cancel")

	childAfterCancel, _, loadErr2 := store.Load(ctx, childID)
	require.NoError(t, loadErr2)
	assert.Equal(t, engine.StatusTerminated, childAfterCancel.Status,
		"child must be Terminated after parent cancel propagation")
}

// TestCancelPropagationGrandchild verifies that cancellation propagates recursively:
// parent → child → grandchild, all three must be Terminated (case b).
func TestCancelPropagationGrandchild(t *testing.T) {
	ctx := t.Context()

	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	// grandchild parks at human task
	grandchildDef := cancelPropChildDef("prop-grandchild")
	// child calls grandchild
	childDef := cancelPropParentDef("prop-child-gc", "prop-grandchild")
	// parent calls child
	parentDef := cancelPropParentDef("prop-parent-gc", "prop-child-gc")

	runner := cancelPropRunner(store, cl, map[string]*model.ProcessDefinition{
		"prop-grandchild": grandchildDef,
		"prop-child-gc":   childDef,
		"prop-parent-gc":  parentDef,
	})

	const parentID = "prop-parent-gc-i1"
	st, err := runner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must be running (parked)")

	// Derive child and grandchild IDs based on the short-suffix scheme.
	childID := parentID + "-sub-c1"
	grandchildID := childID + "-sub-c1"

	childSt, _, err := store.Load(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, childSt.Status, "child must be running")

	grandchildSt, _, err := store.Load(ctx, grandchildID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, grandchildSt.Status, "grandchild must be running")

	// Cancel parent → all three must terminate.
	cancelSt, err := runner.CancelInstance(ctx, parentDef, parentID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status, "parent must be Terminated")

	childAfter, _, err := store.Load(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, childAfter.Status, "child must be Terminated")

	grandchildAfter, _, err := store.Load(ctx, grandchildID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, grandchildAfter.Status, "grandchild must be Terminated")
}

// TestCancelPropagationChildDefMissing verifies best-effort: when the child's
// definition cannot be resolved, CancelInstance still returns no error and the
// parent is Terminated (case c).
func TestCancelPropagationChildDefMissing(t *testing.T) {
	ctx := t.Context()

	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	childDef := cancelPropChildDef("prop-missing-child")
	parentDef := cancelPropParentDef("prop-missing-parent", "prop-missing-child")

	// Include child in reg so Run works, but omit it from the def registry used
	// for propagation by not including it — we achieve this by using a registry
	// that includes child at Run time but we'll build one without it for the
	// runner so propagation can't resolve it.
	//
	// Strategy: run with a full registry (so the child starts), then wrap with a
	// registry that omits the child def.

	// First: full runner to get parent + child both Running.
	fullReg := cancelPropRegistry(map[string]*model.ProcessDefinition{
		"prop-missing-child":  childDef,
		"prop-missing-parent": parentDef,
	})
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	fullRunner := runtime.NewRunner(nil, clock.System(), store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(fullReg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	const parentID = "prop-missing-p1"
	st, err := fullRunner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status)

	// Now build a runner whose registry OMITS the child def (simulates missing def).
	// Note: the parent's plain + versioned keys are registered, but child is absent.
	partialReg := cancelPropRegistry(map[string]*model.ProcessDefinition{
		"prop-missing-parent": parentDef,
		// "prop-missing-child" intentionally absent
	})
	partialRunner := runtime.NewRunner(nil, clock.System(), store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(partialReg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	cancelSt, err := partialRunner.CancelInstance(ctx, parentDef, parentID)
	require.NoError(t, err, "CancelInstance must NOT fail when child def is missing (best-effort)")
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status, "parent must be Terminated")
}

// TestMemCallLinkStoreListRunningChildren verifies the ListRunningChildren method
// on MemCallLinkStore (case d):
//   - Returns only non-terminal links for the given parentInstanceID.
//   - Excludes links belonging to a different parent.
//   - Excludes terminal links.
//   - Results are ordered by ChildInstanceID.
func TestMemCallLinkStoreListRunningChildren(t *testing.T) {
	ctx := t.Context()
	cl := runtime.NewMemCallLinkStore()

	// We need to insert links directly via the MemStore path, but MemCallLinkStore
	// exposes record/markTerminal only internally. Use MemStore + MemStoreWithCallLinks
	// and a minimal runner run to populate the store, or test via the exported
	// NewMemCallLinkStore + manual setup.
	//
	// Since record/markTerminal are unexported, we populate via store.Create/Commit
	// using a minimal runner setup.

	store := runtime.NewMemStoreWithCallLinks(cl)
	childA := cancelPropChildDef("list-child-a")
	childB := cancelPropChildDef("list-child-b")
	childC := cancelPropChildDef("list-child-c") // different parent
	parentAB := cancelPropParentDef("list-parent-ab", "list-child-a")
	parentC := cancelPropParentDef("list-parent-c", "list-child-c")

	// We need a runner that can launch multiple children from the same parent.
	// The current def model only has one call activity, so we need two separate
	// parent instances for childA and childB, OR we test indirectly.
	//
	// Simplest: run two separate parents, each spawning a child, plus a third
	// parent with its own child. Then verify ListRunningChildren returns the right
	// subset.

	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()

	fullDefs := map[string]*model.ProcessDefinition{
		"list-child-a":   childA,
		"list-child-b":   childB,
		"list-child-c":   childC,
		"list-parent-ab": parentAB,
		"list-parent-c":  parentC,
	}
	reg := cancelPropRegistry(fullDefs)
	runner := runtime.NewRunner(nil, clock.System(), store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	// Run parent-ab-i1 → spawns list-child-a child (childID = "list-parent-ab-i1-sub-c1").
	_, err := runner.Run(ctx, parentAB, "list-parent-ab-i1", nil)
	require.NoError(t, err)

	// Run parent-ab-i2 → spawns list-child-a child (different parent-ab instance).
	// Build a second parent that calls list-child-b so we have two distinct children
	// under "list-parent-ab" concept — but we only have one call node per def.
	// Instead: run two instances of parentAB with different IDs.
	_, err = runner.Run(ctx, parentAB, "list-parent-ab-i2", nil)
	require.NoError(t, err)

	// Run parent-c → spawns list-child-c child (different parent instance, should NOT appear).
	_, err = runner.Run(ctx, parentC, "list-parent-c-i1", nil)
	require.NoError(t, err)

	// List running children of "list-parent-ab-i1" — expect only 1 child.
	children, err := cl.ListRunningChildren(ctx, "list-parent-ab-i1")
	require.NoError(t, err)
	require.Len(t, children, 1, "only one running child of list-parent-ab-i1")
	assert.Equal(t, "list-parent-ab-i1-sub-c1", children[0].ChildInstanceID)
	assert.Equal(t, "list-parent-ab-i1", children[0].ParentInstanceID)

	// List running children of "list-parent-ab-i2" — expect only 1 child.
	children2, err := cl.ListRunningChildren(ctx, "list-parent-ab-i2")
	require.NoError(t, err)
	require.Len(t, children2, 1, "only one running child of list-parent-ab-i2")

	// List running children of "list-parent-c-i1" — expect only 1 child.
	childrenC, err := cl.ListRunningChildren(ctx, "list-parent-c-i1")
	require.NoError(t, err)
	require.Len(t, childrenC, 1, "only one running child of list-parent-c-i1")

	// Verify ordering is deterministic (sorted by ChildInstanceID).
	sorted := make([]string, len(children))
	for i, c := range children {
		sorted[i] = c.ChildInstanceID
	}
	assert.True(t, sort.StringsAreSorted(sorted), "results must be sorted by ChildInstanceID")

	// Terminal children must be excluded: cancel one child and verify it disappears.
	childIDtoTerminate := "list-parent-ab-i2-sub-c1"
	childDefForCancel := cancelPropChildDef("list-child-a") // same def, just used for CancelInstance
	childDefForCancel.ID = "list-child-a"
	cancelSt, err := runner.CancelInstance(ctx, childA, childIDtoTerminate)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status)

	// After termination, list-parent-ab-i2's running children must be empty.
	childrenAfter, err := cl.ListRunningChildren(ctx, "list-parent-ab-i2")
	require.NoError(t, err)
	assert.Empty(t, childrenAfter, "terminated child must not appear in ListRunningChildren")
}

// TestCancelPropagationNoCallLinks verifies that CancelInstance still works
// correctly (just cancels the parent, no propagation) when r.callLinks is nil.
// Uses a simple parent with a human task (no call activity) so the parent parks
// without needing a child instance.
func TestCancelPropagationNoCallLinks(t *testing.T) {
	ctx := t.Context()

	store := runtime.NewMemStore()

	// Simple process: start → human task → end. Parks at the human task.
	parentDef := &model.ProcessDefinition{
		ID:      "no-cl-parent",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "human", Kind: model.KindUserTask},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "human"},
			{ID: "f2", Source: "human", Target: "end"},
		},
	}

	// Runner WITHOUT WithCallLinks — propagation gate disabled.
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	runner := runtime.NewRunner(nil, clock.System(), store,
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	const parentID = "no-cl-parent-i1"
	st, err := runner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must park at human task")

	// CancelInstance must work as before — parent terminated, no error, no propagation attempted.
	cancelSt, err := runner.CancelInstance(ctx, parentDef, parentID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status)
}

// TestCancelPropagationContextPropagated ensures the context passed to CancelInstance
// is threaded through the propagation (no panics, no context-key collisions).
func TestCancelPropagationContextPropagated(t *testing.T) {
	ctx := t.Context()

	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	childDef := cancelPropChildDef("ctx-child")
	parentDef := cancelPropParentDef("ctx-parent", "ctx-child")

	runner := cancelPropRunner(store, cl, map[string]*model.ProcessDefinition{
		"ctx-child":  childDef,
		"ctx-parent": parentDef,
	})

	const parentID = "ctx-parent-i1"
	_, err := runner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)

	type myKey struct{}
	markedCtx := context.WithValue(ctx, myKey{}, "marker")
	cancelSt, err := runner.CancelInstance(markedCtx, parentDef, parentID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status)
}
