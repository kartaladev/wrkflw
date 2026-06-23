package model

// DefinitionBuilder is a fluent builder for ProcessDefinition. Construct one
// with NewDefinition, chain Add/Connect/CancelActions calls, then call Build to
// assemble and validate the definition.
type DefinitionBuilder struct {
	id            string
	version       int
	nodes         []Node
	flows         []SequenceFlow
	cancelActions []string
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

// Build assembles the ProcessDefinition and runs Validate. If validation fails
// the error is returned; the partial definition is not returned on error.
func (b *DefinitionBuilder) Build() (*ProcessDefinition, error) {
	def := ProcessDefinition{
		ID:            b.id,
		Version:       b.version,
		Nodes:         b.nodes,
		Flows:         b.flows,
		CancelActions: b.cancelActions,
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
