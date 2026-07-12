package action

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Catalog resolves action names to implementations.
type Catalog interface {
	Resolve(name string) (Action, bool)
}

// MapCatalog is a map-backed, read-only Catalog. It is safe for concurrent
// reads; mutating the underlying map after construction is the caller's
// responsibility.
type MapCatalog map[string]Action

// NewCatalog wraps m in a read-only Catalog. The caller must not modify m after
// calling NewCatalog.
//
// With no opts it returns a bare [MapCatalog]: stored actions resolve exactly as
// registered. When one or more
// resiliency options are supplied they become a DEFAULT policy applied lazily at
// Resolve — only to a resolved action that declares no policy of its own (see
// [ResolvePolicy]). A per-action [Wrap] (or a type implementing a capability
// interface directly) therefore always wins over the catalog default; bare stored
// actions are decorated on the way out and remain bare in the map.
//
// A default retry policy applies in the ACTION tier (action > node > runtime-default),
// so it overrides a node-level retry policy for any action that declares none of its
// own — see the precedence note on [RetrySpecs].
func NewCatalog(m map[string]Action, opts ...Option) Catalog {
	base := MapCatalog(m)
	if len(opts) == 0 {
		return base
	}
	return &defaultingCatalog{inner: base, defaults: opts}
}

func (c MapCatalog) Resolve(name string) (Action, bool) {
	a, ok := c[name]
	return a, ok
}

// defaultingCatalog decorates an inner Catalog with a default resiliency policy
// applied lazily at Resolve to any action that declares none of its own.
type defaultingCatalog struct {
	inner    Catalog
	defaults []Option
}

func (c *defaultingCatalog) Resolve(name string) (Action, bool) {
	a, ok := c.inner.Resolve(name)
	if !ok {
		return nil, false
	}
	return applyDefaults(a, c.defaults), true
}

// applyDefaults wraps a with defaults only when a carries no policy of its own.
func applyDefaults(a Action, defaults []Option) Action {
	if len(defaults) == 0 || !ResolvePolicy(a).empty() {
		return a
	}
	return Wrap(a, defaults...)
}

// Compile-time assertion.
var _ Catalog = MapCatalog(nil)

// ── Sentinel errors ────────────────────────────────────────────────────────

// ErrEmptyActionName is returned by Registry when the supplied name is empty.
var ErrEmptyActionName = errors.New("workflow-action: empty action name")

// ErrNilAction is returned by Registry when a nil Action (or nil func)
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
	Register(name string, a Action) error

	// RegisterFunc wraps fn as a ActionFunc and delegates to Register. A nil fn is
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
	actions map[string]Action

	// defaults is the optional default resiliency policy applied lazily at
	// Resolve to any registered action that declares none of its own. Empty when
	// NewRegistry was called with no options.
	defaults []Option
}

// NewRegistry returns an empty, ready-to-use Registry. When one or more resiliency
// options are supplied they become a DEFAULT policy applied lazily at [Registry.Resolve]
// — only to a resolved action that declares no policy of its own (a per-action
// [Wrap], or a type implementing a capability interface directly, wins). NewRegistry()
// with no options yields a plain registry with no default policy. A default retry policy applies in the
// ACTION tier and thus overrides a node-level retry policy for actions that declare
// none of their own — see the precedence note on [RetrySpecs].
func NewRegistry(opts ...Option) *Registry {
	return &Registry{
		actions:  make(map[string]Action),
		defaults: opts,
	}
}

// Register adds a to the catalog under name. It returns:
//   - ErrEmptyActionName if name is empty
//   - ErrNilAction if a is nil
//   - ErrActionExists (wrapped, with the name) if name was already registered
//
// The first successful registration is always retained on duplicate attempts.
func (r *Registry) Register(name string, a Action) error {
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

// RegisterFunc wraps fn as a ActionFunc and delegates to Register. A nil fn is
// rejected with ErrNilAction before wrapping so that the error sentinel is
// consistent with Register's nil-action guard.
func (r *Registry) RegisterFunc(
	name string,
	fn func(context.Context, map[string]any) (map[string]any, error),
) error {
	if fn == nil {
		return ErrNilAction
	}
	return r.Register(name, ActionFunc(fn))
}

// MustRegister calls Register and panics if it returns an error. Use this for
// init-time wiring where a registration failure is a programming error.
func (r *Registry) MustRegister(name string, a Action) {
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
func (r *Registry) Resolve(name string) (Action, bool) {
	r.mu.RLock()
	a, ok := r.actions[name]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return applyDefaults(a, r.defaults), true
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
