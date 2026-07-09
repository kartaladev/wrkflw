package model

import (
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// ErrUnserializableValidation is returned by ProcessDefinition.MarshalJSON when a
// node carries a validate.ValidationStrategy that is neither a
// validate.DescribableStrategy nor a pending reconstruction placeholder
// (PendingValidation) — i.e. a validation/callback strategy. Callback strategies
// are a Go-authoring-only escape hatch and cannot round-trip through wire/YAML;
// use a declarative strategy (validation/expr, validation/jsonschema,
// validation/avro) to persist a definition.
var ErrUnserializableValidation = errors.New("workflow-model: validation strategy is not serializable")

// ErrValidatorRegistryRequired is returned by Build when a loaded definition
// carries a pending validation descriptor (decoded from wire/YAML via
// PendingValidation) but no *validate.Registry was configured to reconstruct
// it. See WithValidatorRegistry.
var ErrValidatorRegistryRequired = errors.New("workflow-model: validation descriptor present but no validator registry configured")

// ErrValidationNotReconstructed is returned by a pending validation strategy's
// NewValidator: it is a wire-reconstruction placeholder, not a runnable
// validator, until Build (with a registry) replaces it with the live strategy.
var ErrValidationNotReconstructed = errors.New("workflow-model: validation strategy not reconstructed (missing registry)")

// pendingStrategy is the wire-reconstruction placeholder FromWire/fromNodeYAML
// stash on a node's validation-strategy field when the wire form carried a
// `validation` descriptor. It implements validate.DescribableStrategy — so an
// unresolved definition still round-trips through MarshalJSON/YAML byte-for-byte
// — but NewValidator refuses to run until definitionCore.build() resolves it into
// the live strategy via a *validate.Registry (see WithValidatorRegistry).
type pendingStrategy struct {
	desc validate.ValidationDescriptor
}

// NewValidator always errors: a pendingStrategy is a placeholder, never a
// runnable validator. Build (via WithValidatorRegistry) must replace it first.
func (p pendingStrategy) NewValidator() (validate.Validator, error) {
	return nil, fmt.Errorf("%w: kind %q", ErrValidationNotReconstructed, p.desc.Kind)
}

// Descriptor returns the descriptor this placeholder was constructed from, so it
// remains describable (and therefore serializable) even before reconstruction.
func (p pendingStrategy) Descriptor() validate.ValidationDescriptor { return p.desc }

// PendingValidation returns a placeholder ValidationStrategy carrying desc. Each
// kind's FromWire assigns it to the node's validation-strategy field when the
// wire form carries a `validation` descriptor; Build (via WithValidatorRegistry)
// replaces it with the live strategy reconstructed from the registry.
func PendingValidation(desc validate.ValidationDescriptor) validate.ValidationStrategy {
	return pendingStrategy{desc: desc}
}

// DescriptorOf reports whether s is describable — either a live
// validate.DescribableStrategy or a pending reconstruction placeholder
// (PendingValidation) — returning its descriptor when so. Reports false for nil
// or a non-describable (callback) strategy.
func DescriptorOf(s validate.ValidationStrategy) (validate.ValidationDescriptor, bool) {
	d, ok := s.(validate.DescribableStrategy)
	if !ok {
		return validate.ValidationDescriptor{}, false
	}
	return d.Descriptor(), true
}

// PutValidation encodes s as a wire descriptor, or returns nil when s is unset
// or non-describable (a callback strategy has no serializable form). Mirror of
// PutTrigger; leaf packages call it from their ToWire specs instead of
// hand-rolling the validate.DescribableStrategy type-assert.
func PutValidation(s validate.ValidationStrategy) *validate.ValidationDescriptor {
	d, ok := DescriptorOf(s)
	if !ok {
		return nil
	}
	return &d
}

// ValidationStrategyFor returns the validation strategy carried by n's
// kind-specific slot (via the registered NodeSpec.ValidationGet), or nil for
// kinds without one or with the slot unset. It delegates to the kind's
// registered spec rather than type-switching on concrete node types, since
// model must not import the leaf node packages (definition/event,
// definition/activity) that define them.
func ValidationStrategyFor(n Node) validate.ValidationStrategy {
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
func reconcileNodeValidation(n Node, reg *validate.Registry) (Node, error) {
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
