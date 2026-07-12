package model

import (
	"encoding/json"
	"fmt"

	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// NodeWire is the flat JSON/JSONB representation of any node. It is the single
// serialization shape; previously stored definitions decode through it
// unchanged. Field names/order mirror the pre-interface Node struct.
type NodeWire struct {
	ID                 string   `json:"id"`
	Kind               NodeKind `json:"kind"`
	Name               string   `json:"name,omitempty"`
	Action             string   `json:"action,omitempty"`
	EligibleRoles      []string `json:"eligibleRoles,omitempty"`
	EligiblePrivileges []string `json:"eligiblePrivileges,omitempty"`
	EligibleExpr       string   `json:"eligibleExpr,omitempty"`
	Manual             bool     `json:"manual,omitempty"`
	ManualImmediate    bool     `json:"manualImmediate,omitempty"`
	// legacy flat forms (decoded via ReadTrigger's flatExpr path; not written by ToWire)
	TimerDuration    string `json:"timerDuration,omitempty"`
	DeadlineDuration string `json:"deadlineDuration,omitempty"`
	WaitEvery        string `json:"waitEvery,omitempty"`
	// nested trigger forms (canonical)
	TimerTrigger     *TriggerWire `json:"timerTrigger,omitempty"`
	DeadlineTrigger  *TriggerWire `json:"deadlineTrigger,omitempty"`
	WaitTrigger      *TriggerWire `json:"waitTrigger,omitempty"`
	DeadlineFlow     string       `json:"deadlineFlow,omitempty"`
	DeadlineAction   string       `json:"deadlineAction,omitempty"`
	WaitAction       string       `json:"waitAction,omitempty"`
	RetryPolicy      *RetryPolicy `json:"retryPolicy,omitempty"`
	RecoveryFlow     string       `json:"recoveryFlow,omitempty"`
	CompensateAction string       `json:"compensateAction,omitempty"`
	CompensateRef    string       `json:"compensateRef,omitempty"`
	// CompensateScopeLocal narrows a scope-wide CompensationThrowEvent at the
	// root scope to root-direct compensable activities (ADR-0120).
	CompensateScopeLocal bool   `json:"compensateScopeLocal,omitempty"`
	CancelAction         string `json:"cancelAction,omitempty"`
	CompletionAction     string `json:"completionAction,omitempty"`
	SignalName           string `json:"signalName,omitempty"`
	MessageName          string `json:"messageName,omitempty"`
	CorrelationKey       string `json:"correlationKey,omitempty"`
	// MessageStartSingleton, when true on a StartEvent, makes a keyless
	// message-start create at most one instance ever for its message name
	// (name-only deterministic id). Default false = fresh instance per message
	// (ADR-0121 review).
	MessageStartSingleton bool   `json:"messageStartSingleton,omitempty"`
	ErrorCode             string `json:"errorCode,omitempty"`
	// EndBehavior is the name-based discriminator for an EndEvent's behavior
	// (ADR-0127): "terminate" or "error"; empty means a normal end. It replaces
	// the former forceTermination bool. TerminationReason/TerminationOutcome are
	// written only for "terminate"; ErrorCode only for "error".
	EndBehavior        string             `json:"endBehavior,omitempty"`
	TerminationReason  string             `json:"terminationReason,omitempty"`
	TerminationOutcome string             `json:"terminationOutcome,omitempty"`
	AttachedTo         string             `json:"attachedTo,omitempty"`
	NonInterrupting    bool               `json:"nonInterrupting,omitempty"`
	BoundaryAction     string             `json:"boundaryAction,omitempty"`
	BoundaryErrorExpr  string             `json:"boundaryErrorExpr,omitempty"`
	Subprocess         *ProcessDefinition `json:"subprocess,omitempty"`
	DefRef             string             `json:"defRef,omitempty"`
	// Validation is the descriptor for the node's validation-strategy slot, when
	// it has one and the strategy is describable (validate.DescribableStrategy)
	// or a pending reconstruction placeholder (PendingValidation). nil means
	// unset. A non-describable (callback) strategy never reaches here —
	// ProcessDefinition.MarshalJSON fails closed first (ErrUnserializableValidation).
	Validation *validate.ValidationDescriptor `json:"validation,omitempty"`
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
func fromWire(w NodeWire) (Node, error) {
	s, ok := specFor(w.Kind)
	if !ok || s.FromWire == nil {
		return nil, fmt.Errorf("%w: %q", ErrKindNotRegistered, w.Kind)
	}
	return s.FromWire(Base{id: w.ID, name: w.Name}, w), nil
}

// definitionWire mirrors ProcessDefinition with Nodes as wire forms.
type definitionWire struct {
	ID            string              `json:"id"`
	Version       int                 `json:"version"`
	Nodes         []NodeWire          `json:"nodes"`
	Flows         []flow.SequenceFlow `json:"flows"`
	CancelActions []string            `json:"cancelActions,omitempty"`
}

// MarshalJSON serializes a ProcessDefinition to JSON using the flat NodeWire
// form so stored JSONB definitions remain backward-compatible.
func (d ProcessDefinition) MarshalJSON() ([]byte, error) {
	dw := definitionWire{
		ID:            d.ID,
		Version:       d.Version,
		Flows:         d.Flows,
		CancelActions: d.CancelActions,
	}
	dw.Nodes = make([]NodeWire, len(d.Nodes))
	for i, n := range d.Nodes {
		if strat := ValidationStrategyFor(n); strat != nil {
			if _, ok := strat.(validate.DescribableStrategy); !ok {
				return nil, fmt.Errorf("%w: node %q", ErrUnserializableValidation, n.ID())
			}
		}
		dw.Nodes[i] = toWire(n)
	}
	return json.Marshal(dw)
}

// UnmarshalJSON deserializes a ProcessDefinition from JSON, reconstructing each
// node into its concrete type via the kind discriminator.
func (d *ProcessDefinition) UnmarshalJSON(data []byte) error {
	var dw definitionWire
	if err := json.Unmarshal(data, &dw); err != nil {
		return err
	}
	d.ID = dw.ID
	d.Version = dw.Version
	d.Flows = dw.Flows
	d.CancelActions = dw.CancelActions
	d.Nodes = make([]Node, len(dw.Nodes))
	for i, w := range dw.Nodes {
		n, err := fromWire(w)
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
