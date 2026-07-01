package model

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// nodeYAML is the flat YAML representation of any node. It mirrors nodeWire
// but uses a plain string for Kind so that yaml.v3 decodes the lowerCamelCase
// discriminator without invoking NodeKind's JSON un/marshalers.
type nodeYAML struct {
	ID                 string          `yaml:"id"`
	Kind               string          `yaml:"kind"`
	Name               string          `yaml:"name,omitempty"`
	Action             string          `yaml:"action,omitempty"`
	CandidateRoles     []string        `yaml:"candidateRoles,omitempty"`
	EligibilityExpr    string          `yaml:"eligibilityExpr,omitempty"`
	TimerDuration      string          `yaml:"timerDuration,omitempty"`
	DeadlineDuration   string          `yaml:"deadlineDuration,omitempty"`
	DeadlineFlow       string          `yaml:"deadlineFlow,omitempty"`
	DeadlineAction     string          `yaml:"deadlineAction,omitempty"`
	ReminderEvery      string          `yaml:"reminderEvery,omitempty"`
	ReminderAction     string          `yaml:"reminderAction,omitempty"`
	RetryPolicy        *RetryPolicy    `yaml:"retryPolicy,omitempty"`
	RecoveryFlow       string          `yaml:"recoveryFlow,omitempty"`
	CompensationAction string          `yaml:"compensationAction,omitempty"`
	CompensateRef      string          `yaml:"compensateRef,omitempty"`
	CancelHandler      string          `yaml:"cancelHandler,omitempty"`
	SignalName         string          `yaml:"signalName,omitempty"`
	MessageName        string          `yaml:"messageName,omitempty"`
	CorrelationKey     string          `yaml:"correlationKey,omitempty"`
	ErrorCode          string          `yaml:"errorCode,omitempty"`
	AttachedTo         string          `yaml:"attachedTo,omitempty"`
	NonInterrupting    bool            `yaml:"nonInterrupting,omitempty"`
	Subprocess         *definitionYAML `yaml:"subprocess,omitempty"`
	DefRef             string          `yaml:"defRef,omitempty"`
}

// sequenceFlowYAML decodes a SequenceFlow from YAML. Field names match the
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
		return nil, fmt.Errorf("workflow-model: unknown node kind %q", ny.Kind)
	}

	var subDef *ProcessDefinition
	if ny.Subprocess != nil {
		d, err := definitionFromYAML(ny.Subprocess)
		if err != nil {
			return nil, fmt.Errorf("workflow-model: subprocess %q: %w", ny.ID, err)
		}
		subDef = d
	}

	w := nodeWire{
		ID:                 ny.ID,
		Kind:               kind,
		Name:               ny.Name,
		Action:             ny.Action,
		CandidateRoles:     ny.CandidateRoles,
		EligibilityExpr:    ny.EligibilityExpr,
		TimerDuration:      ny.TimerDuration,
		DeadlineDuration:   ny.DeadlineDuration,
		DeadlineFlow:       ny.DeadlineFlow,
		DeadlineAction:     ny.DeadlineAction,
		ReminderEvery:      ny.ReminderEvery,
		ReminderAction:     ny.ReminderAction,
		RetryPolicy:        ny.RetryPolicy,
		RecoveryFlow:       ny.RecoveryFlow,
		CompensationAction: ny.CompensationAction,
		CompensateRef:      ny.CompensateRef,
		CancelHandler:      ny.CancelHandler,
		SignalName:         ny.SignalName,
		MessageName:        ny.MessageName,
		CorrelationKey:     ny.CorrelationKey,
		ErrorCode:          ny.ErrorCode,
		AttachedTo:         ny.AttachedTo,
		NonInterrupting:    ny.NonInterrupting,
		Subprocess:         subDef,
		DefRef:             ny.DefRef,
	}
	return fromWire(w)
}

// definitionFromYAML converts a decoded definitionYAML into a ProcessDefinition
// with concrete node types. It does NOT validate — the caller (ParseYAML) runs Validate.
func definitionFromYAML(dy *definitionYAML) (*ProcessDefinition, error) {
	def := ProcessDefinition{
		ID:            dy.ID,
		Version:       dy.Version,
		CancelActions: dy.CancelActions,
	}

	def.Nodes = make([]Node, len(dy.Nodes))
	for i, ny := range dy.Nodes {
		n, err := fromNodeYAML(ny)
		if err != nil {
			return nil, err
		}
		def.Nodes[i] = n
	}

	def.Flows = make([]SequenceFlow, len(dy.Flows))
	for i, fy := range dy.Flows {
		def.Flows[i] = SequenceFlow(fy)
	}

	return &def, nil
}

// ParseYAML decodes a YAML process-definition from data, reconstructs concrete
// node types via the lowerCamelCase kind discriminator, and validates the
// resulting definition. Returns the validated *ProcessDefinition or an error.
func ParseYAML(data []byte) (*ProcessDefinition, error) {
	var dy definitionYAML
	if err := yaml.Unmarshal(data, &dy); err != nil {
		return nil, fmt.Errorf("workflow-model: parse YAML: %w", err)
	}
	def, err := definitionFromYAML(&dy)
	if err != nil {
		return nil, err
	}
	if err := Validate(def); err != nil {
		return nil, err
	}
	return def, nil
}

// LoadYAML reads all bytes from r and calls ParseYAML.
func LoadYAML(r io.Reader) (*ProcessDefinition, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("workflow-model: read YAML: %w", err)
	}
	return ParseYAML(data)
}
