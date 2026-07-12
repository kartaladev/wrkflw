package model_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/model/validate"
	"github.com/kartaladev/wrkflw/definition/model/validate/callback"
	vexpr "github.com/kartaladev/wrkflw/definition/model/validate/expr"
)

// TestStartValidation_WireRoundTrip marshals a StartEvent carrying an expr
// InputValidation strategy to JSON and decodes it back, asserting the descriptor
// round-trips byte-for-byte (model.DescriptorOf preserves Kind + Schema). Because
// this test binary imports the expr adapter, "expr" is registered in the process-
// global DefaultRegistry, so UnmarshalJSON reconstructs the strategy LIVE — a
// runnable Validator, not the pending placeholder. The fail-closed asymmetry for
// an unregistered kind is covered by TestDurableReload_ReconstructsViaDefaultRegistry.
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
	require.True(t, ok, "reconstructed strategy must remain describable")
	require.Equal(t, vexpr.Kind, desc.Kind)
	require.Equal(t, "amount > 0", desc.Schema)

	v, err := se.InputValidation.NewValidator()
	require.NoError(t, err, "expr registered in DefaultRegistry must reconstruct live on reload")
	require.Error(t, v.Validate(t.Context(), map[string]any{"amount": -1}))
	require.NoError(t, v.Validate(t.Context(), map[string]any{"amount": 5}))
}

// TestDurableReload_ReconstructsViaDefaultRegistry proves the durable-reload path:
// a validation-bearing definition that was persisted (MarshalJSON) reconstructs its
// live strategy on json.Unmarshal via the process-global validate.DefaultRegistry,
// which the expr adapter self-registered through its init(). A registered kind
// yields a runnable Validator; an unregistered kind stays pending and fails CLOSED
// at runtime (ErrValidationNotReconstructed) — never an unmarshal error.
func TestDurableReload_ReconstructsViaDefaultRegistry(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data   func(t *testing.T) []byte
		assert func(t *testing.T, def *model.ProcessDefinition, err error)
	}{
		"registered kind reconstructs live and validates": {
			data: func(t *testing.T) []byte {
				def := &model.ProcessDefinition{
					ID: "d", Version: 1,
					Nodes: []model.Node{
						event.NewStart("s", event.WithInputValidation(vexpr.New("amount > 0"))),
					},
				}
				data, err := json.Marshal(def)
				require.NoError(t, err)
				return data
			},
			assert: func(t *testing.T, def *model.ProcessDefinition, err error) {
				require.NoError(t, err)
				node, ok := def.Node("s")
				require.True(t, ok)
				se, ok := node.(event.StartEvent)
				require.True(t, ok)
				v, verr := se.InputValidation.NewValidator()
				require.NoError(t, verr, "durably-reloaded expr strategy must reconstruct live")
				require.Error(t, v.Validate(t.Context(), map[string]any{"amount": -1}))
				require.NoError(t, v.Validate(t.Context(), map[string]any{"amount": 5}))
			},
		},
		"unregistered kind stays pending and fails closed": {
			data: func(t *testing.T) []byte {
				return []byte(`{"id":"d","version":1,"nodes":[` +
					`{"id":"s","kind":"startEvent","validation":{"kind":"bogus","schema":"x"}}]}`)
			},
			assert: func(t *testing.T, def *model.ProcessDefinition, err error) {
				require.NoError(t, err, "unregistered kind must not break unmarshal")
				node, ok := def.Node("s")
				require.True(t, ok)
				se, ok := node.(event.StartEvent)
				require.True(t, ok)
				require.NotNil(t, se.InputValidation)
				_, verr := se.InputValidation.NewValidator()
				require.ErrorIs(t, verr, model.ErrValidationNotReconstructed)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var decoded model.ProcessDefinition
			err := json.Unmarshal(tc.data(t), &decoded)
			tc.assert(t, &decoded, err)
		})
	}
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
		"no explicit registry falls back to DefaultRegistry": {
			registry: nil,
			assert: func(t *testing.T, def *model.ProcessDefinition, err error) {
				require.NoError(t, err, "expr is self-registered in DefaultRegistry, so Build succeeds without an explicit registry")
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

// TestValidationStrategyFor asserts model.ValidationStrategyFor resolves the
// validation strategy carried by each of the 4 slot-bearing kinds without a
// type switch (via the registered NodeSpec.ValidationGet), and returns nil for
// a kind without a validation slot.
func TestValidationStrategyFor(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		node   model.Node
		assert func(t *testing.T, s validate.ValidationStrategy)
	}{
		"start event input validation": {
			node: event.NewStart("s", event.WithInputValidation(vexpr.New("a > 0"))),
			assert: func(t *testing.T, s validate.ValidationStrategy) {
				require.NotNil(t, s)
			},
		},
		"intermediate catch event payload validation": {
			node: event.NewIntermediateCatch("wait", event.WithPayloadValidation(vexpr.New("a > 0"))),
			assert: func(t *testing.T, s validate.ValidationStrategy) {
				require.NotNil(t, s)
			},
		},
		"user task completion validation": {
			node: activity.NewUserTask("approve", activity.WithCompletionValidation(vexpr.New("a > 0"))),
			assert: func(t *testing.T, s validate.ValidationStrategy) {
				require.NotNil(t, s)
			},
		},
		"receive task payload validation": {
			node: activity.NewReceiveTask("await", "OrderPlaced", activity.WithPayloadValidation(vexpr.New("a > 0"))),
			assert: func(t *testing.T, s validate.ValidationStrategy) {
				require.NotNil(t, s)
			},
		},
		"plain node without a validation slot": {
			node: event.NewEnd("end"),
			assert: func(t *testing.T, s validate.ValidationStrategy) {
				require.Nil(t, s)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, model.ValidationStrategyFor(tc.node))
		})
	}
}

// TestPutValidation asserts model.PutValidation (the mirror of PutTrigger) encodes
// a describable strategy as a non-nil *validate.ValidationDescriptor carrying the
// right Kind, and returns nil for a non-describable (callback) strategy and for a
// nil (unset) strategy.
func TestPutValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		strategy validate.ValidationStrategy
		assert   func(t *testing.T, w *validate.ValidationDescriptor)
	}{
		"describable strategy encodes": {
			strategy: vexpr.New("amount > 0"),
			assert: func(t *testing.T, w *validate.ValidationDescriptor) {
				require.NotNil(t, w)
				require.Equal(t, vexpr.Kind, w.Kind)
			},
		},
		"callback strategy is nil": {
			strategy: callback.New(func(_ context.Context, _ map[string]any) error { return nil }),
			assert: func(t *testing.T, w *validate.ValidationDescriptor) {
				require.Nil(t, w)
			},
		},
		"nil strategy is nil": {
			strategy: nil,
			assert: func(t *testing.T, w *validate.ValidationDescriptor) {
				require.Nil(t, w)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, model.PutValidation(tc.strategy))
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
					activity.NewUserTask("u", activity.WithCompletionValidation(
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
