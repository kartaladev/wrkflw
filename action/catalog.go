package action

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Catalog resolves action names to implementations.
type Catalog interface {
	Resolve(name string) (ServiceAction, bool)
}

// MapCatalog is a map-backed, read-only Catalog. It is safe for concurrent
// reads; mutating the underlying map after construction is the caller's
// responsibility.
type MapCatalog map[string]ServiceAction

// NewMapCatalog wraps m in a MapCatalog. The caller must not modify m after
// calling NewMapCatalog.
func NewMapCatalog(m map[string]ServiceAction) MapCatalog { return MapCatalog(m) }

func (c MapCatalog) Resolve(name string) (ServiceAction, bool) {
	a, ok := c[name]
	return a, ok
}

// Compile-time assertion.
var _ Catalog = MapCatalog(nil)

// ── Sentinel errors ────────────────────────────────────────────────────────

// ErrEmptyActionName is returned by Registry when the supplied name is empty.
var ErrEmptyActionName = errors.New("workflow-action: empty action name")

// ErrNilAction is returned by Registry when a nil ServiceAction (or nil func)
// is registered.
var ErrNilAction = errors.New("workflow-action: nil action")

// ErrActionExists is returned by Registry when an action with the same name
// has already been registered.
var ErrActionExists = errors.New("workflow-action: action already registered")

// ── Registrar ──────────────────────────────────────────────────────────────

// Registrar is the write side of an action catalog: register actions by name
// after construction. The read side is Catalog.
type Registrar interface {
	// Register adds a to the catalog under name. It returns an error if name
	// is empty, a is nil, or name was already registered.
	Register(name string, a ServiceAction) error

	// RegisterFunc wraps fn as a Func and delegates to Register. A nil fn is
	// rejected with ErrNilAction.
	RegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) error
}

// ── Registry ───────────────────────────────────────────────────────────────

// Registry is a concurrency-safe Catalog that accepts action registrations
// after construction. It satisfies both Catalog (read) and Registrar (write)
// so it can be used as a global or scoped catalog slot and populated lazily
// during application startup.
//
// Never copy a Registry value — it contains a sync.RWMutex.
type Registry struct {
	noCopy noCopy //nolint:unused // detected by go vet

	mu      sync.RWMutex
	actions map[string]ServiceAction
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		actions: make(map[string]ServiceAction),
	}
}

// Register adds a to the catalog under name. It returns:
//   - ErrEmptyActionName (wrapped) if name is empty
//   - ErrNilAction (wrapped) if a is nil
//   - ErrActionExists (wrapped) if name was already registered
//
// The first successful registration is always retained on duplicate attempts.
func (r *Registry) Register(name string, a ServiceAction) error {
	if name == "" {
		return ErrEmptyActionName
	}
	if a == nil {
		return ErrNilAction
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.actions[name]; exists {
		return fmt.Errorf("%w: %q", ErrActionExists, name)
	}
	r.actions[name] = a
	return nil
}

// RegisterFunc wraps fn as a Func and delegates to Register. A nil fn is
// rejected with ErrNilAction before wrapping so that the error sentinel is
// consistent with Register's nil-action guard.
func (r *Registry) RegisterFunc(
	name string,
	fn func(context.Context, map[string]any) (map[string]any, error),
) error {
	if fn == nil {
		return ErrNilAction
	}
	return r.Register(name, Func(fn))
}

// MustRegister calls Register and panics if it returns an error. Use this for
// init-time wiring where a registration failure is a programming error.
func (r *Registry) MustRegister(name string, a ServiceAction) {
	if err := r.Register(name, a); err != nil {
		panic(fmt.Sprintf("action.Registry.MustRegister: %v", err))
	}
}

// MustRegisterFunc calls RegisterFunc and panics if it returns an error.
func (r *Registry) MustRegisterFunc(
	name string,
	fn func(context.Context, map[string]any) (map[string]any, error),
) {
	if err := r.RegisterFunc(name, fn); err != nil {
		panic(fmt.Sprintf("action.Registry.MustRegisterFunc: %v", err))
	}
}

// Resolve looks up name in the registry. It returns the action and true on a
// hit, or nil and false on a miss. Safe for concurrent use.
func (r *Registry) Resolve(name string) (ServiceAction, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[name]
	return a, ok
}

// Compile-time assertions.
var _ Catalog = (*Registry)(nil)
var _ Registrar = (*Registry)(nil)

// ── noCopy ─────────────────────────────────────────────────────────────────

// noCopy may be embedded in a struct to signal go vet that the struct must not
// be copied after first use. See https://pkg.go.dev/sync#noCopy
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
