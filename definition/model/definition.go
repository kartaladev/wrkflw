// Package model holds the in-memory process-definition types: the Node interface,
// gateways/events/activities' shared embeds, the ProcessDefinition template, the
// kind registry, validation, and serialization. The root definition package
// re-exports its public surface. It models a workflow as tasks, gateways, events,
// and sequence flows; it is pure data plus validation.
package model

import (
	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
)

// NodeKind discriminates the kind of a Node.
type NodeKind int

const (
	KindUnspecified NodeKind = iota
	KindStartEvent
	KindEndEvent
	KindTerminateEndEvent
	KindErrorEndEvent
	KindServiceTask
	KindUserTask
	KindReceiveTask
	KindSendTask
	KindBusinessRuleTask
	KindSubProcess
	KindCallActivity
	KindEventSubProcess
	KindIntermediateCatchEvent
	KindIntermediateThrowEvent
	KindBoundaryEvent
	KindExclusiveGateway
	KindParallelGateway
	KindInclusiveGateway
	KindEventBasedGateway
)

// ProcessDefinition is the reusable template a process instance executes.
type ProcessDefinition struct {
	ID      string
	Version int
	// Nodes is the ordered list of process nodes. Each element satisfies the
	// Node interface; use type assertions to access kind-specific fields.
	Nodes []Node
	Flows []flow.SequenceFlow
	// CancelActions are optional, ordered action.Action names invoked best-effort
	// by the engine when the instance is cancelled (see ADR-0028). Empty means no
	// cancel actions. Action-name existence is not validated here (the catalog is
	// not available at validate time); an unresolved name is logged at runtime.
	CancelActions []string
	// scoped is the optional definition-scoped action catalog. nil means none.
	// It is never serialized; resolution falls back to the global catalog on a
	// miss (see action.Resolve).
	scoped action.Catalog
	// scopedNames is the sorted slice of names registered in the scoped catalog.
	// nil when no scoped actions were registered. Set by Build().
	scopedNames []string
}

// Qualifier returns a Qualifier pinned to this definition's exact ID and Version.
func (d *ProcessDefinition) Qualifier() Qualifier { return Qualifier{ID: d.ID, Version: d.Version} }

// ScopedCatalog returns the definition-scoped action catalog, or nil when the
// definition registered no scoped actions.
func (d *ProcessDefinition) ScopedCatalog() action.Catalog { return d.scoped }

// ScopedActionNames returns the sorted names registered in the definition-scoped
// action catalog, or nil when none were registered. The returned slice is a
// defensive copy; callers may mutate it without affecting the definition.
func (d *ProcessDefinition) ScopedActionNames() []string {
	return append([]string(nil), d.scopedNames...)
}

// Node returns the node with the given id.
func (d *ProcessDefinition) Node(id string) (Node, bool) {
	for _, n := range d.Nodes {
		if n.ID() == id {
			return n, true
		}
	}
	return nil, false
}

// Outgoing returns the sequence flows leaving nodeID.
func (d *ProcessDefinition) Outgoing(nodeID string) []flow.SequenceFlow {
	var out []flow.SequenceFlow
	for _, f := range d.Flows {
		if f.Source == nodeID {
			out = append(out, f)
		}
	}
	return out
}

// Incoming returns the sequence flows entering nodeID.
func (d *ProcessDefinition) Incoming(nodeID string) []flow.SequenceFlow {
	var in []flow.SequenceFlow
	for _, f := range d.Flows {
		if f.Target == nodeID {
			in = append(in, f)
		}
	}
	return in
}

// StartNodes returns all start-event nodes.
func (d *ProcessDefinition) StartNodes() []Node {
	var starts []Node
	for _, n := range d.Nodes {
		if n.Kind() == KindStartEvent {
			starts = append(starts, n)
		}
	}
	return starts
}
