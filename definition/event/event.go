// Package event holds the BPMN event node kinds — start, end, terminate-end,
// error-end, intermediate catch/throw, boundary, and event sub-process — for the
// definition authoring layer. Import it to construct events (event.NewStart, …)
// and, via its init, to register their (de)serialization with the definition
// package.
package event

import "github.com/zakyalvan/krtlwrkflw/definition/model"

// --- concrete node types ---

// StartEvent is the BPMN start event: the entry point of a process. As the
// trigger start of an EventSubProcess it may carry correlation fields.
type StartEvent struct {
	model.Base
	SignalName     string
	MessageName    string
	CorrelationKey string
	TimerDuration  string
}

// Kind returns model.KindStartEvent.
func (StartEvent) Kind() model.NodeKind { return model.KindStartEvent }

// EndEvent is the BPMN end event: a normal process completion point.
type EndEvent struct{ model.Base }

// Kind returns model.KindEndEvent.
func (EndEvent) Kind() model.NodeKind { return model.KindEndEvent }

// TerminateEndEvent terminates the entire process (including parallel branches).
type TerminateEndEvent struct{ model.Base }

// Kind returns model.KindTerminateEndEvent.
func (TerminateEndEvent) Kind() model.NodeKind { return model.KindTerminateEndEvent }

// ErrorEndEvent throws a BPMN error when reached, caught by a boundary error event.
type ErrorEndEvent struct {
	model.Base
	// ErrorCode is the BPMN error code thrown (empty = anonymous catch-all).
	ErrorCode string
}

// Kind returns model.KindErrorEndEvent.
func (ErrorEndEvent) Kind() model.NodeKind { return model.KindErrorEndEvent }

// IntermediateCatchEvent waits for a timer, signal, or message. It can wait, so
// it embeds model.WaitFields (deadline escalation + reminders).
type IntermediateCatchEvent struct {
	model.Base
	model.WaitFields
	TimerDuration  string
	SignalName     string
	MessageName    string
	CorrelationKey string
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
	TimerDuration   string
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

// NewStart constructs a StartEvent. Use WithName plus WithStartSignal/
// WithStartMessage/WithStartTimer to configure EventSubProcess triggers.
func NewStart(id string, opts ...StartOption) model.Node {
	n := StartEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyStart(&n)
	}
	return n
}

// NewEnd constructs an EndEvent. An optional name may be provided.
func NewEnd(id string, name ...string) model.Node {
	return EndEvent{model.NewBase(id, optName(name))}
}

// NewTerminateEnd constructs a TerminateEndEvent. An optional name may be provided.
func NewTerminateEnd(id string, name ...string) model.Node {
	return TerminateEndEvent{model.NewBase(id, optName(name))}
}

// NewErrorEnd constructs an ErrorEndEvent. An optional name may be provided.
func NewErrorEnd(id, errorCode string, name ...string) model.Node {
	return ErrorEndEvent{model.NewBase(id, optName(name)), errorCode}
}

// NewCatch constructs an IntermediateCatchEvent. Options can be WithCatchTimer,
// WithCatchSignal, WithCatchMessage, WithCatchDeadline, WithCatchReminder, or WithName.
func NewCatch(id string, opts ...CatchOption) model.Node {
	n := IntermediateCatchEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyCatch(&n)
	}
	return n
}

// NewThrow constructs an IntermediateThrowEvent. Use WithThrowSignal,
// WithCompensateRef, or WithThrowName.
func NewThrow(id string, opts ...ThrowOption) model.Node {
	n := IntermediateThrowEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o(&n)
	}
	return n
}

// NewBoundary constructs a BoundaryEvent attached to the given host activity.
// Use WithBoundarySignal/Message/Timer/ErrorCode, WithBoundaryNonInterrupting, WithName.
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
			return StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(StartEvent)
			w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
		},
	})
	model.RegisterKind(model.KindEndEvent, model.NodeSpec{
		Name:     "endEvent",
		FromWire: func(b model.Base, _ model.NodeWire) model.Node { return EndEvent{b} },
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
			return IntermediateCatchEvent{Base: b, WaitFields: w.Wait(), TimerDuration: w.TimerDuration, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(IntermediateCatchEvent)
			w.TimerDuration, w.SignalName, w.MessageName, w.CorrelationKey = v.TimerDuration, v.SignalName, v.MessageName, v.CorrelationKey
			w.PutWait(v.WaitFields)
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
			return BoundaryEvent{Base: b, AttachedTo: w.AttachedTo, NonInterrupting: w.NonInterrupting, ErrorCode: w.ErrorCode, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(BoundaryEvent)
			w.AttachedTo, w.NonInterrupting, w.ErrorCode = v.AttachedTo, v.NonInterrupting, v.ErrorCode
			w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
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
