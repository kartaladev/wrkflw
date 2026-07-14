package runtime

import (
	"context"
	"log/slog"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// CancelInstance terminates a running instance by delivering a CancelRequested
// trigger. Any definition-level CancelActions run best-effort inside the same
// deliverLoop (failures are logged, never fail the cancel). When CallLinks and
// a DefinitionRegistry are both configured, running async child instances are
// cancelled recursively (best-effort: errors are logged, never returned). Returns
// the terminated parent InstanceState. See ADR-0028, ADR-0032.
func (driver *ProcessDriver) CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error) {
	release, ok := driver.admit()
	if !ok {
		return engine.InstanceState{}, ErrDriverShuttingDown
	}
	defer release()

	// Parent-first: terminate the parent before propagating to children so that
	// no CallNotifier can resume a child-completed parent during propagation.
	st, err := driver.applyTrigger(ctx, def, instanceID, engine.NewCancelRequested(driver.clk.Now()))
	if err != nil {
		return st, err
	}
	if driver.callLinks != nil && driver.defsReg != nil {
		visited := map[string]bool{instanceID: true}
		driver.propagateCancel(ctx, instanceID, visited)
	}
	return st, nil
}

// propagateCancel recursively cancels all running async child instances of
// parentID. Every error is logged and swallowed — this is best-effort only
// (ADR-0032). visited is shared across the entire cancel tree so that a node
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

		// ApplyTrigger CancelRequested directly (parent-first) then recurse into
		// propagateCancel with the SAME shared visited map. Re-entering CancelInstance
		// would allocate a fresh visited map per child, breaking the diamond guard.
		if _, cancelErr := driver.applyTrigger(ctx, childDef, child.ChildInstanceID, engine.NewCancelRequested(driver.clk.Now())); cancelErr != nil {
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: cancel child instance failed",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("error", cancelErr.Error()),
			)
			continue
		}
		// Recurse into the child's own subtree with the shared visited map.
		driver.propagateCancel(ctx, child.ChildInstanceID, visited)
	}
}
