package model

import (
	"bytes"
	"errors"
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
	TerminationReason  string `yaml:"termination_reason,omitempty"`
	TerminationOutcome string `yaml:"termination_outcome,omitempty"`
	AttachedTo         string `yaml:"attached_to,omitempty"`
	NonInterrupting    bool   `yaml:"non_interrupting,omitempty"`
	// BoundaryAction / BoundaryErrorExpr mirror the like-named NodeWire fields so
	// that event.WithBoundaryAction and event.WithBoundaryErrorExpr are reachable
	// from YAML, which they were not before backlog 143: a boundary could be
	// attached in YAML but never given an action or an error predicate.
	BoundaryAction    string          `yaml:"boundary_action,omitempty"`
	BoundaryErrorExpr string          `yaml:"boundary_error_expr,omitempty"`
	Subprocess        *definitionYAML `yaml:"subprocess,omitempty"`
	DefRef            string          `yaml:"def_ref,omitempty"`
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
		BoundaryAction:        ny.BoundaryAction,
		BoundaryErrorExpr:     ny.BoundaryErrorExpr,
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

// maxReportedFieldErrors bounds how many per-field messages a YAML parse error
// carries. yaml.v3 reports one line per unknown key, so a document made of
// unknown keys yields an error string larger than the document itself — measured
// at ~2.4x (a 1.9 MB input produced a 4.7 MB error) — and that string is logged
// in full. The first few names are what an author needs to fix the file; the
// rest are a log-flooding vector with no diagnostic value.
const maxReportedFieldErrors = 20

// boundFieldErrors caps the per-field messages in a *yaml.TypeError, replacing
// the tail with a count. Any other error is returned unchanged, so ordinary
// syntax errors keep their exact text and position.
//
// It returns a *yaml.TypeError rather than a flat fmt.Errorf so the concrete
// type does NOT depend on how many fields were wrong: a consumer building a
// field-level 400 by enumerating te.Errors would otherwise get the full list for
// a small document and nothing at all for a large one — precisely the case where
// the list is most useful.
func boundFieldErrors(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) || len(te.Errors) <= maxReportedFieldErrors {
		return err
	}
	bounded := make([]string, 0, maxReportedFieldErrors+1)
	bounded = append(bounded, te.Errors[:maxReportedFieldErrors]...)
	bounded = append(bounded, fmt.Sprintf("and %d more field errors",
		len(te.Errors)-maxReportedFieldErrors))
	return &yaml.TypeError{Errors: bounded}
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
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// io.EOF is tolerated deliberately: yaml.Unmarshal returns nil for an empty
	// or comment-only document while Decode reports io.EOF, so without this the
	// change would silently also make empty input a parse error (ADR-0167 D3a).
	// dy keeps its zero value in that case, exactly as before.
	if err := dec.Decode(&dy); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("workflow-definition: parse YAML: %w", boundFieldErrors(err))
	}
	// Decode consumes exactly ONE document, so KnownFields(true) never sees what
	// follows a `---`: every later document was silently discarded, unknown
	// fields and all. That is the YAML mirror of the JSON trailing-data hole,
	// and it is a live instance of the bypass this change exists to close — an
	// overlay document declaring eligible_roles parses clean and still builds a
	// task with none, so a reviewer reads a rule the engine never applies.
	//
	// An EMPTY trailing document (a bare `---`, or one followed only by
	// whitespace) is legal framing and stays accepted; only a document carrying
	// content is refused.
	for {
		var extra any
		err := dec.Decode(&extra)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("workflow-definition: parse YAML: %w", boundFieldErrors(err))
		}
		// A `null` document (`---` followed by `null` or `~`) decodes to a nil
		// interface and counts as empty, exactly like a bare separator.
		if extra != nil {
			return nil, fmt.Errorf(
				"workflow-definition: parse YAML: a definition must be a single YAML document, but content was found after a %q separator", "---")
		}
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
