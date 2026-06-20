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

	return errors.Join(errs...)
}
