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

// gatewayKinds is the set of gateway NodeKinds (used by the mixed split+join
// check). errorBoundaryHostKinds is the subset of activityKinds that can throw a
// BPMN error and therefore may host a boundary error event.
var (
	gatewayKinds = map[NodeKind]bool{
		KindExclusiveGateway:  true,
		KindInclusiveGateway:  true,
		KindParallelGateway:   true,
		KindEventBasedGateway: true,
	}
	errorBoundaryHostKinds = map[NodeKind]bool{
		KindServiceTask:  true,
		KindSubProcess:   true,
		KindCallActivity: true,
	}
)

var (
	ErrNoStartEvent        = errors.New("workflow-definition: no start event")
	ErrMultipleStartEvents = errors.New("workflow-definition: multiple start events")
	ErrDanglingFlow        = errors.New("workflow-definition: flow references unknown node")
	ErrDeadEnd             = errors.New("workflow-definition: non-end node has no outgoing flow")
	ErrStartHasIncoming    = errors.New("workflow-definition: start event has incoming flow")
	ErrEndHasOutgoing      = errors.New("workflow-definition: end event has outgoing flow")
	ErrConditionNotAllowed = errors.New("workflow-definition: condition on flow from a non-conditional gateway")
	ErrDefaultNotAllowed   = errors.New("workflow-definition: default flow from a non-conditional gateway")
	ErrMultipleDefaults    = errors.New("workflow-definition: node has more than one default flow")
	// ErrEventGatewayTarget is returned when an outgoing flow from a
	// KindEventBasedGateway targets a node that is not a catch event.
	// Every outgoing flow from an event-based gateway must target a
	// KindIntermediateCatchEvent node.
	ErrEventGatewayTarget = errors.New("workflow-definition: event-based gateway flow targets non-catch event node")
	// ErrBoundaryAttachment is returned when a KindBoundaryEvent node's
	// AttachedTo field does not reference an existing activity node.
	// Boundary events may only be attached to activity nodes
	// (KindServiceTask, KindUserTask, KindReceiveTask, KindSendTask,
	// KindBusinessRuleTask, KindSubProcess, KindCallActivity).
	ErrBoundaryAttachment = errors.New("workflow-definition: boundary event attached to missing or non-activity node")
	// ErrMissingSubprocess is returned when a KindSubProcess or
	// KindEventSubProcess node has a nil Subprocess field. Embedded sub-process
	// and event-sub-process nodes must carry their nested definition inline.
	ErrMissingSubprocess = errors.New("workflow-definition: subprocess or event-subprocess node missing nested definition")
	// ErrMissingDefRef is returned when a KindCallActivity node has an empty
	// DefRef field. A call-activity must name the top-level definition it
	// delegates to so the runtime registry can resolve it at execution time.
	ErrMissingDefRef = errors.New("workflow-definition: call-activity node missing definition reference")
	// ErrMixedGateway is returned when a gateway node has both more than one
	// incoming flow and more than one outgoing flow. Such a gateway is
	// structurally ambiguous — it combines join and split semantics in a single
	// node, leading to silent mis-routing. Pure split (1-in/N-out), pure join
	// (N-in/1-out), and pass-through (1-in/1-out) remain valid. ADR-0014.
	ErrMixedGateway = errors.New("workflow-definition: gateway both splits and joins")
	// ErrBoundaryErrorHost is returned when a boundary error event
	// (KindBoundaryEvent with no TimerDuration/SignalName/MessageName) is
	// attached to an activity that cannot throw a BPMN error. Only
	// KindServiceTask, KindSubProcess, and KindCallActivity may host a
	// boundary error event; user tasks and task variants are not valid hosts.
	ErrBoundaryErrorHost = errors.New("workflow-definition: boundary error event attached to non-error-throwing activity")
	// ErrInvalidRetryPolicy is returned when a node's RetryPolicy carries
	// field values that violate the documented constraints: MaxAttempts must be
	// ≥ 0, InitialInterval and MaxInterval must be ≥ 0, and BackoffCoef must
	// be ≥ 1.0 whenever InitialInterval is positive (a coefficient below 1.0
	// would shrink delays on successive attempts instead of growing them).
	ErrInvalidRetryPolicy = errors.New("workflow-definition: invalid retry policy")
	// ErrInvalidRecoveryFlow is returned when a node's RecoveryFlow names a
	// sequence-flow ID that does not exist in the process definition or whose
	// Source is not the node itself. A recovery flow must be a real outgoing
	// flow of the node that carries it.
	ErrInvalidRecoveryFlow = errors.New("workflow-definition: invalid recovery flow")
	// ErrEmptyCancelAction is returned when a process definition's CancelActions
	// slice contains an empty string. All action names must be non-empty.
	ErrEmptyCancelAction = errors.New("workflow-definition: empty cancel action name")
	// ErrUnreachableNode is returned when a node cannot be reached from the start
	// event — directly via sequence flows, or via a reachable boundary event or an
	// event-sub-process (an event-triggered root). It signals dead/orphan structure.
	ErrUnreachableNode = errors.New("workflow-definition: unreachable node")
	// ErrUnpairedJoin is returned when a parallel join gateway has no concurrency
	// source — no parallel/inclusive split can deliver two concurrent tokens toward
	// it — so it would deadlock at runtime waiting for branches that never arrive.
	ErrUnpairedJoin = errors.New("workflow-definition: unpaired parallel join")
	// ErrCompensateRefNotFound is returned when a KindIntermediateThrowEvent node
	// carries a non-empty CompensateRef that does not match any node ID in the
	// enclosing process definition. The referenced node must exist so the engine
	// can resolve the compensation target at execution time.
	ErrCompensateRefNotFound = errors.New("workflow-definition: compensation throw references unknown node")
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
		isEnd := n.Kind() == KindEndEvent || n.Kind() == KindTerminateEndEvent || n.Kind() == KindErrorEndEvent
		out := d.Outgoing(n.ID())
		in := d.Incoming(n.ID())

		if !isEnd && len(out) == 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrDeadEnd, n.ID()))
		}
		if n.Kind() == KindStartEvent && len(in) > 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrStartHasIncoming, n.ID()))
		}
		if isEnd && len(out) > 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrEndHasOutgoing, n.ID()))
		}
	}

	for _, n := range d.Nodes {
		conditional := n.Kind() == KindExclusiveGateway || n.Kind() == KindInclusiveGateway
		defaults := 0
		for _, f := range d.Outgoing(n.ID()) {
			if f.Condition != "" && !conditional {
				errs = append(errs, fmt.Errorf("%w: flow %q from node %q", ErrConditionNotAllowed, f.ID, n.ID()))
			}
			if f.IsDefault {
				if !conditional {
					errs = append(errs, fmt.Errorf("%w: flow %q from node %q", ErrDefaultNotAllowed, f.ID, n.ID()))
				}
				defaults++
			}
		}
		if defaults > 1 {
			errs = append(errs, fmt.Errorf("%w: node %q has %d", ErrMultipleDefaults, n.ID(), defaults))
		}
	}

	// Event-based gateway: every outgoing flow must target a catch event node.
	// A "catch event" is identified by KindIntermediateCatchEvent — the only
	// node kind capable of catching triggers (timer, signal, message) in this
	// model. Boundary events are attached nodes, not valid EBG targets.
	for _, n := range d.Nodes {
		if n.Kind() != KindEventBasedGateway {
			continue
		}
		for _, f := range d.Outgoing(n.ID()) {
			target, ok := d.Node(f.Target)
			if !ok {
				// Dangling flows are already reported; skip here to avoid duplicate noise.
				continue
			}
			if target.Kind() != KindIntermediateCatchEvent {
				errs = append(errs, fmt.Errorf("%w: flow %q from event-based gateway %q targets %q (kind %d)", ErrEventGatewayTarget, f.ID, n.ID(), f.Target, target.Kind()))
			}
		}
	}

	// Mixed split+join gateway: a gateway with both >1 incoming and >1 outgoing
	// flows is structurally ambiguous and is rejected. Pure split (1-in/N-out),
	// pure join (N-in/1-out), and pass-through (1-in/1-out) are all valid.
	// ADR-0014.
	for _, n := range d.Nodes {
		if !gatewayKinds[n.Kind()] {
			continue
		}
		if len(d.Incoming(n.ID())) > 1 && len(d.Outgoing(n.ID())) > 1 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMixedGateway, n.ID()))
		}
	}

	// Reachability (ErrUnreachableNode). Runs only with exactly one start event;
	// with 0 or >1 starts the start-count error already fires and reachability is
	// ill-defined, so we skip to avoid cascade noise. Boundary events have no
	// incoming flow (reachable iff their host is reachable, to a fixpoint, since a
	// boundary branch may host another activity-with-boundary) and event-sub-processes
	// are event-triggered roots.
	var reached map[string]bool
	if starts := d.StartNodes(); len(starts) == 1 {
		reached = forwardReachable(d, starts[0].ID())
		for _, n := range d.Nodes {
			if n.Kind() == KindEventSubProcess {
				for id := range forwardReachable(d, n.ID()) {
					reached[id] = true
				}
			}
		}
		for {
			grew := false
			for _, n := range d.Nodes {
				if n.Kind() != KindBoundaryEvent {
					continue
				}
				attachedTo := toWire(n).AttachedTo
				if reached[n.ID()] || !reached[attachedTo] {
					continue
				}
				for id := range forwardReachable(d, n.ID()) {
					if !reached[id] {
						reached[id] = true
						grew = true
					}
				}
			}
			if !grew {
				break
			}
		}
		for _, n := range d.Nodes {
			if !reached[n.ID()] {
				errs = append(errs, fmt.Errorf("%w: node %q", ErrUnreachableNode, n.ID()))
			}
		}
	}

	// Parallel-join pairing (ErrUnpairedJoin). Only KindParallelGateway joins can
	// deadlock: they wait for a token on every incoming flow unconditionally.
	// Exclusive/event-based joins fire on first arrival, and inclusive joins
	// self-adjust via runtime reachability — none deadlock, so they are excluded.
	// A parallel join is flagged iff no parallel/inclusive split can deliver two
	// concurrent tokens toward it (a provable deadlock). Conservative: any plausible
	// concurrency source clears the join (favouring no false positives). Unreachable
	// joins are skipped — ErrUnreachableNode already reports them.
	//
	// reached == nil means 0 or >1 start events: reachability is ill-defined and the
	// start-count error already fires, so we skip pairing entirely to avoid noise on
	// an already-invalid definition (it is re-checked once the start count is fixed).
	if reached != nil {
		for _, n := range d.Nodes {
			if n.Kind() != KindParallelGateway {
				continue
			}
			if len(d.Incoming(n.ID())) <= 1 || len(d.Outgoing(n.ID())) != 1 {
				continue // not a pure parallel join (mixed already rejected; split is fine)
			}
			if !reached[n.ID()] {
				continue // unreachable join — ErrUnreachableNode already reports it
			}
			if !hasConcurrencySource(d, n.ID()) {
				errs = append(errs, fmt.Errorf("%w: node %q", ErrUnpairedJoin, n.ID()))
			}
		}
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
		if n.Kind() != KindBoundaryEvent {
			continue
		}
		w := toWire(n)
		host, hok := d.Node(w.AttachedTo)
		if !hok || !activityKinds[host.Kind()] {
			errs = append(errs, fmt.Errorf("%w: boundary event %q AttachedTo %q", ErrBoundaryAttachment, n.ID(), w.AttachedTo))
			continue // skip further checks — attachment itself is invalid
		}
		// If this is a boundary error event (no timer/signal/message trigger),
		// the host must be an error-throwing activity.
		isErrorBoundary := w.TimerDuration == "" && w.SignalName == "" && w.MessageName == ""
		if isErrorBoundary && !errorBoundaryHostKinds[host.Kind()] {
			errs = append(errs, fmt.Errorf("%w: boundary error event %q AttachedTo %q (kind %d)", ErrBoundaryErrorHost, n.ID(), w.AttachedTo, host.Kind()))
		}
	}

	// Sub-process and event-sub-process: Subprocess must be non-nil, and the
	// nested definition must itself be valid (recursive). Errors from the nested
	// definition are wrapped with the host node id so callers can trace which
	// sub-process contains the violation.
	for _, n := range d.Nodes {
		if n.Kind() != KindSubProcess && n.Kind() != KindEventSubProcess {
			continue
		}
		sub := toWire(n).Subprocess
		if sub == nil {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMissingSubprocess, n.ID()))
			continue
		}
		if nestedErr := validate(sub, seen); nestedErr != nil {
			errs = append(errs, fmt.Errorf("subprocess %q: %w", n.ID(), nestedErr))
		}
	}

	// Call-activity: DefRef must be non-empty.
	for _, n := range d.Nodes {
		if n.Kind() == KindCallActivity && toWire(n).DefRef == "" {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMissingDefRef, n.ID()))
		}
	}

	// RetryPolicy and RecoveryFlow field-level constraints (activity nodes only).
	for _, n := range d.Nodes {
		rp := RetryPolicyOf(n)
		if rp != nil {
			p := *rp
			if p.MaxAttempts < 0 || p.InitialInterval < 0 || p.MaxInterval < 0 ||
				(p.InitialInterval > 0 && p.BackoffCoef < 1.0) {
				errs = append(errs, fmt.Errorf("%w: node %q", ErrInvalidRetryPolicy, n.ID()))
			}
		}
		rf := recoveryFlowOf(n)
		if rf != "" {
			found := false
			for _, f := range d.Flows {
				if f.ID == rf && f.Source == n.ID() {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Errorf("%w: node %q flow %q", ErrInvalidRecoveryFlow, n.ID(), rf))
			}
		}
	}

	// CancelActions: reject empty action names.
	for i, name := range d.CancelActions {
		if name == "" {
			errs = append(errs, fmt.Errorf("%w: CancelActions[%d]", ErrEmptyCancelAction, i))
		}
	}

	// CompensateRef: a KindIntermediateThrowEvent with a non-empty CompensateRef
	// must reference a node that exists in this definition. An empty CompensateRef
	// means "scope-wide compensation" and is always valid. This rule recurses into
	// sub-processes automatically (it lives inside validate).
	for _, n := range d.Nodes {
		if n.Kind() != KindIntermediateThrowEvent {
			continue
		}
		compensateRef := toWire(n).CompensateRef
		if compensateRef == "" {
			continue
		}
		if _, ok := d.Node(compensateRef); !ok {
			errs = append(errs, fmt.Errorf("%w: throw %q -> %q", ErrCompensateRefNotFound, n.ID(), compensateRef))
		}
	}

	return errors.Join(errs...)
}

// forwardReachable returns the set of node IDs reachable from seed by following
// outgoing sequence flows (BFS, cycle-safe via the visited set). seed is included.
func forwardReachable(d *ProcessDefinition, seed string) map[string]bool {
	reached := map[string]bool{seed: true}
	queue := []string{seed}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, f := range d.Outgoing(n) {
			if !reached[f.Target] {
				reached[f.Target] = true
				queue = append(queue, f.Target)
			}
		}
	}
	return reached
}

// hasConcurrencySource reports whether some parallel or inclusive split (a
// gateway with >1 outgoing flow) has at least two distinct outgoing branches
// whose targets can each forward-reach joinID. Only parallel/inclusive splits
// create concurrency; exclusive and event-based splits take a single branch, so
// they are not concurrency sources.
func hasConcurrencySource(d *ProcessDefinition, joinID string) bool {
	for _, f := range d.Nodes {
		if f.ID() == joinID {
			continue
		}
		if f.Kind() != KindParallelGateway && f.Kind() != KindInclusiveGateway {
			continue
		}
		out := d.Outgoing(f.ID())
		if len(out) <= 1 {
			continue // a join or pass-through, not a split
		}
		count := 0
		for _, b := range out {
			if forwardReachable(d, b.Target)[joinID] {
				count++
				if count >= 2 {
					return true
				}
			}
		}
	}
	return false
}

// recoveryFlowOf returns the RecoveryFlow field of an activity node, or "" if
// the node does not carry one.
func recoveryFlowOf(n Node) string {
	if a, ok := n.(interface{ recoveryFlow() string }); ok {
		return a.recoveryFlow()
	}
	return ""
}
