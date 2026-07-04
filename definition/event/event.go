// Package event holds the BPMN event node kinds — start, end, terminate-end,
// error-end, intermediate catch/throw, boundary, and event sub-process — for the
// definition authoring layer. Import it to construct events (event.NewStart, …)
// and, via its init, to register their (de)serialization with the definition
// package.
package event

import "github.com/zakyalvan/krtlwrkflw/definition"

// --- concrete node types ---

// StartEvent is the BPMN start event: the entry point of a process. As the
// trigger start of an EventSubProcess it may carry correlation fields.
type StartEvent struct {
	definition.Base
	SignalName     string
	MessageName    string
	CorrelationKey string
	TimerDuration  string
}

// Kind returns definition.KindStartEvent.
func (StartEvent) Kind() definition.NodeKind { return definition.KindStartEvent }

// EndEvent is the BPMN end event: a normal process completion point.
type EndEvent struct{ definition.Base }

// Kind returns definition.KindEndEvent.
func (EndEvent) Kind() definition.NodeKind { return definition.KindEndEvent }

// TerminateEndEvent terminates the entire process (including parallel branches).
type TerminateEndEvent struct{ definition.Base }

// Kind returns definition.KindTerminateEndEvent.
func (TerminateEndEvent) Kind() definition.NodeKind { return definition.KindTerminateEndEvent }

// ErrorEndEvent throws a BPMN error when reached, caught by a boundary error event.
type ErrorEndEvent struct {
	definition.Base
	// ErrorCode is the BPMN error code thrown (empty = anonymous catch-all).
	ErrorCode string
}

// Kind returns definition.KindErrorEndEvent.
func (ErrorEndEvent) Kind() definition.NodeKind { return definition.KindErrorEndEvent }

// IntermediateCatchEvent waits for a timer, signal, or message. It can wait, so
// it embeds definition.WaitFields (deadline escalation + reminders).
type IntermediateCatchEvent struct {
	definition.Base
	definition.WaitFields
	TimerDuration  string
	SignalName     string
	MessageName    string
	CorrelationKey string
}

// Kind returns definition.KindIntermediateCatchEvent.
func (IntermediateCatchEvent) Kind() definition.NodeKind {
	return definition.KindIntermediateCatchEvent
}

// IntermediateThrowEvent throws a signal or triggers a compensation.
type IntermediateThrowEvent struct {
	definition.Base
	SignalName string
	// CompensateRef names the node whose compensation to run (empty = scope-wide).
	CompensateRef string
}

// Kind returns definition.KindIntermediateThrowEvent.
func (IntermediateThrowEvent) Kind() definition.NodeKind {
	return definition.KindIntermediateThrowEvent
}

// BoundaryEvent is attached to an activity and fires on timer, signal, message,
// or error.
type BoundaryEvent struct {
	definition.Base
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

// Kind returns definition.KindBoundaryEvent.
func (BoundaryEvent) Kind() definition.NodeKind { return definition.KindBoundaryEvent }

// EventSubProcess is an event-triggered subprocess rooted at an event start.
type EventSubProcess struct {
	definition.Base
	// Subprocess is the nested process definition (must be non-nil).
	Subprocess *definition.ProcessDefinition
	// NonInterrupting, when true, runs the event sub-process alongside the
	// enclosing scope without cancelling it (default false = interrupting).
	NonInterrupting bool
}

// Kind returns definition.KindEventSubProcess.
func (EventSubProcess) Kind() definition.NodeKind { return definition.KindEventSubProcess }

// --- constructors ---

func optName(name []string) string {
	if len(name) > 0 {
		return name[0]
	}
	return ""
}

// NewStart constructs a StartEvent. Use WithName plus WithStartSignal/
// WithStartMessage/WithStartTimer to configure EventSubProcess triggers.
func NewStart(id string, opts ...StartOption) definition.Node {
	n := StartEvent{Base: definition.NewBase(id, "")}
	for _, o := range opts {
		o.applyStart(&n)
	}
	return n
}

// NewEnd constructs an EndEvent. An optional name may be provided.
func NewEnd(id string, name ...string) definition.Node {
	return EndEvent{definition.NewBase(id, optName(name))}
}

// NewTerminateEnd constructs a TerminateEndEvent. An optional name may be provided.
func NewTerminateEnd(id string, name ...string) definition.Node {
	return TerminateEndEvent{definition.NewBase(id, optName(name))}
}

// NewErrorEnd constructs an ErrorEndEvent. An optional name may be provided.
func NewErrorEnd(id, errorCode string, name ...string) definition.Node {
	return ErrorEndEvent{definition.NewBase(id, optName(name)), errorCode}
}

// NewCatch constructs an IntermediateCatchEvent. Options can be WithCatchTimer,
// WithCatchSignal, WithCatchMessage, WithCatchDeadline, WithCatchReminder, or WithName.
func NewCatch(id string, opts ...CatchOption) definition.Node {
	n := IntermediateCatchEvent{Base: definition.NewBase(id, "")}
	for _, o := range opts {
		o.applyCatch(&n)
	}
	return n
}

// NewThrow constructs an IntermediateThrowEvent. Use WithThrowSignal,
// WithCompensateRef, or WithThrowName.
func NewThrow(id string, opts ...ThrowOption) definition.Node {
	n := IntermediateThrowEvent{Base: definition.NewBase(id, "")}
	for _, o := range opts {
		o(&n)
	}
	return n
}

// NewBoundary constructs a BoundaryEvent attached to the given host activity.
// Use WithBoundarySignal/Message/Timer/ErrorCode, WithBoundaryNonInterrupting, WithName.
func NewBoundary(id, attachedTo string, opts ...BoundaryOption) definition.Node {
	n := BoundaryEvent{Base: definition.NewBase(id, ""), AttachedTo: attachedTo}
	for _, o := range opts {
		o.applyBoundary(&n)
	}
	return n
}

// NewEventSubProcess constructs an EventSubProcess with the given id and nested
// definition. Use WithEventSubProcessNonInterrupting and WithName.
func NewEventSubProcess(id string, sub *definition.ProcessDefinition, opts ...EventSubProcessOption) definition.Node {
	n := EventSubProcess{Base: definition.NewBase(id, ""), Subprocess: sub}
	for _, o := range opts {
		o.applyEventSubProcess(&n)
	}
	return n
}

// --- serialization registration ---

func init() {
	definition.RegisterKind(definition.KindStartEvent, definition.NodeSpec{
		Name: "startEvent",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(StartEvent)
			w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
		},
	})
	definition.RegisterKind(definition.KindEndEvent, definition.NodeSpec{
		Name:     "endEvent",
		FromWire: func(b definition.Base, _ definition.NodeWire) definition.Node { return EndEvent{b} },
		ToWire:   func(definition.Node, *definition.NodeWire) {},
	})
	definition.RegisterKind(definition.KindTerminateEndEvent, definition.NodeSpec{
		Name:     "terminateEndEvent",
		FromWire: func(b definition.Base, _ definition.NodeWire) definition.Node { return TerminateEndEvent{b} },
		ToWire:   func(definition.Node, *definition.NodeWire) {},
	})
	definition.RegisterKind(definition.KindErrorEndEvent, definition.NodeSpec{
		Name:     "errorEndEvent",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node { return ErrorEndEvent{b, w.ErrorCode} },
		ToWire:   func(n definition.Node, w *definition.NodeWire) { w.ErrorCode = n.(ErrorEndEvent).ErrorCode },
	})
	definition.RegisterKind(definition.KindIntermediateCatchEvent, definition.NodeSpec{
		Name: "intermediateCatchEvent",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return IntermediateCatchEvent{Base: b, WaitFields: w.Wait(), TimerDuration: w.TimerDuration, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(IntermediateCatchEvent)
			w.TimerDuration, w.SignalName, w.MessageName, w.CorrelationKey = v.TimerDuration, v.SignalName, v.MessageName, v.CorrelationKey
			w.PutWait(v.WaitFields)
		},
	})
	definition.RegisterKind(definition.KindIntermediateThrowEvent, definition.NodeSpec{
		Name: "intermediateThrowEvent",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return IntermediateThrowEvent{Base: b, SignalName: w.SignalName, CompensateRef: w.CompensateRef}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(IntermediateThrowEvent)
			w.SignalName, w.CompensateRef = v.SignalName, v.CompensateRef
		},
	})
	definition.RegisterKind(definition.KindBoundaryEvent, definition.NodeSpec{
		Name: "boundaryEvent",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return BoundaryEvent{Base: b, AttachedTo: w.AttachedTo, NonInterrupting: w.NonInterrupting, ErrorCode: w.ErrorCode, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey, TimerDuration: w.TimerDuration}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(BoundaryEvent)
			w.AttachedTo, w.NonInterrupting, w.ErrorCode = v.AttachedTo, v.NonInterrupting, v.ErrorCode
			w.SignalName, w.MessageName, w.CorrelationKey, w.TimerDuration = v.SignalName, v.MessageName, v.CorrelationKey, v.TimerDuration
		},
	})
	definition.RegisterKind(definition.KindEventSubProcess, definition.NodeSpec{
		Name: "eventSubProcess",
		FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
			return EventSubProcess{Base: b, Subprocess: w.Subprocess, NonInterrupting: w.NonInterrupting}
		},
		ToWire: func(n definition.Node, w *definition.NodeWire) {
			v := n.(EventSubProcess)
			w.Subprocess, w.NonInterrupting = v.Subprocess, v.NonInterrupting
		},
	})
}
