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
// definition with the same "<ID>:<Version>" key has already been registered
// (first-registration-wins on the versioned key).
var ErrDefinitionExists = errors.New("workflow-runtime: definition already registered")

// ── MemDefinitionRegistry ─────────────────────────────────────────────────

// MemDefinitionRegistry is a concurrency-safe, register-after-construction
// in-memory DefinitionRegistry. It is the mutable sibling of the immutable
// MapDefinitionRegistry; use it when definitions are registered incrementally
// (e.g. the process-global default populated at application init).
//
// Register indexes each definition under two keys:
//   - "<ID>:<Version>"  — exact versioned key; first-registration-wins.
//   - "<ID>"            — bare ID key; overwritten to the most-recently-registered
//     version so a DefRef without a version always resolves the latest registered.
//
// # Concurrency
//
// All methods are safe for concurrent use. Never copy a MemDefinitionRegistry
// after first use — it contains a sync.RWMutex that must not be copied.
type MemDefinitionRegistry struct {
	mu sync.RWMutex
	m  map[string]*model.ProcessDefinition
}

// NewMemDefinitionRegistry returns an empty, ready-to-use MemDefinitionRegistry.
func NewMemDefinitionRegistry() *MemDefinitionRegistry {
	return &MemDefinitionRegistry{
		m: make(map[string]*model.ProcessDefinition),
	}
}

// Register indexes def under both "<ID>" and "<ID>:<Version>". It returns:
//   - [ErrNilDefinition] if def is nil.
//   - [ErrEmptyDefinitionID] if def.ID is empty.
//   - [ErrDefinitionExists] (wrapped with the versioned key) if "<ID>:<Version>"
//     was already registered (first-registration-wins on the versioned key).
//
// On success the bare "<ID>" key is overwritten to point at def, so subsequent
// Lookup calls without a version resolve the most-recently-registered version.
func (r *MemDefinitionRegistry) Register(def *model.ProcessDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if def.ID == "" {
		return ErrEmptyDefinitionID
	}

	versionedKey := fmt.Sprintf("%s:%d", def.ID, def.Version)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.m[versionedKey]; exists {
		return fmt.Errorf("%w: %q", ErrDefinitionExists, versionedKey)
	}

	r.m[versionedKey] = def
	r.m[def.ID] = def // overwrite bare key to track latest registered version

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
// registered under defRef, or ([ErrDefinitionNotFound], nil) when no definition
// matches. ctx is ignored — the lookup is entirely in-memory.
//
// defRef may be in either form:
//   - "<ID>"            — resolves the most-recently-registered version.
//   - "<ID>:<Version>"  — resolves the exact versioned registration.
func (r *MemDefinitionRegistry) Lookup(_ context.Context, defRef string) (*model.ProcessDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	def, ok := r.m[defRef]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDefinitionNotFound, defRef)
	}
	return def, nil
}

// Compile-time assertion: MemDefinitionRegistry satisfies DefinitionRegistry.
var _ DefinitionRegistry = (*MemDefinitionRegistry)(nil)
