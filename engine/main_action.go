package engine

import "github.com/zakyalvan/krtlwrkflw/definition"

// mainActionName returns the lookup key for a task's primary action: the
// explicit action name, or the node id when no name was set (default-by-id).
// Inline actions take precedence at resolution time and are unaffected by this.
func mainActionName(n definition.Node) string {
	if name := definition.ActionOf(n); name != "" {
		return name
	}
	return n.ID()
}
