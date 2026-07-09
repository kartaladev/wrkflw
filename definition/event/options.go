package event

import (
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

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

// --- WithMessageCorrelator (Start, Catch, Boundary) ---

type messageCorrelatorOpt struct{ msg, key string }

func (o messageCorrelatorOpt) applyStart(n *StartEvent) {
	n.MessageName, n.CorrelationKey = o.msg, o.key
}
func (o messageCorrelatorOpt) applyCatch(n *IntermediateCatchEvent) {
	n.MessageName, n.CorrelationKey = o.msg, o.key
}
func (o messageCorrelatorOpt) applyBoundary(n *BoundaryEvent) {
	n.MessageName, n.CorrelationKey = o.msg, o.key
}

// WithMessageCorrelator sets the message name and correlation key on a start,
// catch, or boundary event.
func WithMessageCorrelator(msg, key string) interface {
	StartOption
	CatchOption
	BoundaryOption
} {
	return messageCorrelatorOpt{msg, key}
}

// --- WithSignalName (Start, Catch, Boundary) ---

type signalNameOpt struct{ name string }

func (o signalNameOpt) applyStart(n *StartEvent)             { n.SignalName = o.name }
func (o signalNameOpt) applyCatch(n *IntermediateCatchEvent) { n.SignalName = o.name }
func (o signalNameOpt) applyBoundary(n *BoundaryEvent)       { n.SignalName = o.name }

// WithSignalName sets the signal reference on a start, catch, or boundary
// event (was the three separate per-kind signal setters).
func WithSignalName(name string) interface {
	StartOption
	CatchOption
	BoundaryOption
} {
	return signalNameOpt{name}
}

// --- StartEvent options (EventSubProcess triggers) ---

type startFuncOpt struct{ fn func(*StartEvent) }

func (o startFuncOpt) applyStart(n *StartEvent) { o.fn(n) }

// WithStartTimer sets the Timer trigger on a StartEvent. Use schedule.AfterExpr,
// schedule.AfterDuration, schedule.Cron, etc. to build the TriggerSpec.
func WithStartTimer(t schedule.TriggerSpec) StartOption {
	return startFuncOpt{func(n *StartEvent) { n.Timer = t }}
}

type inputValidationOpt struct{ s validate.ValidationStrategy }

func (o inputValidationOpt) applyStart(n *StartEvent) { n.InputValidation = o.s }

// WithInputValidation validates the manually-provided start vars (Drive)
// against the start event's contract before the instance is created.
func WithInputValidation(s validate.ValidationStrategy) StartOption {
	return inputValidationOpt{s: s}
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

// WithWaitDeadline sets the DeadlineTimer (schedule.TriggerSpec) and DeadlineFlow
// on an IntermediateCatchEvent — the trigger governing when the deadline fires
// and the sequence flow taken on breach. Use schedule.AfterDuration,
// schedule.AfterExpr, or any other TriggerSpec constructor. Pair with
// WithDeadlineAction to also run an action on breach.
func WithWaitDeadline(t schedule.TriggerSpec, flowID string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) {
		n.DeadlineTimer, n.DeadlineFlow = t, flowID
	}}
}

// WithDeadlineAction sets the optional action.Action name invoked on deadline
// breach, in addition to (or instead of) taking DeadlineFlow.
func WithDeadlineAction(action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.DeadlineAction = action }}
}

// WithWaitAction sets the WaitEvery (schedule.TriggerSpec) and WaitAction
// on an IntermediateCatchEvent — the in-wait action run periodically while the
// event is pending. Use schedule.Every, schedule.EveryExpr, or any other
// recurring TriggerSpec constructor.
func WithWaitAction(t schedule.TriggerSpec, action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.WaitEvery, n.WaitAction = t, action }}
}

type catchPayloadValidationOpt struct{ s validate.ValidationStrategy }

func (o catchPayloadValidationOpt) applyCatch(n *IntermediateCatchEvent) { n.PayloadValidation = o.s }

// WithPayloadValidation validates a message IntermediateCatchEvent's payload
// before it is applied.
func WithPayloadValidation(s validate.ValidationStrategy) CatchOption {
	return catchPayloadValidationOpt{s: s}
}

// --- IntermediateThrowEvent options ---

// WithThrowSignalName sets SignalName on an IntermediateThrowEvent.
func WithThrowSignalName(name string) ThrowOption {
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
// thrown error code string). Truthy = catch. Serializable.
//
// Precedence: if ErrorCheck is set, it is the SOLE decider and WithBoundaryErrorExpr
// is NOT consulted (even if ErrorCheck returns false — no fallthrough). If
// ErrorCheck is absent, WithBoundaryErrorExpr is the SOLE decider; WithBoundaryErrorCode
// is NOT consulted regardless of the Expr result.
//
// Summary: Check → Expr → Code; the FIRST configured tier decides; there is no
// fallthrough between tiers.
func WithBoundaryErrorExpr(expr string) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.ErrorExpr = expr }}
}

// WithBoundaryErrorCheck sets a Go predicate (instance vars, thrown error)
// deciding whether an error boundary catches. Highest precedence; non-serializable
// (Go-authoring only). For action-thrown failures err is the ORIGINAL error
// (use errors.Is/As); for bare-code sources err.Error() == the code.
//
// Precedence: when ErrorCheck is set, it is the SOLE decider — returning false
// is a definitive no-catch; WithBoundaryErrorExpr and WithBoundaryErrorCode are
// NOT consulted. There is no fallthrough: the first configured tier (Check, then
// Expr, then Code) decides exclusively.
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
