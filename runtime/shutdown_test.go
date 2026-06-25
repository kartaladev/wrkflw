package runtime_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestShutdownGroup(t *testing.T) {
	t.Parallel()

	errA := errors.New("a failed")
	errC := errors.New("c failed")

	type testCase struct {
		name string
		// build registers shutdowns onto g and returns the slice that records
		// the order in which shutdowns actually ran.
		build  func(g *runtime.ShutdownGroup, order *[]string)
		ctx    func(ctx context.Context) context.Context // nil means identity
		assert func(t *testing.T, order []string, err error)
	}

	cases := []testCase{
		{
			name: "closes in reverse registration order",
			build: func(g *runtime.ShutdownGroup, order *[]string) {
				g.Add(func(context.Context) error { *order = append(*order, "first"); return nil })
				g.Add(func(context.Context) error { *order = append(*order, "second"); return nil })
				g.Add(func(context.Context) error { *order = append(*order, "third"); return nil })
			},
			assert: func(t *testing.T, order []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"third", "second", "first"}, order)
			},
		},
		{
			name: "a failing shutdown does not prevent the others and errors join",
			build: func(g *runtime.ShutdownGroup, order *[]string) {
				g.Add(func(context.Context) error { *order = append(*order, "a"); return errA })
				g.Add(func(context.Context) error { *order = append(*order, "b"); return nil })
				g.Add(func(context.Context) error { *order = append(*order, "c"); return errC })
			},
			assert: func(t *testing.T, order []string, err error) {
				// All three ran, in reverse order, despite c and a failing.
				assert.Equal(t, []string{"c", "b", "a"}, order)
				require.Error(t, err)
				assert.ErrorIs(t, err, errA)
				assert.ErrorIs(t, err, errC)
			},
		},
		{
			name: "AddCloser adapts an io.Closer",
			build: func(g *runtime.ShutdownGroup, order *[]string) {
				g.AddCloser(closerFunc(func() error { *order = append(*order, "closer"); return nil }))
			},
			assert: func(t *testing.T, order []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"closer"}, order)
			},
		},
		{
			name:  "empty group is a no-op",
			build: func(*runtime.ShutdownGroup, *[]string) {},
			assert: func(t *testing.T, order []string, err error) {
				require.NoError(t, err)
				assert.Empty(t, order)
			},
		},
		{
			name: "nil func and nil closer are ignored",
			build: func(g *runtime.ShutdownGroup, order *[]string) {
				g.Add(nil)
				g.AddCloser(nil)
				g.Add(func(context.Context) error { *order = append(*order, "real"); return nil })
			},
			assert: func(t *testing.T, order []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"real"}, order)
			},
		},
		{
			name: "shutdown stops at a canceled context after running it",
			build: func(g *runtime.ShutdownGroup, order *[]string) {
				g.Add(func(ctx context.Context) error {
					*order = append(*order, "uses-ctx")
					return ctx.Err()
				})
			},
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			assert: func(t *testing.T, order []string, err error) {
				assert.Equal(t, []string{"uses-ctx"}, order)
				require.ErrorIs(t, err, context.Canceled)
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

			var g runtime.ShutdownGroup
			var order []string
			tc.build(&g, &order)

			err := g.Shutdown(ctx)
			tc.assert(t, order, err)
		})
	}
}

func TestShutdownGroupIdempotent(t *testing.T) {
	t.Parallel()

	var g runtime.ShutdownGroup
	var calls int
	g.Add(func(context.Context) error { calls++; return nil })

	require.NoError(t, g.Shutdown(t.Context()))
	require.NoError(t, g.Shutdown(t.Context())) // second call is a no-op
	assert.Equal(t, 1, calls)
}

// TestShutdownGroupAddAfterShutdownClosesImmediately asserts that a component
// registered AFTER the group has already shut down is not silently dropped
// (which would leak its resource) — it is closed immediately instead.
func TestShutdownGroupAddAfterShutdownClosesImmediately(t *testing.T) {
	t.Parallel()

	var g runtime.ShutdownGroup
	require.NoError(t, g.Shutdown(t.Context()))

	closed := false
	g.Add(func(context.Context) error { closed = true; return nil })
	assert.True(t, closed, "a component added after Shutdown must be closed immediately, not dropped")

	// AddCloser routes through Add, so it benefits from the same guard.
	closerRan := false
	g.AddCloser(closerFunc(func() error { closerRan = true; return nil }))
	assert.True(t, closerRan, "AddCloser after Shutdown must also close immediately")
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

var _ io.Closer = closerFunc(nil)
