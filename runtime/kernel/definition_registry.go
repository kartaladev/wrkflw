package kernel

import (
	"context"
	"errors"
	"fmt"

	"github.com/kartaladev/wrkflw/definition/model"
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
//
// Unlike [MemDefinitionRegistry.Register], this constructor does not run
// [model.Validate] — it returns no error, so it has no way to reject. It is
// therefore the one documented escape hatch from the authoring gate the engine's
// contract assumes: a caller passing hand-constructed *model.ProcessDefinition
// literals here owns validating them.
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

// ListDefinitions implements [DefinitionLister]. It returns each registered
// definition exactly once, even though the constructor indexes every
// definition under two Qualifier keys (pinned and latest) — dedupe is by
// concrete *model.ProcessDefinition pointer, not by map key. ctx is ignored —
// the enumeration is entirely in-memory. The registry is immutable after
// construction, so no locking is required.
func (r *MapDefinitionRegistry) ListDefinitions(context.Context) []*model.ProcessDefinition {
	return distinctDefinitions(r.m)
}

// distinctDefinitions returns each definition in m exactly once, deduped by
// concrete *model.ProcessDefinition pointer (a registry indexes every
// definition under both its pinned and its latest Qualifier key). The result is
// always non-nil, and its iteration order is unspecified (Go map order).
//
// The caller is responsible for whatever locking m requires.
func distinctDefinitions(m map[model.Qualifier]*model.ProcessDefinition) []*model.ProcessDefinition {
	seen := make(map[*model.ProcessDefinition]struct{}, len(m))
	out := make([]*model.ProcessDefinition, 0, len(m))
	for _, d := range m {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// Compile-time assertion: MapDefinitionRegistry satisfies DefinitionLister.
var _ DefinitionLister = (*MapDefinitionRegistry)(nil)
