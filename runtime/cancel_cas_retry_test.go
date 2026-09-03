package runtime_test

// cancel_cas_retry_test.go
//
// CancelInstance's parent cancel and propagateCancel's child cancel both drove
// the single-shot applyTrigger, which has no CAS retry at all. One
// kernel.ErrConcurrentUpdate on a child's commit was therefore terminal: the
// child stayed running, the `continue` skipped the recursion, and the whole
// grandchild subtree was orphaned while CancelInstance returned err=nil.
// ListRunningChildren has exactly one non-test caller, so nothing revisits them.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// instanceCASConflictStore injects exactly ONE kernel.ErrConcurrentUpdate into
// the first Commit that targets instanceID, then delegates everything. It models
// the ordinary race the cancel cascade loses to: a concurrent writer advanced
// the child's row between this cascade's Load and its Commit.
type instanceCASConflictStore struct {
	inner      *kernel.MemInstanceStore
	instanceID string
	injected   atomic.Bool
}

func (s *instanceCASConflictStore) Create(ctx context.Context, step kernel.AppliedStep) (kernel.Version, error) {
	return s.inner.Create(ctx, step)
}

func (s *instanceCASConflictStore) Load(ctx context.Context, id string) (engine.InstanceState, kernel.Version, error) {
	return s.inner.Load(ctx, id)
}

func (s *instanceCASConflictStore) Commit(ctx context.Context, expected kernel.Version, step kernel.AppliedStep) (kernel.Version, error) {
	if step.State.InstanceID == s.instanceID && s.injected.CompareAndSwap(false, true) {
		return 0, kernel.ErrConcurrentUpdate
	}
	return s.inner.Commit(ctx, expected, step)
}

// TestCancelPropagationRetriesChildCASConflict pins the retry: a single CAS
// conflict on a child's cancel commit must be retried, not swallowed, and must
// not cost the GRANDCHILD its cancellation.
//
// What makes it fail before the fix: applyTrigger is a single Load+deliverLoop,
// so kernel.ErrConcurrentUpdate propagates straight out of the child cancel at
// propagateCancel; the error is not engine.ErrCancelNotApplicable, so the loop
// logs a WARN and `continue`s — skipping the recursion. The child AND the
// grandchild both stay StatusRunning while CancelInstance returns err=nil.
// The grandchild assertion is load-bearing: a one-level fix (retry the child but
// keep the `continue`) still leaves it running.
func TestCancelPropagationRetriesChildCASConflict(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	parentDef := cancelPropChildDef("cascas-parent")
	childDef := cancelPropChildDef("cascas-child")
	grandchildDef := cancelPropChildDef("cascas-grandchild")

	cl := kernel.NewMemCallLinkStore()
	inner := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))
	store := &instanceCASConflictStore{inner: inner, instanceID: "cascas-c1"}

	seedInstance(t, inner, runningChildState(t, parentDef, "cascas-p1"), nil)
	seedInstance(t, inner, runningChildState(t, childDef, "cascas-c1"), &kernel.CallLink{
		ChildInstanceID:  "cascas-c1",
		ParentInstanceID: "cascas-p1",
		ParentCommandID:  "cascas-cmd1",
		ParentDefID:      parentDef.ID,
		ParentDefVersion: parentDef.Version,
		Depth:            1,
	})
	seedInstance(t, inner, runningChildState(t, grandchildDef, "cascas-g1"), &kernel.CallLink{
		ChildInstanceID:  "cascas-g1",
		ParentInstanceID: "cascas-c1",
		ParentCommandID:  "cascas-cmd2",
		ParentDefID:      childDef.ID,
		ParentDefVersion: childDef.Version,
		Depth:            2,
	})

	driver := runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(kernel.NewMapDefinitionRegistry(parentDef, childDef, grandchildDef)),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(map[string][]authz.Actor{}), humantask.NewMemTaskStore(), nil),
	)

	_, err := driver.CancelInstance(ctx, parentDef, "cascas-p1")
	require.NoError(t, err, "a retried CAS conflict must not surface as a cancel failure")

	load := func(id string) engine.InstanceState {
		st, _, loadErr := inner.Load(ctx, id)
		require.NoError(t, loadErr)
		return st
	}

	assert.True(t, store.injected.Load(),
		"self-certifying: the CAS conflict must actually have been injected")
	assert.Equal(t, engine.StatusTerminated, load("cascas-p1").Status, "the parent must terminate")
	assert.Equal(t, engine.StatusTerminated, load("cascas-c1").Status,
		"the child's cancel must be RETRIED past the single CAS conflict")
	assert.Equal(t, engine.StatusTerminated, load("cascas-g1").Status,
		"the grandchild must terminate too: a retry that does not restore the recursion still orphans the subtree")
}
