package jsonschema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
	vjs "github.com/zakyalvan/krtlwrkflw/definition/model/validate/jsonschema"
)

const schemaJSON = `{
  "type": "object",
  "required": ["amount"],
  "properties": { "amount": { "type": "number", "minimum": 0 } }
}`

func TestJSONSchema_Validate(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		strategy func(t *testing.T) validate.DescribableStrategy
		assert   func(t *testing.T, v validate.Validator, err error)
	}{
		"from text": {
			strategy: func(t *testing.T) validate.DescribableStrategy { return vjs.New(schemaJSON) },
			assert: func(t *testing.T, v validate.Validator, err error) {
				require.NoError(t, err, "build")
				assert.NoError(t, v.Validate(t.Context(), map[string]any{"amount": 10.0}), "valid rejected")
				assert.Error(t, v.Validate(t.Context(), map[string]any{"amount": -1.0}), "expected rejection for negative amount")
				assert.Error(t, v.Validate(t.Context(), map[string]any{}), "expected rejection for missing amount")

				// round-trip through Factory
				rebuilt, ferr := vjs.Factory(schemaJSON)
				require.NoError(t, ferr, "factory")
				rv, rerr := rebuilt.NewValidator()
				require.NoError(t, rerr)
				assert.NoError(t, rv.Validate(t.Context(), map[string]any{"amount": 3.0}), "rebuilt rejected valid")
			},
		},
		"from value": {
			strategy: func(t *testing.T) validate.DescribableStrategy {
				s, err := vjs.NewFromValue(map[string]any{
					"type":     "object",
					"required": []any{"amount"},
					"properties": map[string]any{
						"amount": map[string]any{"type": "number", "minimum": 0},
					},
				})
				require.NoError(t, err)
				return s
			},
			assert: func(t *testing.T, v validate.Validator, err error) {
				require.NoError(t, err, "build")
				assert.NoError(t, v.Validate(t.Context(), map[string]any{"amount": 10.0}), "valid rejected")
				assert.Error(t, v.Validate(t.Context(), map[string]any{"amount": -1.0}), "expected rejection for negative amount")
				assert.Error(t, v.Validate(t.Context(), map[string]any{}), "expected rejection for missing amount")

				// round-trip through Factory
				rebuilt, ferr := vjs.Factory(schemaJSON)
				require.NoError(t, ferr, "factory")
				rv, rerr := rebuilt.NewValidator()
				require.NoError(t, rerr)
				assert.NoError(t, rv.Validate(t.Context(), map[string]any{"amount": 3.0}), "rebuilt rejected valid")
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

type startInput struct {
	Amount float64 `json:"amount" jsonschema:"minimum=0"`
}

func TestJSONSchema_FromStruct(t *testing.T) {
	t.Parallel()
	s, err := vjs.NewFromStruct(&startInput{})
	require.NoError(t, err, "from struct")

	v, err := s.NewValidator()
	require.NoError(t, err)
	assert.NoError(t, v.Validate(t.Context(), map[string]any{"amount": 5.0}), "valid rejected")
	assert.Error(t, v.Validate(t.Context(), map[string]any{"amount": -2.0}), "expected rejection")
}

// TestJSONSchema_BuildErrors covers the failure branches of strategy construction and
// validator compilation: a schema value that can't be JSON-marshaled, a struct type that
// can't be reflected into JSON, malformed schema text, and schema text that parses as JSON
// but is not a valid JSON Schema (fails compilation).
func TestJSONSchema_BuildErrors(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"NewFromValue: unmarshalable schema value": {
			assert: func(t *testing.T) {
				s, err := vjs.NewFromValue(map[string]any{"bad": make(chan int)})
				assert.Nil(t, s)
				assert.ErrorContains(t, err, "workflow-validation/jsonschema:")
			},
		},
		"NewFromStruct: recovers invopop panic on unreflectable field type": {
			assert: func(t *testing.T) {
				type unreflectable struct {
					Callback func() `json:"callback"`
				}
				s, err := vjs.NewFromStruct(unreflectable{})
				assert.Nil(t, s)
				assert.ErrorContains(t, err, "workflow-validation/jsonschema:")
			},
		},
		"NewValidator: malformed schema JSON": {
			assert: func(t *testing.T) {
				_, err := vjs.New("not json").NewValidator()
				assert.ErrorContains(t, err, "workflow-validation/jsonschema:")
			},
		},
		"NewValidator: schema fails to compile": {
			assert: func(t *testing.T) {
				_, err := vjs.New(`{"type": 123}`).NewValidator()
				assert.ErrorContains(t, err, "workflow-validation/jsonschema:")
			},
		},
		"Factory then NewValidator: malformed schema JSON": {
			assert: func(t *testing.T) {
				s, err := vjs.Factory("not json")
				require.NoError(t, err, "Factory itself should not fail")
				_, err = s.NewValidator()
				assert.ErrorContains(t, err, "workflow-validation/jsonschema:")
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
