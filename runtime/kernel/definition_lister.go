package kernel

import (
	"context"

	"github.com/kartaladev/wrkflw/definition/model"
)

// DefinitionLister is an OPTIONAL capability a DefinitionRegistry may
// implement so the event-based-start subsystem can enumerate registered
// definitions and find the ones carrying a signal, message, or timer start
// event. Registries that do not implement it simply disable event-based
// *start* — correlating an event to an already-running instance still works
// through DefinitionRegistry.Lookup alone.
type DefinitionLister interface {
	// ListDefinitions returns each registered definition exactly once. A
	// registry that indexes the same definition under multiple Qualifier
	// keys (e.g. both a pinned "<ID>:<Version>" key and a "<ID>" latest key)
	// must deduplicate by concrete definition, not by key.
	ListDefinitions(ctx context.Context) []*model.ProcessDefinition
}
