package runtime

import (
	"context"
	"errors"
	"log/slog"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// CancelInstance terminates a running instance by delivering a CancelRequested
// trigger. Any definition-level CancelActions run best-effort inside the same
// deliverLoop (failures are logged, never fail the cancel). When CallLinks and
// a DefinitionRegistry are both configured, running async child instances are
// cancelled recursively (best-effort: errors are logged, never returned). Returns
// the terminated parent InstanceState.
//
// Every cancel delivery in the cascade — the parent's and each descendant's —
// goes through applyTriggerRetryingCAS. A cancel has no other delivery path, so
// a surfaced [kernel.ErrConcurrentUpdate] used to be a permanent loss: it left
// the losing child running AND skipped the recursion, orphaning that child's
// whole subtree while this call still returned nil.
//
// Returns [engine.ErrCancelNotApplicable] when the engine DROPPED this
// instance's cancel — an admin partial rollback owns it, so the cancel did
// nothing and never will. That answer is a report, not an abort: the
// returned state is the untouched one, and the child subtree has still been
// cancelled by the time it is returned.
func (driver *ProcessDriver) CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error) {
	release, ok := driver.admit()
	if !ok {
		return engine.InstanceState{}, ErrDriverShuttingDown
	}
	defer release()

	// Parent-first: terminate the parent before propagating to children so that
	// no CallNotifier can resume a child-completed parent during propagation.
	st, err := driver.applyTriggerRetryingCAS(ctx, def, instanceID, engine.NewCancelRequested(driver.clk.Now()))
	// engine.ErrCancelNotApplicable REPORTS that this instance's own cancel was
	// dropped; it is not a reason to abandon its children. Returning
	// here on it was measured leaving a subtree permanently running — strictly
	// worse than the silent nil the sentinel replaces — so the sentinel is
	// re-reported AFTER propagation, and only after it.
	if err != nil && !errors.Is(err, engine.ErrCancelNotApplicable) {
		return st, err
	}
	if driver.callLinks != nil && driver.defsReg != nil {
		visited := map[string]bool{instanceID: true}
		driver.propagateCancel(ctx, instanceID, visited)
	}
	return st, err
}

// propagateCancel recursively cancels all running async child instances of
// parentID. Every error is logged and swallowed — this is best-effort only.
// visited is shared across the entire cancel tree so that a node
// reachable via multiple paths (diamond topology) is delivered CancelRequested
// exactly once and never double-cancelled.
func (driver *ProcessDriver) propagateCancel(ctx context.Context, parentID string, visited map[string]bool) {
	children, err := driver.callLinks.ListRunningChildren(ctx, parentID)
	if err != nil {
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
			"runtime: propagateCancel: list running children failed",
			slog.String("parent_id", parentID),
			slog.String("error", err.Error()),
		)
		return
	}
	for _, child := range children {
		if visited[child.ChildInstanceID] {
			continue
		}
		visited[child.ChildInstanceID] = true

		childSt, _, loadErr := driver.store.Load(ctx, child.ChildInstanceID)
		if loadErr != nil {
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: load child instance failed",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("error", loadErr.Error()),
			)
			continue
		}

		childQ := model.Version(childSt.DefID, childSt.DefVersion)
		childDef, lookupErr := driver.defsReg.Lookup(ctx, childQ)
		if lookupErr != nil {
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: child def not found",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("def_ref", childQ.String()),
				slog.String("error", lookupErr.Error()),
			)
			continue
		}

		// Deliver CancelRequested directly (parent-first) then recurse into
		// propagateCancel with the SAME shared visited map. Re-entering CancelInstance
		// would allocate a fresh visited map per child, breaking the diamond guard.
		// The delivery retries a lost CAS race rather than surfacing it: nothing
		// else revisits a child once ListRunningChildren has returned it.
		if _, cancelErr := driver.applyTriggerRetryingCAS(ctx, childDef, child.ChildInstanceID, engine.NewCancelRequested(driver.clk.Now())); cancelErr != nil {
			// A child whose own cancel is DROPPED (engine.ErrCancelNotApplicable)
			// keeps running by design — but its own children have no walk
			// in flight and must still be cancelled. Skipping the recursion here was
			// measured orphaning the whole grandchild subtree; every OTHER error is
			// a failure to reach the child at all, where recursing would be guessing.
			//
			// The two outcomes are logged apart on purpose. A dropped cancel is the
			// EXPECTED answer for a child owned by an admin partial rollback, so
			// reporting it at WARN as a failure — which this did until
			// `/code-review` caught it — trains an operator to ignore the one line
			// that means the propagation really could not reach a child.
			if !errors.Is(cancelErr, engine.ErrCancelNotApplicable) {
				driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
					"runtime: propagateCancel: cancel child instance failed",
					slog.String("child_id", child.ChildInstanceID),
					slog.String("error", cancelErr.Error()),
				)
				continue
			}
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelDebug,
				"runtime: propagateCancel: child kept its own compensation walk; cancel dropped",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("reason", cancelErr.Error()),
			)
		}
		// Recurse into the child's own subtree with the shared visited map.
		driver.propagateCancel(ctx, child.ChildInstanceID, visited)
	}
}
