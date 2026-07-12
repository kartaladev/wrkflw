// Package event holds the workflow event node kinds — start, end, error-end,
// intermediate catch/throw, and boundary — for the definition authoring layer.
// Import it to construct events (event.NewStart, …) and, via its init, to
// register their (de)serialization with the definition package. An event
// sub-process is authored as an activity.SubProcess whose inner start is
// event-triggered (ADR-0122); it is not a distinct kind.
package event

import (
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/model/validate"
	"github.com/kartaladev/wrkflw/definition/schedule"
)

// --- concrete node types ---

// StartEvent is the workflow start event: the entry point of a process. As the
// event-triggered inner start of a SubProcess acting as an event sub-process it
// may carry correlation fields.
type StartEvent struct {
	model.Base
	SignalName     string
	MessageName    string
	CorrelationKey string
	// MessageStartSingleton, when true, makes a KEYLESS message-start create at
	// most one instance ever for its message name (name-only deterministic id +
	// ErrInstanceExists no-op). Default false: each message mints a fresh
	// instance (BPMN fan-in). Ignored when a correlation key is set — keyed
	// message-start already dedups per key. Set via WithMessageStartSingleton.
	MessageStartSingleton bool
	// Timer is the trigger spec for a timer-start event (e.g. schedule.AfterExpr("...")).
	Timer schedule.TriggerSpec
	// InputValidation, when set, validates the manually-provided start vars
	// (Drive) against this start event's contract before the instance is
	// created. Nil = no validation. Set via WithInputValidation.
	InputValidation validate.ValidationStrategy
	// NonInterrupting applies only when this start is the event-triggered inner
	// start of a SubProcess acting as an event sub-process: false (default) =
	// interrupting (the event sub-process cancels and replaces its enclosing
	// scope); true = non-interrupting (runs alongside). It has no effect on a
	// root / manual start. Set via WithNonInterrupting.
	NonInterrupting bool
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

// EndBehavior selects what an EndEvent does when a token reaches it. It mirrors
// BPMN's optional end event definition — none, terminate, or error — which are
// mutually exclusive (an end event carries at most one).
type EndBehavior int

const (
	// EndNormal is a plain completion point (BPMN: no event definition).
	EndNormal EndBehavior = iota
	// EndTerminate force-terminates the whole instance (ADR-0119). Payload:
	// TerminationReason + Outcome.
	EndTerminate
	// EndError throws a workflow error caught by a boundary error event (BPMN
	// error end event). Payload: ErrorCode.
	EndError
)

// String returns the stable lowercase name ("normal"/"terminate"/"error"),
// used for wire encoding and logging.
func (b EndBehavior) String() string {
	switch b {
	case EndTerminate:
		return "terminate"
	case EndError:
		return "error"
	default:
		return "normal"
	}
}

// EndEvent is the workflow end event: a normal process completion point. Its
// Behavior discriminator (ADR-0127) selects one of three behaviors. With
// EndTerminate (via WithForceTermination) it terminates the whole instance —
// cancelling remaining parallel tokens, timers, boundaries, event sub-process
// arms, and open tasks — and ends at the Outcome-selected status carrying
// TerminationReason. With EndError (via WithErrorCode) it throws ErrorCode as a
// workflow error caught by an enclosing boundary error event.
type EndEvent struct {
	model.Base
	// Behavior selects what happens when a token reaches this end event
	// (ADR-0127). EndNormal (default) completes; EndTerminate force-terminates
	// the instance; EndError throws ErrorCode.
	Behavior EndBehavior
	// TerminationReason is recorded on EndTerminate (empty otherwise).
	TerminationReason string
	// Outcome selects the terminal status on EndTerminate. Ignored otherwise.
	Outcome TerminationOutcome
	// ErrorCode is the workflow error thrown on EndError ("" = anonymous
	// catch-all). Ignored unless Behavior == EndError.
	ErrorCode string
}

// Kind returns model.KindEndEvent.
func (EndEvent) Kind() model.NodeKind { return model.KindEndEvent }

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

// IntermediateThrowEvent throws a signal (broadcast to every waiting instance).
// Compensation throws are a separate node kind — see CompensationThrowEvent.
type IntermediateThrowEvent struct {
	model.Base
	SignalName string
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
	// (a Go-authoring-only escape hatch) — absent from wire.
	ErrorCheck func(map[string]any, error) bool
}

// Kind returns model.KindBoundaryEvent.
func (BoundaryEvent) Kind() model.NodeKind { return model.KindBoundaryEvent }

// CompensationThrowEvent triggers intra-process compensation when reached. It
// runs completed compensable activities' compensation actions in reverse order,
// then continues past the throw (it does NOT terminate). With CompensateRef set
// it targets a specific completed sub-process node's archived records; empty
// CompensateRef is scope-wide (the throwing scope's completed compensable
// activities). Unlike a signal throw (IntermediateThrowEvent), compensation
// never crosses process boundaries.
type CompensationThrowEvent struct {
	model.Base
	// CompensateRef names a completed sub-process node whose archived compensation
	// records to run (targeted). Empty = scope-wide.
	CompensateRef string
	// ScopeLocal narrows a scope-wide throw at the ROOT scope to root-direct
	// compensable activities, excluding records archived from completed
	// sub-processes. Default false = whole-instance (BPMN-conformant). Ignored for
	// a targeted throw and at a sub-process scope.
	ScopeLocal bool
}

// Kind returns model.KindCompensationThrowEvent.
func (CompensationThrowEvent) Kind() model.NodeKind { return model.KindCompensationThrowEvent }

// --- constructors ---

// NewStart constructs a StartEvent. Use WithName plus WithSignalName/
// WithMessageCorrelator/WithStartTimer to configure an event-triggered
// (signal/message/timer) start, e.g. the inner start of an event sub-process.
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

// NewIntermediateThrow constructs an IntermediateThrowEvent. Use WithThrowSignalName
// or WithThrowName. For compensation, use NewCompensateThrow instead.
func NewIntermediateThrow(id string, opts ...ThrowOption) model.Node {
	n := IntermediateThrowEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o(&n)
	}
	return n
}

// NewCompensateThrow constructs a compensation throw. With no options it is a
// scope-wide, whole-instance throw; WithCompensateRef makes it targeted,
// WithScopeLocalCompensation narrows the root breadth, WithCompensateThrowName
// sets a display name.
func NewCompensateThrow(id string, opts ...CompensateThrowOption) model.Node {
	n := CompensationThrowEvent{Base: model.NewBase(id, "")}
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

// --- serialization registration ---

func init() {
	model.RegisterKind(model.KindStartEvent, model.NodeSpec{
		Name: "startEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			n := StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey,
				MessageStartSingleton: w.MessageStartSingleton,
				Timer:                 model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false),
				NonInterrupting:       w.NonInterrupting}
			if w.Validation != nil {
				n.InputValidation = model.PendingValidation(*w.Validation)
			}
			return n
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(StartEvent)
			w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
			w.MessageStartSingleton = v.MessageStartSingleton
			w.TimerTrigger = model.PutTrigger(v.Timer)
			w.Validation = model.PutValidation(v.InputValidation)
			w.NonInterrupting = v.NonInterrupting
		},
		ValidationGet: func(n model.Node) validate.ValidationStrategy { return n.(StartEvent).InputValidation },
		ValidationSet: func(n model.Node, s validate.ValidationStrategy) model.Node {
			v := n.(StartEvent)
			v.InputValidation = s
			return v
		},
	})
	model.RegisterKind(model.KindEndEvent, model.NodeSpec{
		Name: "endEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			e := EndEvent{Base: b}
			switch w.EndBehavior {
			case "terminate":
				e.Behavior = EndTerminate
				e.TerminationReason = w.TerminationReason
				e.Outcome = OutcomeComplete
				if w.TerminationOutcome == "abort" {
					e.Outcome = OutcomeAbort
				}
			case "error":
				e.Behavior = EndError
				e.ErrorCode = w.ErrorCode
			}
			return e
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(EndEvent)
			w.EndBehavior = ""
			switch v.Behavior {
			case EndTerminate:
				w.EndBehavior = "terminate"
				w.TerminationReason = v.TerminationReason
				w.TerminationOutcome = v.Outcome.String()
			case EndError:
				w.EndBehavior = "error"
				w.ErrorCode = v.ErrorCode
			}
		},
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
			return IntermediateThrowEvent{Base: b, SignalName: w.SignalName}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			w.SignalName = n.(IntermediateThrowEvent).SignalName
		},
	})
	model.RegisterKind(model.KindCompensationThrowEvent, model.NodeSpec{
		Name: "compensationThrowEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return CompensationThrowEvent{Base: b, CompensateRef: w.CompensateRef, ScopeLocal: w.CompensateScopeLocal}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(CompensationThrowEvent)
			w.CompensateRef, w.CompensateScopeLocal = v.CompensateRef, v.ScopeLocal
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
}
