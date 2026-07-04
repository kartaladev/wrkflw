package runtime

import (
	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// resolveInvokeAction resolves the action for a main-action InvokeAction:
// node-local inline (carried by the engine, scope-correct) → the scope-effective
// scoped catalog carried on the command (falling back to the top-level def's
// scoped catalog when the command carries none, e.g. secondary actions) → the
// global catalog.
func (r *ProcessDriver) resolveInvokeAction(def *definition.ProcessDefinition, cmd engine.InvokeAction) (action.ServiceAction, bool) {
	if cmd.Inline != nil {
		return cmd.Inline, true
	}
	scoped := cmd.Scoped
	if scoped == nil && def != nil {
		scoped = def.ScopedCatalog()
	}
	return action.Resolve(scoped, r.cat, cmd.Name)
}

// resolveActionName resolves a name-only (secondary / cancel) action against the
// top-level definition's scoped catalog, then the global catalog.
func (r *ProcessDriver) resolveActionName(def *definition.ProcessDefinition, name string) (action.ServiceAction, bool) {
	var scoped action.Catalog
	if def != nil {
		scoped = def.ScopedCatalog()
	}
	return action.Resolve(scoped, r.cat, name)
}
