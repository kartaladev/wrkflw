// Package gateway holds the BPMN gateway node kinds — exclusive, parallel,
// inclusive, and event-based — for the definition authoring layer. Import it to
// construct gateways (gateway.NewExclusive, …) and, via its init, to register
// their (de)serialization with the definition package.
//
// Gateways carry no options beyond an optional name; their routing behaviour
// emerges entirely from the number and conditions of their incoming/outgoing
// flows (see model.Validate and the runtime).
package gateway

import "github.com/zakyalvan/krtlwrkflw/definition/model"

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

// optName returns the first name from a variadic or "".
func optName(name []string) string {
	if len(name) > 0 {
		return name[0]
	}
	return ""
}

// NewExclusive constructs an ExclusiveGateway. An optional name may be provided
// as a trailing variadic argument.
func NewExclusive(id string, name ...string) model.Node {
	return ExclusiveGateway{model.NewBase(id, optName(name))}
}

// NewParallel constructs a ParallelGateway. An optional name may be provided as a
// trailing variadic argument.
func NewParallel(id string, name ...string) model.Node {
	return ParallelGateway{model.NewBase(id, optName(name))}
}

// NewInclusive constructs an InclusiveGateway. An optional name may be provided
// as a trailing variadic argument.
func NewInclusive(id string, name ...string) model.Node {
	return InclusiveGateway{model.NewBase(id, optName(name))}
}

// NewEventBased constructs an EventBasedGateway. An optional name may be provided
// as a trailing variadic argument.
func NewEventBased(id string, name ...string) model.Node {
	return EventBasedGateway{model.NewBase(id, optName(name))}
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
