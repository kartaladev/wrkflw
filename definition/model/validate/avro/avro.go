// Package avro is an Avro-record validation adapter over github.com/linkedin/goavro/v2.
// It checks that the input map conforms to an Avro record schema by attempting to encode
// the map against the parsed codec; an encode error means the input does not conform. The
// goavro dep is isolated in this package (ADR-0112); the definition/engine core never
// imports it. The serialized descriptor always carries the raw .avsc schema text.
package avro

import (
	"context"
	"fmt"

	"github.com/linkedin/goavro/v2"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// Kind is the registry key for Avro strategies.
const Kind = "avro"

type strategy struct{ avsc string } // raw .avsc record schema text

// New builds a strategy from an Avro record schema (.avsc text).
func New(avsc string) validate.DescribableStrategy { return strategy{avsc: avsc} }

// Factory rebuilds a strategy from serialized .avsc text.
func Factory(schema string) (validate.ValidationStrategy, error) {
	return New(schema), nil
}

func (s strategy) Descriptor() validate.ValidationDescriptor {
	return validate.ValidationDescriptor{Kind: Kind, Schema: s.avsc}
}

func (s strategy) NewValidator() (validate.Validator, error) {
	codec, err := goavro.NewCodec(s.avsc)
	if err != nil {
		return nil, fmt.Errorf("workflow-validation/avro: parse schema: %w", err)
	}
	return &validator{codec: codec}, nil
}

type validator struct{ codec *goavro.Codec }

func (v *validator) Validate(_ context.Context, input map[string]any) error {
	if _, err := v.codec.BinaryFromNative(nil, input); err != nil {
		return fmt.Errorf("workflow-validation/avro: does not conform to avro schema: %w", err)
	}
	return nil
}
