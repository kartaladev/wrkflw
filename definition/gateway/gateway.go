// Package gateway holds the BPMN gateway node kinds — exclusive, parallel,
// inclusive, and event-based — for the definition authoring layer. Import it to
// construct gateways (gateway.NewExclusive, …) and, via its init, to register
// their (de)serialization with the definition package.
//
// Gateways carry no options beyond an optional name; their routing behaviour
// emerges entirely from the number and conditions of their incoming/outgoing
// flows (see definition.Validate and the runtime).
package gateway

import "github.com/zakyalvan/krtlwrkflw/definition"

// ExclusiveGateway routes to exactly one outgoing flow (XOR split / merge).
type ExclusiveGateway struct{ definition.Base }

// Kind returns definition.KindExclusiveGateway.
func (ExclusiveGateway) Kind() definition.NodeKind { return definition.KindExclusiveGateway }

// ParallelGateway splits into all outgoing flows (AND split) or waits for all (AND join).
type ParallelGateway struct{ definition.Base }

// Kind returns definition.KindParallelGateway.
func (ParallelGateway) Kind() definition.NodeKind { return definition.KindParallelGateway }

// InclusiveGateway routes to one or more outgoing flows (OR split / join).
type InclusiveGateway struct{ definition.Base }

// Kind returns definition.KindInclusiveGateway.
func (InclusiveGateway) Kind() definition.NodeKind { return definition.KindInclusiveGateway }

// EventBasedGateway routes based on which event arrives first (race).
type EventBasedGateway struct{ definition.Base }

// Kind returns definition.KindEventBasedGateway.
func (EventBasedGateway) Kind() definition.NodeKind { return definition.KindEventBasedGateway }

// optName returns the first name from a variadic or "".
func optName(name []string) string {
	if len(name) > 0 {
		return name[0]
	}
	return ""
}

// NewExclusive constructs an ExclusiveGateway. An optional name may be provided
// as a trailing variadic argument.
func NewExclusive(id string, name ...string) definition.Node {
	return ExclusiveGateway{definition.NewBase(id, optName(name))}
}

// NewParallel constructs a ParallelGateway. An optional name may be provided as a
// trailing variadic argument.
func NewParallel(id string, name ...string) definition.Node {
	return ParallelGateway{definition.NewBase(id, optName(name))}
}

// NewInclusive constructs an InclusiveGateway. An optional name may be provided
// as a trailing variadic argument.
func NewInclusive(id string, name ...string) definition.Node {
	return InclusiveGateway{definition.NewBase(id, optName(name))}
}

// NewEventBased constructs an EventBasedGateway. An optional name may be provided
// as a trailing variadic argument.
func NewEventBased(id string, name ...string) definition.Node {
	return EventBasedGateway{definition.NewBase(id, optName(name))}
}

func init() {
	definition.RegisterKind(definition.KindExclusiveGateway, definition.NodeSpec{
		Name:     "exclusiveGateway",
		FromWire: func(b definition.Base, _ definition.NodeWire) definition.Node { return ExclusiveGateway{b} },
		ToWire:   func(definition.Node, *definition.NodeWire) {},
	})
	definition.RegisterKind(definition.KindParallelGateway, definition.NodeSpec{
		Name:     "parallelGateway",
		FromWire: func(b definition.Base, _ definition.NodeWire) definition.Node { return ParallelGateway{b} },
		ToWire:   func(definition.Node, *definition.NodeWire) {},
	})
	definition.RegisterKind(definition.KindInclusiveGateway, definition.NodeSpec{
		Name:     "inclusiveGateway",
		FromWire: func(b definition.Base, _ definition.NodeWire) definition.Node { return InclusiveGateway{b} },
		ToWire:   func(definition.Node, *definition.NodeWire) {},
	})
	definition.RegisterKind(definition.KindEventBasedGateway, definition.NodeSpec{
		Name:     "eventBasedGateway",
		FromWire: func(b definition.Base, _ definition.NodeWire) definition.Node { return EventBasedGateway{b} },
		ToWire:   func(definition.Node, *definition.NodeWire) {},
	})
}
