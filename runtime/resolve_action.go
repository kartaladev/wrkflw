package runtime

import (
	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// resolveActionName resolves name against the definition-scoped catalog first,
// then the runner's global catalog (action.Resolve). Used for every secondary
// action reference (compensation, SLA, reminder, cancel handler, CancelActions).
func (r *Runner) resolveActionName(def *model.ProcessDefinition, name string) (action.ServiceAction, bool) {
	var scoped action.Catalog
	if def != nil {
		scoped = def.ScopedCatalog()
	}
	return action.Resolve(scoped, r.cat, name)
}

// resolveActionFor resolves a node's primary action: a node-local inline action
// (highest precedence) when nodeID names a task carrying one, else the
// scoped→global name chain. nodeID may be empty for non-node invocations.
func (r *Runner) resolveActionFor(def *model.ProcessDefinition, nodeID, name string) (action.ServiceAction, bool) {
	if def != nil && nodeID != "" {
		if n, ok := def.Node(nodeID); ok {
			if inline := model.InlineActionOf(n); inline != nil {
				return inline, true
			}
		}
	}
	return r.resolveActionName(def, name)
}
