// Package model defines the in-memory process-definition types: nodes,
// gateways, sequence flows, and the ProcessDefinition template. The concepts
// are inspired by BPMN, but this is NOT a BPMN-compatible implementation and
// does not aim to load or round-trip arbitrary BPMN2 documents. It is pure data
// plus validation; it imports only the standard library and the in-repo
// [action] package (a pure leaf).
package model

import "github.com/zakyalvan/krtlwrkflw/action"

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

// SequenceFlow is a directed edge between two nodes.
type SequenceFlow struct {
	ID        string
	Source    string
	Target    string
	Condition string // expr; empty means unconditional
	IsDefault bool
}

// ProcessDefinition is the reusable template a process instance executes.
type ProcessDefinition struct {
	ID      string
	Version int
	// Nodes is the ordered list of process nodes. Each element satisfies the
	// Node interface; use type assertions to access kind-specific fields.
	Nodes []Node
	Flows []SequenceFlow
	// CancelActions are optional, ordered ServiceAction names invoked best-effort
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
func (d *ProcessDefinition) Outgoing(nodeID string) []SequenceFlow {
	var out []SequenceFlow
	for _, f := range d.Flows {
		if f.Source == nodeID {
			out = append(out, f)
		}
	}
	return out
}

// Incoming returns the sequence flows entering nodeID.
func (d *ProcessDefinition) Incoming(nodeID string) []SequenceFlow {
	var in []SequenceFlow
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
