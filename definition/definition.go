// Package definition is the root authoring entry point for the
// process-definition layer. It intentionally holds only the two constructors —
// NewBuilder (Go) and NewLoader (YAML) — because they are the one place that can
// import definition/build (which imports the node-family leaf packages) without
// creating an import cycle.
//
// Every other symbol lives in — and is used directly from — its source package:
//
//   - types & validation: definition/model (model.Node, model.ProcessDefinition,
//     model.Validate, model.KindServiceTask, model.RetryPolicy, the accessors,
//     the ErrX sentinels, …)
//   - sequence flows:      definition/flow (flow.SequenceFlow, flow.WithCondition, …)
//   - node constructors:   definition/{event,gateway,activity}
//   - deserialization:     blank-import definition/kinds
//
// Example:
//
//	def, err := definition.NewBuilder("order", 1).
//		AddStartEvent("start").
//		AddServiceTask("charge", activity.WithActionName("charge-card")).
//		AddEndEvent("end").
//		Connect("start", "charge").Connect("charge", "end").
//		Build() // returns *model.ProcessDefinition
package definition

import (
	"io"

	"github.com/zakyalvan/krtlwrkflw/definition/build"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// Qualifier references a process definition by id and version (0 == latest).
type Qualifier = model.Qualifier

// Latest returns a Qualifier resolving the newest registered version of id.
func Latest(id string) Qualifier { return model.Latest(id) }

// Version returns a Qualifier pinned to (id, v).
func Version(id string, v int) Qualifier { return model.Version(id, v) }

// ParseQualifier parses "id" or "id:version" into a Qualifier.
func ParseQualifier(s string) (Qualifier, error) { return model.ParseQualifier(s) }

// NewBuilder starts the fluent builder for a definition with the given id and
// version. Each AddX method mirrors a node-family constructor; Build returns a
// *model.ProcessDefinition.
func NewBuilder(id string, version int) *build.Builder { return build.NewBuilder(id, version) }

// LoaderOption configures a DefinitionLoader before Build; see
// WithValidatorRegistry.
type LoaderOption = build.LoaderOption

// WithValidatorRegistry configures the *validate.Registry NewLoader uses to
// reconstruct validation-strategy descriptors decoded from a definition's
// wire/YAML `validation` block (see validate.Registry,
// validate.DescribableStrategy, validate.ValidationDescriptor). When omitted,
// Build falls back to validate.DefaultRegistry (adapters self-register via
// init()); an unregistered kind then fails with validate.ErrUnknownKind.
func WithValidatorRegistry(reg *validate.Registry) LoaderOption {
	return build.WithValidatorRegistry(reg)
}

// NewLoader reads a YAML process-definition from r and returns a
// model.DefinitionLoader whose structure is already declared. Register
// definition-scoped actions via RegisterAction/RegisterActionFunc, apply any
// LoaderOption (e.g. WithValidatorRegistry), then call Build.
func NewLoader(r io.Reader, opts ...LoaderOption) (model.DefinitionLoader, error) {
	return build.NewLoader(r, opts...)
}
