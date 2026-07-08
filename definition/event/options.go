package event

import "github.com/zakyalvan/krtlwrkflw/definition/schedule"

// --- option interfaces ---

// StartOption configures a StartEvent.
type StartOption interface{ applyStart(n *StartEvent) }

// CatchOption configures an IntermediateCatchEvent.
type CatchOption interface {
	applyCatch(n *IntermediateCatchEvent)
}

// ThrowOption configures an IntermediateThrowEvent.
type ThrowOption func(n *IntermediateThrowEvent)

// BoundaryOption configures a BoundaryEvent.
type BoundaryOption interface{ applyBoundary(n *BoundaryEvent) }

// EventSubProcessOption configures an EventSubProcess.
type EventSubProcessOption interface{ applyEventSubProcess(n *EventSubProcess) }

// --- WithName (Start, Catch, Boundary, EventSubProcess) ---

type nameOpt struct{ name string }

func (o nameOpt) applyStart(n *StartEvent)                { n.SetName(o.name) }
func (o nameOpt) applyCatch(n *IntermediateCatchEvent)    { n.SetName(o.name) }
func (o nameOpt) applyBoundary(n *BoundaryEvent)          { n.SetName(o.name) }
func (o nameOpt) applyEventSubProcess(n *EventSubProcess) { n.SetName(o.name) }

// WithName sets the display name on a start, catch, boundary, or event
// sub-process node. IntermediateThrowEvent uses WithThrowName instead; the end
// events take an optional name as a trailing constructor argument.
func WithName(name string) interface {
	StartOption
	CatchOption
	BoundaryOption
	EventSubProcessOption
} {
	return nameOpt{name}
}

// --- StartEvent options (EventSubProcess triggers) ---

type startFuncOpt struct{ fn func(*StartEvent) }

func (o startFuncOpt) applyStart(n *StartEvent) { o.fn(n) }

// WithStartSignal sets SignalName on a StartEvent (for EventSubProcess triggers).
func WithStartSignal(name string) StartOption {
	return startFuncOpt{func(n *StartEvent) { n.SignalName = name }}
}

// WithStartMessage sets MessageName and CorrelationKey on a StartEvent.
func WithStartMessage(msg, key string) StartOption {
	return startFuncOpt{func(n *StartEvent) { n.MessageName, n.CorrelationKey = msg, key }}
}

// WithStartTimer sets the Timer trigger on a StartEvent. Use schedule.AfterExpr,
// schedule.AfterDuration, schedule.Cron, etc. to build the TriggerSpec.
func WithStartTimer(t schedule.TriggerSpec) StartOption {
	return startFuncOpt{func(n *StartEvent) { n.Timer = t }}
}

// --- IntermediateCatchEvent options (renamed from the WithICE*/WithTimerDuration family) ---

type catchFuncOpt struct{ fn func(*IntermediateCatchEvent) }

func (o catchFuncOpt) applyCatch(n *IntermediateCatchEvent) { o.fn(n) }

// WithCatchTimer sets the Timer trigger on an IntermediateCatchEvent. Use
// schedule.AfterExpr, schedule.AfterDuration, schedule.Cron, etc. to build
// the TriggerSpec.
func WithCatchTimer(t schedule.TriggerSpec) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.Timer = t }}
}

// WithCatchSignal sets the signal reference (was WithSignalName).
func WithCatchSignal(name string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.SignalName = name }}
}

// WithCatchMessage sets MessageName and CorrelationKey (was WithMessageNameAndKey).
func WithCatchMessage(msg, key string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.MessageName, n.CorrelationKey = msg, key }}
}

// WithCatchDeadline sets the DeadlineTimer (schedule.TriggerSpec), DeadlineFlow,
// and DeadlineAction on an IntermediateCatchEvent. Use schedule.AfterDuration,
// schedule.AfterExpr, or any other TriggerSpec constructor.
func WithCatchDeadline(t schedule.TriggerSpec, flowID, action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) {
		n.DeadlineTimer, n.DeadlineFlow, n.DeadlineAction = t, flowID, action
	}}
}

// WithCatchWaitReminder sets the ReminderEvery (schedule.TriggerSpec) and ReminderAction
// on an IntermediateCatchEvent. Use schedule.Every, schedule.EveryExpr, or any
// other recurring TriggerSpec constructor.
func WithCatchWaitReminder(t schedule.TriggerSpec, action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.ReminderEvery, n.ReminderAction = t, action }}
}

// --- IntermediateThrowEvent options ---

// WithThrowSignal sets SignalName on an IntermediateThrowEvent.
func WithThrowSignal(name string) ThrowOption {
	return func(n *IntermediateThrowEvent) { n.SignalName = name }
}

// WithCompensateRef sets CompensateRef on an IntermediateThrowEvent.
func WithCompensateRef(ref string) ThrowOption {
	return func(n *IntermediateThrowEvent) { n.CompensateRef = ref }
}

// WithThrowName sets the display name on an IntermediateThrowEvent.
func WithThrowName(name string) ThrowOption {
	return func(n *IntermediateThrowEvent) { n.SetName(name) }
}

// --- BoundaryEvent options ---

type boundaryFuncOpt struct{ fn func(*BoundaryEvent) }

func (o boundaryFuncOpt) applyBoundary(n *BoundaryEvent) { o.fn(n) }

// WithBoundarySignal sets SignalName on a BoundaryEvent.
func WithBoundarySignal(name string) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.SignalName = name }}
}

// WithBoundaryMessage sets MessageName and CorrelationKey on a BoundaryEvent.
func WithBoundaryMessage(msg, key string) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.MessageName, n.CorrelationKey = msg, key }}
}

// WithBoundaryTimer sets the Timer trigger on a BoundaryEvent. Use
// schedule.AfterDuration, schedule.AfterExpr, schedule.Cron, etc. to build
// the TriggerSpec.
func WithBoundaryTimer(t schedule.TriggerSpec) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.Timer = t }}
}

// WithBoundaryErrorCode sets ErrorCode on a BoundaryEvent (empty = catch-all).
func WithBoundaryErrorCode(code string) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.ErrorCode = code }}
}

// WithBoundaryNonInterrupting marks a BoundaryEvent non-interrupting (was
// BoundaryNonInterrupting).
func WithBoundaryNonInterrupting() BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.NonInterrupting = true }}
}

// WithBoundaryAction attaches a fire-once catalog action run when the boundary
// fires (any trigger type). Result discarded; failure logs + continues routing.
func WithBoundaryAction(name string) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.Action = name }}
}

// WithBoundaryErrorExpr sets an expr-lang predicate deciding whether an error
// boundary catches, evaluated over the instance variables plus _error (the
// thrown error code string). Truthy = catch. Serializable. Precedence: applied
// after WithBoundaryErrorCheck, before WithBoundaryErrorCode.
func WithBoundaryErrorExpr(expr string) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.ErrorExpr = expr }}
}

// WithBoundaryErrorCheck sets a Go predicate (instance vars, thrown error)
// deciding whether an error boundary catches. Highest precedence; non-serializable
// (Go-authoring only). For action-thrown failures err is the ORIGINAL error
// (use errors.Is/As); for bare-code sources err.Error() == the code.
func WithBoundaryErrorCheck(fn func(map[string]any, error) bool) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.ErrorCheck = fn }}
}

// --- EventSubProcess options ---

type espFuncOpt struct{ fn func(*EventSubProcess) }

func (o espFuncOpt) applyEventSubProcess(n *EventSubProcess) { o.fn(n) }

// WithEventSubProcessNonInterrupting marks an EventSubProcess non-interrupting
// (was WithESPNonInterrupting).
func WithEventSubProcessNonInterrupting() EventSubProcessOption {
	return espFuncOpt{func(n *EventSubProcess) { n.NonInterrupting = true }}
}
