// Package action defines the service-action catalog: named, interface-based
// units of work referenced from definition nodes and resolved at execution time.
package action

import "context"

// ServiceAction performs a unit of work for a service task.
type ServiceAction interface {
	Do(ctx context.Context, in map[string]any) (out map[string]any, err error)
}

// Func adapts a plain function to ServiceAction.
type Func func(ctx context.Context, in map[string]any) (map[string]any, error)

func (f Func) Do(ctx context.Context, in map[string]any) (map[string]any, error) { return f(ctx, in) }

// Catalog resolves action names to implementations.
type Catalog interface {
	Resolve(name string) (ServiceAction, bool)
}

// MapCatalog is a map-backed Catalog.
type MapCatalog map[string]ServiceAction

func NewMapCatalog(m map[string]ServiceAction) MapCatalog { return MapCatalog(m) }

func (c MapCatalog) Resolve(name string) (ServiceAction, bool) {
	a, ok := c[name]
	return a, ok
}
