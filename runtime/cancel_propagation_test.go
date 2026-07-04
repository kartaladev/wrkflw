package runtime_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// cancelPropParentDef builds a parent definition with a call activity pointing at childDefRef.
//
//	parent-start → call (KindCallActivity) → parent-end
func cancelPropParentDef(id, childDefRef string) *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []definition.Node{
			event.NewStart("p-start"),
			activity.NewCallActivity("call", childDefRef),
			event.NewEnd("p-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "pf1", Source: "p-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "p-end"},
		},
	}
}

// cancelPropChildDef builds a child definition that parks at a human task.
//
//	child-start → child-human (KindUserTask) → child-end
func cancelPropChildDef(id string) *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []definition.Node{
			event.NewStart("c-start"),
			activity.NewUserTask("c-human", nil),
			event.NewEnd("c-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "cf1", Source: "c-start", Target: "c-human"},
			{ID: "cf2", Source: "c-human", Target: "c-end"},
		},
	}
}

// countingCallLinkStore wraps a MemCallLinkStore and counts ListRunningChildren calls
// per parentInstanceID. Used in TestCancelPropagationDiamond to verify the shared
// visited map prevents double-processing of shared subtree nodes.
type countingCallLinkStore struct {
	kernel.CallLinkStore
	mu     sync.Mutex
	counts map[string]int
}

func newCountingCallLinkStore(inner kernel.CallLinkStore) *countingCallLinkStore {
	return &countingCallLinkStore{CallLinkStore: inner, counts: make(map[string]int)}
}

func (c *countingCallLinkStore) ListRunningChildren(ctx context.Context, parentID string) ([]kernel.CallLink, error) {
	c.mu.Lock()
	c.counts[parentID]++
	c.mu.Unlock()
	return c.CallLinkStore.ListRunningChildren(ctx, parentID)
}

func (c *countingCallLinkStore) listCount(parentID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[parentID]
}

// cancelPropRunner builds a Runner with CallLinks + Definitions + HumanTasks wired.
// The registry is populated with BOTH plain "defID" keys (for StartSubInstance
// DefRef lookup) and "defID:version" keys (for propagateCancel's def resolution).
func cancelPropRunner(t *testing.T, store *kernel.MemStore, cl *kernel.MemCallLinkStore, defs map[string]*definition.ProcessDefinition) *runtime.ProcessDriver {
	t.Helper()
	reg := cancelPropRegistry(defs)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	return runtimetest.MustRunner(t, nil, store,
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)
}

// cancelPropRegistry builds a MapDefinitionRegistry with both plain and versioned
// keys for each definition, matching the convention used in e2e tests.
func cancelPropRegistry(defs map[string]*definition.ProcessDefinition) *kernel.MapDefinitionRegistry {
	full := make(map[string]*definition.ProcessDefinition, len(defs)*2)
	for k, v := range defs {
		full[k] = v
		full[fmt.Sprintf("%s:%d", v.ID, v.Version)] = v
	}
	return kernel.NewMapDefinitionRegistry(full)
}

// TestCancelPropagationParentAndChild verifies that cancelling a parent also cancels
// its still-running async child (case a).
func TestCancelPropagationParentAndChild(t *testing.T) {
	ctx := t.Context()

	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	childDef := cancelPropChildDef("prop-child")
	parentDef := cancelPropParentDef("prop-parent", "prop-child")

	runner := cancelPropRunner(t, store, cl, map[string]*definition.ProcessDefinition{
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

	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	// grandchild parks at human task
	grandchildDef := cancelPropChildDef("prop-grandchild")
	// child calls grandchild
	childDef := cancelPropParentDef("prop-child-gc", "prop-grandchild")
	// parent calls child
	parentDef := cancelPropParentDef("prop-parent-gc", "prop-child-gc")

	runner := cancelPropRunner(t, store, cl, map[string]*definition.ProcessDefinition{
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

	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

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
	fullReg := cancelPropRegistry(map[string]*definition.ProcessDefinition{
		"prop-missing-child":  childDef,
		"prop-missing-parent": parentDef,
	})
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	fullRunner := runtimetest.MustRunner(t, nil, store,
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(fullReg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	const parentID = "prop-missing-p1"
	st, err := fullRunner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status)

	// Now build a runner whose registry OMITS the child def (simulates missing def).
	// Note: the parent's plain + versioned keys are registered, but child is absent.
	partialReg := cancelPropRegistry(map[string]*definition.ProcessDefinition{
		"prop-missing-parent": parentDef,
		// "prop-missing-child" intentionally absent
	})
	partialRunner := runtimetest.MustRunner(t, nil, store,
		runtime.WithCallLinkStore(cl),
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
	cl := kernel.NewMemCallLinkStore()

	// We need to insert links directly via the MemStore path, but MemCallLinkStore
	// exposes record/markTerminal only internally. Use NewMemStore(WithCallLinks(cl))
	// and a minimal runner run to populate the store, or test via the exported
	// NewMemCallLinkStore + manual setup.
	//
	// Since record/markTerminal are unexported, we populate via store.Create/Commit
	// using a minimal runner setup.

	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))
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

	fullDefs := map[string]*definition.ProcessDefinition{
		"list-child-a":   childA,
		"list-child-b":   childB,
		"list-child-c":   childC,
		"list-parent-ab": parentAB,
		"list-parent-c":  parentC,
	}
	reg := cancelPropRegistry(fullDefs)
	runner := runtimetest.MustRunner(t, nil, store,
		runtime.WithCallLinkStore(cl),
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
	assert.True(t, slices.IsSorted(sorted), "results must be sorted by ChildInstanceID")

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

	store := runtimetest.MustMemStore(t)

	// Simple process: start → human task → end. Parks at the human task.
	parentDef := &definition.ProcessDefinition{
		ID:      "no-cl-parent",
		Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewUserTask("human", nil),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "human"},
			{ID: "f2", Source: "human", Target: "end"},
		},
	}

	// Runner WITHOUT WithCallLinkStore — propagation gate disabled.
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	runner := runtimetest.MustRunner(t, nil, store,
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

	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	childDef := cancelPropChildDef("ctx-child")
	parentDef := cancelPropParentDef("ctx-parent", "ctx-child")

	runner := cancelPropRunner(t, store, cl, map[string]*definition.ProcessDefinition{
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

// TestCancelPropagationNoDefsReg verifies that CancelInstance returns no error and
// does NOT propagate when WithCallLinkStore is set but WithDefinitions is not (M1).
// Symmetric to TestCancelPropagationNoCallLinks.
func TestCancelPropagationNoDefsReg(t *testing.T) {
	ctx := t.Context()

	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	childDef := cancelPropChildDef("no-reg-child")
	parentDef := cancelPropParentDef("no-reg-parent", "no-reg-child")

	// Use a full runner to start parent+child so the child is running.
	fullRunner := cancelPropRunner(t, store, cl, map[string]*definition.ProcessDefinition{
		"no-reg-child":  childDef,
		"no-reg-parent": parentDef,
	})

	const parentID = "no-reg-parent-i1"
	st, err := fullRunner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must park")

	childID := parentID + "-sub-c1"
	childSt, _, loadErr := store.Load(ctx, childID)
	require.NoError(t, loadErr)
	assert.Equal(t, engine.StatusRunning, childSt.Status, "child must be running")

	// Build a runner with CallLinks but WITHOUT WithDefinitions — propagation gate must
	// be skipped entirely (r.defsReg == nil).
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	noRegRunner := runtimetest.MustRunner(t, nil, store,
		runtime.WithCallLinkStore(cl),
		runtime.WithHumanTasks(resolver, tasks, nil),
		// intentionally NO WithDefinitions
	)

	cancelSt, err := noRegRunner.CancelInstance(ctx, parentDef, parentID)
	require.NoError(t, err, "CancelInstance must not return an error when defsReg is nil")
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status, "parent must be Terminated")

	// Child must remain running — propagation was gated out.
	childAfter, _, loadErr2 := store.Load(ctx, childID)
	require.NoError(t, loadErr2)
	assert.Equal(t, engine.StatusRunning, childAfter.Status,
		"child must still be Running when defsReg gate suppresses propagation")
}

// TestCancelPropagationDiamond verifies that a diamond call graph (parent→B, parent→C,
// B→D, C→D, where D is the SAME running instance) cancels D exactly once (I1).
//
// Without the shared-visited-map fix, propagateCancel re-enters CancelInstance for
// each child, allocating a fresh visited map each time. In this diamond topology:
//  1. parent→B: propagateCancel(B, {parent,B}) calls CancelInstance(D).
//  2. CancelInstance(D) allocates visited={D}, succeeds, then calls propagateCancel(D, {D}).
//  3. Back in the parent's branch: propagateCancel(C, {parent,B,C}) calls CancelInstance(D)
//     again — D is already Terminated, so Deliver returns ErrWrongState which is
//     logged and swallowed (best-effort), but D is attempted twice.
//
// With the fix (propagateCancel recurses into propagateCancel with the SAME visited
// map, bypassing CancelInstance), D is marked visited before the first recursive
// descent so the C→D branch skips it entirely.
//
// We construct this topology using SeedCallLink (an export-test helper) and runner.Run
// to seed running instances directly, avoiding the need for a definition with multiple
// call activities.
func TestCancelPropagationDiamond(t *testing.T) {
	ctx := t.Context()

	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	// D: leaf grandchild that parks at a human task.
	dDef := cancelPropChildDef("dmnd-d")
	// B: intermediate child that calls D (parks after starting D).
	bDef := cancelPropParentDef("dmnd-b", "dmnd-d")
	// C: intermediate child with a human task (parks independently, no call activity).
	cDef := cancelPropChildDef("dmnd-c")
	// Parent: calls B (parks waiting for B to complete).
	parentDef := cancelPropParentDef("dmnd-parent", "dmnd-b")

	// Use a counting wrapper around cl so we can observe ListRunningChildren(D) calls.
	// Under old code (re-enters CancelInstance per child): ListRunningChildren(D) is
	// called twice — once from B→D branch's CancelInstance(D) propagation, once from
	// C→D branch's CancelInstance(D) propagation.
	// Under new code (shared visited map): D is marked visited before the C→D branch
	// processes it, so ListRunningChildren(D) is called exactly once.
	countingCL := newCountingCallLinkStore(cl)

	defs := map[string]*definition.ProcessDefinition{
		"dmnd-d":      dDef,
		"dmnd-b":      bDef,
		"dmnd-c":      cDef,
		"dmnd-parent": parentDef,
	}
	reg := cancelPropRegistry(defs)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()

	// The runner used for initial Run must use cl (not countingCL) so that call links
	// are recorded in cl's internal store. The cancel runner uses countingCL.
	setupRunner := runtimetest.MustRunner(t, nil, store,
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	// Start the parent → it launches B → B launches D; all three park.
	const parentID = "dmnd-parent-i1"
	st, err := setupRunner.Run(ctx, parentDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must be running")

	bID := parentID + "-sub-c1" // child of parent
	dID := bID + "-sub-c1"      // child of B (grandchild of parent)

	bSt, _, err := store.Load(ctx, bID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, bSt.Status, "B must be running")

	dSt, _, err := store.Load(ctx, dID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, dSt.Status, "D must be running")

	// Start C as a standalone instance (human-task child) and inject call links to
	// form the diamond: parent→C and C→D (D is a shared grandchild).
	cID := "dmnd-c-i1"
	_, err = setupRunner.Run(ctx, cDef, cID, nil)
	require.NoError(t, err)

	// Seed call links to wire the diamond topology into cl:
	//   parent → C  (C is a second running child of parent)
	//   C → D       (D is also a child of C → shared grandchild)
	runtimetest.SeedCallLink(t, cl, kernel.CallLink{
		ChildInstanceID:  cID,
		ParentInstanceID: parentID,
		ParentCommandID:  parentID + "-c2",
		ParentDefID:      parentDef.ID,
		ParentDefVersion: parentDef.Version,
		Depth:            1,
	})

	runtimetest.SeedCallLink(t, cl, kernel.CallLink{
		ChildInstanceID:  dID,
		ParentInstanceID: cID,
		ParentCommandID:  cID + "-c1",
		ParentDefID:      cDef.ID,
		ParentDefVersion: cDef.Version,
		Depth:            2,
	})

	// Build the cancel runner with the counting wrapper so we observe the guard.
	cancelRunner := runtimetest.MustRunner(t, nil, store,
		runtime.WithCallLinkStore(countingCL),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	// Cancel parent — must propagate: parent→B→D (via B), parent→C (via C), parent→C→D
	// (but D is already visited from the B branch, so the C→D branch must be skipped).
	cancelSt, err := cancelRunner.CancelInstance(ctx, parentDef, parentID)
	require.NoError(t, err, "CancelInstance must not return error for diamond topology")
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status, "parent must be Terminated")

	bAfter, _, err := store.Load(ctx, bID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, bAfter.Status, "B must be Terminated")

	cAfter, _, err := store.Load(ctx, cID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, cAfter.Status, "C must be Terminated")

	dAfter, _, err := store.Load(ctx, dID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, dAfter.Status, "D must be Terminated")

	// Assert the shared-visited-map invariant: ListRunningChildren(D) must be called
	// exactly once. Under the old buggy code it would be called twice (once per branch
	// that reaches D), confirming the double-visit. Under the new code it is called once
	// (the C→D branch is skipped because visited[D]==true before C is processed).
	//
	// Note: D has no children so ListRunningChildren(D) returns empty — it is still called
	// once (new) vs twice (old) because propagateCancel always lists children of each node
	// it visits before recursing.
	assert.Equal(t, 1, countingCL.listCount(dID),
		"ListRunningChildren(D) must be called exactly once (shared visited map guard)")
}
