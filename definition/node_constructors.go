package definition

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/action"
)

// --- universal option (name) ---

// optNameArg returns the first name from a variadic or "".
func optNameArg(name []string) string {
	if len(name) > 0 {
		return name[0]
	}
	return ""
}

// --- activity options ---

// activityOption is the functional-options type for activity kinds.
// Options that only set activityFields are plain activityOption values.
// Options that also set name use the combined activityOrNameOption type.
type activityOption interface {
	applyActivity(a *activityFields)
	applyName(b *baseNode)
}

// userTaskOption is the functional-options type for UserTask.
// All activityOption values that implement applyUserTask also satisfy this interface,
// so all shared activity options (WithName, WithRetryPolicy, etc.) work on NewUserTask.
type userTaskOption interface {
	applyUserTask(u *UserTask)
}

// receiveTaskOption is the functional-options type for ReceiveTask.
// All activityOption values that implement applyReceiveTask also satisfy this interface,
// so all shared activity options work on NewReceiveTask.
type receiveTaskOption interface {
	applyReceiveTask(r *ReceiveTask)
}

// sendTaskOption is the functional-options type for SendTask.
// All activityOption values that implement applySendTask also satisfy this interface,
// so all shared activity options work on NewSendTask, as does WithCorrelationKey.
type sendTaskOption interface {
	applySendTask(s *SendTask)
}

// serviceTaskOption configures a ServiceTask.
type serviceTaskOption interface{ applyServiceTask(s *ServiceTask) }

// businessRuleOption configures a BusinessRuleTask.
type businessRuleOption interface{ applyBusinessRule(b *BusinessRuleTask) }

// actionNameOpt sets the action name on a ServiceTask or BusinessRuleTask.
type actionNameOpt struct{ name string }

func (o actionNameOpt) applyServiceTask(s *ServiceTask)       { s.Action = o.name }
func (o actionNameOpt) applyBusinessRule(b *BusinessRuleTask) { b.Action = o.name }

// WithActionName sets the catalog action name. Resolved scoped→global at runtime.
// Mutually exclusive with WithAction/WithActionFunc (Build reports a conflict).
func WithActionName(name string) interface {
	serviceTaskOption
	businessRuleOption
} {
	return actionNameOpt{name}
}

// inlineActionOpt sets a node-local inline action.
type inlineActionOpt struct{ a action.ServiceAction }

func (o inlineActionOpt) applyServiceTask(s *ServiceTask)       { s.inline = o.a }
func (o inlineActionOpt) applyBusinessRule(b *BusinessRuleTask) { b.inline = o.a }

// WithAction attaches a node-local inline ServiceAction available to this node
// only. Mutually exclusive with WithActionName (Build reports a conflict).
//
// Inline actions resolve at any sub-process nesting depth (the engine carries
// the resolved action on the invocation command). They are never serialized: a
// definition round-tripped through JSONB loses its inline actions, so a consumer
// that persists definitions must re-attach them in code on restart.
func WithAction(a action.ServiceAction) interface {
	serviceTaskOption
	businessRuleOption
} {
	return inlineActionOpt{a}
}

// WithActionFunc is WithAction sugar wrapping a plain function as action.Func.
func WithActionFunc(fn func(context.Context, map[string]any) (map[string]any, error)) interface {
	serviceTaskOption
	businessRuleOption
} {
	return inlineActionOpt{action.Func(fn)}
}

// activityOnlyOption wraps a function that mutates activityFields only.
type activityOnlyOption struct{ fn func(*activityFields) }

func (o activityOnlyOption) applyActivity(a *activityFields) { o.fn(a) }
func (activityOnlyOption) applyName(_ *baseNode)             {}
func (o activityOnlyOption) applyUserTask(u *UserTask)       { o.fn(&u.activityFields) }
func (o activityOnlyOption) applyReceiveTask(r *ReceiveTask) { o.fn(&r.activityFields) }
func (o activityOnlyOption) applySendTask(s *SendTask)       { o.fn(&s.activityFields) }
func (o activityOnlyOption) applyServiceTask(s *ServiceTask) { o.fn(&s.activityFields) }
func (o activityOnlyOption) applyBusinessRule(b *BusinessRuleTask) {
	o.fn(&b.activityFields)
}

// withActivity constructs an activityOnlyOption. The concrete return type is
// intentional: activityOnlyOption satisfies activityOption, userTaskOption,
// receiveTaskOption, and sendTaskOption simultaneously, so callers can pass it to
// any constructor.
func withActivity(fn func(*activityFields)) activityOnlyOption {
	return activityOnlyOption{fn}
}

// nameOpt sets the name on a baseNode; it implements every node option interface
// (activity, catch, boundary, startEvent, eventSubProcess, userTask, receiveTask,
// sendTask, serviceTask, businessRule), so WithName is accepted by every
// option-taking constructor.
type nameOpt struct{ name string }

func (o nameOpt) applyActivity(_ *activityFields)         {}
func (o nameOpt) applyName(b *baseNode)                   { b.name = o.name }
func (o nameOpt) applyCatch(n *IntermediateCatchEvent)    { n.name = o.name }
func (o nameOpt) applyBoundary(n *BoundaryEvent)          { n.name = o.name }
func (o nameOpt) applyStart(n *StartEvent)                { n.name = o.name }
func (o nameOpt) applyEventSubProcess(n *EventSubProcess) { n.name = o.name }
func (o nameOpt) applyUserTask(u *UserTask)               { u.name = o.name }
func (o nameOpt) applyReceiveTask(r *ReceiveTask)         { r.name = o.name }
func (o nameOpt) applySendTask(s *SendTask)               { s.name = o.name }
func (o nameOpt) applyServiceTask(s *ServiceTask)         { s.name = o.name }
func (o nameOpt) applyBusinessRule(b *BusinessRuleTask)   { b.name = o.name }

// WithName returns an option that sets the Name field. It is accepted by every
// option-taking node constructor EXCEPT NewIntermediateThrowEvent, which sets its
// name via the dedicated WithThrowName instead.
func WithName(name string) nameOpt { return nameOpt{name} }

// WithRetryPolicy returns an activity option that sets RetryPolicy.
// The concrete return type (activityOnlyOption) satisfies activityOption,
// userTaskOption, and receiveTaskOption so it works on all constructors.
func WithRetryPolicy(p *RetryPolicy) activityOnlyOption {
	return withActivity(func(a *activityFields) { a.RetryPolicy = p })
}

// WithRecoveryFlow returns an activity option that sets RecoveryFlow.
func WithRecoveryFlow(flowID string) activityOnlyOption {
	return withActivity(func(a *activityFields) { a.RecoveryFlow = flowID })
}

// WithCompensation returns an activity option that sets CompensationAction.
func WithCompensation(action string) activityOnlyOption {
	return withActivity(func(a *activityFields) { a.CompensationAction = action })
}

// WithCancelHandler returns an activity option that sets CancelHandler.
func WithCancelHandler(action string) activityOnlyOption {
	return withActivity(func(a *activityFields) { a.CancelHandler = action })
}

// WithDeadline returns an activity option that sets DeadlineDuration, DeadlineFlow, and DeadlineAction.
func WithDeadline(duration, flowID, action string) activityOnlyOption {
	return withActivity(func(a *activityFields) {
		a.DeadlineDuration, a.DeadlineFlow, a.DeadlineAction = duration, flowID, action
	})
}

// WithReminder returns an activity option that sets ReminderEvery and ReminderAction.
func WithReminder(every, action string) activityOnlyOption {
	return withActivity(func(a *activityFields) {
		a.ReminderEvery, a.ReminderAction = every, action
	})
}

// eligibilityExprOpt satisfies only userTaskOption — passing it to any other
// constructor (NewServiceTask, NewSendTask, etc.) is a compile-time error.
type eligibilityExprOpt struct{ expr string }

func (o eligibilityExprOpt) applyUserTask(u *UserTask) { u.EligibilityExpr = o.expr }

// WithEligibilityExpr returns a userTaskOption that sets EligibilityExpr.
// It may only be passed to NewUserTask; passing it to any other constructor
// is a compile-time error.
func WithEligibilityExpr(expr string) userTaskOption { return eligibilityExprOpt{expr} }

// eligibilityPrivilegesOpt satisfies only userTaskOption — passing it to any
// other constructor (NewServiceTask, NewSendTask, etc.) is a compile-time error.
type eligibilityPrivilegesOpt struct{ privs []string }

func (o eligibilityPrivilegesOpt) applyUserTask(u *UserTask) {
	u.EligibilityPrivileges = append(u.EligibilityPrivileges, o.privs...)
}

// WithEligibilityPrivileges returns a userTaskOption that sets resource-privilege
// tokens on a UserTask. Each token is a space-separated "object action" pair (e.g.
// "finance-task claim") that a casbin-backed [authz.Authorizer] evaluates via
// enforcer.Enforce(subject, object, action). A single-token value without a space
// uses "*" as the action (casbin convention).
//
// Multiple calls are additive; the privileges are appended in order.
//
// It may only be passed to NewUserTask; passing it to any other constructor
// is a compile-time error.
func WithEligibilityPrivileges(privs ...string) userTaskOption {
	return eligibilityPrivilegesOpt{privs: privs}
}

// correlationKeyOpt satisfies receiveTaskOption and sendTaskOption — passing it to
// any other constructor is a compile-time error.
type correlationKeyOpt struct{ key string }

func (o correlationKeyOpt) applyReceiveTask(r *ReceiveTask) { r.CorrelationKey = o.key }
func (o correlationKeyOpt) applySendTask(s *SendTask)       { s.CorrelationKey = o.key }

// WithCorrelationKey returns an option that sets CorrelationKey on a ReceiveTask
// or a SendTask. It may only be passed to NewReceiveTask or NewSendTask; passing
// it to any other constructor is a compile-time error.
func WithCorrelationKey(key string) interface {
	receiveTaskOption
	sendTaskOption
} {
	return correlationKeyOpt{key}
}

// applyActivityOpts applies all options to the given base and activity fields.
func applyActivityOpts(b *baseNode, a *activityFields, opts []activityOption) {
	for _, o := range opts {
		o.applyActivity(a)
		o.applyName(b)
	}
}

// --- event constructors ---

// startEventOption is the typed functional-options interface for StartEvent,
// consistent with activityOption, catchOption, and boundaryOption.
type startEventOption interface {
	applyStart(n *StartEvent)
}

// startEventFuncOpt wraps a function that mutates a StartEvent only.
type startEventFuncOpt struct{ fn func(*StartEvent) }

func (o startEventFuncOpt) applyStart(n *StartEvent) { o.fn(n) }

// WithStartSignal sets SignalName on a StartEvent (for EventSubProcess triggers).
func WithStartSignal(name string) startEventOption {
	return startEventFuncOpt{func(n *StartEvent) { n.SignalName = name }}
}

// WithStartMessage sets MessageName and CorrelationKey on a StartEvent (for EventSubProcess triggers).
func WithStartMessage(msg, key string) startEventOption {
	return startEventFuncOpt{func(n *StartEvent) { n.MessageName, n.CorrelationKey = msg, key }}
}

// WithStartTimer sets TimerDuration on a StartEvent (for EventSubProcess triggers).
func WithStartTimer(dur string) startEventOption {
	return startEventFuncOpt{func(n *StartEvent) { n.TimerDuration = dur }}
}

// NewStartEvent constructs a StartEvent. Use WithName to set the display name and
// WithStartSignal, WithStartMessage, or WithStartTimer to configure EventSubProcess triggers.
func NewStartEvent(id string, opts ...startEventOption) Node {
	n := StartEvent{baseNode: baseNode{id: id}}
	for _, o := range opts {
		o.applyStart(&n)
	}
	return n
}

// NewEndEvent constructs an EndEvent. An optional name may be provided as a
// trailing variadic argument.
func NewEndEvent(id string, name ...string) Node {
	return EndEvent{baseNode{id, optNameArg(name)}}
}

// NewTerminateEndEvent constructs a TerminateEndEvent. An optional name may be
// provided as a trailing variadic argument.
func NewTerminateEndEvent(id string, name ...string) Node {
	return TerminateEndEvent{baseNode{id, optNameArg(name)}}
}

// NewErrorEndEvent constructs an ErrorEndEvent. An optional name may be
// provided as a trailing variadic argument.
func NewErrorEndEvent(id, errorCode string, name ...string) Node {
	return ErrorEndEvent{baseNode{id, optNameArg(name)}, errorCode}
}

// --- gateway constructors ---

// NewExclusiveGateway constructs an ExclusiveGateway. An optional name may be
// provided as a trailing variadic argument.
func NewExclusiveGateway(id string, name ...string) Node {
	return ExclusiveGateway{baseNode{id, optNameArg(name)}}
}

// NewParallelGateway constructs a ParallelGateway. An optional name may be
// provided as a trailing variadic argument.
func NewParallelGateway(id string, name ...string) Node {
	return ParallelGateway{baseNode{id, optNameArg(name)}}
}

// NewInclusiveGateway constructs an InclusiveGateway. An optional name may be
// provided as a trailing variadic argument.
func NewInclusiveGateway(id string, name ...string) Node {
	return InclusiveGateway{baseNode{id, optNameArg(name)}}
}

// NewEventBasedGateway constructs an EventBasedGateway. An optional name may be
// provided as a trailing variadic argument.
func NewEventBasedGateway(id string, name ...string) Node {
	return EventBasedGateway{baseNode{id, optNameArg(name)}}
}

// --- activity constructors ---

// NewServiceTask constructs a ServiceTask. Set the action with WithActionName
// (catalog reference) or WithAction/WithActionFunc (node-local inline); with
// neither, the action name defaults to the node id at execution time. Other
// behaviour (retry, deadline, name, etc.) is configured via the shared activity options.
func NewServiceTask(id string, opts ...serviceTaskOption) Node {
	s := ServiceTask{baseNode: baseNode{id: id}}
	for _, o := range opts {
		o.applyServiceTask(&s)
	}
	return s
}

// NewUserTask constructs a UserTask with the given id and candidate roles.
// Options may be any userTaskOption: all shared activity options (WithName,
// WithRetryPolicy, WithDeadline, WithReminder, WithRecoveryFlow, WithCompensation,
// WithCancelHandler) work here, as does the UserTask-specific WithEligibilityExpr.
// Passing a non-userTaskOption (e.g. WithCorrelationKey) is a compile-time error.
func NewUserTask(id string, roles []string, opts ...userTaskOption) Node {
	u := UserTask{baseNode: baseNode{id: id}, CandidateRoles: roles}
	for _, o := range opts {
		o.applyUserTask(&u)
	}
	return u
}

// NewReceiveTask constructs a ReceiveTask with the given id and message name.
// Options may be any receiveTaskOption: all shared activity options work here,
// as does the ReceiveTask-specific WithCorrelationKey.
// Passing a non-receiveTaskOption (e.g. WithEligibilityExpr) is a compile-time error.
func NewReceiveTask(id, messageName string, opts ...receiveTaskOption) Node {
	r := ReceiveTask{baseNode: baseNode{id: id}, MessageName: messageName}
	for _, o := range opts {
		o.applyReceiveTask(&r)
	}
	return r
}

// NewSendTask constructs a SendTask with the given id and message name.
// Options may be any sendTaskOption: all shared activity options work here, as
// does the SendTask-specific WithCorrelationKey.
// Passing a non-sendTaskOption (e.g. WithEligibilityExpr) is a compile-time error.
func NewSendTask(id, messageName string, opts ...sendTaskOption) Node {
	s := SendTask{baseNode: baseNode{id: id}, MessageName: messageName}
	for _, o := range opts {
		o.applySendTask(&s)
	}
	return s
}

// NewBusinessRuleTask constructs a BusinessRuleTask. Action configuration mirrors
// NewServiceTask (WithActionName / WithAction / WithActionFunc / default-by-id).
func NewBusinessRuleTask(id string, opts ...businessRuleOption) Node {
	b := BusinessRuleTask{baseNode: baseNode{id: id}}
	for _, o := range opts {
		o.applyBusinessRule(&b)
	}
	return b
}

// NewSubProcess constructs a SubProcess with the given id and nested definition.
func NewSubProcess(id string, sub *ProcessDefinition, opts ...activityOption) Node {
	b := baseNode{id: id}
	var a activityFields
	applyActivityOpts(&b, &a, opts)
	return SubProcess{baseNode: b, activityFields: a, Subprocess: sub}
}

// NewCallActivity constructs a CallActivity with the given id and definition reference.
func NewCallActivity(id, defRef string, opts ...activityOption) Node {
	b := baseNode{id: id}
	var a activityFields
	applyActivityOpts(&b, &a, opts)
	return CallActivity{baseNode: b, activityFields: a, DefRef: defRef}
}

// eventSubProcessOption is the functional-options interface for EventSubProcess,
// consistent with activityOption, catchOption, and boundaryOption.
type eventSubProcessOption interface {
	applyEventSubProcess(n *EventSubProcess)
}

// espNonInterruptingOpt sets NonInterrupting to true on an EventSubProcess.
type espNonInterruptingOpt struct{}

func (espNonInterruptingOpt) applyEventSubProcess(n *EventSubProcess) { n.NonInterrupting = true }

// WithESPNonInterrupting returns an EventSubProcess option that sets NonInterrupting to true,
// meaning the event sub-process runs alongside the enclosing scope without cancelling it.
func WithESPNonInterrupting() eventSubProcessOption { return espNonInterruptingOpt{} }

// NewEventSubProcess constructs an EventSubProcess with the given id and nested definition.
// Use WithESPNonInterrupting to mark the event sub-process as non-interrupting, and
// WithName to set the display name.
func NewEventSubProcess(id string, sub *ProcessDefinition, opts ...eventSubProcessOption) Node {
	n := EventSubProcess{baseNode: baseNode{id: id}, Subprocess: sub}
	for _, o := range opts {
		o.applyEventSubProcess(&n)
	}
	return n
}

// --- intermediate / boundary event options ---

// catchOption is the functional-options type for IntermediateCatchEvent.
type catchOption interface {
	applyCatch(n *IntermediateCatchEvent)
}

// timerDurationOpt sets TimerDuration on an IntermediateCatchEvent.
type timerDurationOpt struct{ dur string }

func (o timerDurationOpt) applyCatch(n *IntermediateCatchEvent) { n.TimerDuration = o.dur }
func (o timerDurationOpt) applyActivity(_ *activityFields)      {}
func (o timerDurationOpt) applyName(_ *baseNode)                {}

// WithTimerDuration returns an IntermediateCatchEvent option that sets TimerDuration.
func WithTimerDuration(dur string) interface {
	catchOption
	activityOption
} {
	return timerDurationOpt{dur}
}

// signalNameOpt sets SignalName on an IntermediateCatchEvent or IntermediateThrowEvent.
type signalNameOpt struct{ name string }

func (o signalNameOpt) applyCatch(n *IntermediateCatchEvent) { n.SignalName = o.name }
func (o signalNameOpt) applyActivity(_ *activityFields)      {}
func (o signalNameOpt) applyName(_ *baseNode)                {}

// WithSignalName returns an IntermediateCatchEvent option that sets SignalName.
func WithSignalName(name string) interface {
	catchOption
	activityOption
} {
	return signalNameOpt{name}
}

// messageNameKeyOpt sets MessageName and CorrelationKey on an IntermediateCatchEvent.
type messageNameKeyOpt struct{ msg, key string }

func (o messageNameKeyOpt) applyCatch(n *IntermediateCatchEvent) {
	n.MessageName, n.CorrelationKey = o.msg, o.key
}
func (o messageNameKeyOpt) applyActivity(_ *activityFields) {}
func (o messageNameKeyOpt) applyName(_ *baseNode)           {}

// WithMessageNameAndKey returns an IntermediateCatchEvent option that sets
// MessageName and CorrelationKey.
func WithMessageNameAndKey(msg, key string) interface {
	catchOption
	activityOption
} {
	return messageNameKeyOpt{msg, key}
}

// iceDeadlineOpt sets deadline fields on an IntermediateCatchEvent.
type iceDeadlineOpt struct{ dur, flow, act string }

func (o iceDeadlineOpt) applyCatch(n *IntermediateCatchEvent) {
	n.DeadlineDuration, n.DeadlineFlow, n.DeadlineAction = o.dur, o.flow, o.act
}
func (o iceDeadlineOpt) applyActivity(_ *activityFields) {}
func (o iceDeadlineOpt) applyName(_ *baseNode)           {}

// WithICEDeadline returns an IntermediateCatchEvent option that sets DeadlineDuration, DeadlineFlow, DeadlineAction.
func WithICEDeadline(duration, flowID, action string) interface {
	catchOption
	activityOption
} {
	return iceDeadlineOpt{duration, flowID, action}
}

// iceReminderOpt sets reminder fields on an IntermediateCatchEvent.
type iceReminderOpt struct{ every, act string }

func (o iceReminderOpt) applyCatch(n *IntermediateCatchEvent) {
	n.ReminderEvery, n.ReminderAction = o.every, o.act
}
func (o iceReminderOpt) applyActivity(_ *activityFields) {}
func (o iceReminderOpt) applyName(_ *baseNode)           {}

// WithICEReminder returns an IntermediateCatchEvent option that sets ReminderEvery and ReminderAction.
func WithICEReminder(every, action string) interface {
	catchOption
	activityOption
} {
	return iceReminderOpt{every, action}
}

// NewIntermediateCatchEvent constructs an IntermediateCatchEvent.
// Options can be WithTimerDuration, WithSignalName, WithMessageNameAndKey,
// WithICEDeadline, WithICEReminder, or WithName (as a catchOption-compatible value).
func NewIntermediateCatchEvent(id string, opts ...catchOption) Node {
	n := IntermediateCatchEvent{baseNode: baseNode{id: id}}
	for _, o := range opts {
		o.applyCatch(&n)
	}
	return n
}

// throwOption is the functional-options type for IntermediateThrowEvent.
type throwOption func(n *IntermediateThrowEvent)

// WithThrowSignal sets SignalName on an IntermediateThrowEvent.
func WithThrowSignal(name string) throwOption {
	return func(n *IntermediateThrowEvent) { n.SignalName = name }
}

// WithCompensateRef sets CompensateRef on an IntermediateThrowEvent.
func WithCompensateRef(ref string) throwOption {
	return func(n *IntermediateThrowEvent) { n.CompensateRef = ref }
}

// WithThrowName sets name on an IntermediateThrowEvent.
func WithThrowName(name string) throwOption {
	return func(n *IntermediateThrowEvent) { n.name = name }
}

// NewIntermediateThrowEvent constructs an IntermediateThrowEvent.
// Use WithThrowSignal, WithCompensateRef, or WithThrowName to set fields.
func NewIntermediateThrowEvent(id string, opts ...throwOption) Node {
	n := IntermediateThrowEvent{baseNode: baseNode{id: id}}
	for _, o := range opts {
		o(&n)
	}
	return n
}

// boundaryOption is the functional-options type for BoundaryEvent.
type boundaryOption interface {
	applyBoundary(n *BoundaryEvent)
}

type boundaryFuncOpt struct{ fn func(*BoundaryEvent) }

func (o boundaryFuncOpt) applyBoundary(n *BoundaryEvent) { o.fn(n) }

func withBoundary(fn func(*BoundaryEvent)) boundaryOption { return boundaryFuncOpt{fn} }

// WithBoundarySignal sets SignalName on a BoundaryEvent.
func WithBoundarySignal(name string) boundaryOption {
	return withBoundary(func(n *BoundaryEvent) { n.SignalName = name })
}

// WithBoundaryMessage sets MessageName and CorrelationKey on a BoundaryEvent.
func WithBoundaryMessage(msg, key string) boundaryOption {
	return withBoundary(func(n *BoundaryEvent) { n.MessageName, n.CorrelationKey = msg, key })
}

// WithBoundaryTimer sets TimerDuration on a BoundaryEvent.
func WithBoundaryTimer(dur string) boundaryOption {
	return withBoundary(func(n *BoundaryEvent) { n.TimerDuration = dur })
}

// WithBoundaryErrorCode sets ErrorCode on a BoundaryEvent.
func WithBoundaryErrorCode(code string) boundaryOption {
	return withBoundary(func(n *BoundaryEvent) { n.ErrorCode = code })
}

// BoundaryNonInterrupting sets NonInterrupting to true on a BoundaryEvent.
func BoundaryNonInterrupting() boundaryOption {
	return withBoundary(func(n *BoundaryEvent) { n.NonInterrupting = true })
}

// NewBoundaryEvent constructs a BoundaryEvent attached to the given host activity.
// Use WithBoundarySignal, WithBoundaryMessage, WithBoundaryTimer, WithBoundaryErrorCode,
// BoundaryNonInterrupting, and WithName to set fields.
func NewBoundaryEvent(id, attachedTo string, opts ...boundaryOption) Node {
	n := BoundaryEvent{baseNode: baseNode{id: id}, AttachedTo: attachedTo}
	for _, o := range opts {
		o.applyBoundary(&n)
	}
	return n
}
