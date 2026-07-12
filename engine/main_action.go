package engine

import "github.com/kartaladev/wrkflw/definition/model"

// mainActionName returns the lookup key for a task's primary action: the
// explicit action name, or the node id when no name was set (default-by-id).
func mainActionName(n model.Node) string {
	if name := model.ActionOf(n); name != "" {
		return name
	}
	return n.ID()
}
