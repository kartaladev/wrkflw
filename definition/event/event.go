// Package event holds the workflow event node kinds — start, end, terminate-end,
// error-end, intermediate catch/throw, boundary, and event sub-process — for the
// definition authoring layer. Import it to construct events (event.NewStart, …)
// and, via its init, to register their (de)serialization with the definition
// package.
package event

import (
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// --- concrete node types ---

// StartEvent is the workflow start event: the entry point of a process. As the
// trigger start of an EventSubProcess it may carry correlation fields.
type StartEvent struct {
	model.Base
	SignalName     string
	MessageName    string
	CorrelationKey string
	// Timer is the trigger spec for a timer-start event (e.g. schedule.AfterExpr("...")).
	Timer schedule.TriggerSpec
	// InputValidation, when set, validates the manually-provided start vars
	// (Drive) against this start event's contract before the instance is
	// created. Nil = no validation. Set via WithInputValidation.
	InputValidation validate.ValidationStrategy
}

// Kind returns model.KindStartEvent.
func (StartEvent) Kind() model.NodeKind { return model.KindStartEvent }

// TerminationOutcome selects the terminal status a force-termination end event
// drives the instance to.
type TerminationOutcome int

const (
	// OutcomeComplete ends the instance at StatusCompleted — a successful
	// business halt that cancels remaining parallel work.
	OutcomeComplete TerminationOutcome = iota
	// OutcomeAbort ends the instance at StatusTerminated — an abort.
	OutcomeAbort
)

// String returns the stable lowercase name of the outcome ("complete"/"abort"),
// used for wire encoding and logging.
func (o TerminationOutcome) String() string {
	switch o {
	case OutcomeAbort:
		return "abort"
	default:
		return "complete"
	}
}

// EndEvent is the workflow end event: a normal process completion point. When
// ForceTermination is set (via WithForceTermination) it instead terminates the
// whole instance — cancelling remaining parallel tokens, timers, boundaries,
// event sub-process arms, and open tasks — and ends at the Outcome-selected
// status carrying TerminationReason.
type EndEvent struct {
	model.Base
	// ForceTermination, when true, makes this end event terminate the entire
	// instance rather than just consuming its own token.
	ForceTermination bool
	// TerminationReason is a human-readable reason recorded on force-termination
	// (empty when ForceTermination is false).
	TerminationReason string
	// Outcome selects the terminal status on force-termination. Ignored when
	// ForceTermination is false.
	Outcome TerminationOutcome
}

// Kind returns model.KindEndEvent.
func (EndEvent) Kind() model.NodeKind { return model.KindEndEvent }

// TerminateEndEvent terminates the entire process (including parallel branches).
type TerminateEndEvent struct{ model.Base }

// Kind returns model.KindTerminateEndEvent.
func (TerminateEndEvent) Kind() model.NodeKind { return model.KindTerminateEndEvent }

// ErrorEndEvent throws a workflow error when reached, caught by a boundary error event.
type ErrorEndEvent struct {
	model.Base
	// ErrorCode is the workflow error code thrown (empty = anonymous catch-all).
	ErrorCode string
}

// Kind returns model.KindErrorEndEvent.
func (ErrorEndEvent) Kind() model.NodeKind { return model.KindErrorEndEvent }

// IntermediateCatchEvent waits for a timer, signal, or message. It can wait, so
// it embeds model.WaitFields (deadline escalation + reminders).
type IntermediateCatchEvent struct {
	model.Base
	model.WaitFields
	// Timer is the trigger spec for the timer catch (e.g. schedule.AfterExpr("...")).
	Timer          schedule.TriggerSpec
	SignalName     string
	MessageName    string
	CorrelationKey string
	// PayloadValidation, when set, validates a message catch's payload before
	// it is applied to the process instance's variables. Nil = no validation.
	// Set via WithPayloadValidation.
	PayloadValidation validate.ValidationStrategy
}

// Kind returns model.KindIntermediateCatchEvent.
func (IntermediateCatchEvent) Kind() model.NodeKind {
	return model.KindIntermediateCatchEvent
}

// IntermediateThrowEvent throws a signal or triggers a compensation.
type IntermediateThrowEvent struct {
	model.Base
	SignalName string
	// CompensateRef names the node whose compensation to run (empty = scope-wide).
	CompensateRef string
}

// Kind returns model.KindIntermediateThrowEvent.
func (IntermediateThrowEvent) Kind() model.NodeKind {
	return model.KindIntermediateThrowEvent
}

// BoundaryEvent is attached to an activity and fires on timer, signal, message,
// or error.
type BoundaryEvent struct {
	model.Base
	// AttachedTo is the ID of the host activity node.
	AttachedTo string
	// NonInterrupting controls interrupting behavior: false = interrupting (default).
	NonInterrupting bool
	ErrorCode       string
	SignalName      string
	MessageName     string
	CorrelationKey  string
	// Timer is the trigger spec for a timer boundary event (e.g. schedule.AfterDuration(72*time.Hour)).
	Timer schedule.TriggerSpec
	// Action is the optional catalog action name run fire-once (FireAndForget)
	// when this boundary fires, for any trigger type. Empty = no action.
	Action string
	// ErrorExpr is an optional expr-lang predicate (over instance vars + the
	// injected _error code string) deciding whether an ERROR boundary catches.
	// Serialized. See the Check→Expr→Code precedence in propagateError.
	ErrorExpr string
	// ErrorCheck is an optional Go predicate (vars, thrown error) deciding
	// whether an ERROR boundary catches. Highest precedence. Non-serializable
	// (Go-authoring-only escape hatch, like inline actions) — absent from wire.
	ErrorCheck func(map[string]any, error) bool
}

// Kind returns model.KindBoundaryEvent.
func (BoundaryEvent) Kind() model.NodeKind { return model.KindBoundaryEvent }

// EventSubProcess is an event-triggered subprocess rooted at an event start.
type EventSubProcess struct {
	model.Base
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *model.ProcessDefinition
	// NonInterrupting, when true, runs the event sub-process alongside the
	// enclosing scope without cancelling it (default false = interrupting).
	NonInterrupting bool
}

// Kind returns model.KindEventSubProcess.
func (EventSubProcess) Kind() model.NodeKind { return model.KindEventSubProcess }

// --- constructors ---

func optName(name []string) string {
	if len(name) > 0 {
		return name[0]
	}
	return ""
}

// NewStart constructs a StartEvent. Use WithName plus WithSignalName/
// WithMessageCorrelator/WithStartTimer to configure EventSubProcess triggers.
func NewStart(id string, opts ...StartOption) model.Node {
	n := StartEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyStart(&n)
	}
	return n
}

// NewEnd constructs an EndEvent. Use WithName for a display name and
// WithForceTermination to make it terminate the whole instance.
func NewEnd(id string, opts ...EndOption) model.Node {
	n := EndEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyEnd(&n)
	}
	return n
}

// NewTerminateEnd constructs a TerminateEndEvent. An optional name may be provided.
func NewTerminateEnd(id string, name ...string) model.Node {
	return TerminateEndEvent{model.NewBase(id, optName(name))}
}

// NewErrorEnd constructs an ErrorEndEvent. An optional name may be provided.
func NewErrorEnd(id, errorCode string, name ...string) model.Node {
	return ErrorEndEvent{model.NewBase(id, optName(name)), errorCode}
}

// NewIntermediateCatch constructs an IntermediateCatchEvent. Options can be WithCatchTimer,
// WithSignalName, WithMessageCorrelator, WithWaitDeadline, WithDeadlineAction, WithWaitAction,
// or WithName.
func NewIntermediateCatch(id string, opts ...CatchOption) model.Node {
	n := IntermediateCatchEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyCatch(&n)
	}
	return n
}

// NewIntermediateThrow constructs an IntermediateThrowEvent. Use WithThrowSignalName,
// WithCompensateRef, or WithThrowName.
func NewIntermediateThrow(id string, opts ...ThrowOption) model.Node {
	n := IntermediateThrowEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o(&n)
	}
	return n
}

// NewBoundary constructs a BoundaryEvent attached to the given host activity.
// Use WithSignalName, WithMessageCorrelator, WithBoundaryTimer/ErrorCode,
// WithBoundaryNonInterrupting, WithName.
func NewBoundary(id, attachedTo string, opts ...BoundaryOption) model.Node {
	n := BoundaryEvent{Base: model.NewBase(id, ""), AttachedTo: attachedTo}
	for _, o := range opts {
		o.applyBoundary(&n)
	}
	return n
}

// NewEventSubProcess constructs an EventSubProcess with the given id and nested
// model. Use WithEventSubProcessNonInterrupting and WithName.
func NewEventSubProcess(id string, sub *model.ProcessDefinition, opts ...EventSubProcessOption) model.Node {
	n := EventSubProcess{Base: model.NewBase(id, ""), Subprocess: sub}
	for _, o := range opts {
		o.applyEventSubProcess(&n)
	}
	return n
}

// --- serialization registration ---

func init() {
	model.RegisterKind(model.KindStartEvent, model.NodeSpec{
		Name: "startEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			n := StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey,
				Timer: model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false)}
			if w.Validation != nil {
				n.InputValidation = model.PendingValidation(*w.Validation)
			}
			return n
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(StartEvent)
			w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
			w.TimerTrigger = model.PutTrigger(v.Timer)
			w.Validation = model.PutValidation(v.InputValidation)
		},
		ValidationGet: func(n model.Node) validate.ValidationStrategy { return n.(StartEvent).InputValidation },
		ValidationSet: func(n model.Node, s validate.ValidationStrategy) model.Node {
			v := n.(StartEvent)
			v.InputValidation = s
			return v
		},
	})
	model.RegisterKind(model.KindEndEvent, model.NodeSpec{
		Name:     "endEvent",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return EndEvent{Base: b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(model.KindTerminateEndEvent, model.NodeSpec{
		Name:     "terminateEndEvent",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return TerminateEndEvent{b} },
		ToWire:   func(model.Node, *model.NodeWire) {},
	})
	model.RegisterKind(model.KindErrorEndEvent, model.NodeSpec{
		Name:     "errorEndEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node { return ErrorEndEvent{b, w.ErrorCode} },
		ToWire:   func(n model.Node, w *model.NodeWire) { w.ErrorCode = n.(ErrorEndEvent).ErrorCode },
	})
	model.RegisterKind(model.KindIntermediateCatchEvent, model.NodeSpec{
		Name: "intermediateCatchEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			n := IntermediateCatchEvent{Base: b, WaitFields: w.Wait(),
				Timer: model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false), SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
			if w.Validation != nil {
				n.PayloadValidation = model.PendingValidation(*w.Validation)
			}
			return n
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(IntermediateCatchEvent)
			w.TimerTrigger = model.PutTrigger(v.Timer)
			w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
			w.PutWait(v.WaitFields)
			w.Validation = model.PutValidation(v.PayloadValidation)
		},
		ValidationGet: func(n model.Node) validate.ValidationStrategy { return n.(IntermediateCatchEvent).PayloadValidation },
		ValidationSet: func(n model.Node, s validate.ValidationStrategy) model.Node {
			v := n.(IntermediateCatchEvent)
			v.PayloadValidation = s
			return v
		},
	})
	model.RegisterKind(model.KindIntermediateThrowEvent, model.NodeSpec{
		Name: "intermediateThrowEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return IntermediateThrowEvent{Base: b, SignalName: w.SignalName, CompensateRef: w.CompensateRef}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(IntermediateThrowEvent)
			w.SignalName, w.CompensateRef = v.SignalName, v.CompensateRef
		},
	})
	model.RegisterKind(model.KindBoundaryEvent, model.NodeSpec{
		Name: "boundaryEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return BoundaryEvent{Base: b, AttachedTo: w.AttachedTo, NonInterrupting: w.NonInterrupting, ErrorCode: w.ErrorCode,
				SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey,
				Timer:     model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false),
				Action:    w.BoundaryAction,
				ErrorExpr: w.BoundaryErrorExpr,
				// ErrorCheck is a Go closure — non-serializable, intentionally absent from wire.
			}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(BoundaryEvent)
			w.AttachedTo, w.NonInterrupting, w.ErrorCode = v.AttachedTo, v.NonInterrupting, v.ErrorCode
			w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
			w.TimerTrigger = model.PutTrigger(v.Timer)
			w.BoundaryAction, w.BoundaryErrorExpr = v.Action, v.ErrorExpr
			// ErrorCheck intentionally not written to wire — non-serializable Go closure.
		},
	})
	model.RegisterKind(model.KindEventSubProcess, model.NodeSpec{
		Name: "eventSubProcess",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return EventSubProcess{Base: b, Subprocess: w.Subprocess, NonInterrupting: w.NonInterrupting}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(EventSubProcess)
			w.Subprocess, w.NonInterrupting = v.Subprocess, v.NonInterrupting
		},
	})
}
