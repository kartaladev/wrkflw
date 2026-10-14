package action_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

// Characterization tests for the typed-action-input capability
// (openspec/specs/typed-action-input/spec.md): the strict-input rows that the
// TestTypedDo table does not already pin.

// sKeyIn declares only "sKey". U+017F LATIN SMALL LETTER LONG S simple-folds onto
// 's', so encoding/json alone would bind "ſKey" to this field.
type sKeyIn struct {
	SKey string `json:"sKey"`
}

// kKeyIn declares only "kKey". U+212A KELVIN SIGN simple-folds onto 'k'.
type kKeyIn struct {
	KKey string `json:"kKey"`
}

// foldPairIn declares both names whose Unicode fold variants the reject rows use.
type foldPairIn struct {
	SKey string `json:"sKey"`
	KKey string `json:"kKey"`
}

func echoSKey(_ context.Context, in sKeyIn) (sKeyIn, error)             { return in, nil }
func echoKKey(_ context.Context, in kKeyIn) (kKeyIn, error)             { return in, nil }
func echoFoldPair(_ context.Context, in foldPairIn) (foldPairIn, error) { return in, nil }

// echoApprove returns the In it received, so the output shows exactly what the
// function was handed.
func echoApprove(_ context.Context, in approveIn) (approveIn, error) { return in, nil }

func TestBaselineTypedActionInputStrict(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		act    action.Action
		in     map[string]any
		assert func(t *testing.T, out map[string]any, err error)
	}

	// refusedNaming asserts the spec's refusal triple: ErrDecodeInput, not
	// retryable, and the offending key named in the error text.
	refusedNaming := func(named string) func(t *testing.T, out map[string]any, err error) {
		return func(t *testing.T, out map[string]any, err error) {
			t.Helper()
			require.ErrorIs(t, err, action.ErrDecodeInput)
			assert.False(t, action.IsRetryable(err), "a strict-input refusal is not retryable")
			assert.Contains(t, err.Error(), named)
			assert.Nil(t, out)
		}
	}

	cases := []testCase{
		{
			name:   "refuses a case-variant of a flattened embedded field as not retryable",
			act:    action.Typed(strictPtrEcho, action.WithStrictInput()),
			in:     map[string]any{"nested": "trusted", "Nested": "spoofed"},
			assert: refusedNaming("Nested"),
		},
		{
			name:   "refuses a json:\"-\" excluded field name as not retryable",
			act:    action.Typed(strictEcho, action.WithStrictInput()),
			in:     map[string]any{"ref": "ana-3", "Skipped": "x"},
			assert: refusedNaming("Skipped"),
		},
		{
			name:   "refuses an unexported field name as not retryable",
			act:    action.Typed(strictEcho, action.WithStrictInput()),
			in:     map[string]any{"ref": "ana-3", "secret": "x"},
			assert: refusedNaming("secret"),
		},
		{
			name:   "refuses a U+017F LONG S fold variant of a declared name",
			act:    action.Typed(echoSKey, action.WithStrictInput()),
			in:     map[string]any{"sKey": "trusted", "ſKey": "spoofed"},
			assert: refusedNaming("ſKey"),
		},
		{
			name:   "refuses a U+212A KELVIN SIGN fold variant of a declared name",
			act:    action.Typed(echoKKey, action.WithStrictInput()),
			in:     map[string]any{"kKey": "trusted", "KKey": "spoofed"},
			assert: refusedNaming("KKey"),
		},
		{
			name: "accepts the exact declared names sKey and kKey",
			act:  action.Typed(echoFoldPair, action.WithStrictInput()),
			in:   map[string]any{"sKey": "s", "kKey": "k"},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"sKey": "s", "kKey": "k"}, out)
			},
		},
		{
			name: "an empty input map decodes to the zero In",
			act:  action.Typed(echoApprove, action.WithStrictInput()),
			in:   map[string]any{},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"ref": "", "count": float64(0)}, out,
					"the function must receive the zero In")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := tc.act.Do(t.Context(), tc.in)
			tc.assert(t, out, err)
		})
	}
}
