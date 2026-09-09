package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kartaladev/wrkflw/definition/model"
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

// ErrInvalidDefinition is returned by MemDefinitionRegistry.Register when the
// supplied definition fails [model.Validate]. The returned error wraps both this
// sentinel and every rule the definition broke, so callers may match either
// ErrInvalidDefinition or a specific rule (e.g. [model.ErrNoStartEvent]) with
// errors.Is.
var ErrInvalidDefinition = errors.New("workflow-runtime: invalid definition")

// ── The shared authoring gate ─────────────────────────────────────────────

// ValidateDefinition is the authoring gate every definition passes through
// before it is admitted to a registry or published to the durable store. It
// returns:
//   - [ErrNilDefinition] if def is nil.
//   - [ErrEmptyDefinitionID] if def.ID is empty.
//   - [ErrInvalidDefinition], wrapped together with the qualifier and every
//     rule def broke, if def fails [model.Validate]. Callers may match either
//     this sentinel or a specific rule (e.g. [model.ErrNoStartEvent],
//     [model.ErrInvalidVersion]) with errors.Is.
//   - nil if def is admissible.
//
// [github.com/kartaladev/wrkflw/engine.Step] assumes the definition it is given
// has passed [model.Validate]. The builder and the YAML loader both end in that
// call, but a hand-constructed *model.ProcessDefinition literal has not — so
// every door into the system runs this gate, and they all run the SAME one:
// [MemDefinitionRegistry.Register] and the durable store's PublishDefinition
// both delegate here, so an in-memory registration and a durable publish accept
// and reject exactly the same definitions.
//
// A Version of 0 needs no separate check: it is the "latest" sentinel and
// [model.Validate] already refuses it with [model.ErrInvalidVersion].
//
// It is exported so a consumer can run the same check itself — before a publish,
// or in a test — instead of discovering the rejection at the write.
//
// [MapDefinitionRegistry] is the one registry that cannot enforce this — its
// variadic constructor returns no error — so a caller assembling one owns
// validation itself.
func ValidateDefinition(def *model.ProcessDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if def.ID == "" {
		return ErrEmptyDefinitionID
	}
	if err := model.Validate(def); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidDefinition, def.Qualifier(), err)
	}
	return nil
}

// ── MemDefinitionRegistry ─────────────────────────────────────────────────

// MemDefinitionRegistry is a concurrency-safe, register-after-construction
// in-memory DefinitionRegistry. It is the mutable sibling of the immutable
// MapDefinitionRegistry; use it when definitions are registered incrementally
// (e.g. the process-global default populated at application init).
//
// Register indexes each definition under two keys:
//   - def.Qualifier()     — exact versioned key; first-registration-wins.
//   - model.Latest(def.ID) — latest key; held by the HIGHEST registered version,
//     so a Latest Qualifier always resolves the newest version rather than
//     whichever one happened to be registered last.
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
//   - whatever [ValidateDefinition] returns — [ErrNilDefinition],
//     [ErrEmptyDefinitionID] or [ErrInvalidDefinition] — if def fails the
//     shared authoring gate.
//   - [ErrDefinitionExists] (wrapped with the pinned key) if the exact
//     Qualifier was already registered (first-registration-wins on the
//     versioned key).
//
// The gate runs before the lock and before any indexing, so a rejected
// definition claims neither key. It is the same [ValidateDefinition] the
// durable store's PublishDefinition runs, so both doors admit exactly the same
// definitions.
//
// On success the latest key is moved to def only when def.Version is greater
// than or equal to the version currently holding that key, so a Lookup with a
// Latest Qualifier resolves the highest registered version. Registering an
// older version after a newer one therefore does not demote "latest".
//
// This matches [MapDefinitionRegistry] and the durable
// DefinitionStore.Lookup, both of which already resolve Latest by highest
// version: "latest" means the same thing on every registry.
func (r *MemDefinitionRegistry) Register(def *model.ProcessDefinition) error {
	// The authoring gate: engine.Step's contract assumes model.Validate has run,
	// and a struct literal is the one route that would otherwise skip it.
	// Checked outside the lock — the gate reads def only.
	if err := ValidateDefinition(def); err != nil {
		return err
	}

	pinned := def.Qualifier()
	latest := model.Latest(def.ID)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.m[pinned]; exists {
		return fmt.Errorf("%w: %q", ErrDefinitionExists, pinned)
	}

	r.m[pinned] = def
	// Highest-version-wins for the latest key, mirroring MapDefinitionRegistry.
	// ">=" rather than ">" so re-registering the current latest version under a
	// fresh pointer still refreshes the key.
	if cur, ok := r.m[latest]; !ok || def.Version >= cur.Version {
		r.m[latest] = def
	}

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
//   - model.Latest(id)        — resolves the highest registered version.
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

// ListDefinitions implements [DefinitionLister]. It returns each registered
// definition exactly once, even though Register indexes every definition
// under two Qualifier keys (pinned and latest) — dedupe is by concrete
// *model.ProcessDefinition pointer, not by map key. ctx is ignored — the
// enumeration is entirely in-memory.
func (r *MemDefinitionRegistry) ListDefinitions(context.Context) []*model.ProcessDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return distinctDefinitions(r.m)
}

// Compile-time assertions: MemDefinitionRegistry satisfies DefinitionRegistry
// and DefinitionLister.
var _ DefinitionRegistry = (*MemDefinitionRegistry)(nil)
var _ DefinitionLister = (*MemDefinitionRegistry)(nil)
