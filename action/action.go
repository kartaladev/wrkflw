// Package action defines the action catalog: named, interface-based units of
// work referenced from definition nodes and resolved at execution time. The same
// Action interface backs service tasks, business-rule tasks, compensation, cancel
// handlers, deadline and reminder actions, and cancel-instance actions.
package action

import "context"

// Action is a named unit of work the engine invokes by name (or inline). It is
// not specific to service tasks — the same interface is used for compensation,
// cancel handlers, deadline/reminder actions, business rules, and more.
type Action interface {
	Do(ctx context.Context, in map[string]any) (out map[string]any, err error)
}

// ActionFunc adapts a plain function to Action.
type ActionFunc func(ctx context.Context, in map[string]any) (map[string]any, error)

func (f ActionFunc) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	return f(ctx, in)
}
