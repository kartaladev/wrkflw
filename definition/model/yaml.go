package model

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// nodeYAML is the flat YAML representation of any node. It mirrors NodeWire
// but uses a plain string for Kind so that yaml.v3 decodes the lowerCamelCase
// discriminator without invoking NodeKind's JSON un/marshalers.
type nodeYAML struct {
	ID                    string          `yaml:"id"`
	Kind                  string          `yaml:"kind"`
	Name                  string          `yaml:"name,omitempty"`
	Action                string          `yaml:"action,omitempty"`
	CandidateRoles        []string        `yaml:"candidateRoles,omitempty"`
	EligibilityPrivileges []string        `yaml:"eligibilityPrivileges,omitempty"`
	EligibilityExpr       string          `yaml:"eligibilityExpr,omitempty"`
	TimerDuration         string          `yaml:"timerDuration,omitempty"`
	DeadlineDuration      string          `yaml:"deadlineDuration,omitempty"`
	DeadlineFlow          string          `yaml:"deadlineFlow,omitempty"`
	DeadlineAction        string          `yaml:"deadlineAction,omitempty"`
	ReminderEvery         string          `yaml:"reminderEvery,omitempty"`
	ReminderAction        string          `yaml:"reminderAction,omitempty"`
	RetryPolicy           *RetryPolicy    `yaml:"retryPolicy,omitempty"`
	RecoveryFlow          string          `yaml:"recoveryFlow,omitempty"`
	CompensateAction      string          `yaml:"compensateAction,omitempty"`
	CompensateRef         string          `yaml:"compensateRef,omitempty"`
	CancelHandler         string          `yaml:"cancelHandler,omitempty"`
	CompletionAction      string          `yaml:"completionAction,omitempty"`
	SignalName            string          `yaml:"signalName,omitempty"`
	MessageName           string          `yaml:"messageName,omitempty"`
	CorrelationKey        string          `yaml:"correlationKey,omitempty"`
	ErrorCode             string          `yaml:"errorCode,omitempty"`
	AttachedTo            string          `yaml:"attachedTo,omitempty"`
	NonInterrupting       bool            `yaml:"nonInterrupting,omitempty"`
	Subprocess            *definitionYAML `yaml:"subprocess,omitempty"`
	DefRef                string          `yaml:"defRef,omitempty"`
	// Validation mirrors NodeWire.Validation for the YAML authoring form.
	Validation *validate.ValidationDescriptor `yaml:"validation,omitempty"`
}

// sequenceFlowYAML decodes a flow.SequenceFlow from YAML. Field names match the
// JSON tags so the same YAML keys work for both representations.
type sequenceFlowYAML struct {
	ID        string `yaml:"id"`
	Source    string `yaml:"source"`
	Target    string `yaml:"target"`
	Condition string `yaml:"condition,omitempty"`
	IsDefault bool   `yaml:"isDefault,omitempty"`
}

// definitionYAML is the YAML mirror of ProcessDefinition. It handles nested
// subprocess definitions recursively.
type definitionYAML struct {
	ID            string             `yaml:"id"`
	Version       int                `yaml:"version"`
	Nodes         []nodeYAML         `yaml:"nodes"`
	Flows         []sequenceFlowYAML `yaml:"flows"`
	CancelActions []string           `yaml:"cancelActions,omitempty"`
}

// fromNodeYAML converts a nodeYAML into a concrete Node via the kind
// discriminator, reusing the fromWire path for consistency.
func fromNodeYAML(ny nodeYAML) (Node, error) {
	kind, ok := nodeKindByName[ny.Kind]
	if !ok {
		return nil, fmt.Errorf("workflow-definition: unknown node kind %q", ny.Kind)
	}

	var subDef *ProcessDefinition
	if ny.Subprocess != nil {
		core, err := coreFromYAML(ny.Subprocess)
		if err != nil {
			return nil, fmt.Errorf("workflow-definition: subprocess %q: %w", ny.ID, err)
		}
		// Subprocess definitions are fully declared inline: build immediately so
		// the parent node holds a *ProcessDefinition rather than a loader handle.
		built, err := core.build()
		if err != nil {
			return nil, fmt.Errorf("workflow-definition: subprocess %q: %w", ny.ID, err)
		}
		subDef = built
	}

	w := NodeWire{
		ID:                    ny.ID,
		Kind:                  kind,
		Name:                  ny.Name,
		Action:                ny.Action,
		CandidateRoles:        ny.CandidateRoles,
		EligibilityPrivileges: ny.EligibilityPrivileges,
		EligibilityExpr:       ny.EligibilityExpr,
		TimerDuration:         ny.TimerDuration,
		DeadlineDuration:      ny.DeadlineDuration,
		DeadlineFlow:          ny.DeadlineFlow,
		DeadlineAction:        ny.DeadlineAction,
		ReminderEvery:         ny.ReminderEvery,
		ReminderAction:        ny.ReminderAction,
		RetryPolicy:           ny.RetryPolicy,
		RecoveryFlow:          ny.RecoveryFlow,
		CompensateAction:      ny.CompensateAction,
		CompensateRef:         ny.CompensateRef,
		CancelHandler:         ny.CancelHandler,
		CompletionAction:      ny.CompletionAction,
		SignalName:            ny.SignalName,
		MessageName:           ny.MessageName,
		CorrelationKey:        ny.CorrelationKey,
		ErrorCode:             ny.ErrorCode,
		AttachedTo:            ny.AttachedTo,
		NonInterrupting:       ny.NonInterrupting,
		Subprocess:            subDef,
		DefRef:                ny.DefRef,
		Validation:            ny.Validation,
	}
	return fromWire(w)
}

// coreFromYAML converts a decoded definitionYAML into a *definitionCore with
// concrete node types. Validation is deferred to Build so callers can register
// definition-scoped actions before validation runs.
func coreFromYAML(dy *definitionYAML) (*definitionCore, error) {
	c := &definitionCore{id: dy.ID, version: dy.Version, cancelActions: dy.CancelActions}
	c.nodes = make([]Node, len(dy.Nodes))
	for i, ny := range dy.Nodes {
		n, err := fromNodeYAML(ny)
		if err != nil {
			return nil, err
		}
		c.nodes[i] = n
	}
	c.flows = make([]flow.SequenceFlow, len(dy.Flows))
	for i, fy := range dy.Flows {
		c.flows[i] = flow.SequenceFlow(fy)
	}
	return c, nil
}

// ParseYAML reads a YAML process-definition from r and returns a
// DefinitionLoader whose structure (nodes, flows) is already declared. Register
// any definition-scoped actions via RegisterAction/RegisterActionFunc, apply any
// LoaderOption (e.g. WithValidatorRegistry), then call Build to validate and
// obtain the *ProcessDefinition. The root definition package exposes this as
// definition.NewLoader.
func ParseYAML(r io.Reader, opts ...LoaderOption) (DefinitionLoader, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("workflow-definition: read YAML: %w", err)
	}
	var dy definitionYAML
	if err := yaml.Unmarshal(data, &dy); err != nil {
		return nil, fmt.Errorf("workflow-definition: parse YAML: %w", err)
	}
	core, err := coreFromYAML(&dy)
	if err != nil {
		return nil, err
	}
	for _, o := range opts {
		o(core)
	}
	return &definitionLoader{core}, nil
}
