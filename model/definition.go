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

	// RetryPolicy is the optional per-node retry policy. When nil the runtime
	// applies its configured default. Non-nil values are validated by
	// [Validate]: MaxAttempts must be ≥ 0, InitialInterval and MaxInterval must
	// be ≥ 0, and BackoffCoef must be ≥ 1.0 whenever InitialInterval is
	// positive (a coefficient below 1.0 would collapse delays instead of
	// growing them).
	RetryPolicy *RetryPolicy

	// RecoveryFlow is the ID of the sequence flow to take when this node's
	// retries are exhausted — the Step-Functions "Catch" equivalent. The flow
	// must already exist in the process definition and its Source must be this
	// node. An empty string means no catch-flow: the error propagates to the
	// caller or ends the instance.
	RecoveryFlow string

	// CompensationAction is the name of the ServiceAction to invoke as compensation
	// when this activity is rolled back (Plan 8 compensation/rollback). Non-empty
	// only on activity nodes (KindServiceTask, KindSubProcess, etc.) that participate
	// in compensation. An empty value means the node is not compensable.
	CompensationAction string

	// Event correlation fields (signal/message catch/throw and boundary events).

	// SignalName is the signal reference for a signal catch/throw event or a
	// signal boundary event (KindIntermediateCatchEvent, KindIntermediateThrowEvent,
	// KindBoundaryEvent).
	SignalName string
	// MessageName is the message reference for a message catch event or a
	// message boundary event (KindIntermediateCatchEvent, KindBoundaryEvent).
	MessageName string
	// CorrelationKey is an expr expression evaluated at runtime to derive the
	// correlation key for message matching. Optional; empty means no correlation.
	// This field is a plain string in the model — evaluation happens in the engine.
	CorrelationKey string

	// ErrorCode is the BPMN error code for error end events (KindErrorEndEvent)
	// and boundary error events (KindBoundaryEvent with error trigger).
	//
	// For KindErrorEndEvent: the error code thrown when the node is reached.
	// Non-empty — an error end event with an empty ErrorCode throws an anonymous
	// error (effectively a catch-all match on any boundary error handler with an
	// empty ErrorCode).
	//
	// For KindBoundaryEvent: the error code this boundary catches.
	// Empty means "catch-all" — catches any error code thrown from the attached
	// activity or its nested scope.
	// Non-empty means "catch specific" — only catches errors whose code equals
	// this value.
	ErrorCode string

	// Boundary event fields (KindBoundaryEvent).

	// AttachedTo is the ID of the host activity node this boundary event is
	// attached to. Must reference an existing activity node (e.g. KindServiceTask,
	// KindUserTask, KindReceiveTask, KindSendTask, KindBusinessRuleTask,
	// KindSubProcess, KindCallActivity).
	AttachedTo string
	// NonInterrupting controls the boundary event interrupting behavior.
	// Zero-value (false) means interrupting: the host activity is cancelled when
	// the boundary event fires (the BPMN default). Set NonInterrupting: true for a
	// non-interrupting boundary event, where the host activity keeps running and an
	// additional token is spawned on the boundary's outgoing flow.
	// The engine reads this as: interrupting = !node.NonInterrupting.
	NonInterrupting bool

	// Sub-process fields (KindSubProcess and KindEventSubProcess).

	// Subprocess is the nested process definition for KindSubProcess and
	// KindEventSubProcess nodes. It must be non-nil for these node kinds; the
	// runtime executes it as a nested scope when the containing node is entered.
	//
	// For KindEventSubProcess, the trigger is encoded on the nested definition's
	// start event node via its existing event-correlation fields (SignalName,
	// MessageName, or TimerDuration on the nested KindStartEvent). This keeps the
	// model self-contained and avoids duplicate trigger fields on the parent node.
	// The engine inspects the nested start event's fields to set up event
	// subscriptions. This design is deferred to Task 4 for full engine handling;
	// the model merely requires the nested definition to be present and valid.
	Subprocess *ProcessDefinition

	// Call-activity fields (KindCallActivity).

	// DefRef is the name of a top-level process definition resolved by the
	// runtime's definition registry at execution time. Must be non-empty for
	// KindCallActivity nodes. The registry maps names to *ProcessDefinition
	// templates; the engine instantiates the referenced definition as a child
	// process instance when the call-activity node is entered.
	DefRef string
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
