package model

import (
	"errors"
	"fmt"
)

// activityKinds is the set of NodeKinds that park execution and may host a
// boundary event. Gateways and events are not valid hosts.
var activityKinds = map[NodeKind]bool{
	KindServiceTask:      true,
	KindUserTask:         true,
	KindReceiveTask:      true,
	KindSendTask:         true,
	KindBusinessRuleTask: true,
	KindSubProcess:       true,
	KindCallActivity:     true,
}

var (
	ErrNoStartEvent        = errors.New("model: no start event")
	ErrMultipleStartEvents = errors.New("model: multiple start events")
	ErrDanglingFlow        = errors.New("model: flow references unknown node")
	ErrDeadEnd             = errors.New("model: non-end node has no outgoing flow")
	ErrStartHasIncoming    = errors.New("model: start event has incoming flow")
	ErrEndHasOutgoing      = errors.New("model: end event has outgoing flow")
	ErrConditionNotAllowed = errors.New("model: condition on flow from a non-conditional gateway")
	ErrDefaultNotAllowed   = errors.New("model: default flow from a non-conditional gateway")
	ErrMultipleDefaults    = errors.New("model: node has more than one default flow")
	// ErrEventGatewayTarget is returned when an outgoing flow from a
	// KindEventBasedGateway targets a node that is not a catch event.
	// Every outgoing flow from an event-based gateway must target a
	// KindIntermediateCatchEvent node.
	ErrEventGatewayTarget = errors.New("model: event-based gateway flow targets non-catch event node")
	// ErrBoundaryAttachment is returned when a KindBoundaryEvent node's
	// AttachedTo field does not reference an existing activity node.
	// Boundary events may only be attached to activity nodes
	// (KindServiceTask, KindUserTask, KindReceiveTask, KindSendTask,
	// KindBusinessRuleTask, KindSubProcess, KindCallActivity).
	ErrBoundaryAttachment = errors.New("model: boundary event attached to missing or non-activity node")
	// ErrMissingSubprocess is returned when a KindSubProcess or
	// KindEventSubProcess node has a nil Subprocess field. Embedded sub-process
	// and event-sub-process nodes must carry their nested definition inline.
	ErrMissingSubprocess = errors.New("model: subprocess or event-subprocess node missing nested definition")
	// ErrMissingDefRef is returned when a KindCallActivity node has an empty
	// DefRef field. A call-activity must name the top-level definition it
	// delegates to so the runtime registry can resolve it at execution time.
	ErrMissingDefRef = errors.New("model: call-activity node missing definition reference")
	// ErrMixedGateway is returned when a gateway node has both more than one
	// incoming flow and more than one outgoing flow. Such a gateway is
	// structurally ambiguous — it combines join and split semantics in a single
	// node, leading to silent mis-routing. Pure split (1-in/N-out), pure join
	// (N-in/1-out), and pass-through (1-in/1-out) remain valid. ADR-0014.
	ErrMixedGateway = errors.New("model: gateway both splits and joins")
	// ErrBoundaryErrorHost is returned when a boundary error event
	// (KindBoundaryEvent with no TimerDuration/SignalName/MessageName) is
	// attached to an activity that cannot throw a BPMN error. Only
	// KindServiceTask, KindSubProcess, and KindCallActivity may host a
	// boundary error event; user tasks and task variants are not valid hosts.
	ErrBoundaryErrorHost = errors.New("model: boundary error event attached to non-error-throwing activity")
	// ErrInvalidRetryPolicy is returned when a node's RetryPolicy carries
	// field values that violate the documented constraints: MaxAttempts must be
	// ≥ 0, InitialInterval and MaxInterval must be ≥ 0, and BackoffCoef must
	// be ≥ 1.0 whenever InitialInterval is positive (a coefficient below 1.0
	// would shrink delays on successive attempts instead of growing them).
	ErrInvalidRetryPolicy = errors.New("model: invalid retry policy")
	// ErrInvalidRecoveryFlow is returned when a node's RecoveryFlow names a
	// sequence-flow ID that does not exist in the process definition or whose
	// Source is not the node itself. A recovery flow must be a real outgoing
	// flow of the node that carries it.
	ErrInvalidRecoveryFlow = errors.New("model: invalid recovery flow")
)

// Validate checks structural well-formedness of a process definition. It
// returns a joined error covering every violation found.
func Validate(d *ProcessDefinition) error {
	return validate(d, make(map[*ProcessDefinition]bool))
}

// validate is the recursive implementation of Validate with a visited-set
// cycle guard. If seen[d] is already true, the definition has already been
// visited in this call chain (cycle detected) and we return immediately to
// avoid a stack overflow on hand-constructed cyclic subprocess pointer graphs.
func validate(d *ProcessDefinition, seen map[*ProcessDefinition]bool) error {
	if seen[d] {
		return nil
	}
	seen[d] = true

	var errs []error

	starts := d.StartNodes()
	switch {
	case len(starts) == 0:
		errs = append(errs, ErrNoStartEvent)
	case len(starts) > 1:
		errs = append(errs, fmt.Errorf("%w: %d found", ErrMultipleStartEvents, len(starts)))
	}

	for _, f := range d.Flows {
		if _, ok := d.Node(f.Source); !ok {
			errs = append(errs, fmt.Errorf("%w: flow %q source %q", ErrDanglingFlow, f.ID, f.Source))
		}
		if _, ok := d.Node(f.Target); !ok {
			errs = append(errs, fmt.Errorf("%w: flow %q target %q", ErrDanglingFlow, f.ID, f.Target))
		}
	}

	for _, n := range d.Nodes {
		isEnd := n.Kind == KindEndEvent || n.Kind == KindTerminateEndEvent || n.Kind == KindErrorEndEvent
		out := d.Outgoing(n.ID)
		in := d.Incoming(n.ID)

		if !isEnd && len(out) == 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrDeadEnd, n.ID))
		}
		if n.Kind == KindStartEvent && len(in) > 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrStartHasIncoming, n.ID))
		}
		if isEnd && len(out) > 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrEndHasOutgoing, n.ID))
		}
	}

	for _, n := range d.Nodes {
		conditional := n.Kind == KindExclusiveGateway || n.Kind == KindInclusiveGateway
		defaults := 0
		for _, f := range d.Outgoing(n.ID) {
			if f.Condition != "" && !conditional {
				errs = append(errs, fmt.Errorf("%w: flow %q from node %q", ErrConditionNotAllowed, f.ID, n.ID))
			}
			if f.IsDefault {
				if !conditional {
					errs = append(errs, fmt.Errorf("%w: flow %q from node %q", ErrDefaultNotAllowed, f.ID, n.ID))
				}
				defaults++
			}
		}
		if defaults > 1 {
			errs = append(errs, fmt.Errorf("%w: node %q has %d", ErrMultipleDefaults, n.ID, defaults))
		}
	}

	// Event-based gateway: every outgoing flow must target a catch event node.
	// A "catch event" is identified by KindIntermediateCatchEvent — the only
	// node kind capable of catching triggers (timer, signal, message) in this
	// model. Boundary events are attached nodes, not valid EBG targets.
	for _, n := range d.Nodes {
		if n.Kind != KindEventBasedGateway {
			continue
		}
		for _, f := range d.Outgoing(n.ID) {
			target, ok := d.Node(f.Target)
			if !ok {
				// Dangling flows are already reported; skip here to avoid duplicate noise.
				continue
			}
			if target.Kind != KindIntermediateCatchEvent {
				errs = append(errs, fmt.Errorf("%w: flow %q from event-based gateway %q targets %q (kind %d)", ErrEventGatewayTarget, f.ID, n.ID, f.Target, target.Kind))
			}
		}
	}

	// Mixed split+join gateway: a gateway with both >1 incoming and >1 outgoing
	// flows is structurally ambiguous and is rejected. Pure split (1-in/N-out),
	// pure join (N-in/1-out), and pass-through (1-in/1-out) are all valid.
	// ADR-0014.
	gatewayKinds := map[NodeKind]bool{
		KindExclusiveGateway:  true,
		KindInclusiveGateway:  true,
		KindParallelGateway:   true,
		KindEventBasedGateway: true,
	}
	for _, n := range d.Nodes {
		if !gatewayKinds[n.Kind] {
			continue
		}
		if len(d.Incoming(n.ID)) > 1 && len(d.Outgoing(n.ID)) > 1 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMixedGateway, n.ID))
		}
	}

	// errorBoundaryHostKinds is the subset of activityKinds that can throw a
	// BPMN error and therefore may host a boundary error event.
	errorBoundaryHostKinds := map[NodeKind]bool{
		KindServiceTask:  true,
		KindSubProcess:   true,
		KindCallActivity: true,
	}

	// Boundary events: AttachedTo must reference an existing activity node.
	// Activities are the node kinds that park execution and can host a boundary:
	// ServiceTask, UserTask, ReceiveTask, SendTask, BusinessRuleTask,
	// SubProcess, CallActivity. Gateways and events are not valid hosts.
	//
	// Additionally, a boundary ERROR event (no TimerDuration/SignalName/MessageName)
	// may only attach to activities that can throw a BPMN error: ServiceTask,
	// SubProcess, or CallActivity.
	for _, n := range d.Nodes {
		if n.Kind != KindBoundaryEvent {
			continue
		}
		host, ok := d.Node(n.AttachedTo)
		if !ok || !activityKinds[host.Kind] {
			errs = append(errs, fmt.Errorf("%w: boundary event %q AttachedTo %q", ErrBoundaryAttachment, n.ID, n.AttachedTo))
			continue // skip further checks — attachment itself is invalid
		}
		// If this is a boundary error event (no timer/signal/message trigger),
		// the host must be an error-throwing activity.
		isErrorBoundary := n.TimerDuration == "" && n.SignalName == "" && n.MessageName == ""
		if isErrorBoundary && !errorBoundaryHostKinds[host.Kind] {
			errs = append(errs, fmt.Errorf("%w: boundary error event %q AttachedTo %q (kind %d)", ErrBoundaryErrorHost, n.ID, n.AttachedTo, host.Kind))
		}
	}

	// Sub-process and event-sub-process: Subprocess must be non-nil, and the
	// nested definition must itself be valid (recursive). Errors from the nested
	// definition are wrapped with the host node id so callers can trace which
	// sub-process contains the violation.
	for _, n := range d.Nodes {
		if n.Kind != KindSubProcess && n.Kind != KindEventSubProcess {
			continue
		}
		if n.Subprocess == nil {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMissingSubprocess, n.ID))
			continue
		}
		if nestedErr := validate(n.Subprocess, seen); nestedErr != nil {
			errs = append(errs, fmt.Errorf("subprocess %q: %w", n.ID, nestedErr))
		}
	}

	// Call-activity: DefRef must be non-empty.
	for _, n := range d.Nodes {
		if n.Kind != KindCallActivity {
			continue
		}
		if n.DefRef == "" {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMissingDefRef, n.ID))
		}
	}

	// RetryPolicy and RecoveryFlow field-level constraints (all node kinds).
	for _, n := range d.Nodes {
		if n.RetryPolicy != nil {
			p := *n.RetryPolicy
			if p.MaxAttempts < 0 || p.InitialInterval < 0 || p.MaxInterval < 0 ||
				(p.InitialInterval > 0 && p.BackoffCoef < 1.0) {
				errs = append(errs, fmt.Errorf("%w: node %q", ErrInvalidRetryPolicy, n.ID))
			}
		}
		if n.RecoveryFlow != "" {
			found := false
			for _, f := range d.Flows {
				if f.ID == n.RecoveryFlow && f.Source == n.ID {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Errorf("%w: node %q flow %q", ErrInvalidRecoveryFlow, n.ID, n.RecoveryFlow))
			}
		}
	}

	return errors.Join(errs...)
}
