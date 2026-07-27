package model

import (
	"fmt"
	"io"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// nodeYAML is the flat YAML representation of any node. It mirrors NodeWire
// but uses a plain string for Kind so that yaml.v3 decodes the lowerCamelCase
// discriminator without invoking NodeKind's JSON un/marshalers.
type nodeYAML struct {
	ID                 string   `yaml:"id"`
	Kind               string   `yaml:"kind"`
	Name               string   `yaml:"name,omitempty"`
	Label              string   `yaml:"label,omitempty"`
	Action             string   `yaml:"action,omitempty"`
	EligibleRoles      []string `yaml:"eligible_roles,omitempty"`
	EligiblePrivileges []string `yaml:"eligible_privileges,omitempty"`
	EligibleExpr       string   `yaml:"eligible_expr,omitempty"`
	Manual             bool     `yaml:"manual,omitempty"`
	ManualImmediate    bool     `yaml:"manual_immediate,omitempty"`
	// Outcomes/ExposeOutcome/OutcomeVariable mirror the like-named NodeWire
	// fields — a UserTask's completion-outcome declaration (ADR-0146).
	Outcomes         []string     `yaml:"outcomes,omitempty"`
	ExposeOutcome    bool         `yaml:"expose_outcome,omitempty"`
	OutcomeVariable  string       `yaml:"outcome_variable,omitempty"`
	TimerDuration    string       `yaml:"timer_duration,omitempty"`
	DeadlineDuration string       `yaml:"deadline_duration,omitempty"`
	DeadlineFlow     string       `yaml:"deadline_flow,omitempty"`
	DeadlineAction   string       `yaml:"deadline_action,omitempty"`
	WaitEvery        string       `yaml:"wait_every,omitempty"`
	WaitAction       string       `yaml:"wait_action,omitempty"`
	RetryPolicy      *RetryPolicy `yaml:"retry_policy,omitempty"`
	RecoveryFlow     string       `yaml:"recovery_flow,omitempty"`
	CompensateAction string       `yaml:"compensate_action,omitempty"`
	CompensateRef    string       `yaml:"compensate_ref,omitempty"`
	// CompensateScopeLocal mirrors NodeWire.CompensateScopeLocal (ADR-0120).
	CompensateScopeLocal bool   `yaml:"compensate_scope_local,omitempty"`
	CancelAction         string `yaml:"cancel_action,omitempty"`
	CompletionAction     string `yaml:"completion_action,omitempty"`
	SignalName           string `yaml:"signal_name,omitempty"`
	MessageName          string `yaml:"message_name,omitempty"`
	CorrelationKey       string `yaml:"correlation_key,omitempty"`
	// MessageStartSingleton mirrors NodeWire.MessageStartSingleton (ADR-0121 review).
	MessageStartSingleton bool   `yaml:"message_start_singleton,omitempty"`
	ErrorCode             string `yaml:"error_code,omitempty"`
	// EndBehavior mirrors NodeWire.EndBehavior — the name-based EndEvent
	// discriminator ("terminate"/"error"); empty means normal (ADR-0127).
	EndBehavior string `yaml:"end_behavior,omitempty"`
	// TerminationReason and TerminationOutcome mirror the like-named NodeWire
	// fields — the EndTerminate payload authored alongside endBehavior:
	// "terminate" (ADR-0119). TerminationOutcome is "complete" or "abort".
	TerminationReason  string          `yaml:"termination_reason,omitempty"`
	TerminationOutcome string          `yaml:"termination_outcome,omitempty"`
	AttachedTo         string          `yaml:"attached_to,omitempty"`
	NonInterrupting    bool            `yaml:"non_interrupting,omitempty"`
	Subprocess         *definitionYAML `yaml:"subprocess,omitempty"`
	DefRef             string          `yaml:"def_ref,omitempty"`
	// Validation mirrors NodeWire.Validation for the YAML authoring form.
	Validation *validate.ValidationDescriptor `yaml:"validation,omitempty"`
}

// definitionYAML is the YAML mirror of ProcessDefinition. It handles nested
// subprocess definitions recursively. Flows decode straight into the canonical
// flow.SequenceFlow — it carries the same snake_case yaml tags (ADR-0144), so no
// mirror struct is needed.
type definitionYAML struct {
	ID            string              `yaml:"id"`
	Version       int                 `yaml:"version"`
	Nodes         []nodeYAML          `yaml:"nodes"`
	Flows         []flow.SequenceFlow `yaml:"flows"`
	CancelActions []string            `yaml:"cancel_actions,omitempty"`
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
		Label:                 ny.Label,
		Action:                ny.Action,
		EligibleRoles:         ny.EligibleRoles,
		EligiblePrivileges:    ny.EligiblePrivileges,
		EligibleExpr:          ny.EligibleExpr,
		Manual:                ny.Manual,
		ManualImmediate:       ny.ManualImmediate,
		Outcomes:              ny.Outcomes,
		ExposeOutcome:         ny.ExposeOutcome,
		OutcomeVariable:       ny.OutcomeVariable,
		TimerDuration:         ny.TimerDuration,
		DeadlineDuration:      ny.DeadlineDuration,
		DeadlineFlow:          ny.DeadlineFlow,
		DeadlineAction:        ny.DeadlineAction,
		WaitEvery:             ny.WaitEvery,
		WaitAction:            ny.WaitAction,
		RetryPolicy:           ny.RetryPolicy,
		RecoveryFlow:          ny.RecoveryFlow,
		CompensateAction:      ny.CompensateAction,
		CompensateRef:         ny.CompensateRef,
		CompensateScopeLocal:  ny.CompensateScopeLocal,
		CancelAction:          ny.CancelAction,
		CompletionAction:      ny.CompletionAction,
		SignalName:            ny.SignalName,
		MessageName:           ny.MessageName,
		CorrelationKey:        ny.CorrelationKey,
		MessageStartSingleton: ny.MessageStartSingleton,
		ErrorCode:             ny.ErrorCode,
		EndBehavior:           ny.EndBehavior,
		TerminationReason:     ny.TerminationReason,
		TerminationOutcome:    ny.TerminationOutcome,
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
	c.flows = slices.Clone(dy.Flows)
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
