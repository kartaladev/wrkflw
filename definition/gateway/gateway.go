// Package gateway holds the workflow gateway node kinds — exclusive, parallel,
// inclusive, and event-based — for the definition authoring layer. Import it to
// construct gateways (gateway.NewExclusive, …) and, via its init, to register
// their (de)serialization with the definition package.
//
// Gateways are configured with functional options: WithName sets the display
// name. Their routing behaviour emerges entirely from the number and conditions
// of their incoming/outgoing flows (see model.Validate and the runtime), not
// from any option here.
package gateway

import (
	"github.com/kartaladev/wrkflw/definition/internal/kindreg"
	"github.com/kartaladev/wrkflw/definition/model"
)

// ExclusiveGateway routes to exactly one outgoing flow (XOR split / merge).
type ExclusiveGateway struct{ model.Base }

// Kind returns model.KindExclusiveGateway.
func (ExclusiveGateway) Kind() model.NodeKind { return model.KindExclusiveGateway }

// Compile-time proof that ExclusiveGateway still satisfies model.Node. Node is a closed
// set — one concrete type per kind, recorded at registration and enforced at
// the Validate gate by model.ErrForeignNodeType — so a type that silently
// stopped implementing Node would take its kind out of the set with no build
// error at all: nothing here assigns these values to a model.Node anywhere the
// compiler would notice. These assignments are that missing signal.
var _ model.Node = ExclusiveGateway{}

// ParallelGateway splits into all outgoing flows (AND split) or waits for all (AND join).
type ParallelGateway struct{ model.Base }

// Kind returns model.KindParallelGateway.
func (ParallelGateway) Kind() model.NodeKind { return model.KindParallelGateway }

var _ model.Node = ParallelGateway{}

// InclusiveGateway routes to one or more outgoing flows (OR split / join).
type InclusiveGateway struct{ model.Base }

// Kind returns model.KindInclusiveGateway.
func (InclusiveGateway) Kind() model.NodeKind { return model.KindInclusiveGateway }

var _ model.Node = InclusiveGateway{}

// EventBasedGateway routes based on which event arrives first (race).
type EventBasedGateway struct{ model.Base }

// Kind returns model.KindEventBasedGateway.
func (EventBasedGateway) Kind() model.NodeKind { return model.KindEventBasedGateway }

var _ model.Node = EventBasedGateway{}

// Option configures a gateway at construction.
type Option func(*model.Base)

// WithName sets the display name.
func WithName(name string) Option { return func(b *model.Base) { b.SetName(name) } }

// newGateway builds the shared identity embed for a gateway, applying opts in
// order.
func newGateway(id string, opts ...Option) model.Base {
	b := model.NewBase(id, "")
	for _, o := range opts {
		o(&b)
	}
	return b
}

// NewExclusive constructs an ExclusiveGateway. Configure it with WithName.
func NewExclusive(id string, opts ...Option) model.Node {
	return ExclusiveGateway{newGateway(id, opts...)}
}

// NewParallel constructs a ParallelGateway. Configure it with WithName.
func NewParallel(id string, opts ...Option) model.Node {
	return ParallelGateway{newGateway(id, opts...)}
}

// NewInclusive constructs an InclusiveGateway. Configure it with WithName.
func NewInclusive(id string, opts ...Option) model.Node {
	return InclusiveGateway{newGateway(id, opts...)}
}

// NewEventBased constructs an EventBasedGateway. Configure it with WithName.
func NewEventBased(id string, opts ...Option) model.Node {
	return EventBasedGateway{newGateway(id, opts...)}
}

func init() {
	model.RegisterKind(kindreg.Grant(), model.KindExclusiveGateway, model.NodeSpec{
		Name:     "exclusiveGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return ExclusiveGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(kindreg.Grant(), model.KindParallelGateway, model.NodeSpec{
		Name:     "parallelGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return ParallelGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(kindreg.Grant(), model.KindInclusiveGateway, model.NodeSpec{
		Name:     "inclusiveGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return InclusiveGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(kindreg.Grant(), model.KindEventBasedGateway, model.NodeSpec{
		Name:     "eventBasedGateway",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return EventBasedGateway{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
}
