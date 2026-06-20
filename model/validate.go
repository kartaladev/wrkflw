package model

import (
	"errors"
	"fmt"
)

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
)

// Validate checks structural well-formedness of a process definition. It
// returns a joined error covering every violation found.
func Validate(d *ProcessDefinition) error {
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

	// Boundary events: AttachedTo must reference an existing activity node.
	// Activities are the node kinds that park execution and can host a boundary:
	// ServiceTask, UserTask, ReceiveTask, SendTask, BusinessRuleTask,
	// SubProcess, CallActivity. Gateways and events are not valid hosts.
	activityKinds := map[NodeKind]bool{
		KindServiceTask:      true,
		KindUserTask:         true,
		KindReceiveTask:      true,
		KindSendTask:         true,
		KindBusinessRuleTask: true,
		KindSubProcess:       true,
		KindCallActivity:     true,
	}
	for _, n := range d.Nodes {
		if n.Kind != KindBoundaryEvent {
			continue
		}
		host, ok := d.Node(n.AttachedTo)
		if !ok || !activityKinds[host.Kind] {
			errs = append(errs, fmt.Errorf("%w: boundary event %q AttachedTo %q", ErrBoundaryAttachment, n.ID, n.AttachedTo))
		}
	}

	return errors.Join(errs...)
}
