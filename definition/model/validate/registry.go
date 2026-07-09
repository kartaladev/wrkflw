package validate

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

// defaultRegistry is the process-global registry adapters self-register into.
var defaultRegistry = NewRegistry()

// DefaultRegistry is the process-global registry consulted on durable reload
// (ProcessDefinition.UnmarshalJSON) and as build()'s fallback when no explicit
// loader registry is configured. Adapters register their kind here via init(), so
// importing a validation adapter (validate/expr, validate/jsonschema,
// validate/avro) arms durable reload for that kind.
func DefaultRegistry() *Registry { return defaultRegistry }

// Register maps kind -> factory in the DefaultRegistry. It is the convenience
// entry point adapters call from their init() to self-register.
func Register(kind string, f StrategyFactory) { defaultRegistry.Register(kind, f) }

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
