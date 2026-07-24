// Package gateway holds the workflow gateway node kinds — exclusive, parallel,
// inclusive, and event-based — for the definition authoring layer. Import it to
// construct gateways (gateway.NewExclusive, …) and, via its init, to register
// their (de)serialization with the definition package.
//
// Gateways are configured with functional options: WithName sets the
// semantic/reference name, WithLabel sets the human display label (falling
// back to Name when unset). Their routing behaviour emerges entirely from the
// number and conditions of their incoming/outgoing flows (see model.Validate
// and the runtime), not from any option here.
package gateway

import "github.com/kartaladev/wrkflw/definition/model"

// ExclusiveGateway routes to exactly one outgoing flow (XOR split / merge).
type ExclusiveGateway struct{ model.Base }

// Kind returns model.KindExclusiveGateway.
func (ExclusiveGateway) Kind() model.NodeKind { return model.KindExclusiveGateway }

// ParallelGateway splits into all outgoing flows (AND split) or waits for all (AND join).
type ParallelGateway struct{ model.Base }

// Kind returns model.KindParallelGateway.
func (ParallelGateway) Kind() model.NodeKind { return model.KindParallelGateway }

// InclusiveGateway routes to one or more outgoing flows (OR split / join).
type InclusiveGateway struct{ model.Base }

// Kind returns model.KindInclusiveGateway.
func (InclusiveGateway) Kind() model.NodeKind { return model.KindInclusiveGateway }

// EventBasedGateway routes based on which event arrives first (race).
type EventBasedGateway struct{ model.Base }

// Kind returns model.KindEventBasedGateway.
func (EventBasedGateway) Kind() model.NodeKind { return model.KindEventBasedGateway }

// Option configures a gateway at construction.
type Option func(*model.Base)

// WithName sets the semantic/reference name on a gateway.
func WithName(name string) Option { return func(b *model.Base) { b.SetName(name) } }

// WithLabel sets the human display label on a gateway.
func WithLabel(label string) Option { return func(b *model.Base) { b.SetLabel(label) } }

// newGateway builds the shared identity embed for a gateway, applying opts in
// order.
func newGateway(id string, opts ...Option) model.Base {
	b := model.NewBase(id, "")
	for _, o := range opts {
		o(&b)
	}
	return b
}

// NewExclusive constructs an ExclusiveGateway. Configure it with WithName
// and/or WithLabel.
func NewExclusive(id string, opts ...Option) model.Node {
	return ExclusiveGateway{newGateway(id, opts...)}
}

// NewParallel constructs a ParallelGateway. Configure it with WithName and/or
// WithLabel.
func NewParallel(id string, opts ...Option) model.Node {
	return ParallelGateway{newGateway(id, opts...)}
}

// NewInclusive constructs an InclusiveGateway. Configure it with WithName
// and/or WithLabel.
func NewInclusive(id string, opts ...Option) model.Node {
	return InclusiveGateway{newGateway(id, opts...)}
}

// NewEventBased constructs an EventBasedGateway. Configure it with WithName
// and/or WithLabel.
func NewEventBased(id string, opts ...Option) model.Node {
	return EventBasedGateway{newGateway(id, opts...)}
}

func init() {
	model.RegisterKind(model.KindExclusiveGateway, model.NodeSpec{
		Name:     "exclusiveGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return ExclusiveGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(model.KindParallelGateway, model.NodeSpec{
		Name:     "parallelGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return ParallelGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(model.KindInclusiveGateway, model.NodeSpec{
		Name:     "inclusiveGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return InclusiveGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(model.KindEventBasedGateway, model.NodeSpec{
		Name:     "eventBasedGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return EventBasedGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
}
