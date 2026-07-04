package definition

import "github.com/zakyalvan/krtlwrkflw/action"

// Node is a single point in a process: an event, activity, or gateway.
// Concrete types (one per NodeKind) carry only the fields meaningful to their
// kind. Construct nodes with the New* constructors; build definitions with
// DefinitionBuilder or the YAML loader.
type Node interface {
	Kind() NodeKind
	ID() string
	Name() string
}

// Base supplies the identity common to every node kind. Every concrete node
// type — in this package's leaf packages (event, gateway, activity) — embeds it.
type Base struct {
	id   string
	name string
}

// NewBase constructs the identity embed for a node. Leaf-package constructors
// call it; consumers use the New* constructors instead.
func NewBase(id, name string) Base { return Base{id: id, name: name} }

func (b Base) ID() string   { return b.id }
func (b Base) Name() string { return b.name }

// SetName sets the display name. Used by the WithName options in the leaf
// packages, which mutate the embedded Base.
func (b *Base) SetName(name string) { b.name = name }

// --- events ---

// StartEvent is the BPMN start event node: the entry point of a process.
// When used as the trigger start of an EventSubProcess, it may carry
// correlation fields (SignalName, MessageName, CorrelationKey, TimerDuration)
// to describe the event that activates the event sub-process.
type StartEvent struct {
	Base
	// SignalName is set when this start event is a signal trigger for an EventSubProcess.
	SignalName string
	// MessageName is set when this start event is a message trigger for an EventSubProcess.
	MessageName string
	// CorrelationKey is an expr expression for message correlation.
	CorrelationKey string
	// TimerDuration is an expr-lang duration expression for a timer-triggered
	// EventSubProcess (a string result → time.ParseDuration, e.g. "1h"; a number → seconds; not ISO-8601).
	TimerDuration string
}

// Kind returns KindStartEvent.
func (StartEvent) Kind() NodeKind { return KindStartEvent }

// EndEvent is the BPMN end event node: a normal process completion point.
type EndEvent struct{ Base }

// Kind returns KindEndEvent.
func (EndEvent) Kind() NodeKind { return KindEndEvent }

// TerminateEndEvent terminates the entire process (including all parallel branches).
type TerminateEndEvent struct{ Base }

// Kind returns KindTerminateEndEvent.
func (TerminateEndEvent) Kind() NodeKind { return KindTerminateEndEvent }

// ErrorEndEvent throws a BPMN error when reached, caught by a boundary error event.
type ErrorEndEvent struct {
	Base
	// ErrorCode is the BPMN error code thrown when this node is reached.
	// An empty value throws an anonymous error (catch-all).
	ErrorCode string
}

// Kind returns KindErrorEndEvent.
func (ErrorEndEvent) Kind() NodeKind { return KindErrorEndEvent }

// --- activities ---

// WaitFields holds the deadline + reminder fields shared by activity kinds and
// by IntermediateCatchEvent (all of which can wait and so can carry a deadline
// escalation and periodic reminders). It is embedded by ActivityFields and by
// IntermediateCatchEvent; the kind-agnostic accessors DeadlineOf/ReminderOf
// dispatch on its (unexported) carrier methods.
type WaitFields struct {
	// DeadlineDuration is an expr-lang duration expression for the deadline (string → time.ParseDuration, e.g. "72h"; number → seconds; not ISO-8601).
	DeadlineDuration string
	// DeadlineFlow is the ID of the sequence flow to take on deadline breach.
	DeadlineFlow string
	// DeadlineAction is the name of the ServiceAction to invoke on deadline breach.
	DeadlineAction string
	// ReminderEvery is an expr-lang duration expression for the reminder interval (string → time.ParseDuration, e.g. "24h"; number → seconds; not ISO-8601).
	ReminderEvery string
	// ReminderAction is the name of the ServiceAction to invoke for each reminder.
	ReminderAction string
}

func (w WaitFields) deadline() (duration, flow, action string) {
	return w.DeadlineDuration, w.DeadlineFlow, w.DeadlineAction
}
func (w WaitFields) reminder() (every, action string) {
	return w.ReminderEvery, w.ReminderAction
}

// ActivityFields holds the cross-cutting fields every activity kind shares
// (retry, recovery, compensation, cancel, plus the embedded WaitFields).
// Embedded into each activity type so the engine reads e.g. node.DeadlineDuration
// with no kind prefix. The RetryPolicyOf/recoveryFlowOf accessors dispatch on its
// carrier methods.
type ActivityFields struct {
	WaitFields
	// RetryPolicy is the optional per-node retry policy. Nil means use runtime default.
	RetryPolicy *RetryPolicy
	// RecoveryFlow is the ID of the sequence flow to take when retries are exhausted.
	RecoveryFlow string
	// CompensationAction is the name of the ServiceAction to invoke during rollback.
	CompensationAction string
	// CancelHandler is the optional ServiceAction to run when this node is interrupted.
	CancelHandler string
}

func (a ActivityFields) retry() *RetryPolicy  { return a.RetryPolicy }
func (a ActivityFields) recoveryFlow() string { return a.RecoveryFlow }

// TaskAction holds the action reference shared by ServiceTask and
// BusinessRuleTask: a catalog name and/or a node-local inline action. Embedded so
// the ActionOf/InlineActionOf accessors dispatch on its carrier method across the
// activity leaf package.
type TaskAction struct {
	// Action is the service-action name; empty means default to the node id.
	Action string
	// Inline is a node-local ServiceAction taking precedence over name lookup.
	// It is never serialized (re-attached in code on rehydration).
	Inline action.ServiceAction
}

func (t TaskAction) taskAction() (string, action.ServiceAction) { return t.Action, t.Inline }

// ServiceTask executes a named service action.
type ServiceTask struct {
	Base
	ActivityFields
	TaskAction
}

// Kind returns KindServiceTask.
func (ServiceTask) Kind() NodeKind { return KindServiceTask }

// UserTask waits for a human to complete a work item.
type UserTask struct {
	Base
	ActivityFields
	// CandidateRoles are the roles eligible to claim and complete this task.
	CandidateRoles []string
	// EligibilityPrivileges is a list of resource-privilege tokens (e.g. "finance-task claim")
	// evaluated by a casbin-backed Authorizer. Each token is split on the first space
	// into (object, action); a single-token value uses "*" as the action.
	// Set via [WithEligibilityPrivileges].
	EligibilityPrivileges []string
	// EligibilityExpr is an optional attribute predicate (expr) for fine-grained eligibility.
	EligibilityExpr string
}

// Kind returns KindUserTask.
func (UserTask) Kind() NodeKind { return KindUserTask }

// ReceiveTask waits for an inbound message (signal or message correlation).
type ReceiveTask struct {
	Base
	ActivityFields
	// MessageName is the message reference for correlation.
	MessageName string
	// CorrelationKey is an expr expression evaluated at runtime to derive the correlation key.
	CorrelationKey string
}

// Kind returns KindReceiveTask.
func (ReceiveTask) Kind() NodeKind { return KindReceiveTask }

// SendTask sends an outbound message.
type SendTask struct {
	Base
	ActivityFields
	// MessageName is the message reference to send.
	MessageName string
	// CorrelationKey is an optional expr expression evaluated at runtime to derive
	// the correlation key carried on the outbound message.
	CorrelationKey string
}

// Kind returns KindSendTask.
func (SendTask) Kind() NodeKind { return KindSendTask }

// BusinessRuleTask executes a business rule action (by name or inline).
type BusinessRuleTask struct {
	Base
	ActivityFields
	TaskAction
}

// Kind returns KindBusinessRuleTask.
func (BusinessRuleTask) Kind() NodeKind { return KindBusinessRuleTask }

// SubProcess embeds a nested process definition executed as a scope.
type SubProcess struct {
	Base
	ActivityFields
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *ProcessDefinition
}

// Kind returns KindSubProcess.
func (SubProcess) Kind() NodeKind { return KindSubProcess }

// CallActivity delegates to a top-level process definition resolved by name.
type CallActivity struct {
	Base
	ActivityFields
	// DefRef is the name of the top-level process definition to call.
	DefRef string
}

// Kind returns KindCallActivity.
func (CallActivity) Kind() NodeKind { return KindCallActivity }

// EventSubProcess is an event-triggered subprocess rooted at an event start.
type EventSubProcess struct {
	Base
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *ProcessDefinition
	// NonInterrupting, when true, means the event sub-process does not cancel
	// the enclosing scope's tokens when triggered. When false (the default, interrupting),
	// the event sub-process interrupts the enclosing scope.
	NonInterrupting bool
}

// Kind returns KindEventSubProcess.
func (EventSubProcess) Kind() NodeKind { return KindEventSubProcess }

// --- intermediate / boundary events ---

// IntermediateCatchEvent waits for a timer, signal, or message. Like activities
// it can wait, so it embeds WaitFields (deadline escalation + reminders).
type IntermediateCatchEvent struct {
	Base
	WaitFields
	// TimerDuration is an expr-lang duration expression for a timer trigger (string → time.ParseDuration, e.g. "1h"; number → seconds; not ISO-8601).
	TimerDuration string
	// SignalName is the signal reference for a signal catch.
	SignalName string
	// MessageName is the message reference for a message catch.
	MessageName string
	// CorrelationKey is an expr expression for message correlation.
	CorrelationKey string
}

// Kind returns KindIntermediateCatchEvent.
func (IntermediateCatchEvent) Kind() NodeKind { return KindIntermediateCatchEvent }

// IntermediateThrowEvent throws a signal or triggers a compensation.
type IntermediateThrowEvent struct {
	Base
	// SignalName is the signal reference for a signal throw.
	SignalName string
	// CompensateRef names the node whose compensation to run (empty = scope-wide).
	CompensateRef string
}

// Kind returns KindIntermediateThrowEvent.
func (IntermediateThrowEvent) Kind() NodeKind { return KindIntermediateThrowEvent }

// BoundaryEvent is an event attached to an activity that fires on timer, signal, message, or error.
type BoundaryEvent struct {
	Base
	// AttachedTo is the ID of the host activity node.
	AttachedTo string
	// NonInterrupting controls interrupting behavior: false = interrupting (the default).
	NonInterrupting bool
	// ErrorCode is the BPMN error code for a boundary error event (empty = catch-all).
	ErrorCode string
	// SignalName is the signal reference for a signal boundary.
	SignalName string
	// MessageName is the message reference for a message boundary.
	MessageName string
	// CorrelationKey is an expr expression for message correlation.
	CorrelationKey string
	// TimerDuration is an expr-lang duration expression for a timer boundary (string → time.ParseDuration, e.g. "1h"; number → seconds; not ISO-8601).
	TimerDuration string
}

// Kind returns KindBoundaryEvent.
func (BoundaryEvent) Kind() NodeKind { return KindBoundaryEvent }

// --- gateways ---

// ExclusiveGateway routes to exactly one outgoing flow (XOR split / merge).
type ExclusiveGateway struct{ Base }

// Kind returns KindExclusiveGateway.
func (ExclusiveGateway) Kind() NodeKind { return KindExclusiveGateway }

// ParallelGateway splits into all outgoing flows (AND split) or waits for all (AND join).
type ParallelGateway struct{ Base }

// Kind returns KindParallelGateway.
func (ParallelGateway) Kind() NodeKind { return KindParallelGateway }

// InclusiveGateway routes to one or more outgoing flows (OR split / join).
type InclusiveGateway struct{ Base }

// Kind returns KindInclusiveGateway.
func (InclusiveGateway) Kind() NodeKind { return KindInclusiveGateway }

// EventBasedGateway routes based on which event arrives first (race).
type EventBasedGateway struct{ Base }

// Kind returns KindEventBasedGateway.
func (EventBasedGateway) Kind() NodeKind { return KindEventBasedGateway }
