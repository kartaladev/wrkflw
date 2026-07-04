// Package build provides a terse fluent surface for authoring a
// ProcessDefinition — one AddX method per node kind — layered over
// definition.DefinitionBuilder. It lives in its own package because it imports
// the node-family leaf packages (event, gateway, activity), which the definition
// package itself must not import (that would create a cycle).
//
// The AddX methods mirror the leaf constructors; node-specific options are the
// leaf option types, so a chain typically imports the relevant leaf for its
// options:
//
//	def, err := build.New("order", 1).
//		AddStart("s").
//		AddServiceTask("charge", activity.WithActionName("charge-card")).
//		AddEnd("e").
//		Connect("s", "charge").Connect("charge", "e").
//		Build()
package build

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
)

// Builder is a fluent wrapper around definition.DefinitionBuilder exposing a
// per-kind AddX method for each node family. Construct one with New.
type Builder struct{ inner definition.DefinitionBuilder }

// New starts a fluent builder for a definition with the given id and version.
func New(id string, version int) *Builder {
	return &Builder{inner: definition.NewDefinition(id, version)}
}

// Add appends a pre-built node (programmatic / dynamic construction).
func (b *Builder) Add(n definition.Node) *Builder { b.inner.Add(n); return b }

// --- events ---

func (b *Builder) AddStart(id string, opts ...event.StartOption) *Builder {
	return b.Add(event.NewStart(id, opts...))
}
func (b *Builder) AddEnd(id string, name ...string) *Builder {
	return b.Add(event.NewEnd(id, name...))
}
func (b *Builder) AddTerminateEnd(id string, name ...string) *Builder {
	return b.Add(event.NewTerminateEnd(id, name...))
}
func (b *Builder) AddErrorEnd(id, errorCode string, name ...string) *Builder {
	return b.Add(event.NewErrorEnd(id, errorCode, name...))
}
func (b *Builder) AddCatch(id string, opts ...event.CatchOption) *Builder {
	return b.Add(event.NewCatch(id, opts...))
}
func (b *Builder) AddThrow(id string, opts ...event.ThrowOption) *Builder {
	return b.Add(event.NewThrow(id, opts...))
}
func (b *Builder) AddBoundary(id, attachedTo string, opts ...event.BoundaryOption) *Builder {
	return b.Add(event.NewBoundary(id, attachedTo, opts...))
}
func (b *Builder) AddEventSubProcess(id string, sub *definition.ProcessDefinition, opts ...event.EventSubProcessOption) *Builder {
	return b.Add(event.NewEventSubProcess(id, sub, opts...))
}

// --- gateways ---

func (b *Builder) AddExclusive(id string, name ...string) *Builder {
	return b.Add(gateway.NewExclusive(id, name...))
}
func (b *Builder) AddParallel(id string, name ...string) *Builder {
	return b.Add(gateway.NewParallel(id, name...))
}
func (b *Builder) AddInclusive(id string, name ...string) *Builder {
	return b.Add(gateway.NewInclusive(id, name...))
}
func (b *Builder) AddEventBased(id string, name ...string) *Builder {
	return b.Add(gateway.NewEventBased(id, name...))
}

// --- activities ---

func (b *Builder) AddServiceTask(id string, opts ...activity.ServiceTaskOption) *Builder {
	return b.Add(activity.NewServiceTask(id, opts...))
}
func (b *Builder) AddUserTask(id string, roles []string, opts ...activity.UserTaskOption) *Builder {
	return b.Add(activity.NewUserTask(id, roles, opts...))
}
func (b *Builder) AddReceiveTask(id, messageName string, opts ...activity.ReceiveTaskOption) *Builder {
	return b.Add(activity.NewReceiveTask(id, messageName, opts...))
}
func (b *Builder) AddSendTask(id, messageName string, opts ...activity.SendTaskOption) *Builder {
	return b.Add(activity.NewSendTask(id, messageName, opts...))
}
func (b *Builder) AddBusinessRuleTask(id string, opts ...activity.BusinessRuleOption) *Builder {
	return b.Add(activity.NewBusinessRuleTask(id, opts...))
}
func (b *Builder) AddSubProcess(id string, sub *definition.ProcessDefinition, opts ...activity.ActivityOption) *Builder {
	return b.Add(activity.NewSubProcess(id, sub, opts...))
}
func (b *Builder) AddCallActivity(id, defRef string, opts ...activity.ActivityOption) *Builder {
	return b.Add(activity.NewCallActivity(id, defRef, opts...))
}

// --- passthroughs to the underlying builder ---

// Connect adds a directed sequence flow.
func (b *Builder) Connect(fromID, toID string, opts ...definition.FlowOption) *Builder {
	b.inner.Connect(fromID, toID, opts...)
	return b
}

// RegisterAction registers a definition-scoped action.
func (b *Builder) RegisterAction(name string, a action.ServiceAction) *Builder {
	b.inner.RegisterAction(name, a)
	return b
}

// RegisterActionFunc registers a definition-scoped action from a plain func.
func (b *Builder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) *Builder {
	b.inner.RegisterActionFunc(name, fn)
	return b
}

// CancelActions sets best-effort actions run when an instance is cancelled.
func (b *Builder) CancelActions(names ...string) *Builder {
	b.inner.CancelActions(names...)
	return b
}

// Build assembles and validates the definition.
func (b *Builder) Build() (*definition.ProcessDefinition, error) { return b.inner.Build() }

// Loader returns a DefinitionLoader backed by the same core.
func (b *Builder) Loader() definition.DefinitionLoader { return b.inner.Loader() }
