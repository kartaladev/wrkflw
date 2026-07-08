package model

import (
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/validation"
)

// ErrUnserializableValidation is returned by ProcessDefinition.MarshalJSON when a
// node carries a validation.ValidationStrategy that is neither a
// validation.DescribableStrategy nor a pending reconstruction placeholder
// (PendingValidation) — i.e. a validation/callback strategy. Callback strategies
// are a Go-authoring-only escape hatch and cannot round-trip through wire/YAML;
// use a declarative strategy (validation/expr, validation/jsonschema,
// validation/avro) to persist a definition.
var ErrUnserializableValidation = errors.New("workflow-model: validation strategy is not serializable")

// ErrValidatorRegistryRequired is returned by Build when a loaded definition
// carries a pending validation descriptor (decoded from wire/YAML via
// PendingValidation) but no *validation.Registry was configured to reconstruct
// it. See WithValidatorRegistry.
var ErrValidatorRegistryRequired = errors.New("workflow-model: validation descriptor present but no validator registry configured")

// ErrValidationNotReconstructed is returned by a pending validation strategy's
// NewValidator: it is a wire-reconstruction placeholder, not a runnable
// validator, until Build (with a registry) replaces it with the live strategy.
var ErrValidationNotReconstructed = errors.New("workflow-model: validation strategy not reconstructed (missing registry)")

// pendingStrategy is the wire-reconstruction placeholder FromWire/fromNodeYAML
// stash on a node's validation-strategy field when the wire form carried a
// `validation` descriptor. It implements validation.DescribableStrategy — so an
// unresolved definition still round-trips through MarshalJSON/YAML byte-for-byte
// — but NewValidator refuses to run until definitionCore.build() resolves it into
// the live strategy via a *validation.Registry (see WithValidatorRegistry).
type pendingStrategy struct {
	desc validation.ValidationDescriptor
}

// NewValidator always errors: a pendingStrategy is a placeholder, never a
// runnable validator. Build (via WithValidatorRegistry) must replace it first.
func (p pendingStrategy) NewValidator() (validation.Validator, error) {
	return nil, fmt.Errorf("%w: kind %q", ErrValidationNotReconstructed, p.desc.Kind)
}

// Descriptor returns the descriptor this placeholder was constructed from, so it
// remains describable (and therefore serializable) even before reconstruction.
func (p pendingStrategy) Descriptor() validation.ValidationDescriptor { return p.desc }

// PendingValidation returns a placeholder ValidationStrategy carrying desc. Each
// kind's FromWire assigns it to the node's validation-strategy field when the
// wire form carries a `validation` descriptor; Build (via WithValidatorRegistry)
// replaces it with the live strategy reconstructed from the registry.
func PendingValidation(desc validation.ValidationDescriptor) validation.ValidationStrategy {
	return pendingStrategy{desc: desc}
}

// DescriptorOf reports whether s is describable — either a live
// validation.DescribableStrategy or a pending reconstruction placeholder
// (PendingValidation) — returning its descriptor when so. Reports false for nil
// or a non-describable (callback) strategy.
func DescriptorOf(s validation.ValidationStrategy) (validation.ValidationDescriptor, bool) {
	d, ok := s.(validation.DescribableStrategy)
	if !ok {
		return validation.ValidationDescriptor{}, false
	}
	return d.Descriptor(), true
}

// nodeValidationStrategy returns the validation strategy carried by n's
// kind-specific slot (via the registered NodeSpec.ValidationGet), or nil for
// kinds without one or with the slot unset.
func nodeValidationStrategy(n Node) validation.ValidationStrategy {
	s, ok := specFor(n.Kind())
	if !ok || s.ValidationGet == nil {
		return nil
	}
	return s.ValidationGet(n)
}

// reconcileNodeValidation resolves n's pending validation-strategy descriptor (if
// any) into the live strategy via reg, returning a copy of n with the slot
// replaced. Nodes without a validation slot, or whose slot already holds
// something other than a pendingStrategy (live/describable/callback/nil), pass
// through unchanged.
func reconcileNodeValidation(n Node, reg *validation.Registry) (Node, error) {
	s, ok := specFor(n.Kind())
	if !ok || s.ValidationGet == nil {
		return n, nil
	}
	p, isPending := s.ValidationGet(n).(pendingStrategy)
	if !isPending {
		return n, nil
	}
	if reg == nil {
		return nil, fmt.Errorf("%w: node %q kind %q", ErrValidatorRegistryRequired, n.ID(), p.desc.Kind)
	}
	live, err := reg.Strategy(p.desc)
	if err != nil {
		return nil, fmt.Errorf("workflow-model: node %q: %w", n.ID(), err)
	}
	return s.ValidationSet(n, live), nil
}
