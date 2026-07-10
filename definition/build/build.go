// Package build provides the fluent definition builder — one AddX method per
// node kind — layered over the core model builder. It imports the node-family
// leaf packages (event, gateway, activity), which the model package must not
// import (that would create a cycle). The root definition package re-exports its
// entry point as definition.NewBuilder.
//
//	def, err := definition.NewBuilder("order", 1).
//		AddStartEvent("s").
//		AddServiceTask("charge", activity.WithTaskAction("charge-card")).
//		AddEndEvent("e").
//		Connect("s", "charge").Connect("charge", "e").
//		Build()
package build

import (
	"context"
	"io"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// This package is the single home for both authoring entry points, which the
// root definition package re-exports symmetrically as definition.NewBuilder and
// definition.NewLoader:
//   - NewBuilder — Go authoring, the fluent per-kind builder.
//   - NewLoader  — YAML authoring, a loader over the parsed definition.

// Builder is a fluent wrapper around the core model builder exposing a per-kind
// AddX method for each node family. Construct one with NewBuilder (or, from the
// root package, definition.NewBuilder).
type Builder struct{ inner model.DefinitionBuilder }

// NewBuilder starts a fluent builder for a definition with the given id and version.
func NewBuilder(id string, version int) *Builder {
	return &Builder{inner: model.NewBuilder(id, version)}
}

// LoaderOption configures a DefinitionLoader before Build; see
// WithValidatorRegistry. The root definition package re-exports it as
// definition.LoaderOption.
type LoaderOption = model.LoaderOption

// WithValidatorRegistry configures the *validate.Registry NewLoader uses to
// reconstruct validation-strategy descriptors decoded from the wire/YAML
// `validation` block (see validate.Registry, validate.DescribableStrategy).
// When omitted, Build falls back to validate.DefaultRegistry (adapters
// self-register via init()); an unregistered kind then fails with
// validate.ErrUnknownKind.
func WithValidatorRegistry(reg *validate.Registry) LoaderOption {
	return model.WithValidatorRegistry(reg)
}

// NewLoader reads a YAML process-definition from r and returns a
// model.DefinitionLoader whose structure is already declared. It is the YAML
// counterpart to NewBuilder; register definition-scoped actions via
// RegisterAction/RegisterActionFunc, apply any LoaderOption, then call Build.
func NewLoader(r io.Reader, opts ...LoaderOption) (model.DefinitionLoader, error) {
	return model.ParseYAML(r, opts...)
}

// Add appends a pre-built node (programmatic / dynamic construction).
func (b *Builder) Add(n model.Node) *Builder { b.inner.Add(n); return b }

// --- events ---

func (b *Builder) AddStartEvent(id string, opts ...event.StartOption) *Builder {
	return b.Add(event.NewStart(id, opts...))
}

// AddEndEvent adds an EndEvent. Use event.WithName and event.WithForceTermination.
func (b *Builder) AddEndEvent(id string, opts ...event.EndOption) *Builder {
	return b.Add(event.NewEnd(id, opts...))
}
func (b *Builder) AddErrorEndEvent(id, errorCode string, name ...string) *Builder {
	return b.Add(event.NewErrorEnd(id, errorCode, name...))
}
func (b *Builder) AddIntermediateCatchEvent(id string, opts ...event.CatchOption) *Builder {
	return b.Add(event.NewIntermediateCatch(id, opts...))
}
func (b *Builder) AddIntermediateThrowEvent(id string, opts ...event.ThrowOption) *Builder {
	return b.Add(event.NewIntermediateThrow(id, opts...))
}
func (b *Builder) AddBoundaryEvent(id, attachedTo string, opts ...event.BoundaryOption) *Builder {
	return b.Add(event.NewBoundary(id, attachedTo, opts...))
}
func (b *Builder) AddEventSubProcess(id string, sub *model.ProcessDefinition, opts ...event.EventSubProcessOption) *Builder {
	return b.Add(event.NewEventSubProcess(id, sub, opts...))
}

// --- gateways ---

func (b *Builder) AddExclusiveGateway(id string, name ...string) *Builder {
	return b.Add(gateway.NewExclusive(id, name...))
}
func (b *Builder) AddParallelGateway(id string, name ...string) *Builder {
	return b.Add(gateway.NewParallel(id, name...))
}
func (b *Builder) AddInclusiveGateway(id string, name ...string) *Builder {
	return b.Add(gateway.NewInclusive(id, name...))
}
func (b *Builder) AddEventBasedGateway(id string, name ...string) *Builder {
	return b.Add(gateway.NewEventBased(id, name...))
}

// --- activities ---

func (b *Builder) AddServiceTask(id string, opts ...activity.ServiceTaskOption) *Builder {
	return b.Add(activity.NewServiceTask(id, opts...))
}
func (b *Builder) AddUserTask(id string, opts ...activity.UserTaskOption) *Builder {
	return b.Add(activity.NewUserTask(id, opts...))
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
func (b *Builder) AddSubProcess(id string, sub *model.ProcessDefinition, opts ...activity.ActivityOption) *Builder {
	return b.Add(activity.NewSubProcess(id, sub, opts...))
}
func (b *Builder) AddCallActivity(id string, ref model.Qualifier, opts ...activity.ActivityOption) *Builder {
	return b.Add(activity.NewCallActivity(id, ref, opts...))
}

// --- passthroughs to the underlying builder ---

// Connect adds a directed sequence flow.
func (b *Builder) Connect(fromID, toID string, opts ...flow.Option) *Builder {
	b.inner.Connect(fromID, toID, opts...)
	return b
}

// RegisterAction registers a definition-scoped action.
func (b *Builder) RegisterAction(name string, a action.Action) *Builder {
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
func (b *Builder) Build() (*model.ProcessDefinition, error) { return b.inner.Build() }

// Loader returns a DefinitionLoader backed by the same core.
func (b *Builder) Loader() model.DefinitionLoader { return b.inner.Loader() }
