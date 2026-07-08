package validation

import (
	"errors"
	"fmt"
	"sync"
)

// ErrUnknownKind is returned by Registry.Strategy for a descriptor Kind with no
// registered factory (the consumer did not opt into that adapter).
var ErrUnknownKind = errors.New("workflow-validation: unknown validation kind")

// StrategyFactory rebuilds a declarative strategy from its serialized schema text.
type StrategyFactory func(schema string) (ValidationStrategy, error)

// Registry maps a descriptor Kind -> factory. The Loader uses it to reconstruct
// strategies from a serialized definition. Registration is explicit (no init magic),
// matching the action-catalog wiring pattern.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]StrategyFactory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{factories: make(map[string]StrategyFactory)} }

// Register maps kind -> factory. A later registration for the same kind wins.
func (r *Registry) Register(kind string, f StrategyFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = f
}

// Strategy rebuilds the live strategy for descriptor d, or ErrUnknownKind if the kind
// is not registered.
func (r *Registry) Strategy(d ValidationDescriptor) (ValidationStrategy, error) {
	r.mu.RLock()
	f, ok := r.factories[d.Kind]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, d.Kind)
	}
	return f(d.Schema)
}
