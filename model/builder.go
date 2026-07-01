package model

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/zakyalvan/krtlwrkflw/action"
)

// ErrActionInlineAndNameConflict is returned by Build when a node carries both
// an inline action (WithAction/WithActionFunc) and an action name (WithActionName).
var ErrActionInlineAndNameConflict = errors.New("workflow-model: node has both an inline action and an action name")

// ErrDuplicateScopedAction is returned by Build when RegisterAction registered
// the same name twice.
var ErrDuplicateScopedAction = errors.New("workflow-model: duplicate scoped action name")

// DefinitionBuilder is a fluent builder for ProcessDefinition. Construct one
// with NewDefinition, chain Add/AddX/Connect/RegisterAction/CancelActions calls,
// then call Build to assemble and validate the definition.
type DefinitionBuilder struct {
	id            string
	version       int
	nodes         []Node
	flows         []SequenceFlow
	cancelActions []string
	actions       map[string]action.ServiceAction // scoped catalog accumulator; nil until first register
	dupAction     string                          // first duplicate-registered name, "" if none
}

// NewDefinition returns a new DefinitionBuilder for a process with the given
// id and version. Use Add, Connect, and CancelActions to populate it, then
// call Build to obtain a validated *ProcessDefinition.
func NewDefinition(id string, version int) *DefinitionBuilder {
	return &DefinitionBuilder{id: id, version: version}
}

// Add appends a node to the definition being built. Returns the builder for chaining.
func (b *DefinitionBuilder) Add(n Node) *DefinitionBuilder {
	b.nodes = append(b.nodes, n)
	return b
}

// Connect adds a directed sequence flow from fromID to toID.  The flow ID is
// auto-generated as "fromID->toID" unless WithFlowID is supplied; use
// WithCondition to set a routing expression, and AsDefault to mark the flow
// as the exclusive-gateway default. Returns the builder for chaining.
func (b *DefinitionBuilder) Connect(fromID, toID string, opts ...FlowOption) *DefinitionBuilder {
	f := SequenceFlow{
		ID:     fromID + "->" + toID,
		Source: fromID,
		Target: toID,
	}
	for _, o := range opts {
		o.applyFlow(&f)
	}
	b.flows = append(b.flows, f)
	return b
}

// CancelActions appends service-action names that the engine invokes best-effort
// when the process instance is cancelled. Returns the builder for chaining.
func (b *DefinitionBuilder) CancelActions(names ...string) *DefinitionBuilder {
	b.cancelActions = append(b.cancelActions, names...)
	return b
}

// RegisterAction adds a definition-scoped action under name, visible only to
// this definition (global catalog is the fallback). Returns the builder.
func (b *DefinitionBuilder) RegisterAction(name string, a action.ServiceAction) *DefinitionBuilder {
	if b.actions == nil {
		b.actions = make(map[string]action.ServiceAction)
	}
	if _, exists := b.actions[name]; exists && b.dupAction == "" {
		b.dupAction = name
	}
	b.actions[name] = a
	return b
}

// RegisterActionFunc is RegisterAction sugar wrapping a plain function.
func (b *DefinitionBuilder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) *DefinitionBuilder {
	return b.RegisterAction(name, action.Func(fn))
}

// Build assembles the ProcessDefinition and runs Validate. If validation fails
// the error is returned; the partial definition is not returned on error.
func (b *DefinitionBuilder) Build() (*ProcessDefinition, error) {
	if b.dupAction != "" {
		return nil, fmt.Errorf("%w: %q", ErrDuplicateScopedAction, b.dupAction)
	}
	for _, n := range b.nodes {
		if ActionOf(n) != "" && InlineActionOf(n) != nil {
			return nil, fmt.Errorf("%w: node %q", ErrActionInlineAndNameConflict, n.ID())
		}
	}
	def := ProcessDefinition{
		ID:            b.id,
		Version:       b.version,
		Nodes:         b.nodes,
		Flows:         b.flows,
		CancelActions: b.cancelActions,
	}
	if b.actions != nil {
		def.scoped = action.NewMapCatalog(b.actions)
		names := make([]string, 0, len(b.actions))
		for name := range b.actions {
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

// FlowOption is a functional option for Connect, configuring a SequenceFlow.
type FlowOption interface {
	applyFlow(f *SequenceFlow)
}

// flowFuncOpt is a FlowOption backed by a plain function.
type flowFuncOpt struct{ fn func(*SequenceFlow) }

func (o flowFuncOpt) applyFlow(f *SequenceFlow) { o.fn(f) }

// WithFlowID overrides the auto-generated flow ID.
func WithFlowID(id string) FlowOption {
	return flowFuncOpt{func(f *SequenceFlow) { f.ID = id }}
}

// WithCondition sets the routing expression on the flow. The expression is
// evaluated by expr-lang/expr against the token's variable map.
func WithCondition(expr string) FlowOption {
	return flowFuncOpt{func(f *SequenceFlow) { f.Condition = expr }}
}

// AsDefault marks the flow as the exclusive-gateway default (IsDefault = true).
func AsDefault() FlowOption {
	return flowFuncOpt{func(f *SequenceFlow) { f.IsDefault = true }}
}
