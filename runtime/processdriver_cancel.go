package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// CancelInstance terminates a running instance by delivering a CancelRequested
// trigger. Any definition-level CancelActions run best-effort inside the same
// deliverLoop (failures are logged, never fail the cancel). When CallLinks and
// a DefinitionRegistry are both configured, running async child instances are
// cancelled recursively (best-effort: errors are logged, never returned). Returns
// the terminated parent InstanceState. See ADR-0028, ADR-0032.
func (r *ProcessDriver) CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error) {
	// Parent-first: terminate the parent before propagating to children so that
	// no CallNotifier can resume a child-completed parent during propagation.
	st, err := r.Deliver(ctx, def, instanceID, engine.NewCancelRequested(r.clk.Now()))
	if err != nil {
		return st, err
	}
	if r.callLinks != nil && r.defsReg != nil {
		visited := map[string]bool{instanceID: true}
		r.propagateCancel(ctx, instanceID, visited)
	}
	return st, nil
}

// propagateCancel recursively cancels all running async child instances of
// parentID. Every error is logged and swallowed — this is best-effort only
// (ADR-0032). visited is shared across the entire cancel tree so that a node
// reachable via multiple paths (diamond topology) is delivered CancelRequested
// exactly once and never double-cancelled.
func (r *ProcessDriver) propagateCancel(ctx context.Context, parentID string, visited map[string]bool) {
	children, err := r.callLinks.ListRunningChildren(ctx, parentID)
	if err != nil {
		r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
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

		childSt, _, loadErr := r.store.Load(ctx, child.ChildInstanceID)
		if loadErr != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: load child instance failed",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("error", loadErr.Error()),
			)
			continue
		}

		ref := fmt.Sprintf("%s:%d", childSt.DefID, childSt.DefVersion)
		childDef, lookupErr := r.defsReg.Lookup(ctx, ref)
		if lookupErr != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: child def not found",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("def_ref", ref),
				slog.String("error", lookupErr.Error()),
			)
			continue
		}

		// Deliver CancelRequested directly (parent-first) then recurse into
		// propagateCancel with the SAME shared visited map. Re-entering CancelInstance
		// would allocate a fresh visited map per child, breaking the diamond guard.
		if _, cancelErr := r.Deliver(ctx, childDef, child.ChildInstanceID, engine.NewCancelRequested(r.clk.Now())); cancelErr != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: cancel child instance failed",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("error", cancelErr.Error()),
			)
			continue
		}
		// Recurse into the child's own subtree with the shared visited map.
		r.propagateCancel(ctx, child.ChildInstanceID, visited)
	}
}
