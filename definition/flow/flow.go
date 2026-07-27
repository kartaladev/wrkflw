// Package flow holds the sequence-flow (directed edge) type and its functional
// options for the definition authoring layer. The root definition package
// re-exports these as flow.SequenceFlow / flow.Option /
// flow.WithFlowID / flow.WithCondition / flow.AsDefault.
package flow

// SequenceFlow is a directed edge between two nodes. Its JSON/YAML encoding is
// snake_case, matching the single canonical definition wire (ADR-0144).
type SequenceFlow struct {
	ID        string `json:"id" yaml:"id"`
	Source    string `json:"source" yaml:"source"`
	Target    string `json:"target" yaml:"target"`
	Condition string `json:"condition,omitempty" yaml:"condition,omitempty"` // expr; empty means unconditional
	IsDefault bool   `json:"is_default,omitempty" yaml:"is_default,omitempty"`
}

// Option is a functional option for a SequenceFlow, applied by New.
type Option interface {
	apply(f *SequenceFlow)
}

type funcOpt struct{ fn func(*SequenceFlow) }

func (o funcOpt) apply(f *SequenceFlow) { o.fn(f) }

// New constructs a SequenceFlow from fromID to toID (default ID "fromID->toID")
// and applies the options.
func New(fromID, toID string, opts ...Option) SequenceFlow {
	f := SequenceFlow{ID: fromID + "->" + toID, Source: fromID, Target: toID}
	for _, o := range opts {
		o.apply(&f)
	}
	return f
}

// WithFlowID overrides the auto-generated flow ID.
func WithFlowID(id string) Option {
	return funcOpt{func(f *SequenceFlow) { f.ID = id }}
}

// WithCondition sets the routing expression on the flow. The expression is
// evaluated by expr-lang/expr against the token's variable map.
func WithCondition(expr string) Option {
	return funcOpt{func(f *SequenceFlow) { f.Condition = expr }}
}

// AsDefault marks the flow as the exclusive-gateway default (IsDefault = true).
func AsDefault() Option {
	return funcOpt{func(f *SequenceFlow) { f.IsDefault = true }}
}
