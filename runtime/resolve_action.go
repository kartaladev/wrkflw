package runtime

import (
	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// resolveInvokeAction resolves the action for a main-action InvokeAction: the
// scope-effective scoped catalog carried on the command (falling back to the
// top-level def's scoped catalog when the command carries none, e.g. secondary
// actions) → the global catalog.
func (driver *ProcessDriver) resolveInvokeAction(def *model.ProcessDefinition, cmd engine.InvokeAction) (action.Action, bool) {
	scoped := cmd.Scoped
	if scoped == nil && def != nil {
		scoped = def.ScopedCatalog()
	}
	return action.Resolve(scoped, driver.cat, cmd.Name)
}

// resolveActionName resolves a name-only (secondary / cancel) action against the
// top-level definition's scoped catalog, then the global catalog.
func (driver *ProcessDriver) resolveActionName(def *model.ProcessDefinition, name string) (action.Action, bool) {
	var scoped action.Catalog
	if def != nil {
		scoped = def.ScopedCatalog()
	}
	return action.Resolve(scoped, driver.cat, name)
}
