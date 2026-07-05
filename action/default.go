package action

import "context"

// defaultCatalog is the process-global action catalog used by a ProcessDriver
// constructed without runtime.WithActionCatalog. It is concurrency-safe (it is a
// *Registry, guarded by an RWMutex). Populate it from application init code via
// Register / MustRegister; a zero-config driver resolves service-action names
// against it.
//
// Because it is process-global and (like any Registry) rejects duplicate names,
// tests that need isolation should construct their own registry with NewRegistry
// and pass it via runtime.WithActionCatalog rather than registering here.
var defaultCatalog = NewRegistry()

// DefaultCatalog returns the process-global action registry.
func DefaultCatalog() *Registry { return defaultCatalog }

// Register adds a to the process-global catalog under name. See Registry.Register
// for the returned errors (ErrEmptyActionName, ErrNilAction, ErrActionExists).
func Register(name string, a Action) error { return defaultCatalog.Register(name, a) }

// RegisterFunc wraps fn as an ActionFunc and registers it in the process-global
// catalog. A nil fn is rejected with ErrNilAction.
func RegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) error {
	return defaultCatalog.RegisterFunc(name, fn)
}

// MustRegister calls Register and panics on error (init-time wiring).
func MustRegister(name string, a Action) { defaultCatalog.MustRegister(name, a) }

// MustRegisterFunc calls RegisterFunc and panics on error (init-time wiring).
func MustRegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) {
	defaultCatalog.MustRegisterFunc(name, fn)
}
