package model

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
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
	// and ErrEventStartMissingTrigger for the per-start trigger rules.
	ErrMultipleManualStarts = errors.New("workflow-definition: multiple manual start events")
	// ErrAmbiguousStartTrigger is returned when a start event sets more than one
	// trigger family (message/signal/timer). Exactly one family — or none — is
	// allowed per start event.
	ErrAmbiguousStartTrigger = errors.New("workflow-definition: start event has ambiguous trigger")
	// ErrEventStartMissingTrigger is returned when a start event declares a
	// trigger family incompletely — currently: a non-empty CorrelationKey with
	// no MessageName, i.e. a message start missing its message name. Such a
	// start is neither a valid manual-start nor a valid message start, so it is
	// rejected rather than silently treated as none.
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
	// (N-in/1-out), and pass-through (1-in/1-out) remain valid.
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
	// time to make the nonsensical combination inexpressible.
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
	// ErrEmptyMessageName is returned when a ReceiveTask's MessageName is empty
	// or whitespace-only. The two sub-cases have different rationale:
	//
	// An EMPTY name is the genuine defect this rule exists to close.
	// receiveTaskStrategy.enter (engine/step_nodes.go:97-99) assigns
	// tok.AwaitMessage = rt.MessageName UNCONDITIONALLY — unlike the
	// catch-event and boundary paths, which guard != "" — so such a node
	// parks its token on AwaitMessage "", and once an empty identity key
	// matches no record, no MessageReceived can ever resume it.
	//
	// A WHITESPACE-ONLY name is NOT that defect: a token parked on e.g.
	// AwaitMessage "   " remains resumable by an exact-equal
	// MessageReceived{Name: "   "}, since the engine's guards reject
	// only "", never a non-empty whitespace string. It is rejected here as
	// authoring hygiene, not a leak fix: not a name any operator can
	// reasonably produce or correlate on, so the shape is made
	// unrepresentable at authoring time rather than merely unmatched.
	ErrEmptyMessageName = errors.New("workflow-definition: receive task requires a message name")
	// ErrBlankEventName is returned when a node declares a SignalName or
	// MessageName consisting only of whitespace. Such a name is non-empty, so
	// it survives the definition's event-kind discriminators (:271-272, :503,
	// :701) undetected, then parks a token on a name no operator can
	// reasonably produce or match against — the whitespace analogue of the
	// empty key closed at the engine's state layer. A declared event
	// name must carry at least one visible character; an ABSENT name ("") is
	// unaffected — several kinds rely on "" meaning "no trigger at all" (a
	// manual start, an error boundary).
	ErrBlankEventName = errors.New("workflow-definition: event name must not be blank")
	// ErrDeadlineTriggerRecurring is returned when a node's DeadlineTimer
	// (set via WithWaitDeadline) is a recurring schedule.TriggerSpec (e.g.
	// Every, Cron, Daily). A deadline must fire at most once: the
	// DeadlineFlow/DeadlineAction breach only makes sense the first time the
	// wait overruns, so the trigger must be one-shot (AfterDuration, At, or
	// AfterExpr).
	ErrDeadlineTriggerRecurring = errors.New("workflow-definition: deadline trigger must be one-shot")
	// ErrTriggerNeverDue is returned when a node carries a timer or in-wait
	// trigger that can never fire at any anchor — schedule.Every(0),
	// schedule.Daily(0), a Monthly day-of-month outside -31..-1 or 1..31, and
	// the rest of schedule.TriggerSpec.NeverDue's decided set. Such a
	// trigger arms a durable timer the scheduler then refuses, parking the
	// instance forever, so it is rejected while the definition is authored
	// instead of at run time.
	//
	// The rule is SOUND but deliberately NOT COMPLETE, exactly as NeverDue is:
	// anchor-dependent calendar specs (Monthly(12, []int{31})), cron, and the
	// engine-resolved expression forms are NOT judged here and stay the arm-time
	// guard's business. Deadline triggers are out of scope for a different
	// reason — no never-due one-shot deadline is reachable through the model
	// (a zero schedule.At converts to a due After(0)), and the recurring half is
	// already rejected by ErrDeadlineTriggerRecurring.
	ErrTriggerNeverDue = errors.New("workflow-definition: trigger can never fire")
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
	// combination is contradictory and rejected at authoring time.
	ErrManualTaskValidation = errors.New("workflow-definition: manual user task cannot carry completion validation")
	// ErrEmptyOutcome is returned when a UserTask declares a blank (empty or
	// whitespace-only) completion outcome. A blank outcome can never be selected
	// — the engine treats an empty outcome as "none given" — so it is dead
	// config rejected at authoring time.
	ErrEmptyOutcome = errors.New("workflow-definition: user task declares a blank outcome")
	// ErrDuplicateOutcome is returned when a UserTask declares the same
	// completion outcome twice. The declaration is a set; a duplicate entry
	// signals an authoring mistake.
	ErrDuplicateOutcome = errors.New("workflow-definition: user task declares a duplicate outcome")
	// ErrInvalidOutcomeVariable is returned when a UserTask's explicit outcome
	// variable name (WithOutcomeVariable) is not a valid expr identifier. The
	// exposed variable exists to be referenced from gateway conditions, which
	// are expr expressions, so a name expr cannot resolve is dead config.
	ErrInvalidOutcomeVariable = errors.New("workflow-definition: outcome variable is not a valid identifier")
	// ErrManualTaskOutcome is returned when a UserTask marked Manual
	// (WithManual) also declares completion outcomes or opts into outcome
	// exposure. A manual task completes on a bare trigger carrying no outcome —
	// the engine fails closed on one — so the declaration could never be
	// satisfied.
	ErrManualTaskOutcome = errors.New("workflow-definition: manual user task cannot declare completion outcomes")
	// ErrOutcomeExposureWithoutOutcomes is returned when a UserTask opts into
	// outcome exposure (WithExposeOutcome or WithOutcomeVariable) without
	// declaring the outcome set it exposes. Exposure publishes the outcome into
	// the process-variable space, where it can feed gateway conditions and
	// eligibility expressions; without a declared set that variable's value is
	// whatever free-text string an actor happened to send. Exposing a value
	// domain requires closing it first, so the combination is rejected at
	// authoring time rather than yielding an unbounded routing input at runtime.
	// A manual task is diagnosed by ErrManualTaskOutcome instead — it may not
	// declare outcomes at all.
	ErrOutcomeExposureWithoutOutcomes = errors.New("workflow-definition: outcome exposure requires a declared outcome set")
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
	// rather than silently picking one interpretation.
	ErrEventSubprocessOnFlow = errors.New("workflow-definition: event-triggered subprocess has incoming or outgoing sequence flow")
	// ErrDuplicateNodeID is returned when two nodes of the same process
	// definition share an ID. A node ID is the definition's lookup key:
	// ProcessDefinition.Node is a first-wins linear scan, so the second node
	// sharing an ID is permanently unreachable, while Outgoing and Incoming
	// filter the flows by that same string and therefore return the union of
	// both nodes' flows. The result is silent misrouting rather than a failure,
	// so the duplicate is rejected at authoring time.
	//
	// Uniqueness is scoped to a single definition: every lookup is per
	// ProcessDefinition, so a nested sub-process definition may legitimately
	// reuse an ID from its parent. Blank IDs are keys like any other and two
	// blank-ID nodes shadow each other in exactly the same way, so they are
	// reported too.
	ErrDuplicateNodeID = errors.New("workflow-definition: duplicate node id")
	// ErrDuplicateFlowID is returned when two sequence flows of the same process
	// definition share a non-blank ID. Flow IDs identify a flow in diagnostics
	// and in the RecoveryFlow / DeadlineFlow references, so a reused ID makes
	// those references ambiguous.
	//
	// A blank flow ID is legal — flow.SequenceFlow literals may omit it and
	// nothing resolves a flow by ID on the execution path — so any number of
	// blank-ID flows is accepted. Flow IDs live in their own namespace: a flow
	// may share its ID with a node. Like ErrDuplicateNodeID, uniqueness is
	// scoped to a single definition.
	ErrDuplicateFlowID = errors.New("workflow-definition: duplicate flow id")
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

	// Identity, checked before anything reads an ID: a node ID is this
	// definition's lookup key (d.Node is a first-wins linear scan, and
	// d.Outgoing/d.Incoming filter the flows by the same string), so a duplicate
	// shadows a node instead of failing. A flow ID identifies a flow in
	// diagnostics and in the RecoveryFlow/DeadlineFlow references. Uniqueness is
	// per definition — a nested sub-process has its own lookups and is checked
	// by the recursive call below. A blank flow ID is legal (see
	// ErrDuplicateFlowID) and never counts as a duplicate; a blank node ID is a
	// key like any other, so blank duplicates are reported.
	nodeIDs := make(map[string]bool, len(d.Nodes))
	for _, n := range d.Nodes {
		id := n.ID()
		if nodeIDs[id] {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrDuplicateNodeID, id))
			continue
		}
		nodeIDs[id] = true
	}
	flowIDs := make(map[string]bool, len(d.Flows))
	for _, f := range d.Flows {
		if f.ID == "" {
			continue
		}
		if flowIDs[f.ID] {
			errs = append(errs, fmt.Errorf("%w: flow %q", ErrDuplicateFlowID, f.ID))
			continue
		}
		flowIDs[f.ID] = true
	}

	// Start events: a definition may have any number of start
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
	for _, n := range d.Nodes {
		if !gatewayKinds[n.Kind()] {
			continue
		}
		if len(d.Incoming(n.ID())) > 1 && len(d.Outgoing(n.ID())) > 1 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMixedGateway, n.ID()))
		}
	}

	// Event-triggered SubProcess must not carry an incoming OR outgoing sequence
	// flow (ErrEventSubprocessOnFlow): it is latent until its trigger
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
	// (multiple starts are legal, so reachability is well-defined for
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
	// noise on an already-invalid definition. With >=1 starts
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

	// ReceiveTask: the node waits for a NAMED message, and MessageName == ""
	// and MessageName == "   " are rejected for different reasons — see
	// ErrEmptyMessageName's doc comment. In short: an EMPTY name parks the
	// token on AwaitMessage "" (engine/step_nodes.go:97-99 assigns it
	// unconditionally, unlike the catch-event and boundary paths), and an
	// empty identity key matches no record, so the token could
	// never be resumed. A WHITESPACE-ONLY name remains resumable in
	// principle — it is rejected as authoring hygiene, not to close a leak.
	// Reject both shapes at authoring time. model cannot import the leaf
	// activity package, so the name is read from the node's wire form.
	//
	// A ReceiveTask with a whitespace-only MessageName also trips
	// ErrBlankEventName below (that loop does not exclude KindReceiveTask):
	// intentional — the "at most one ErrBlankEventName per node"
	// dedup requirement is scoped to that rule alone, not to
	// cross-rule exclusivity, so a definition can carry both errors at once.
	for _, n := range d.Nodes {
		if n.Kind() != KindReceiveTask {
			continue
		}
		if strings.TrimSpace(toWire(n).MessageName) == "" {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrEmptyMessageName, n.ID()))
		}
	}

	// A DECLARED event name must not be whitespace-only. This deliberately does
	// not fire on an absent name: "" is how a node says it has no signal/message
	// at all, and several kinds rely on that (a manual start, an error
	// boundary). Only a name that was written but carries no visible character
	// is rejected. At most one ErrBlankEventName is reported per
	// node even when both SignalName and MessageName are blank.
	//
	// This must NEVER be confused with, or replace, the event-kind
	// discriminators elsewhere in this file (hasSignal/hasMessage above,
	// isErrorBoundary below, isEventTriggeredSubprocess) — those stay on the
	// bare != ""/== "" comparison. Trimming a discriminator would silently
	// RECLASSIFY a node (e.g. a boundary with SignalName " " turning into an
	// error boundary); this rule only REJECTS the definition, so a
	// reclassification question never arises for it.
	for _, n := range d.Nodes {
		w := toWire(n)
		blankSignal := w.SignalName != "" && strings.TrimSpace(w.SignalName) == ""
		blankMessage := w.MessageName != "" && strings.TrimSpace(w.MessageName) == ""
		if blankSignal || blankMessage {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrBlankEventName, n.ID()))
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

	// Never-due timer and in-wait triggers. Both fields reach the
	// same durable-arm path — a timer trigger through the node's own arm, an
	// in-wait trigger through engine.armWaitReminder's ScheduleTimer{Kind:
	// TimerInWait} — where a spec that can never fire parks the instance on a
	// timer that never arrives. Reject it while the definition is authored.
	//
	// This loop lives INSIDE validateStructure, not in Validate, so it reaches
	// nested sub-processes: Validate is called on the root only, and a nested
	// definition is visited solely through this function's own recursion, which
	// wraps the error with the host node id.
	//
	// The timer trigger is read from the wire form because there is no TimerOf
	// accessor and there cannot be one: the timer lives on the leaf event types
	// and model -> event is a real import cycle. The in-wait trigger uses
	// WaitActionOf, the same accessor engine.armWaitReminder reads, so the gate
	// and the arm path cannot disagree about which spec is being judged. Zero
	// specs (no trigger at all) are skipped, as the deadline rules above skip
	// nodes without a deadline.
	//
	// DeadlineTimer is deliberately NOT gated here; see ErrTriggerNeverDue.
	for _, n := range d.Nodes {
		w := toWire(n)
		if timer := ReadTrigger(w.TimerTrigger, w.TimerDuration, false); !timer.IsZero() && timer.NeverDue() {
			errs = append(errs, fmt.Errorf("%w: timer trigger on node %q", ErrTriggerNeverDue, n.ID()))
		}
		if wait, _ := WaitActionOf(n); !wait.IsZero() && wait.NeverDue() {
			errs = append(errs, fmt.Errorf("%w: in-wait trigger on node %q", ErrTriggerNeverDue, n.ID()))
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

	// UserTask rules, in a single pass over the nodes so each task is projected
	// to its wire form exactly once:
	//
	//   - Manual UserTask must not carry completion validation: a manual task
	//     completes with no payload, so a validation strategy would never
	//     receive input to check.
	//   - The completion-outcome declaration must be well-formed.
	//
	// model cannot import the activity package, so both Manual and the outcome
	// declaration are read via the wire projection.
	//
	// Outcome errors are collected separately and appended after the pass so the
	// joined error still reports every ErrManualTaskValidation ahead of every
	// outcome error, exactly as when the two rules ran as consecutive loops.
	var outcomeErrs []error
	for _, n := range d.Nodes {
		if n.Kind() != KindUserTask {
			continue
		}
		w := toWire(n)
		if w.Manual && ValidationStrategyFor(n) != nil {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrManualTaskValidation, n.ID()))
		}
		outcomeErrs = append(outcomeErrs, validateOutcomes(n.ID(), &w)...)
	}
	errs = append(errs, outcomeErrs...)

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
		// branch, so the combination is a silent no-op — reject it.
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

// isIdentifier reports whether s is a valid expr identifier — the form a
// gateway condition can reference. Mirrors expr's lexer: a leading letter or
// underscore followed by letters, digits, or underscores.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', unicode.IsLetter(r):
		case i > 0 && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return true
}

// validateOutcomes checks a UserTask's completion-outcome declaration: the
// declared set must be usable (no blank or duplicate entries), an explicit
// outcome variable must be an expr identifier, and a manual task must not
// declare outcomes at all.
//
// It takes the caller's already-computed wire projection rather than
// re-deriving one, so a UserTask is flattened once per Validate call. nodeID is
// passed explicitly (not read off w.ID) because a kind's registered ToWire spec
// owns the wire struct and is not obliged to leave the id intact.
func validateOutcomes(nodeID string, w *NodeWire) []error {
	var errs []error

	seen := make(map[string]bool, len(w.Outcomes))
	for i, o := range w.Outcomes {
		if strings.TrimSpace(o) == "" {
			errs = append(errs, fmt.Errorf("%w: node %q outcomes[%d]", ErrEmptyOutcome, nodeID, i))
			continue
		}
		if seen[o] {
			errs = append(errs, fmt.Errorf("%w: node %q outcome %q", ErrDuplicateOutcome, nodeID, o))
			continue
		}
		seen[o] = true
	}

	if w.OutcomeVariable != "" && !isIdentifier(w.OutcomeVariable) {
		errs = append(errs, fmt.Errorf("%w: node %q variable %q", ErrInvalidOutcomeVariable, nodeID, w.OutcomeVariable))
	}

	if w.Manual && (len(w.Outcomes) > 0 || w.ExposeOutcome || w.OutcomeVariable != "") {
		errs = append(errs, fmt.Errorf("%w: node %q", ErrManualTaskOutcome, nodeID))
	}

	// Exposure publishes the outcome as a process variable, so the value domain
	// must be closed first: without a declared set, any free-text string an actor
	// sends becomes a routing input. A manual task is exempt because the rule
	// above already rejects it with the more precise ErrManualTaskOutcome.
	if !w.Manual && len(w.Outcomes) == 0 && (w.ExposeOutcome || w.OutcomeVariable != "") {
		errs = append(errs, fmt.Errorf("%w: node %q", ErrOutcomeExposureWithoutOutcomes, nodeID))
	}

	return errs
}
