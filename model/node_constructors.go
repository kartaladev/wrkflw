package model

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

// activityOnlyOption wraps a function that mutates activityFields only.
type activityOnlyOption struct{ fn func(*activityFields) }

func (o activityOnlyOption) applyActivity(a *activityFields) { o.fn(a) }
func (activityOnlyOption) applyName(_ *baseNode)             {}
func (o activityOnlyOption) applyUserTask(u *UserTask)       { o.fn(&u.activityFields) }
func (o activityOnlyOption) applyReceiveTask(r *ReceiveTask) { o.fn(&r.activityFields) }

// withActivity constructs an activityOnlyOption. The concrete return type is
// intentional: activityOnlyOption satisfies activityOption, userTaskOption, and
// receiveTaskOption simultaneously, so callers can pass it to any constructor.
func withActivity(fn func(*activityFields)) activityOnlyOption {
	return activityOnlyOption{fn}
}

// nameOpt sets the name on a baseNode; implements activityOption, catchOption, boundaryOption, startEventOption, eventSubProcessOption, userTaskOption, and receiveTaskOption.
type nameOpt struct{ name string }

func (o nameOpt) applyActivity(_ *activityFields)         {}
func (o nameOpt) applyName(b *baseNode)                   { b.name = o.name }
func (o nameOpt) applyCatch(n *IntermediateCatchEvent)    { n.name = o.name }
func (o nameOpt) applyBoundary(n *BoundaryEvent)          { n.name = o.name }
func (o nameOpt) applyStart(n *StartEvent)                { n.name = o.name }
func (o nameOpt) applyEventSubProcess(n *EventSubProcess) { n.name = o.name }
func (o nameOpt) applyUserTask(u *UserTask)               { u.name = o.name }
func (o nameOpt) applyReceiveTask(r *ReceiveTask)         { r.name = o.name }

// WithName returns an option that sets the Name field on any node that accepts it.
// It implements activityOption, catchOption, boundaryOption, userTaskOption, and receiveTaskOption.
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

// WithSLA returns an activity option that sets SLADuration, SLAFlow, and SLAAction.
func WithSLA(duration, flowID, action string) activityOnlyOption {
	return withActivity(func(a *activityFields) {
		a.SLADuration, a.SLAFlow, a.SLAAction = duration, flowID, action
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

// correlationKeyOpt satisfies only receiveTaskOption — passing it to any other
// constructor is a compile-time error.
type correlationKeyOpt struct{ key string }

func (o correlationKeyOpt) applyReceiveTask(r *ReceiveTask) { r.CorrelationKey = o.key }

// WithCorrelationKey returns a receiveTaskOption that sets CorrelationKey.
// It may only be passed to NewReceiveTask; passing it to any other constructor
// is a compile-time error.
func WithCorrelationKey(key string) receiveTaskOption { return correlationKeyOpt{key} }

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

// NewServiceTask constructs a ServiceTask with the given id and service-action name.
// Additional behaviour (retry, SLA, etc.) is configured via activityOption values.
func NewServiceTask(id, action string, opts ...activityOption) Node {
	b := baseNode{id: id}
	var a activityFields
	applyActivityOpts(&b, &a, opts)
	return ServiceTask{baseNode: b, activityFields: a, Action: action}
}

// NewUserTask constructs a UserTask with the given id and candidate roles.
// Options may be any userTaskOption: all shared activity options (WithName,
// WithRetryPolicy, WithSLA, WithReminder, WithRecoveryFlow, WithCompensation,
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
func NewSendTask(id, messageName string, opts ...activityOption) Node {
	b := baseNode{id: id}
	var a activityFields
	applyActivityOpts(&b, &a, opts)
	return SendTask{baseNode: b, activityFields: a, MessageName: messageName}
}

// NewBusinessRuleTask constructs a BusinessRuleTask with the given id and action name.
func NewBusinessRuleTask(id, action string, opts ...activityOption) Node {
	b := baseNode{id: id}
	var a activityFields
	applyActivityOpts(&b, &a, opts)
	return BusinessRuleTask{baseNode: b, activityFields: a, Action: action}
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

// iceSLAOpt sets SLA fields on an IntermediateCatchEvent.
type iceSLAOpt struct{ dur, flow, act string }

func (o iceSLAOpt) applyCatch(n *IntermediateCatchEvent) {
	n.SLADuration, n.SLAFlow, n.SLAAction = o.dur, o.flow, o.act
}
func (o iceSLAOpt) applyActivity(_ *activityFields) {}
func (o iceSLAOpt) applyName(_ *baseNode)           {}

// WithICESLA returns an IntermediateCatchEvent option that sets SLADuration, SLAFlow, SLAAction.
func WithICESLA(duration, flowID, action string) interface {
	catchOption
	activityOption
} {
	return iceSLAOpt{duration, flowID, action}
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
// WithICESLA, WithICEReminder, or WithName (as a catchOption-compatible value).
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
