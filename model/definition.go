// Package model defines the in-memory BPMN-flavored process-definition types.
// It is pure data plus validation; it imports only the standard library.
package model

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

// Node is a single point in a process: an event, activity, or gateway.
// Kind-specific fields beyond those below are added in later plans.
type Node struct {
	ID     string
	Kind   NodeKind
	Name   string
	Action string // service-action name, for KindServiceTask
	// User-task eligibility (KindUserTask). The engine maps these to authz.AuthzSpec.
	CandidateRoles  []string
	EligibilityExpr string // optional attribute predicate (expr)

	// Timer fields (KindIntermediateCatchEvent timer nodes).
	// TimerDuration is an ISO-8601 duration string (e.g. "PT1H") describing how
	// long the engine waits before the timer fires.
	TimerDuration string

	// SLA fields (any wait node that carries a deadline).
	// SLADuration is an ISO-8601 duration string for the SLA deadline.
	SLADuration string
	// SLAFlow is the ID of the sequence flow to take when the SLA is breached.
	SLAFlow string
	// SLAAction is the name of the ServiceAction to invoke when the SLA is breached.
	SLAAction string

	// Reminder fields (periodic in-wait actions during a wait period).
	// ReminderEvery is an ISO-8601 duration string for the reminder interval.
	ReminderEvery string
	// ReminderAction is the name of the ServiceAction to invoke on each reminder.
	ReminderAction string
}

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
	Nodes   []Node
	Flows   []SequenceFlow
}

// Node returns the node with the given id.
func (d *ProcessDefinition) Node(id string) (Node, bool) {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
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
		if n.Kind == KindStartEvent {
			starts = append(starts, n)
		}
	}
	return starts
}
