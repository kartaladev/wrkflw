package kernel

import (
	"context"
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/model"
)

// ErrDefinitionNotFound is returned by DefinitionRegistry.Lookup when no
// definition is registered for the given DefRef.
var ErrDefinitionNotFound = errors.New("workflow-runtime: definition not found in registry")

// DefinitionRegistry resolves a DefRef string (as stored on a KindCallActivity
// node) to a *model.ProcessDefinition. Implementations must be safe for
// concurrent read access by multiple goroutines.
//
// Contract: Lookup returns (nil, ErrDefinitionNotFound) when the DefRef is not
// registered. Any other error indicates a transient or structural problem and
// the caller should propagate it.
type DefinitionRegistry interface {
	Lookup(ctx context.Context, defRef string) (*model.ProcessDefinition, error)
}

// MapDefinitionRegistry is an immutable-after-construction, in-memory
// DefinitionRegistry backed by a plain map. It is safe for concurrent reads.
//
// Construct via NewMapDefinitionRegistry; do not use the zero value.
type MapDefinitionRegistry struct {
	m map[string]*model.ProcessDefinition
}

// NewMapDefinitionRegistry constructs a MapDefinitionRegistry from the
// supplied map. The map is shallow-copied at construction time so subsequent
// mutations to the caller's map do not affect the registry.
//
// Keys are the DefRef strings referenced by KindCallActivity nodes; values are
// the corresponding process definitions. Nil definitions are ignored (skipped).
func NewMapDefinitionRegistry(defs map[string]*model.ProcessDefinition) *MapDefinitionRegistry {
	m := make(map[string]*model.ProcessDefinition, len(defs))
	for k, v := range defs {
		if v != nil {
			m[k] = v
		}
	}
	return &MapDefinitionRegistry{m: m}
}

// Lookup returns the ProcessDefinition registered under defRef, or
// ErrDefinitionNotFound if none is registered. ctx is ignored — in-memory lookup.
func (r *MapDefinitionRegistry) Lookup(_ context.Context, defRef string) (*model.ProcessDefinition, error) {
	def, ok := r.m[defRef]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDefinitionNotFound, defRef)
	}
	return def, nil
}
