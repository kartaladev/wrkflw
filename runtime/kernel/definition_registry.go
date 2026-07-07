package kernel

import (
	"context"
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// ErrDefinitionNotFound is returned by DefinitionRegistry.Lookup when no
// definition is registered for the given Qualifier.
var ErrDefinitionNotFound = errors.New("workflow-runtime: definition not found in registry")

// DefinitionRegistry resolves a [model.Qualifier] to a *model.ProcessDefinition.
// Implementations must be safe for concurrent read access by multiple goroutines.
//
// Contract: Lookup returns (nil, ErrDefinitionNotFound) when the Qualifier is not
// registered. Any other error indicates a transient or structural problem and
// the caller should propagate it.
type DefinitionRegistry interface {
	Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error)
}

// MapDefinitionRegistry is an immutable-after-construction, in-memory
// DefinitionRegistry backed by a plain map. It is safe for concurrent reads.
//
// Construct via NewMapDefinitionRegistry; do not use the zero value.
type MapDefinitionRegistry struct {
	m map[model.Qualifier]*model.ProcessDefinition
}

// NewMapDefinitionRegistry indexes each non-nil definition under both its pinned
// Qualifier (def.Qualifier()) and its latest Qualifier (Latest(def.ID)); the
// latest key resolves to the highest version seen.
func NewMapDefinitionRegistry(defs ...*model.ProcessDefinition) *MapDefinitionRegistry {
	m := make(map[model.Qualifier]*model.ProcessDefinition, len(defs)*2)
	for _, d := range defs {
		if d == nil {
			continue
		}
		m[d.Qualifier()] = d
		latest := model.Latest(d.ID)
		if cur, ok := m[latest]; !ok || d.Version >= cur.Version {
			m[latest] = d
		}
	}
	return &MapDefinitionRegistry{m: m}
}

// Lookup returns the ProcessDefinition registered under q, or
// ErrDefinitionNotFound if none is registered. ctx is ignored — in-memory lookup.
func (r *MapDefinitionRegistry) Lookup(_ context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	def, ok := r.m[q]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDefinitionNotFound, q)
	}
	return def, nil
}
