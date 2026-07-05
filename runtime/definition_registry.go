package runtime

import (
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// defaultDefinitionRegistry is the process-global DefinitionRegistry a
// ProcessDriver uses when [WithDefinitions] is not supplied. Populate it at
// application startup via [RegisterDefinition] or [MustRegisterDefinition].
//
// # Test isolation
//
// This registry is process-wide: once a definition is registered under a given
// "<ID>:<Version>" key it cannot be overwritten (first-registration-wins). Tests
// that need a clean slate must build an isolated registry and pass it via
// [WithDefinitions](kernel.NewMemDefinitionRegistry()) rather than using the
// global default.
var defaultDefinitionRegistry = kernel.NewMemDefinitionRegistry()

// DefaultDefinitionRegistry returns the process-global [kernel.MemDefinitionRegistry]
// used by [NewProcessDriver] when no [WithDefinitions] option is supplied. The
// same pointer is returned on every call — callers may bind it to local variables
// to compare identity.
//
// Use [RegisterDefinition] or [MustRegisterDefinition] for the ergonomic one-call
// path; call this function directly only when you need the registry value itself
// (e.g. to pass it to a [kernel.CachingDefinitionRegistry]).
func DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry {
	return defaultDefinitionRegistry
}

// RegisterDefinition registers def into the process-global [DefaultDefinitionRegistry].
// It is the definition-side counterpart of [action.Register].
//
// The definition is indexed under both "<ID>" and "<ID>:<Version>" so a
// [engine.StartSubInstance] DefRef in either form resolves correctly. The bare
// "<ID>" key always points to the most-recently-registered version.
//
// Returns:
//   - [kernel.ErrNilDefinition] if def is nil.
//   - [kernel.ErrEmptyDefinitionID] if def.ID is empty.
//   - [kernel.ErrDefinitionExists] (wrapped with the versioned key) if
//     "<ID>:<Version>" was already registered (first-registration-wins).
//
// For init-time wiring where a registration failure is a programming error use
// [MustRegisterDefinition].
//
// # Test isolation
//
// The global registry is process-wide. Tests that need an isolated registry must
// pass [WithDefinitions](kernel.NewMemDefinitionRegistry()) to [NewProcessDriver]
// and register directly into that isolated instance.
func RegisterDefinition(def *model.ProcessDefinition) error {
	return defaultDefinitionRegistry.Register(def)
}

// MustRegisterDefinition registers def into the process-global
// [DefaultDefinitionRegistry] and panics if registration fails. Intended for
// init-time wiring where a registration failure is a programming error (e.g. in
// package-level var blocks or TestMain).
//
// See [RegisterDefinition] for the error-returning variant and the full contract.
func MustRegisterDefinition(def *model.ProcessDefinition) {
	defaultDefinitionRegistry.MustRegister(def)
}
