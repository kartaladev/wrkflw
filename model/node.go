package model

// Node is a single point in a process: an event, activity, or gateway.
// Concrete types (one per NodeKind) carry only the fields meaningful to their
// kind. Construct nodes with the New* constructors; build definitions with
// DefinitionBuilder or the YAML loader.
type Node interface {
	Kind() NodeKind
	ID() string
	Name() string
}

// baseNode supplies the identity common to every node kind.
type baseNode struct {
	id   string
	name string
}

func (b baseNode) ID() string   { return b.id }
func (b baseNode) Name() string { return b.name }

// --- events ---

// StartEvent is the BPMN start event node: the entry point of a process.
// When used as the trigger start of an EventSubProcess, it may carry
// correlation fields (SignalName, MessageName, CorrelationKey, TimerDuration)
// to describe the event that activates the event sub-process.
type StartEvent struct {
	baseNode
	// SignalName is set when this start event is a signal trigger for an EventSubProcess.
	SignalName string
	// MessageName is set when this start event is a message trigger for an EventSubProcess.
	MessageName string
	// CorrelationKey is an expr expression for message correlation.
	CorrelationKey string
	// TimerDuration is an ISO-8601 duration for a timer-triggered EventSubProcess.
	TimerDuration string
}

// Kind returns KindStartEvent.
func (StartEvent) Kind() NodeKind { return KindStartEvent }

// EndEvent is the BPMN end event node: a normal process completion point.
type EndEvent struct{ baseNode }

// Kind returns KindEndEvent.
func (EndEvent) Kind() NodeKind { return KindEndEvent }

// TerminateEndEvent terminates the entire process (including all parallel branches).
type TerminateEndEvent struct{ baseNode }

// Kind returns KindTerminateEndEvent.
func (TerminateEndEvent) Kind() NodeKind { return KindTerminateEndEvent }

// ErrorEndEvent throws a BPMN error when reached, caught by a boundary error event.
type ErrorEndEvent struct {
	baseNode
	// ErrorCode is the BPMN error code thrown when this node is reached.
	// An empty value throws an anonymous error (catch-all).
	ErrorCode string
}

// Kind returns KindErrorEndEvent.
func (ErrorEndEvent) Kind() NodeKind { return KindErrorEndEvent }

// --- activities ---

// activityFields holds the cross-cutting fields every activity kind shares
// (retry, recovery, compensation, cancel, SLA, reminder). Embedded into each
// activity type so the engine reads e.g. node.SLADuration with no kind prefix.
type activityFields struct {
	// RetryPolicy is the optional per-node retry policy. Nil means use runtime default.
	RetryPolicy *RetryPolicy
	// RecoveryFlow is the ID of the sequence flow to take when retries are exhausted.
	RecoveryFlow string
	// CompensationAction is the name of the ServiceAction to invoke during rollback.
	CompensationAction string
	// CancelHandler is the optional ServiceAction to run when this node is interrupted.
	CancelHandler string
	// SLADuration is an ISO-8601 duration string for the SLA deadline.
	SLADuration string
	// SLAFlow is the ID of the sequence flow to take on SLA breach.
	SLAFlow string
	// SLAAction is the name of the ServiceAction to invoke on SLA breach.
	SLAAction string
	// ReminderEvery is an ISO-8601 duration string for the reminder interval.
	ReminderEvery string
	// ReminderAction is the name of the ServiceAction to invoke for each reminder.
	ReminderAction string
}

// ServiceTask executes a named service action.
type ServiceTask struct {
	baseNode
	activityFields
	// Action is the service-action name.
	Action string
}

// Kind returns KindServiceTask.
func (ServiceTask) Kind() NodeKind { return KindServiceTask }

// UserTask waits for a human to complete a work item.
type UserTask struct {
	baseNode
	activityFields
	// CandidateRoles are the roles eligible to claim and complete this task.
	CandidateRoles []string
	// EligibilityExpr is an optional attribute predicate (expr) for fine-grained eligibility.
	EligibilityExpr string
}

// Kind returns KindUserTask.
func (UserTask) Kind() NodeKind { return KindUserTask }

// ReceiveTask waits for an inbound message (signal or message correlation).
type ReceiveTask struct {
	baseNode
	activityFields
	// MessageName is the message reference for correlation.
	MessageName string
	// CorrelationKey is an expr expression evaluated at runtime to derive the correlation key.
	CorrelationKey string
}

// Kind returns KindReceiveTask.
func (ReceiveTask) Kind() NodeKind { return KindReceiveTask }

// SendTask sends an outbound message.
type SendTask struct {
	baseNode
	activityFields
	// MessageName is the message reference to send.
	MessageName string
}

// Kind returns KindSendTask.
func (SendTask) Kind() NodeKind { return KindSendTask }

// BusinessRuleTask executes a named business rule action.
type BusinessRuleTask struct {
	baseNode
	activityFields
	// Action is the business-rule action name.
	Action string
}

// Kind returns KindBusinessRuleTask.
func (BusinessRuleTask) Kind() NodeKind { return KindBusinessRuleTask }

// SubProcess embeds a nested process definition executed as a scope.
type SubProcess struct {
	baseNode
	activityFields
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *ProcessDefinition
}

// Kind returns KindSubProcess.
func (SubProcess) Kind() NodeKind { return KindSubProcess }

// CallActivity delegates to a top-level process definition resolved by name.
type CallActivity struct {
	baseNode
	activityFields
	// DefRef is the name of the top-level process definition to call.
	DefRef string
}

// Kind returns KindCallActivity.
func (CallActivity) Kind() NodeKind { return KindCallActivity }

// EventSubProcess is an event-triggered subprocess rooted at an event start.
type EventSubProcess struct {
	baseNode
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *ProcessDefinition
}

// Kind returns KindEventSubProcess.
func (EventSubProcess) Kind() NodeKind { return KindEventSubProcess }

// --- intermediate / boundary events ---

// IntermediateCatchEvent waits for a timer, signal, or message.
type IntermediateCatchEvent struct {
	baseNode
	// TimerDuration is an ISO-8601 duration string for a timer trigger.
	TimerDuration string
	// SignalName is the signal reference for a signal catch.
	SignalName string
	// MessageName is the message reference for a message catch.
	MessageName string
	// CorrelationKey is an expr expression for message correlation.
	CorrelationKey string
	// SLADuration is an ISO-8601 duration string for the SLA deadline.
	SLADuration string
	// SLAFlow is the ID of the sequence flow to take on SLA breach.
	SLAFlow string
	// SLAAction is the name of the ServiceAction to invoke on SLA breach.
	SLAAction string
	// ReminderEvery is an ISO-8601 duration string for the reminder interval.
	ReminderEvery string
	// ReminderAction is the name of the ServiceAction to invoke for each reminder.
	ReminderAction string
}

// Kind returns KindIntermediateCatchEvent.
func (IntermediateCatchEvent) Kind() NodeKind { return KindIntermediateCatchEvent }

// IntermediateThrowEvent throws a signal or triggers a compensation.
type IntermediateThrowEvent struct {
	baseNode
	// SignalName is the signal reference for a signal throw.
	SignalName string
	// CompensateRef names the node whose compensation to run (empty = scope-wide).
	CompensateRef string
}

// Kind returns KindIntermediateThrowEvent.
func (IntermediateThrowEvent) Kind() NodeKind { return KindIntermediateThrowEvent }

// BoundaryEvent is an event attached to an activity that fires on timer, signal, message, or error.
type BoundaryEvent struct {
	baseNode
	// AttachedTo is the ID of the host activity node.
	AttachedTo string
	// NonInterrupting controls interrupting behavior: false = interrupting (BPMN default).
	NonInterrupting bool
	// ErrorCode is the BPMN error code for a boundary error event (empty = catch-all).
	ErrorCode string
	// SignalName is the signal reference for a signal boundary.
	SignalName string
	// MessageName is the message reference for a message boundary.
	MessageName string
	// CorrelationKey is an expr expression for message correlation.
	CorrelationKey string
	// TimerDuration is an ISO-8601 duration string for a timer boundary.
	TimerDuration string
}

// Kind returns KindBoundaryEvent.
func (BoundaryEvent) Kind() NodeKind { return KindBoundaryEvent }

// --- gateways ---

// ExclusiveGateway routes to exactly one outgoing flow (XOR split / merge).
type ExclusiveGateway struct{ baseNode }

// Kind returns KindExclusiveGateway.
func (ExclusiveGateway) Kind() NodeKind { return KindExclusiveGateway }

// ParallelGateway splits into all outgoing flows (AND split) or waits for all (AND join).
type ParallelGateway struct{ baseNode }

// Kind returns KindParallelGateway.
func (ParallelGateway) Kind() NodeKind { return KindParallelGateway }

// InclusiveGateway routes to one or more outgoing flows (OR split / join).
type InclusiveGateway struct{ baseNode }

// Kind returns KindInclusiveGateway.
func (InclusiveGateway) Kind() NodeKind { return KindInclusiveGateway }

// EventBasedGateway routes based on which event arrives first (race).
type EventBasedGateway struct{ baseNode }

// Kind returns KindEventBasedGateway.
func (EventBasedGateway) Kind() NodeKind { return KindEventBasedGateway }
