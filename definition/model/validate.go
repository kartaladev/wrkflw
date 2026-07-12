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
// workflow error and therefore may host a boundary error event.
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
	ErrNoStartEvent = errors.New("workflow-definition: no start event")
	// ErrMultipleManualStarts is returned when a definition has more than one
	// manual (trigger-less, caller-driven) start event (BPMN's none start event);
	// at most one is allowed. A manual start is a KindStartEvent whose MessageName,
	// SignalName, and Timer are all unset. Multiple event-triggered starts
	// (message/signal/timer) remain legal alongside it — see ErrAmbiguousStartTrigger
	// and ErrEventStartMissingTrigger for the per-start trigger rules (ADR-0121).
	ErrMultipleManualStarts = errors.New("workflow-definition: multiple manual start events")
	// ErrAmbiguousStartTrigger is returned when a start event sets more than one
	// trigger family (message/signal/timer). Exactly one family — or none — is
	// allowed per start event (ADR-0121).
	ErrAmbiguousStartTrigger = errors.New("workflow-definition: start event has ambiguous trigger")
	// ErrEventStartMissingTrigger is returned when a start event declares a
	// trigger family incompletely — currently: a non-empty CorrelationKey with
	// no MessageName, i.e. a message start missing its message name. Such a
	// start is neither a valid manual-start nor a valid message start, so it is
	// rejected rather than silently treated as none (ADR-0121).
	ErrEventStartMissingTrigger = errors.New("workflow-definition: event start missing trigger detail")
	ErrDanglingFlow             = errors.New("workflow-definition: flow references unknown node")
	ErrDeadEnd                  = errors.New("workflow-definition: non-end node has no outgoing flow")
	ErrStartHasIncoming         = errors.New("workflow-definition: start event has incoming flow")
	ErrEndHasOutgoing           = errors.New("workflow-definition: end event has outgoing flow")
	ErrConditionNotAllowed      = errors.New("workflow-definition: condition on flow from a non-conditional gateway")
	ErrDefaultNotAllowed        = errors.New("workflow-definition: default flow from a non-conditional gateway")
	ErrMultipleDefaults         = errors.New("workflow-definition: node has more than one default flow")
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
	// ErrMissingSubprocess is returned when a KindSubProcess node has a nil
	// Subprocess field. Embedded sub-process nodes (including a SubProcess acting
	// as an event sub-process) must carry their nested definition inline.
	ErrMissingSubprocess = errors.New("workflow-definition: subprocess node missing nested definition")
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
	// attached to an activity that cannot throw a workflow error. Only
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
	// ErrCompensateRefNotFound is returned when a KindCompensationThrowEvent node
	// carries a non-empty CompensateRef that does not match any node ID in the
	// enclosing process definition. The referenced node must exist so the engine
	// can resolve the compensation target at execution time.
	ErrCompensateRefNotFound = errors.New("workflow-definition: compensation throw references unknown node")
	// ErrScopeLocalWithCompensateRef is returned when a KindCompensationThrowEvent
	// node carries BOTH a non-empty CompensateRef (targeted throw) AND ScopeLocal
	// (WithScopeLocalCompensation). ScopeLocal narrows only the scope-wide (empty
	// CompensateRef) throw's root breadth; the engine ignores it on the targeted
	// branch, so the combination is a silent no-op. It is rejected at authoring
	// time to make the nonsensical combination inexpressible (ADR-0120).
	ErrScopeLocalWithCompensateRef = errors.New("workflow-definition: compensation throw cannot combine CompensateRef with scope-local compensation")
	// ErrInvalidVersion is returned by Validate when a (root) definition's
	// Version is below 1. Version 0 is reserved as the "latest" resolution
	// sentinel (see Qualifier), so an authored definition must use a concrete
	// Version >= 1.
	ErrInvalidVersion = errors.New("workflow-definition: definition version must be >= 1 (0 reserved as latest sentinel)")
	// ErrPayloadValidationRequiresMessage is returned when a
	// KindIntermediateCatchEvent declares payload validation but is not a
	// message catch. Only message-delivered payloads reach a single validatable
	// target at runtime; signal catches are broadcast (no single target) and
	// timer catches carry no payload, so the declared validation would be
	// silently skipped (fail-open). The combination is rejected at authoring
	// time to keep validation fail-closed.
	ErrPayloadValidationRequiresMessage = errors.New("workflow-definition: payload validation requires a message catch")
	// ErrDeadlineTriggerRecurring is returned when a node's DeadlineTimer
	// (set via WithWaitDeadline) is a recurring schedule.TriggerSpec (e.g.
	// Every, Cron, Daily). A deadline must fire at most once: the
	// DeadlineFlow/DeadlineAction breach only makes sense the first time the
	// wait overruns, so the trigger must be one-shot (AfterDuration, At, or
	// AfterExpr).
	ErrDeadlineTriggerRecurring = errors.New("workflow-definition: deadline trigger must be one-shot")
	// ErrCompletionActionUnsupportedKind is returned when a node's
	// CompletionAction is non-empty but the node's kind is not UserTask or
	// ReceiveTask — the only two kinds with an external completion trigger that
	// engine.completionActionOf honors. CompletionAction lives on the shared
	// ActivityFields embed, so it can be set on any activity kind via direct
	// construction or a hand-authored wire/YAML payload even though no
	// WithCompletionAction option targets those kinds; without this guard the
	// field would silently never run.
	ErrCompletionActionUnsupportedKind = errors.New("workflow-definition: completion action only supported on UserTask or ReceiveTask")
	// ErrDeadlineActionWithoutDeadline is returned when a node's DeadlineAction
	// is non-empty but its DeadlineTimer is zero (WithDeadlineAction used
	// without WithWaitDeadline). Without an armed deadline timer the action
	// would never fire, so the combination is rejected at authoring time.
	ErrDeadlineActionWithoutDeadline = errors.New("workflow-definition: deadline action set without a deadline timer")
	// ErrCompensateActionWithoutForwardAction is returned when a UserTask or
	// ReceiveTask node's CompensateAction is non-empty but its CompletionAction
	// is empty. For these two kinds, the completion action IS the forward
	// action: engine.handleActionCompleted records a compensation entry only
	// when a completion action runs (a UserTask/ReceiveTask never runs any
	// other action). Without a completion action, the node can never have
	// "done" anything to undo, so the compensate action is dead config — you
	// can only compensate a node that executed a forward action. Other
	// activity kinds (ServiceTask, BusinessRuleTask, SendTask, SubProcess,
	// CallActivity) always have their own forward action and are not gated by
	// this rule.
	ErrCompensateActionWithoutForwardAction = errors.New("workflow-definition: compensate action requires a forward action (completion action) on user/receive task")
	// ErrManualTaskValidation is returned when a UserTask marked Manual
	// (WithManual) also carries completion validation. A manual task completes
	// on a bare trigger with no payload, so there is no output to validate — the
	// combination is contradictory and rejected at authoring time. See ADR-0118.
	ErrManualTaskValidation = errors.New("workflow-definition: manual user task cannot carry completion validation")
	// ErrEventSubprocessOnFlow is returned when a KindSubProcess node whose
	// nested definition has an event-triggered (signal/message/timer) start
	// also carries an incoming or outgoing sequence flow. An event sub-process
	// is latent until its trigger fires — it is never entered by a token
	// flowing to it, and it resumes via its enclosing scope rather than
	// traversing its own sequence flows — so any incoming or outgoing flow on
	// one is unmodelable. An incoming flow makes authoring intent ambiguous
	// between "embedded sub-process" (token-driven, none start) and "event
	// sub-process" (trigger-driven, no flow); an outgoing flow is dead, and the
	// reachability seed would follow it and wrongly mark an otherwise-orphan
	// node reachable (escaping ErrUnreachableNode). Rejected at authoring time
	// rather than silently picking one interpretation (ADR-0122).
	ErrEventSubprocessOnFlow = errors.New("workflow-definition: event-triggered subprocess has incoming or outgoing sequence flow")
)

// Validate checks structural well-formedness of a process definition. It
// returns a joined error covering every violation found. The Version >= 1
// check applies only to the root definition — a nested subprocess definition
// is not independently resolved by qualifier and may legitimately be Version 0.
func Validate(d *ProcessDefinition) error {
	var errs []error
	if d.Version < 1 {
		errs = append(errs, fmt.Errorf("%w: got %d", ErrInvalidVersion, d.Version))
	}
	if err := validateStructure(d, make(map[*ProcessDefinition]bool)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// validateStructure is the recursive implementation of Validate with a
// visited-set cycle guard. If seen[d] is already true, the definition has
// already been visited in this call chain (cycle detected) and we return
// immediately to avoid a stack overflow on hand-constructed cyclic subprocess
// pointer graphs. Named distinctly from the imported definition/model/validate
// package to avoid a file-scope identifier collision.
func validateStructure(d *ProcessDefinition, seen map[*ProcessDefinition]bool) error {
	if seen[d] {
		return nil
	}
	seen[d] = true

	var errs []error

	// Start events (ADR-0121): a definition may have any number of start
	// events. At most one may be a trigger-less "none" start
	// (ErrMultipleManualStarts); each event-triggered start must set exactly one
	// trigger family — message, signal, or timer (ErrAmbiguousStartTrigger for
	// >1 set). A non-empty CorrelationKey with no MessageName is an
	// incompletely-specified message start, not a manual-start
	// (ErrEventStartMissingTrigger).
	starts := d.StartNodes()
	if len(starts) == 0 {
		errs = append(errs, ErrNoStartEvent)
	}
	var manualCount int
	for _, s := range starts {
		w := toWire(s)
		hasMessage := w.MessageName != ""
		hasSignal := w.SignalName != ""
		hasTimer := w.TimerTrigger != nil || w.TimerDuration != ""
		switch {
		case !hasMessage && w.CorrelationKey != "":
			errs = append(errs, fmt.Errorf("%w: node %q", ErrEventStartMissingTrigger, s.ID()))
		default:
			fams := 0
			if hasMessage {
				fams++
			}
			if hasSignal {
				fams++
			}
			if hasTimer {
				fams++
			}
			switch {
			case fams == 0:
				manualCount++
			case fams > 1:
				errs = append(errs, fmt.Errorf("%w: node %q", ErrAmbiguousStartTrigger, s.ID()))
			}
		}
	}
	if manualCount > 1 {
		errs = append(errs, ErrMultipleManualStarts)
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
		isEnd := n.Kind() == KindEndEvent
		// An event sub-process (a KindSubProcess whose inner start is
		// event-triggered) is not sequenced by flow: it is latent until its
		// trigger fires, runs its nested definition to its OWN end, and never
		// hands a token back to the enclosing graph via an outgoing sequence
		// flow. It is exempt from the outgoing-flow requirement the same way it
		// is exempt from the incoming-flow requirement (see the reachability-root
		// seed below).
		isEventSubprocessRoot := isEventTriggeredSubprocess(n)
		out := d.Outgoing(n.ID())
		in := d.Incoming(n.ID())

		if !isEnd && !isEventSubprocessRoot && len(out) == 0 {
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

	// Event-triggered SubProcess must not carry an incoming OR outgoing sequence
	// flow (ErrEventSubprocessOnFlow, ADR-0122): it is latent until its trigger
	// fires, never entered by a flowing token, and resumes via its enclosing
	// scope rather than traversing its own flows. An incoming flow is ambiguous
	// between "embedded" (token-driven) and "event sub-process" (trigger-driven)
	// semantics; an outgoing flow is dead and would let the reachability seed
	// (forwardReachable) wrongly mark an otherwise-orphan target reachable.
	for _, n := range d.Nodes {
		if isEventTriggeredSubprocess(n) && (len(d.Incoming(n.ID())) > 0 || len(d.Outgoing(n.ID())) > 0) {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrEventSubprocessOnFlow, n.ID()))
		}
	}

	// Reachability (ErrUnreachableNode). Runs whenever there is at least one
	// start event, over the UNION of forward-reachable sets from every start
	// (ADR-0121: multiple starts are legal, so reachability is well-defined for
	// any start count > 0). With 0 starts the start-count error already fires
	// and reachability is undefined, so we skip. Boundary events have no
	// incoming flow (reachable iff their host is reachable, to a fixpoint, since a
	// boundary branch may host another activity-with-boundary) and event-sub-processes
	// are event-triggered roots.
	var reached map[string]bool
	if starts := d.StartNodes(); len(starts) > 0 {
		reached = map[string]bool{}
		for _, s := range starts {
			for id := range forwardReachable(d, s.ID()) {
				reached[id] = true
			}
		}
		for _, n := range d.Nodes {
			if isEventTriggeredSubprocess(n) {
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
	// reached == nil means 0 start events: reachability is undefined and the
	// no-start-event error already fires, so we skip pairing entirely to avoid
	// noise on an already-invalid definition. With >=1 starts (ADR-0121)
	// reachability is well-defined via the union above, so pairing runs even
	// when the start configuration itself is otherwise invalid (e.g. multiple
	// manual-starts) — it is an independent structural rule.
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
	// may only attach to activities that can throw a workflow error: ServiceTask,
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
		// the host must be an error-throwing activity. Check both the canonical
		// nested TimerTrigger field (written by ToWire) and the legacy flat
		// TimerDuration string (decoded-only; written by older serializers).
		isErrorBoundary := w.TimerTrigger == nil && w.TimerDuration == "" && w.SignalName == "" && w.MessageName == ""
		if isErrorBoundary && !errorBoundaryHostKinds[host.Kind()] {
			errs = append(errs, fmt.Errorf("%w: boundary error event %q AttachedTo %q (kind %d)", ErrBoundaryErrorHost, n.ID(), w.AttachedTo, host.Kind()))
		}
	}

	// Sub-process and event-sub-process: Subprocess must be non-nil, and the
	// nested definition must itself be valid (recursive). Errors from the nested
	// definition are wrapped with the host node id so callers can trace which
	// sub-process contains the violation.
	for _, n := range d.Nodes {
		if n.Kind() != KindSubProcess {
			continue
		}
		sub := toWire(n).Subprocess
		if sub == nil {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMissingSubprocess, n.ID()))
			continue
		}
		if nestedErr := validateStructure(sub, seen); nestedErr != nil {
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

	// IntermediateCatchEvent payload validation is only meaningful for a message
	// catch: a message is delivered to a single correlated target that can be
	// validated before commit. Signal catches are broadcast (no single target)
	// and timer catches carry no payload, so a validation strategy declared on a
	// non-message catch would be silently skipped at runtime (fail-open). Reject
	// the combination at authoring time. model cannot import the leaf event
	// package, so a message catch is identified by a non-empty wire MessageName.
	for _, n := range d.Nodes {
		if n.Kind() != KindIntermediateCatchEvent {
			continue
		}
		if ValidationStrategyFor(n) != nil && toWire(n).MessageName == "" {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrPayloadValidationRequiresMessage, n.ID()))
		}
	}

	// DeadlineTimer: a deadline trigger (WithWaitDeadline, on activities and
	// IntermediateCatchEvent) must be one-shot. A recurring trigger (Every,
	// Cron, Daily, ...) would keep re-firing the same DeadlineFlow/Action
	// after the first breach, which is not a meaningful deadline semantics.
	// Nodes without a deadline (zero TriggerSpec) are skipped.
	for _, n := range d.Nodes {
		deadline, _, _ := DeadlineOf(n)
		if !deadline.IsZero() && deadline.Recurring() {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrDeadlineTriggerRecurring, n.ID()))
		}
	}

	// DeadlineAction without a DeadlineTimer: the action would never fire since
	// no deadline timer is ever armed. Nodes without a DeadlineAction are skipped.
	for _, n := range d.Nodes {
		deadline, _, deadlineAction := DeadlineOf(n)
		if deadlineAction != "" && deadline.IsZero() {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrDeadlineActionWithoutDeadline, n.ID()))
		}
	}

	// CompletionAction only supported on UserTask/ReceiveTask: the field lives on
	// the shared ActivityFields embed, so it can be set on any activity kind, but
	// engine.completionActionOf only honors it for the two kinds with an
	// external completion trigger.
	for _, n := range d.Nodes {
		if CompletionActionOf(n) == "" {
			continue
		}
		if n.Kind() != KindUserTask && n.Kind() != KindReceiveTask {
			errs = append(errs, fmt.Errorf("%w: node %q (kind %d)", ErrCompletionActionUnsupportedKind, n.ID(), n.Kind()))
		}
	}

	// CompensateAction on UserTask/ReceiveTask requires a forward action (their
	// CompletionAction): the completion action IS the forward action for these
	// two kinds, and the engine only records a compensation entry when a
	// completion action runs. Without it, the compensate action is dead config.
	for _, n := range d.Nodes {
		if n.Kind() != KindUserTask && n.Kind() != KindReceiveTask {
			continue
		}
		if CompensateActionOf(n) != "" && CompletionActionOf(n) == "" {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrCompensateActionWithoutForwardAction, n.ID()))
		}
	}

	// Manual UserTask must not carry completion validation: a manual task
	// completes with no payload, so a validation strategy would never receive
	// input to check. model cannot import the activity package, so Manual is
	// read via the wire projection. See ADR-0118.
	for _, n := range d.Nodes {
		if n.Kind() != KindUserTask {
			continue
		}
		if toWire(n).Manual && ValidationStrategyFor(n) != nil {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrManualTaskValidation, n.ID()))
		}
	}

	// CancelActions: reject empty action names.
	for i, name := range d.CancelActions {
		if name == "" {
			errs = append(errs, fmt.Errorf("%w: CancelActions[%d]", ErrEmptyCancelAction, i))
		}
	}

	// CompensateRef: a KindCompensationThrowEvent with a non-empty CompensateRef
	// must reference a node that exists in this definition. An empty
	// CompensateRef means "scope-wide compensation" and is always valid. This
	// rule recurses into sub-processes automatically (it lives inside validate).
	for _, n := range d.Nodes {
		if n.Kind() != KindCompensationThrowEvent {
			continue
		}
		w := toWire(n)
		compensateRef := w.CompensateRef
		if compensateRef == "" {
			continue
		}
		// A targeted throw (non-empty CompensateRef) must not also request
		// scope-local compensation: ScopeLocal applies only to the scope-wide
		// branch, so the combination is a silent no-op — reject it (ADR-0120).
		if w.CompensateScopeLocal {
			errs = append(errs, fmt.Errorf("%w: throw %q", ErrScopeLocalWithCompensateRef, n.ID()))
		}
		if _, ok := d.Node(compensateRef); !ok {
			errs = append(errs, fmt.Errorf("%w: throw %q -> %q", ErrCompensateRefNotFound, n.ID(), compensateRef))
		}
	}

	return errors.Join(errs...)
}

// isEventTriggeredSubprocess reports whether n is a KindSubProcess whose nested
// definition has an event-triggered (signal/message/timer) start. Model-space
// only — uses the wire projection because definition/model cannot import event
// (import cycle). A SubProcess whose inner start carries a trigger is an event
// sub-process (a reachability root, latent until its trigger fires); a
// SubProcess whose inner start is a plain "none" start is an embedded
// sub-process (token-driven inline). Returns false for a nil Subprocess
// (reported separately as ErrMissingSubprocess).
func isEventTriggeredSubprocess(n Node) bool {
	if n.Kind() != KindSubProcess {
		return false
	}
	sub := toWire(n).Subprocess
	if sub == nil {
		return false
	}
	for _, st := range sub.StartNodes() {
		w := toWire(st)
		if w.SignalName != "" || w.MessageName != "" || w.TimerTrigger != nil || w.TimerDuration != "" {
			return true
		}
	}
	return false
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
