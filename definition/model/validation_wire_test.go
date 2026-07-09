package model_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate/callback"
	vexpr "github.com/zakyalvan/krtlwrkflw/definition/model/validate/expr"
)

// TestStartValidation_WireRoundTrip marshals a StartEvent carrying an expr
// InputValidation strategy to JSON and decodes it back. The decoded strategy is a
// pending reconstruction placeholder (no *validate.Registry is available at
// json.Unmarshal time) but must still be introspectable via model.DescriptorOf —
// proving the descriptor itself round-tripped byte-for-byte — while refusing to
// build a live Validator until reconstructed (see the Loader test below).
func TestStartValidation_WireRoundTrip(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s", event.WithInputValidation(vexpr.New("amount > 0"))),
		},
	}

	data, err := json.Marshal(def)
	require.NoError(t, err)
	require.Contains(t, string(data), `"kind":"expr"`)

	var decoded model.ProcessDefinition
	require.NoError(t, json.Unmarshal(data, &decoded))

	node, ok := decoded.Node("s")
	require.True(t, ok)
	se, ok := node.(event.StartEvent)
	require.True(t, ok)
	require.NotNil(t, se.InputValidation)

	desc, ok := model.DescriptorOf(se.InputValidation)
	require.True(t, ok, "pending strategy must remain describable")
	require.Equal(t, vexpr.Kind, desc.Kind)
	require.Equal(t, "amount > 0", desc.Schema)

	_, err = se.InputValidation.NewValidator()
	require.ErrorIs(t, err, model.ErrValidationNotReconstructed)
}

// TestLoader_WithValidatorRegistry_ReconstructsStrategy is the primary,
// production-shaped path: a YAML definition declares a `validation:` block on its
// start event, and definition.NewLoader(r, definition.WithValidatorRegistry(reg))
// reconstructs the live strategy at Build so it validates identically to one
// authored directly in Go.
func TestLoader_WithValidatorRegistry_ReconstructsStrategy(t *testing.T) {
	t.Parallel()

	const src = `
id: validation-demo
version: 1
nodes:
  - id: s
    kind: startEvent
    validation:
      kind: expr
      schema: "amount > 0"
  - id: e
    kind: endEvent
flows:
  - id: s->e
    source: s
    target: e
`

	tests := map[string]struct {
		registry func() *validate.Registry // nil = no WithValidatorRegistry option at all
		assert   func(t *testing.T, def *model.ProcessDefinition, err error)
	}{
		"registered kind reconstructs and validates identically": {
			registry: func() *validate.Registry {
				reg := validate.NewRegistry()
				reg.Register(vexpr.Kind, vexpr.Factory)
				return reg
			},
			assert: func(t *testing.T, def *model.ProcessDefinition, err error) {
				require.NoError(t, err)
				node, ok := def.Node("s")
				require.True(t, ok)
				se, ok := node.(event.StartEvent)
				require.True(t, ok)
				v, verr := se.InputValidation.NewValidator()
				require.NoError(t, verr)
				require.Error(t, v.Validate(t.Context(), map[string]any{"amount": -1}))
				require.NoError(t, v.Validate(t.Context(), map[string]any{"amount": 5}))
			},
		},
		"missing registry errors": {
			registry: nil,
			assert: func(t *testing.T, _ *model.ProcessDefinition, err error) {
				require.ErrorIs(t, err, model.ErrValidatorRegistryRequired)
			},
		},
		"unregistered kind surfaces unknown kind": {
			registry: func() *validate.Registry { return validate.NewRegistry() },
			assert: func(t *testing.T, _ *model.ProcessDefinition, err error) {
				require.ErrorIs(t, err, validate.ErrUnknownKind)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []definition.LoaderOption
			if tc.registry != nil {
				opts = append(opts, definition.WithValidatorRegistry(tc.registry()))
			}

			ld, err := definition.NewLoader(strings.NewReader(src), opts...)
			require.NoError(t, err)

			def, buildErr := ld.Build()
			tc.assert(t, def, buildErr)
		})
	}
}

// TestMarshalJSON_ValidationStrategyFailClosed asserts the central fail-closed
// check in ProcessDefinition.MarshalJSON: a describable (declarative) strategy
// marshals fine, but a callback strategy (validation/callback — Go-authoring-only,
// non-serializable) makes MarshalJSON return model.ErrUnserializableValidation
// rather than silently dropping the validation requirement.
func TestMarshalJSON_ValidationStrategyFailClosed(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		def    *model.ProcessDefinition
		assert func(t *testing.T, data []byte, err error)
	}{
		"describable strategy marshals": {
			def: &model.ProcessDefinition{
				ID: "d", Version: 1,
				Nodes: []model.Node{
					event.NewStart("s", event.WithInputValidation(vexpr.New("amount > 0"))),
				},
			},
			assert: func(t *testing.T, data []byte, err error) {
				require.NoError(t, err)
				require.Contains(t, string(data), `"kind":"expr"`)
			},
		},
		"callback strategy fails closed": {
			def: &model.ProcessDefinition{
				ID: "d", Version: 1,
				Nodes: []model.Node{
					activity.NewUserTask("u", nil, activity.WithCompletionValidation(
						callback.New(func(_ context.Context, _ map[string]any) error { return nil }),
					)),
				},
			},
			assert: func(t *testing.T, _ []byte, err error) {
				require.ErrorIs(t, err, model.ErrUnserializableValidation)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.def)
			tc.assert(t, data, err)
		})
	}
}
