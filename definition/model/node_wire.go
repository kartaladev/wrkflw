package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// NodeWire is the flat JSON/JSONB representation of any node. It is the single
// serialization shape through which every node kind decodes and encodes.
type NodeWire struct {
	ID     string   `json:"id"`
	Kind   NodeKind `json:"kind"`
	Name   string   `json:"name,omitempty"`
	Action string   `json:"action,omitempty"`
	// Rule is the reserved rule-engine reference on a businessRuleTask: a catalog
	// name or an inline rule document (see RuleSpec). It is a POINTER so an
	// absent rule is dropped by omitempty — omitempty has no effect on a struct
	// field, so a value field would emit "rule":{} on every businessRuleTask and
	// break the byte-identical golden file.
	//
	// A literal "rule":null therefore means ABSENT (encoding/json nils the
	// pointer without consulting RuleSpec.UnmarshalJSON), which loses nothing:
	// there is no rule in it to lose. Every other malformed shape is refused with
	// ErrInvalidRule, and any non-nil rule is refused by Validate with
	// ErrRuleNotSupported until the adapter ships.
	Rule               *RuleSpec `json:"rule,omitempty"`
	EligibleRoles      []string  `json:"eligible_roles,omitempty"`
	EligiblePrivileges []string  `json:"eligible_privileges,omitempty"`
	EligibleExpr       string    `json:"eligible_expr,omitempty"`
	Manual             bool      `json:"manual,omitempty"`
	ManualImmediate    bool      `json:"manual_immediate,omitempty"`
	// Outcomes/ExposeOutcome/OutcomeVariable carry a UserTask's completion-outcome
	// declaration and its variable-exposure opt-in.
	Outcomes        []string `json:"outcomes,omitempty"`
	ExposeOutcome   bool     `json:"expose_outcome,omitempty"`
	OutcomeVariable string   `json:"outcome_variable,omitempty"`
	// legacy flat forms (decoded via ReadTrigger's flatExpr path; not written by ToWire)
	TimerDuration    string `json:"timer_duration,omitempty"`
	DeadlineDuration string `json:"deadline_duration,omitempty"`
	WaitEvery        string `json:"wait_every,omitempty"`
	// nested trigger forms (canonical)
	TimerTrigger     *TriggerWire `json:"timer_trigger,omitempty"`
	DeadlineTrigger  *TriggerWire `json:"deadline_trigger,omitempty"`
	WaitTrigger      *TriggerWire `json:"wait_trigger,omitempty"`
	DeadlineFlow     string       `json:"deadline_flow,omitempty"`
	DeadlineAction   string       `json:"deadline_action,omitempty"`
	WaitAction       string       `json:"wait_action,omitempty"`
	RetryPolicy      *RetryPolicy `json:"retry_policy,omitempty"`
	RecoveryFlow     string       `json:"recovery_flow,omitempty"`
	CompensateAction string       `json:"compensate_action,omitempty"`
	CompensateRef    string       `json:"compensate_ref,omitempty"`
	// CompensateScopeLocal narrows a scope-wide CompensationThrowEvent at the
	// root scope to root-direct compensable activities.
	CompensateScopeLocal bool   `json:"compensate_scope_local,omitempty"`
	CancelAction         string `json:"cancel_action,omitempty"`
	CompletionAction     string `json:"completion_action,omitempty"`
	SignalName           string `json:"signal_name,omitempty"`
	MessageName          string `json:"message_name,omitempty"`
	CorrelationKey       string `json:"correlation_key,omitempty"`
	// MessageStartSingleton, when true on a StartEvent, makes a keyless
	// message-start create at most one instance ever for its message name
	// (name-only deterministic id). Default false = fresh instance per message.
	MessageStartSingleton bool   `json:"message_start_singleton,omitempty"`
	ErrorCode             string `json:"error_code,omitempty"`
	// EndBehavior is the name-based discriminator for an EndEvent's behavior:
	// "terminate" or "error"; empty means a normal end.
	// TerminationReason/TerminationOutcome are written only for "terminate";
	// ErrorCode only for "error".
	EndBehavior        string             `json:"end_behavior,omitempty"`
	TerminationReason  string             `json:"termination_reason,omitempty"`
	TerminationOutcome string             `json:"termination_outcome,omitempty"`
	AttachedTo         string             `json:"attached_to,omitempty"`
	NonInterrupting    bool               `json:"non_interrupting,omitempty"`
	BoundaryAction     string             `json:"boundary_action,omitempty"`
	BoundaryErrorExpr  string             `json:"boundary_error_expr,omitempty"`
	Subprocess         *ProcessDefinition `json:"subprocess,omitempty"`
	DefRef             string             `json:"def_ref,omitempty"`
	// Validation is the descriptor for the node's validation-strategy slot, when
	// it has one and the strategy is describable (validate.DescribableStrategy)
	// or a pending reconstruction placeholder (PendingValidation). nil means
	// unset. A non-describable (callback) strategy never reaches here —
	// ProcessDefinition.MarshalJSON fails closed first (ErrUnserializableValidation).
	Validation *validate.ValidationDescriptor `json:"validation,omitempty"`
}

// nodeWireIn is the DECODE shape for a node: NodeWire plus the retired keys that
// have to be refused rather than ignored.
//
// The retired key lives here rather than on NodeWire so that nothing outside
// this file can set it. NodeWire is cross-package API — registry.go hands a
// *NodeWire to all ~20 leaf ToWire specs — so a publicly-settable field whose
// only meaning is "never write this" would leave the never-emitted invariant as
// a promise kept by a test. Kept here it is a property of the type instead: no
// ToWire can reach the field, so no marshalled definition can carry the key.
//
// MarshalJSON encodes through this type too, with Label always nil. Promoted
// fields inline at the same level and index order places every NodeWire field
// before Label, so the output is byte-identical to encoding a bare NodeWire —
// which the byte-identical golden round-trip is the standing proof of.
type nodeWireIn struct {
	NodeWire
	// Label is detection-only. The "label" node key is retired and "name" is the
	// display string, so a label has nowhere to go. It is json.RawMessage rather
	// than string so that PRESENCE is what is detected, not a non-empty value:
	// "label":"", "label":null and "label":{"any":"shape"} are all the retired
	// key and all refused.
	//
	// Dropping the field would not make the key silently accepted — the decoder
	// sets DisallowUnknownFields — but it would report
	// `json: unknown field "label"`, naming the RETIRED key and never the
	// replacement, which tells a migrating consumer nothing about where the
	// string now goes. See ErrRetiredLabelKey.
	Label json.RawMessage `json:"label,omitempty"`
}

// node reconstructs the concrete Node for one decoded node, refusing any retired
// key it carries first. This is the JSON half of the retirement; fromNodeYAML is
// the YAML half, and both report through retiredLabelKeyErr.
func (w nodeWireIn) node() (Node, error) {
	if len(w.Label) > 0 {
		return nil, retiredLabelKeyErr(w.ID)
	}
	return fromWire(w.NodeWire)
}

// toWire flattens a Node into its wire form via the kind's registered spec.
func toWire(n Node) NodeWire {
	w := NodeWire{ID: n.ID(), Kind: n.Kind(), Name: n.Name()}
	if s, ok := specFor(n.Kind()); ok && s.ToWire != nil {
		s.ToWire(n, &w)
	}
	return w
}

// PutActivity projects the shared activity fields into the wire form. Leaf
// packages call it from their ToWire specs.
func (w *NodeWire) PutActivity(a ActivityFields) {
	w.RetryPolicy, w.RecoveryFlow = a.RetryPolicy, a.RecoveryFlow
	w.CompensateAction, w.CancelAction, w.CompletionAction = a.CompensateAction, a.CancelAction, a.CompletionAction
	w.PutWait(a.WaitFields)
}

// Activity reconstructs the shared activity fields from the wire form. Leaf
// packages call it from their FromWire specs.
func (w NodeWire) Activity() ActivityFields {
	return ActivityFields{WaitFields: w.Wait(), RetryPolicy: w.RetryPolicy, RecoveryFlow: w.RecoveryFlow, CompensateAction: w.CompensateAction, CancelAction: w.CancelAction, CompletionAction: w.CompletionAction}
}

// Wait reconstructs the shared deadline+wait fields from the wire form,
// for kinds (IntermediateCatchEvent) that carry WaitFields without the full
// ActivityFields. The canonical nested TriggerWire is preferred; the legacy
// flat string fields are decoded as expression triggers for backward compatibility.
func (w NodeWire) Wait() WaitFields {
	return WaitFields{
		DeadlineTimer:  ReadTrigger(w.DeadlineTrigger, w.DeadlineDuration, false),
		DeadlineFlow:   w.DeadlineFlow,
		DeadlineAction: w.DeadlineAction,
		WaitEvery:      ReadTrigger(w.WaitTrigger, w.WaitEvery, true),
		WaitAction:     w.WaitAction,
	}
}

// PutWait projects the shared deadline+wait fields into the wire form using
// the canonical nested TriggerWire encoding.
func (w *NodeWire) PutWait(a WaitFields) {
	w.DeadlineTrigger = PutTrigger(a.DeadlineTimer)
	w.DeadlineFlow, w.DeadlineAction = a.DeadlineFlow, a.DeadlineAction
	w.WaitTrigger = PutTrigger(a.WaitEvery)
	w.WaitAction = a.WaitAction
}

// fromWire reconstructs the concrete Node for w.Kind via the registered spec.
//
// It is the single seam both decoders funnel through — nodeWireIn.node for JSON
// and fromNodeYAML for YAML — which is why the kind-key gate lives here and not
// in either decoder, and emphatically not inside a spec's own FromWire:
// RegisterKind calls FromWire(Base{}, NodeWire{}) at init to record the kind's
// concrete type, so a FromWire that refused anything would abort package
// initialisation for every binary importing the leaf. See node_wire_keys.go for
// what the gate refuses and the limit it documents.
func fromWire(w NodeWire) (Node, error) {
	s, ok := specFor(w.Kind)
	if !ok || s.FromWire == nil {
		return nil, fmt.Errorf("%w: %q", ErrKindNotRegistered, w.Kind)
	}
	if err := checkNodeKeys(w, s); err != nil {
		return nil, err
	}
	return s.FromWire(Base{id: w.ID, name: w.Name}, w), nil
}

// retiredLabelKeyErr builds the ErrRetiredLabelKey diagnostic for node id. It is
// the single message shared by both decoders: the JSON path detects the retired
// key as a present nodeWireIn.Label, the YAML path as a present nodeYAML.Label,
// and each reports it through here.
func retiredLabelKeyErr(id string) error {
	return fmt.Errorf("%w: node %q", ErrRetiredLabelKey, id)
}

// definitionWire mirrors ProcessDefinition with Nodes as wire forms.
type definitionWire struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	// ScopedActions carries the definition-scoped action NAMES so a marshalled
	// definition is self-describing. It is derived, MARSHAL-ONLY
	// state: the scoped catalog holds live action implementations that have no
	// serializable form, so UnmarshalJSON accepts the key and drops it — a
	// reloaded definition falls back to the global catalog for those names.
	ScopedActions []string            `json:"scoped_actions,omitempty"`
	Nodes         []nodeWireIn        `json:"nodes"`
	Flows         []flow.SequenceFlow `json:"flows"`
	CancelActions []string            `json:"cancel_actions,omitempty"`
}

// MarshalJSON serializes a ProcessDefinition to JSON using the flat NodeWire
// form so stored JSONB definitions remain backward-compatible.
//
// checkNodeTypes runs first, over this level's nodes only, before either
// ValidationStrategyFor or toWire touches one. For a kind with a ValidationGet
// slot (userTask, receiveTask, startEvent, intermediateCatchEvent), both
// dispatch on the node's concrete type with a bare assertion, so a foreign
// node — one satisfying Node and claiming a registered kind without being that
// kind's concrete type — would otherwise panic instead of being refused
// (#147). BREAKING, for the four gateway kinds only (exclusiveGateway,
// parallelGateway, inclusiveGateway, eventBasedGateway — see
// definition/gateway/gateway.go): neither ToWire nor ValidationGet asserts
// there, so a foreign node under one of them marshalled cleanly before this
// gate, and now returns ErrForeignNodeType instead. That is success -> error,
// not panic -> error — the only class of node whose MarshalJSON result changes
// in that direction. It brings MarshalJSON in line with Validate, which
// already refused the same node; nothing that Validate rejects becomes newly
// acceptable here.
//
// The flat check is enough for a nested subprocess too: NodeWire.Subprocess is
// a *ProcessDefinition with its own MarshalJSON, so json.Marshal recurses into
// it below and re-runs this same gate at every level for free — but that
// recursion is UNBOUNDED, unlike validateNodeTypes' cycle-guarded walk. A
// cyclic subprocess pointer graph is pre-existing (Validate itself accepts one
// today; validateNodeTypes' cycle guard only stops it from being walked twice,
// not from being marshalled), reachable only through in-process Go
// construction — never through JSON or YAML input — and not introduced by this
// gate: json.Marshal aborts the process on it with or without checkNodeTypes
// in front, since the missing guard is in the check, not in encoding/json's
// own recursion. Tracked as a follow-up, not fixed here.
func (d ProcessDefinition) MarshalJSON() ([]byte, error) {
	if err := checkNodeTypes(d.Nodes); err != nil {
		return nil, err
	}
	dw := definitionWire{
		ID:            d.ID,
		Version:       d.Version,
		ScopedActions: d.ScopedActionNames(),
		Flows:         d.Flows,
		CancelActions: d.CancelActions,
	}
	dw.Nodes = make([]nodeWireIn, len(d.Nodes))
	for i, n := range d.Nodes {
		if strat := ValidationStrategyFor(n); strat != nil {
			if _, ok := strat.(validate.DescribableStrategy); !ok {
				return nil, fmt.Errorf("%w: node %q", ErrUnserializableValidation, n.ID())
			}
		}
		dw.Nodes[i] = nodeWireIn{NodeWire: toWire(n)}
	}
	return json.Marshal(dw)
}

// UnmarshalJSON deserializes a ProcessDefinition from JSON, reconstructing each
// node into its concrete type via the kind discriminator.
func (d *ProcessDefinition) UnmarshalJSON(data []byte) error {
	var dw definitionWire
	// Strictness must be applied here, inside the custom unmarshaler: an outer
	// json.Decoder's DisallowUnknownFields is discarded once this method takes
	// over, so this is the only place it survives.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dw); err != nil {
		// Decode reports empty/whitespace-only input as a bare io.EOF where
		// json.Unmarshal reported a *json.SyntaxError. Letting io.EOF escape
		// would be a real behaviour change, not just a message change: a caller
		// testing errors.Is(err, io.EOF) to mean "clean end of stream" would
		// silently skip an empty or truncated definition. Translate it back so
		// the EOF identity stays inside this method.
		if errors.Is(err, io.EOF) {
			return errors.New("workflow-definition: unexpected end of definition JSON")
		}
		return err
	}
	// Decode stops after one value, so unlike json.Unmarshal it would accept
	// anything following it. Reject trailing data explicitly, otherwise this
	// change would loosen a check while tightening another.
	//
	// Two distinct causes reach here and both matter to whoever is debugging a
	// rejected definition: a genuine second JSON value (err == nil), and corrupt
	// trailing bytes (a *json.SyntaxError naming the offending character). Keep
	// the underlying error when there is one rather than collapsing both to the
	// same message — the same shape decodeCursorInto uses in
	// runtime/kernel/cursorcodec.go. Trailing WHITESPACE is legal JSON framing
	// and stays accepted; only a further value or garbage is rejected.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("workflow-definition: trailing data after definition: %w", err)
		}
		return errors.New("workflow-definition: trailing data after definition")
	}
	d.ID = dw.ID
	d.Version = dw.Version
	d.Flows = dw.Flows
	d.CancelActions = dw.CancelActions
	d.Nodes = make([]Node, len(dw.Nodes))
	for i, w := range dw.Nodes {
		n, err := w.node()
		if err != nil {
			return err
		}
		// Durable-reload reconciliation: resolve a pending validation descriptor
		// against the process-global DefaultRegistry (adapters self-register via
		// init()). Lenient by design — an unregistered kind leaves the slot pending
		// so it fails closed at runtime rather than breaking the load.
		d.Nodes[i] = reconcileNodeValidationLenient(n, validate.DefaultRegistry())
	}
	return nil
}
