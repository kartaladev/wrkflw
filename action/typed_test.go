package action_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

// approveIn / approveOut are a consumer's own Go types: the engine, runtime and
// persistence never see them, only the map[string]any envelope on either side.
type approveIn struct {
	Ref   string `json:"ref"`
	Count int    `json:"count"`
}

type approveOut struct {
	Approved bool `json:"approved"`
}

func approve(_ context.Context, in approveIn) (approveOut, error) {
	return approveOut{Approved: in.Count > 0}, nil
}

// unencodableOut carries a channel, which encoding/json cannot marshal.
type unencodableOut struct {
	Ch chan int `json:"ch"`
}

// arrayOut marshals to a JSON array, which cannot be decoded into the
// map[string]any envelope the engine carries.
type arrayOut struct{}

func (arrayOut) MarshalJSON() ([]byte, error) { return []byte(`[1,2]`), nil }

// TestTypedDo covers the decode → invoke → encode round trip performed by a
// [action.Typed] action, across the lenient (default) and strict input modes.
func TestTypedDo(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		act    action.Action
		in     map[string]any
		ctx    func(ctx context.Context) context.Context // nil means identity
		assert func(t *testing.T, out map[string]any, err error)
	}

	cases := []testCase{
		{
			name: "lenient default ignores unknown keys and the engine's _idempotencyKey",
			act:  action.Typed(approve),
			in: map[string]any{
				"ref":             "ana-3",
				"count":           2,
				"unrelated":       "a variable the service task did not ask for",
				"_idempotencyKey": "idem-7",
			},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"approved": true}, out)
			},
		},
		{
			name: "strict rejects an unknown field and names it",
			act:  action.Typed(approve, action.WithStrictInput()),
			in: map[string]any{
				"ref":       "ana-3",
				"count":     2,
				"unrelated": "not declared by In",
			},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.ErrorIs(t, err, action.ErrDecodeInput)
				assert.Contains(t, err.Error(), `unrelated`,
					"the error must name the offending field so the operator can fix the envelope")
				assert.False(t, action.IsRetryable(err),
					"a decode failure is deterministic for a snapshot, so retrying cannot help")
				assert.Nil(t, out)
			},
		},
		{
			// Documents the limit ruled on in the plan (R1): WithStrictInput rejects
			// the engine's own _idempotencyKey stamp. It is NOT whitelisted — the
			// limit fails closed and loudly rather than being papered over.
			name: "strict rejects the engine's _idempotencyKey stamp",
			act:  action.Typed(approve, action.WithStrictInput()),
			in: map[string]any{
				"ref":             "ana-3",
				"count":           2,
				"_idempotencyKey": "idem-7",
			},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.ErrorIs(t, err, action.ErrDecodeInput)
				assert.Contains(t, err.Error(), `_idempotencyKey`,
					"strict input on a primary service task must fail loudly naming the stamped key")
				assert.False(t, action.IsRetryable(err))
				assert.Nil(t, out)
			},
		},
		{
			name: "fractional number into an int field is a decode failure",
			act:  action.Typed(approve),
			in:   map[string]any{"ref": "ana-3", "count": 2.5},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.ErrorIs(t, err, action.ErrDecodeInput)
				assert.Contains(t, err.Error(), "count")
				assert.False(t, action.IsRetryable(err))
				assert.Nil(t, out)
			},
		},
		{
			// A nil input marshals to JSON null, which has no members, so even a
			// strict decode succeeds and yields the zero In.
			name: "strict decode of a nil input yields the zero In",
			act:  action.Typed(approve, action.WithStrictInput()),
			in:   nil,
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"approved": false}, out,
					"zero In means Count == 0, so approve reports false")
			},
		},
		{
			// A nil pointer Out marshals to JSON null, which decodes to a nil map:
			// a valid "no output" result, not an error.
			name: "nil pointer Out yields a nil map and no error",
			act: action.Typed(func(context.Context, approveIn) (*approveOut, error) {
				return nil, nil
			}),
			in: map[string]any{"ref": "ana-3", "count": 2},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.NoError(t, err)
				assert.Nil(t, out)
			},
		},
		{
			name: "an input variable that cannot be marshalled is a decode failure",
			act:  action.Typed(approve),
			in:   map[string]any{"ref": "ana-3", "bad": make(chan int)},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.ErrorIs(t, err, action.ErrDecodeInput)
				assert.False(t, action.IsRetryable(err))
				assert.Nil(t, out)
			},
		},
		{
			// Ruling R5: only the INPUT decode is reclassified. An output-encode
			// failure originates in the value fn returned, which the engine's
			// snapshot does not fix, so it keeps the retryable-by-default path.
			name: "output that cannot be marshalled is a plain, retryable error",
			act: action.Typed(func(context.Context, approveIn) (unencodableOut, error) {
				return unencodableOut{Ch: make(chan int)}, nil
			}),
			in: map[string]any{"ref": "ana-3", "count": 2},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "encode typed output")
				assert.NotErrorIs(t, err, action.ErrDecodeInput,
					"an output failure is not an input-decode failure")
				assert.True(t, action.IsRetryable(err),
					"an output-encode failure keeps the retryable-by-default contract")
				assert.Nil(t, out)
			},
		},
		{
			name: "output that is not a JSON object is a plain, retryable error",
			act: action.Typed(func(context.Context, approveIn) (arrayOut, error) {
				return arrayOut{}, nil
			}),
			in: map[string]any{"ref": "ana-3", "count": 2},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "encode typed output")
				assert.NotErrorIs(t, err, action.ErrDecodeInput)
				assert.True(t, action.IsRetryable(err))
				assert.Nil(t, out)
			},
		},
		{
			name: "context reaches fn and its error is passed through unclassified",
			act: action.Typed(func(ctx context.Context, _ approveIn) (approveOut, error) {
				return approveOut{}, ctx.Err()
			}),
			in: map[string]any{"ref": "ana-3", "count": 2},
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			assert: func(t *testing.T, out map[string]any, err error) {
				require.ErrorIs(t, err, context.Canceled)
				assert.NotErrorIs(t, err, action.ErrDecodeInput,
					"fn's own error is not an input-decode failure")
				assert.True(t, action.IsRetryable(err),
					"Typed must not reclassify the error fn returned")
				assert.Nil(t, out)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			out, err := tc.act.Do(ctx, tc.in)
			tc.assert(t, out, err)
		})
	}
}

// TestTypedConstruction pins the construction-time contract: Typed rejects a nil
// fn, and rejects an Out type that cannot carry a map envelope. Both are
// programming errors that no runtime input can trigger and no retry can clear, so
// they panic rather than surfacing as an invocation error.
func TestTypedConstruction(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		construct func() action.Action
		assert    func(t *testing.T, construct func() action.Action)
	}

	accepted := func(t *testing.T, construct func() action.Action) {
		require.NotPanics(t, func() { _ = construct() })
	}
	rejectedAs := func(msg string) func(*testing.T, func() action.Action) {
		return func(t *testing.T, construct func() action.Action) {
			require.PanicsWithError(t, msg, func() { _ = construct() })
		}
	}

	cases := []testCase{
		{
			name:      "struct Out is accepted",
			construct: func() action.Action { return action.Typed(approve) },
			assert:    accepted,
		},
		{
			name: "map[string]any Out is accepted",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) (map[string]any, error) {
					return nil, nil
				})
			},
			assert: accepted,
		},
		{
			name: "pointer-to-struct Out is accepted",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) (*approveOut, error) {
					return nil, nil
				})
			},
			assert: accepted,
		},
		{
			name: "pointer-to-map Out is accepted",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) (*map[string]any, error) {
					return nil, nil
				})
			},
			assert: accepted,
		},
		{
			name: "string Out is rejected",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) (string, error) {
					return "", nil
				})
			},
			assert: rejectedAs("action.Typed: invalid Out type string: " +
				"Out must be a struct or a map, optionally behind a single pointer"),
		},
		{
			name: "int Out is rejected",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) (int, error) {
					return 0, nil
				})
			},
			assert: rejectedAs("action.Typed: invalid Out type int: " +
				"Out must be a struct or a map, optionally behind a single pointer"),
		},
		{
			// Rejected deliberately: an interface can hold a scalar at runtime, so
			// accepting it would move a construction-time failure to invocation time.
			name: "any Out is rejected",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) (any, error) {
					return nil, nil
				})
			},
			assert: rejectedAs("action.Typed: invalid Out type interface {}: " +
				"Out must be a struct or a map, optionally behind a single pointer"),
		},
		{
			name: "slice Out is rejected",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) ([]string, error) {
					return nil, nil
				})
			},
			assert: rejectedAs("action.Typed: invalid Out type []string: " +
				"Out must be a struct or a map, optionally behind a single pointer"),
		},
		{
			// Exactly one pointer level is dereferenced.
			name: "double-pointer Out is rejected",
			construct: func() action.Action {
				return action.Typed(func(context.Context, approveIn) (**approveOut, error) {
					return nil, nil
				})
			},
			assert: rejectedAs("action.Typed: invalid Out type **action_test.approveOut: " +
				"Out must be a struct or a map, optionally behind a single pointer"),
		},
		{
			name: "nil fn is rejected",
			construct: func() action.Action {
				return action.Typed[approveIn, approveOut](nil)
			},
			assert: rejectedAs("action.Typed: fn must not be nil"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.construct)
		})
	}
}

// TestTypedComposesWithWrap pins that a typed action is the INNERMOST bare action:
// Wrap layers a resiliency policy over it, ResolvePolicy reports that policy, and
// Unwrap strips back to the typed action itself — which still decodes.
func TestTypedComposesWithWrap(t *testing.T) {
	t.Parallel()

	const timeout = 3 * time.Second
	typed := action.Typed(approve)
	wrapped := action.Wrap(typed, action.WithExecTimeout(timeout))

	p := action.ResolvePolicy(wrapped)
	require.NotNil(t, p.Timeout, "Wrap must declare the exec timeout over a typed action")
	assert.Equal(t, timeout, *p.Timeout)

	bare := action.Unwrap(wrapped)
	require.NotNil(t, bare)
	assert.Empty(t, action.ResolvePolicy(bare),
		"the typed action is the innermost bare action and declares no policy of its own")

	// The concrete type is unexported, so identity is asserted behaviourally: the
	// unwrapped action still performs the typed decode.
	out, err := bare.Do(t.Context(), map[string]any{"ref": "ana-3", "count": 2})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"approved": true}, out)
}
