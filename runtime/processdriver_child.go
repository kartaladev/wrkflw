package runtime

import (
	"context"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// callDepthKey is the private context key used to thread the call-activity
// recursion depth counter through perform → driver.Drive → deliverLoop → perform chains.
// It is unexported so that no caller outside this package can set or read it
// accidentally; the helpers callDepth / withCallDepth are the only access points.
type callDepthKey struct{}

// maxCallDepth is the maximum nesting depth allowed for call-activity invocations.
// For the synchronous path (no CallLinkStore) it guards against stack overflow via
// the ctx-threaded depth counter. For the async path (CallLinkStore present) it is
// computed from stored link depths and blocks runaway call chains before they start.
//
// Child instance IDs use a SHORT suffix scheme (see childInstanceIDFor in
// processdriver_action.go): "<parentInstanceID>-sub-<suffix>", where <suffix> is
// the bare command-sequence counter ("c3") when the engine minted the command id
// from its built-in counter, and a fixed-length digest of the command id when an
// IDGenerator minted an opaque one (xid/uuid). Either way the suffix
// is short and constant-length, so growth is O(depth) rather than O(2^depth) and
// each level adds a fixed number of characters. A total length cap folds a
// runaway derivation, so depth 64 is bounded well inside the instance_id column.
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
// when driver.callLinks != nil.
//
// It drives the child's first burst (StartInstance trigger) through deliverLoop
// with create=true, passing link so the child's first AppliedStep.NewCallLink
// is set atomically. The parent stays parked; the child may park too (e.g. at a
// human task) — that is the expected outcome for the async path.
func (driver *ProcessDriver) runChild(ctx context.Context, def *model.ProcessDefinition, childInstanceID string, vars map[string]any, link *kernel.CallLink) error {
	st := engine.InstanceState{InstanceID: childInstanceID}
	_, err := driver.deliverLoop(ctx, def, st, 0, true, link, engine.NewStartInstance(driver.clk.Now(), vars))
	return err
}
