package jsonschema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vjs "github.com/kartaladev/wrkflw/definition/model/validate/jsonschema"
)

// TestBaselineJSONSchemaRoundTripsThroughItsDescriptor characterizes the
// definition-validation scenario "A json-schema strategy round-trips through its
// descriptor" (openspec/specs/definition-validation/spec.md): a strategy rebuilt
// by the json-schema Factory from the ORIGINAL strategy's descriptor accepts and
// rejects exactly the inputs the original does.
func TestBaselineJSONSchemaRoundTripsThroughItsDescriptor(t *testing.T) {
	t.Parallel()

	original := vjs.New(schemaJSON)
	desc := original.Descriptor()
	require.Equal(t, vjs.Kind, desc.Kind, "the descriptor must name the json-schema kind")

	rebuilt, err := vjs.Factory(desc.Schema)
	require.NoError(t, err, "Factory from the descriptor's schema")

	origV, err := original.NewValidator()
	require.NoError(t, err, "original validator")
	rebuiltV, err := rebuilt.NewValidator()
	require.NoError(t, err, "rebuilt validator")

	type testCase struct {
		name   string
		input  map[string]any
		accept bool
	}

	cases := []testCase{
		{name: "positive amount", input: map[string]any{"amount": 10.0}, accept: true},
		{name: "zero amount at the minimum", input: map[string]any{"amount": 0.0}, accept: true},
		{name: "negative amount", input: map[string]any{"amount": -1.0}, accept: false},
		{name: "missing amount", input: map[string]any{}, accept: false},
		{name: "amount of the wrong type", input: map[string]any{"amount": "ten"}, accept: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			origErr := origV.Validate(t.Context(), tc.input)
			rebuiltErr := rebuiltV.Validate(t.Context(), tc.input)

			assert.Equal(t, tc.accept, origErr == nil, "original outcome; err = %v", origErr)
			assert.Equal(t, origErr == nil, rebuiltErr == nil,
				"the rebuilt validator must agree with the original; original err = %v, rebuilt err = %v", origErr, rebuiltErr)
		})
	}
}
