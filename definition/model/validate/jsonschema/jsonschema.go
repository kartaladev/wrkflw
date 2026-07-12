// Package jsonschema is a JSON Schema validation adapter. It validates the input map
// against a compiled schema using github.com/santhosh-tekuri/jsonschema/v6, and can also
// DERIVE a schema from a Go type via github.com/invopop/jsonschema (NewFromStruct). Both
// third-party deps are isolated in this package (ADR-0111); the definition/engine core
// never imports them. The serialized descriptor always carries canonical JSON text.
package jsonschema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	invopop "github.com/invopop/jsonschema"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// Kind is the registry key for JSON Schema strategies.
const Kind = "json-schema"

type strategy struct{ schema string } // canonical JSON text

// New builds a strategy from JSON Schema text.
func New(schemaJSON string) validate.DescribableStrategy { return strategy{schema: schemaJSON} }

// NewFromValue builds a strategy from a schema assembled as a Go map.
func NewFromValue(schema map[string]any) (validate.DescribableStrategy, error) {
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: marshal schema value: %w", err)
	}
	return strategy{schema: string(b)}, nil
}

// NewFromStruct derives a JSON Schema from v's type (invopop reflection) and returns a
// strategy. It returns an error (rather than panicking) if v's type contains a field
// invopop's reflector cannot represent as JSON Schema (e.g. a chan or func field) — invopop
// panics internally on such types, and this constructor recovers that panic and converts it
// into an error to honor its (validate.DescribableStrategy, error) contract.
func NewFromStruct(v any) (_ validate.DescribableStrategy, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("workflow-validation/jsonschema: reflect schema from %T: %v", v, r)
		}
	}()
	r := &invopop.Reflector{DoNotReference: true}
	sch := r.Reflect(v)
	b, mErr := json.Marshal(sch)
	if mErr != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: marshal reflected schema: %w", mErr)
	}
	return strategy{schema: string(b)}, nil
}

// Factory rebuilds a strategy from serialized JSON schema text.
func Factory(schema string) (validate.ValidationStrategy, error) {
	return New(schema), nil
}

func (s strategy) Descriptor() validate.ValidationDescriptor {
	return validate.ValidationDescriptor{Kind: Kind, Schema: s.schema}
}

func (s strategy) NewValidator() (validate.Validator, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(s.schema)))
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: parse schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	const resource = "mem://schema.json"
	if err := c.AddResource(resource, doc); err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: add resource: %w", err)
	}
	compiled, err := c.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/jsonschema: compile: %w", err)
	}
	return &validator{schema: compiled}, nil
}

type validator struct{ schema *jsonschema.Schema }

func (v *validator) Validate(_ context.Context, input map[string]any) error {
	if err := v.schema.Validate(input); err != nil {
		return fmt.Errorf("workflow-validation/jsonschema: %w", err)
	}
	return nil
}
