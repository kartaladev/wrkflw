// Package validate is the neutral external-input validation port — the
// DECLARATIVE authoring side. A Validator is the executable check; a
// ValidationStrategy (attached to a definition node) provides the runtime
// Validator. Concrete strategies live in opt-in adapter subpackages
// (definition/model/validate/expr, .../callback, .../jsonschema, .../avro) so
// the definition/engine core imports no schema library. The executor side
// (Gate, ErrInvalidInput) lives in runtime/validation, which depends on this
// package.
package validate

import (
	"context"
)

// Validator is the runtime port: the executable check. A non-nil error rejects the
// operation before any state mutation.
type Validator interface {
	Validate(ctx context.Context, input map[string]any) error
}

// ValidationStrategy is attached to a node in the definition and PROVIDES the runtime
// Validator (a strategy may also implement Validator directly). NewValidator is called
// once (may compile a schema); the built Validator is cached by the Gate and reused.
type ValidationStrategy interface {
	NewValidator() (Validator, error)
}

// DescribableStrategy is implemented by DECLARATIVE strategies (expr/json-schema/avro) so
// they round-trip through wire/YAML. The callback strategy does NOT implement it.
type DescribableStrategy interface {
	ValidationStrategy
	Descriptor() ValidationDescriptor
}

// ValidationDescriptor is the serialized form stored on a node's wire representation.
type ValidationDescriptor struct {
	Kind   string `json:"kind" yaml:"kind"`                         // "expr" | "json-schema" | "avro" (registry key)
	Schema string `json:"schema,omitempty" yaml:"schema,omitempty"` // schema text / predicate list (adapter-interpreted)
}
