package runtime

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// callDepthKey is the private context key used to thread the call-activity
// recursion depth counter through perform → r.Run → deliverLoop → perform chains.
// It is unexported so that no caller outside this package can set or read it
// accidentally; the helpers callDepth / withCallDepth are the only access points.
type callDepthKey struct{}

// maxCallDepth is the maximum nesting depth allowed for call-activity invocations.
// For the synchronous path (no CallLinkStore) it guards against stack overflow via
// the ctx-threaded depth counter. For the async path (CallLinkStore present) it is
// computed from stored link depths and blocks runaway call chains before they start.
//
// Child instance IDs use a SHORT suffix scheme (see StartSubInstance handling):
// "<parentInstanceID>-sub-c<N>" where c<N> is only the command-sequence suffix,
// not the full parent ID. This gives O(depth) growth rather than O(2^depth), so
// depth 64 is safely bounded.
const maxCallDepth = 64

// callDepth returns the current call-activity nesting depth stored in ctx.
// Returns 0 if no depth has been set (i.e. the outermost call).
func callDepth(ctx context.Context) int {
	if d, ok := ctx.Value(callDepthKey{}).(int); ok {
		return d
	}
	return 0
}

// withCallDepth returns a child context with the call-activity depth set to d.
func withCallDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, callDepthKey{}, d)
}

// runChild starts a child instance — driving its first burst SYNCHRONOUSLY on the
// caller's goroutine — with the call link threaded into the child's first Create.
// It is "non-blocking" only in the engine sense: the PARENT does not wait for the
// child's eventual terminal state (a notifier resumes the parent later). Do NOT
// wrap this in a goroutine — it shares the Store, and concurrent child starts would
// break the store's write ordering. It is called by the async StartSubInstance path
// when r.callLinks != nil.
//
// It drives the child's first burst (StartInstance trigger) through deliverLoop
// with create=true, passing link so the child's first AppliedStep.NewCallLink
// is set atomically. The parent stays parked; the child may park too (e.g. at a
// human task) — that is the expected outcome for the async path.
func (r *ProcessDriver) runChild(ctx context.Context, def *model.ProcessDefinition, childInstanceID string, vars map[string]any, link *kernel.CallLink) error {
	st := engine.InstanceState{InstanceID: childInstanceID}
	_, err := r.deliverLoop(ctx, def, st, 0, true, link, engine.NewStartInstance(r.clk.Now(), vars))
	return err
}
