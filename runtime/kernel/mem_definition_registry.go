package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// ── Sentinel errors ────────────────────────────────────────────────────────

// ErrNilDefinition is returned by MemDefinitionRegistry.Register when the
// supplied definition pointer is nil.
var ErrNilDefinition = errors.New("workflow-runtime: nil definition")

// ErrEmptyDefinitionID is returned by MemDefinitionRegistry.Register when the
// supplied definition has an empty ID field.
var ErrEmptyDefinitionID = errors.New("workflow-runtime: empty definition ID")

// ErrDefinitionExists is returned by MemDefinitionRegistry.Register when a
// definition with the same Qualifier (ID+Version) key has already been registered
// (first-registration-wins on the versioned key).
var ErrDefinitionExists = errors.New("workflow-runtime: definition already registered")

// ── MemDefinitionRegistry ─────────────────────────────────────────────────

// MemDefinitionRegistry is a concurrency-safe, register-after-construction
// in-memory DefinitionRegistry. It is the mutable sibling of the immutable
// MapDefinitionRegistry; use it when definitions are registered incrementally
// (e.g. the process-global default populated at application init).
//
// Register indexes each definition under two keys:
//   - def.Qualifier()     — exact versioned key; first-registration-wins.
//   - model.Latest(def.ID) — latest key; overwritten to the most-recently-registered
//     version so a Latest Qualifier always resolves the newest registered version.
//
// # Concurrency
//
// All methods are safe for concurrent use. Never copy a MemDefinitionRegistry
// after first use — it contains a sync.RWMutex that must not be copied.
type MemDefinitionRegistry struct {
	mu sync.RWMutex
	m  map[model.Qualifier]*model.ProcessDefinition
}

// NewMemDefinitionRegistry returns an empty, ready-to-use MemDefinitionRegistry.
func NewMemDefinitionRegistry() *MemDefinitionRegistry {
	return &MemDefinitionRegistry{
		m: make(map[model.Qualifier]*model.ProcessDefinition),
	}
}

// Register indexes def under both its pinned Qualifier and its latest Qualifier.
// It returns:
//   - [ErrNilDefinition] if def is nil.
//   - [ErrEmptyDefinitionID] if def.ID is empty.
//   - [ErrDefinitionExists] (wrapped with the pinned key) if the exact
//     Qualifier was already registered (first-registration-wins on the
//     versioned key).
//
// On success the latest key is overwritten to point at def, so subsequent
// Lookup calls with a Latest Qualifier resolve the most-recently-registered version.
func (r *MemDefinitionRegistry) Register(def *model.ProcessDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if def.ID == "" {
		return ErrEmptyDefinitionID
	}

	pinned := def.Qualifier()
	latest := model.Latest(def.ID)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.m[pinned]; exists {
		return fmt.Errorf("%w: %q", ErrDefinitionExists, pinned)
	}

	r.m[pinned] = def
	// Last-registered-wins for the latest key (NOT highest-version);
	// MapDefinitionRegistry keeps the highest version instead.
	r.m[latest] = def

	return nil
}

// MustRegister calls Register and panics if it returns an error. Intended for
// init-time wiring where a registration failure is a programming error.
func (r *MemDefinitionRegistry) MustRegister(def *model.ProcessDefinition) {
	if err := r.Register(def); err != nil {
		panic(fmt.Sprintf("kernel.MemDefinitionRegistry.MustRegister: %v", err))
	}
}

// Lookup implements [DefinitionRegistry]. It returns the ProcessDefinition
// registered under q, or ([ErrDefinitionNotFound], nil) when no definition
// matches. ctx is ignored — the lookup is entirely in-memory.
//
// q may be either:
//   - model.Latest(id)        — resolves the most-recently-registered version.
//   - model.Version(id, v)    — resolves the exact versioned registration.
func (r *MemDefinitionRegistry) Lookup(_ context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	def, ok := r.m[q]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDefinitionNotFound, q)
	}
	return def, nil
}

// Compile-time assertion: MemDefinitionRegistry satisfies DefinitionRegistry.
var _ DefinitionRegistry = (*MemDefinitionRegistry)(nil)
