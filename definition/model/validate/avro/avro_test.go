package avro_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
	vavro "github.com/zakyalvan/krtlwrkflw/definition/model/validate/avro"
)

const avsc = `{
  "type": "record",
  "name": "StartInput",
  "fields": [
    {"name": "amount", "type": "double"},
    {"name": "decision", "type": "string"}
  ]
}`

func TestAvro_Validate(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		strategy func(t *testing.T) validate.DescribableStrategy
		assert   func(t *testing.T, v validate.Validator, err error)
	}{
		"from text": {
			strategy: func(t *testing.T) validate.DescribableStrategy { return vavro.New(avsc) },
			assert: func(t *testing.T, v validate.Validator, err error) {
				require.NoError(t, err, "build")
				assert.NoError(t, v.Validate(t.Context(), map[string]any{"amount": 10.0, "decision": "approve"}), "valid rejected")
				assert.Error(t, v.Validate(t.Context(), map[string]any{"amount": 10.0}), "expected rejection for missing field")
				assert.Error(t, v.Validate(t.Context(), map[string]any{"amount": "nan", "decision": "x"}), "expected rejection for wrong type")

				// round-trip through Factory
				rebuilt, ferr := vavro.Factory(avsc)
				require.NoError(t, ferr, "factory")
				rv, rerr := rebuilt.NewValidator()
				require.NoError(t, rerr)
				assert.NoError(t, rv.Validate(t.Context(), map[string]any{"amount": 1.0, "decision": "reject"}), "rebuilt rejected valid")
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := tc.strategy(t)
			v, err := s.NewValidator()
			tc.assert(t, v, err)
		})
	}
}

func TestAvro_Descriptor(t *testing.T) {
	t.Parallel()
	s := vavro.New(avsc)
	d := s.Descriptor()
	assert.Equal(t, vavro.Kind, d.Kind)
	assert.Equal(t, avsc, d.Schema)
}

// TestAvro_BuildErrors covers the failure branches of strategy construction: malformed
// schema text fails to parse both via New and via a Factory round-trip.
func TestAvro_BuildErrors(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"NewValidator: malformed schema JSON": {
			assert: func(t *testing.T) {
				_, err := vavro.New("not json").NewValidator()
				assert.ErrorContains(t, err, "workflow-validation/avro:")
			},
		},
		"Factory then NewValidator: malformed schema JSON": {
			assert: func(t *testing.T) {
				s, err := vavro.Factory("not json")
				require.NoError(t, err, "Factory itself should not fail")
				_, err = s.NewValidator()
				assert.ErrorContains(t, err, "workflow-validation/avro:")
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}
