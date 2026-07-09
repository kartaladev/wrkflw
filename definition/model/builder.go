package model

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// ErrActionInlineAndNameConflict is returned by Build when a node carries both
// an inline action (WithAction/WithActionFunc) and an action name (WithActionName).
var ErrActionInlineAndNameConflict = errors.New("workflow-definition: node has both an inline action and an action name")

// ErrDuplicateScopedAction is returned by Build when RegisterAction registered
// the same name twice.
var ErrDuplicateScopedAction = errors.New("workflow-definition: duplicate scoped action name")

// DefinitionLoader is a post-parse handle returned by ParseYAML (definition.NewLoader).
// The structural declaration (nodes, flows) is already loaded; callers register
// definition-scoped actions and call Build to validate and obtain the
// *ProcessDefinition. All methods return DefinitionLoader for chaining.
type DefinitionLoader interface {
	RegisterAction(name string, a action.Action) DefinitionLoader
	RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionLoader
	CancelActions(names ...string) DefinitionLoader
	Build() (*ProcessDefinition, error)
}

// DefinitionBuilder is a fluent builder for ProcessDefinition. Construct one
// with NewBuilder, chain Add/Connect/RegisterAction/CancelActions calls, then
// call Build to assemble and validate the definition. Loader returns a
// DefinitionLoader backed by the same core — useful when handing off to code
// that only registers actions without knowing the full builder API.
//
// Nodes are added with Add(node), constructing the node from the family packages
// (event.NewStart, gateway.NewExclusive, activity.NewServiceTask, …). The terse
// per-kind fluent adders (AddStart, AddServiceTask, …) live in the
// definition/build package, which can import the leaf packages without creating a
// cycle.
//
// All mutating methods return DefinitionBuilder so that both the
// actions-first idiom (RegisterAction before Add) and the structure-first
// idiom (Add/Connect then RegisterAction) compile identically.
type DefinitionBuilder interface {
	Add(n Node) DefinitionBuilder
	Connect(fromID, toID string, opts ...flow.Option) DefinitionBuilder
	RegisterAction(name string, a action.Action) DefinitionBuilder
	RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionBuilder
	CancelActions(names ...string) DefinitionBuilder
	Build() (*ProcessDefinition, error)
	Loader() DefinitionLoader
}

// Compile-time interface satisfaction checks.
var (
	_ DefinitionBuilder = (*definitionBuilder)(nil)
	_ DefinitionLoader  = (*definitionLoader)(nil)
)

// definitionCore holds the accumulated state shared between definitionBuilder
// and definitionLoader. Both wrappers embed a pointer to the same core so
// mutations from either view are always visible to the other.
type definitionCore struct {
	id            string
	version       int
	nodes         []Node
	flows         []flow.SequenceFlow
	cancelActions []string
	actions       map[string]action.Action // scoped catalog accumulator; nil until first register
	dupAction     string                   // first duplicate-registered name, "" if none
	validators    *validate.Registry       // resolves pending validation descriptors at build(); nil = none configured
}

// LoaderOption configures a DefinitionLoader before Build. Currently the only
// option is WithValidatorRegistry; the root definition and definition/build
// packages re-export both the type and the constructor.
type LoaderOption func(*definitionCore)

// WithValidatorRegistry configures the *validate.Registry Build uses to
// reconstruct any pending validation-strategy descriptors decoded from wire/YAML
// (see PendingValidation, ValidationDescriptor). When omitted, Build falls back
// to validate.DefaultRegistry (adapters self-register via init()); an
// unregistered kind then fails with validate.ErrUnknownKind.
func WithValidatorRegistry(reg *validate.Registry) LoaderOption {
	return func(c *definitionCore) { c.validators = reg }
}

func (c *definitionCore) register(name string, a action.Action) {
	if c.actions == nil {
		c.actions = make(map[string]action.Action)
	}
	if _, exists := c.actions[name]; exists && c.dupAction == "" {
		c.dupAction = name
	}
	c.actions[name] = a
}

func (c *definitionCore) connect(fromID, toID string, opts ...flow.Option) {
	c.flows = append(c.flows, flow.New(fromID, toID, opts...))
}

func (c *definitionCore) build() (*ProcessDefinition, error) {
	if c.dupAction != "" {
		return nil, fmt.Errorf("%w: %q", ErrDuplicateScopedAction, c.dupAction)
	}
	reg := c.validators
	if reg == nil {
		reg = validate.DefaultRegistry()
	}
	for i, n := range c.nodes {
		reconciled, err := reconcileNodeValidation(n, reg)
		if err != nil {
			return nil, err
		}
		c.nodes[i] = reconciled
	}
	for _, n := range c.nodes {
		if ActionOf(n) != "" && InlineActionOf(n) != nil {
			return nil, fmt.Errorf("%w: node %q", ErrActionInlineAndNameConflict, n.ID())
		}
	}
	def := ProcessDefinition{
		ID:            c.id,
		Version:       c.version,
		Nodes:         c.nodes,
		Flows:         c.flows,
		CancelActions: c.cancelActions,
	}
	if c.actions != nil {
		def.scoped = action.NewCatalog(c.actions)
		names := make([]string, 0, len(c.actions))
		for name := range c.actions {
			names = append(names, name)
		}
		sort.Strings(names)
		def.scopedNames = names
	}
	if err := Validate(&def); err != nil {
		return nil, err
	}
	return &def, nil
}

// definitionBuilder implements DefinitionBuilder over a shared *definitionCore.
type definitionBuilder struct{ *definitionCore }

// definitionLoader implements DefinitionLoader over a shared *definitionCore.
type definitionLoader struct{ *definitionCore }

// NewBuilder returns a new DefinitionBuilder for a process with the given id and
// version. It is the root-package entry point for authoring a definition in Go.
// Add nodes constructed from the family packages (event.NewStart,
// gateway.NewExclusive, activity.NewServiceTask, …) via Add, wire them with
// Connect, then call Build to obtain a validated *ProcessDefinition:
//
//	def, err := definition.NewBuilder("order", 1).
//		Add(event.NewStart("s")).
//		Add(activity.NewServiceTask("charge", activity.WithActionName("charge-card"))).
//		Add(event.NewEnd("e")).
//		Connect("s", "charge").Connect("charge", "e").
//		Build()
func NewBuilder(id string, version int) DefinitionBuilder {
	return &definitionBuilder{&definitionCore{id: id, version: version}}
}

// Add appends a node to the definition being built. Returns the builder for chaining.
func (b *definitionBuilder) Add(n Node) DefinitionBuilder {
	b.nodes = append(b.nodes, n)
	return b
}

// Connect adds a directed sequence flow from fromID to toID. The flow ID is
// auto-generated as "fromID->toID" unless flow.WithFlowID is supplied; use
// flow.WithCondition to set a routing expression, and flow.AsDefault to mark the
// flow as the exclusive-gateway default. Returns the builder for chaining.
func (b *definitionBuilder) Connect(fromID, toID string, opts ...flow.Option) DefinitionBuilder {
	b.connect(fromID, toID, opts...)
	return b
}

// CancelActions appends service-action names that the engine invokes best-effort
// when the process instance is cancelled. Returns the builder for chaining.
func (b *definitionBuilder) CancelActions(names ...string) DefinitionBuilder {
	b.cancelActions = append(b.cancelActions, names...)
	return b
}

// RegisterAction adds a definition-scoped action under name, visible only to
// this definition (global catalog is the fallback). Returns the builder.
func (b *definitionBuilder) RegisterAction(name string, a action.Action) DefinitionBuilder {
	b.register(name, a)
	return b
}

// RegisterActionFunc is RegisterAction sugar wrapping a plain function.
func (b *definitionBuilder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionBuilder {
	b.register(name, action.ActionFunc(fn))
	return b
}

// Build assembles the ProcessDefinition and runs Validate. If validation fails
// the error is returned; the partial definition is not returned on error.
func (b *definitionBuilder) Build() (*ProcessDefinition, error) { return b.build() }

// Loader returns a DefinitionLoader backed by the same core as this builder.
// Mutations via the loader (RegisterAction, CancelActions) are visible to the
// builder and vice-versa — they share a single *definitionCore.
func (b *definitionBuilder) Loader() DefinitionLoader { return &definitionLoader{b.definitionCore} }

// --- DefinitionLoader methods ---

// CancelActions appends cancel action names. Returns the loader for chaining.
func (l *definitionLoader) CancelActions(names ...string) DefinitionLoader {
	l.cancelActions = append(l.cancelActions, names...)
	return l
}

// RegisterAction adds a definition-scoped action under name. Returns the loader for chaining.
func (l *definitionLoader) RegisterAction(name string, a action.Action) DefinitionLoader {
	l.register(name, a)
	return l
}

// RegisterActionFunc is RegisterAction sugar wrapping a plain function.
func (l *definitionLoader) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionLoader {
	l.register(name, action.ActionFunc(fn))
	return l
}

// Build assembles the ProcessDefinition and runs Validate. Validation errors
// that were deferred from parse time (e.g. structural issues) surface here.
func (l *definitionLoader) Build() (*ProcessDefinition, error) { return l.build() }
