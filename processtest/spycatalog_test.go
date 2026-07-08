package processtest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

func TestSpyCatalog(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	type testCase struct {
		name   string
		inner  action.Catalog
		lookup string
		assert func(t *testing.T, spy *processtest.SpyCatalog, act action.Action, ok bool)
	}

	cases := []testCase{
		{
			name: "resolve hit records a successful invocation",
			inner: action.NewCatalog(map[string]action.Action{
				"double": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
					return map[string]any{"out": in["n"].(int) * 2}, nil
				}),
			}),
			lookup: "double",
			assert: func(t *testing.T, spy *processtest.SpyCatalog, act action.Action, ok bool) {
				require.True(t, ok)
				require.NotNil(t, act)

				out, err := act.Do(context.Background(), map[string]any{"n": 21})
				require.NoError(t, err)
				assert.Equal(t, 42, out["out"])

				inv := spy.Invocations()
				require.Len(t, inv, 1)
				assert.Equal(t, "double", inv[0].Name)
				assert.Equal(t, 21, inv[0].In["n"])
				assert.Equal(t, 42, inv[0].Out["out"])
				assert.NoError(t, inv[0].Err)
				assert.Equal(t, 1, spy.Count("double"))
				assert.Len(t, spy.InvocationsOf("double"), 1)
			},
		},
		{
			name: "resolve hit records a failing invocation",
			inner: action.NewCatalog(map[string]action.Action{
				"fail": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					return nil, errBoom
				}),
			}),
			lookup: "fail",
			assert: func(t *testing.T, spy *processtest.SpyCatalog, act action.Action, ok bool) {
				require.True(t, ok)

				_, err := act.Do(context.Background(), nil)
				require.ErrorIs(t, err, errBoom)

				inv := spy.Invocations()
				require.Len(t, inv, 1)
				assert.ErrorIs(t, inv[0].Err, errBoom)
				assert.Equal(t, 1, spy.Count("fail"))
			},
		},
		{
			name:   "nil inner behaves as an empty catalog",
			inner:  nil,
			lookup: "anything",
			assert: func(t *testing.T, spy *processtest.SpyCatalog, act action.Action, ok bool) {
				assert.False(t, ok)
				assert.Nil(t, act)
			},
		},
		{
			name:   "resolve miss returns false and records nothing",
			inner:  action.NewCatalog(nil),
			lookup: "absent",
			assert: func(t *testing.T, spy *processtest.SpyCatalog, act action.Action, ok bool) {
				assert.False(t, ok)
				assert.Nil(t, act)
				assert.Empty(t, spy.Invocations())
				assert.Equal(t, 0, spy.Count("absent"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spy := processtest.NewSpyCatalog(tc.inner)
			act, ok := spy.Resolve(tc.lookup)
			tc.assert(t, spy, act, ok)
		})
	}
}
