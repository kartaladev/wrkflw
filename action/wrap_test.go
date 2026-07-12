package action_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

// countAction counts invocations and optionally errors/blocks/panics.
type countAction struct {
	calls  *int
	err    error
	block  bool
	panics bool
}

func (a countAction) Do(ctx context.Context, _ map[string]any) (map[string]any, error) {
	if a.calls != nil {
		*a.calls++
	}
	if a.panics {
		panic("boom")
	}
	if a.block {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return nil, nil
		}
	}
	return map[string]any{"ok": true}, a.err
}

// customRetriable implements RetriableAction directly (no Wrap), to prove
// ResolvePolicy detects a consumer type that natively declares a capability.
type customRetriable struct{ p action.RetrySpecs }

func (c customRetriable) Do(context.Context, map[string]any) (map[string]any, error) {
	return nil, nil
}
func (c customRetriable) RetrySpecs() action.RetrySpecs { return c.p }

func TestWrappersStandaloneDo(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func(calls *int) action.Action
		assert func(t *testing.T, calls int, out map[string]any, err error)
	}

	cases := []testCase{
		{
			name: "timed Do enforces the deadline on a blocking action",
			build: func(calls *int) action.Action {
				return action.Wrap(countAction{calls: calls, block: true}, action.WithExecTimeout(20*time.Millisecond))
			},
			assert: func(t *testing.T, calls int, _ map[string]any, err error) {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.Equal(t, 1, calls)
			},
		},
		{
			name: "recoverable Do converts a panic to an error when on",
			build: func(calls *int) action.Action {
				return action.Wrap(countAction{calls: calls, panics: true}, action.WithRecover(true))
			},
			assert: func(t *testing.T, calls int, _ map[string]any, err error) {
				require.Error(t, err)
				assert.Equal(t, 1, calls)
			},
		},
		{
			name: "recoverable Do lets a panic propagate when off",
			build: func(calls *int) action.Action {
				return action.Wrap(countAction{calls: calls, panics: true}, action.WithRecover(false))
			},
			assert: func(t *testing.T, _ int, _ map[string]any, _ error) {
				// asserted via require.Panics below
			},
		},
		{
			name: "retriable Do does NOT retry a failing action",
			build: func(calls *int) action.Action {
				return action.Wrap(countAction{calls: calls, err: errors.New("boom")},
					action.WithRetrySpecs(action.RetrySpecs{MaxAttempts: 5}))
			},
			assert: func(t *testing.T, calls int, _ map[string]any, err error) {
				require.Error(t, err)
				assert.Equal(t, 1, calls, "retriable.Do is declarative; it must call the bare action exactly once")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			a := tc.build(&calls)
			if tc.name == "recoverable Do lets a panic propagate when off" {
				assert.Panics(t, func() { _, _ = a.Do(t.Context(), nil) })
				assert.Equal(t, 1, calls)
				return
			}
			out, err := a.Do(t.Context(), nil)
			tc.assert(t, calls, out, err)
		})
	}
}

func TestResolvePolicyAndWrap(t *testing.T) {
	t.Parallel()

	d10 := 10 * time.Second
	d30 := 30 * time.Second
	rp3 := action.RetrySpecs{MaxAttempts: 3, InitialInterval: time.Second, Multiplier: 2, MaxInterval: time.Minute}
	rp5 := action.RetrySpecs{MaxAttempts: 5, InitialInterval: 2 * time.Second, Multiplier: 3, MaxInterval: time.Hour}
	on := true

	bare := action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) { return nil, nil })

	type testCase struct {
		name   string
		build  func() action.Action
		assert func(t *testing.T, p action.Policy, a action.Action)
	}

	cases := []testCase{
		{
			name:  "bare action has an empty policy",
			build: func() action.Action { return bare },
			assert: func(t *testing.T, p action.Policy, _ action.Action) {
				assert.Nil(t, p.Timeout)
				assert.Nil(t, p.Retry)
				assert.Nil(t, p.Recover)
			},
		},
		{
			name:  "Wrap with no opts returns bare unchanged",
			build: func() action.Action { return action.Wrap(bare) },
			assert: func(t *testing.T, p action.Policy, a action.Action) {
				_, isWrapper := a.(interface{ Unwrap() action.Action })
				assert.False(t, isWrapper, "no-op Wrap must not add a resiliency layer")
				assert.True(t, p.Timeout == nil && p.Retry == nil && p.Recover == nil)
			},
		},
		{
			name: "distinct concerns nest and aggregate",
			build: func() action.Action {
				return action.Wrap(bare, action.WithExecTimeout(d10), action.WithRetrySpecs(rp3), action.WithRecover(on))
			},
			assert: func(t *testing.T, p action.Policy, _ action.Action) {
				require.NotNil(t, p.Timeout)
				assert.Equal(t, d10, *p.Timeout)
				require.NotNil(t, p.Retry)
				assert.Equal(t, rp3, *p.Retry)
				require.NotNil(t, p.Recover)
				assert.True(t, *p.Recover)
			},
		},
		{
			name: "re-wrapping the same concern replaces, never double-stacks",
			build: func() action.Action {
				once := action.Wrap(bare, action.WithExecTimeout(d10), action.WithRetrySpecs(rp3))
				return action.Wrap(once, action.WithExecTimeout(d30), action.WithRetrySpecs(rp5))
			},
			assert: func(t *testing.T, p action.Policy, a action.Action) {
				require.NotNil(t, p.Timeout)
				assert.Equal(t, d30, *p.Timeout, "later timeout must override earlier")
				require.NotNil(t, p.Retry)
				assert.Equal(t, rp5, *p.Retry, "later retry must override earlier")
			},
		},
		{
			name:  "custom type implementing one capability directly is detected",
			build: func() action.Action { return customRetriable{p: rp3} },
			assert: func(t *testing.T, p action.Policy, _ action.Action) {
				require.NotNil(t, p.Retry)
				assert.Equal(t, rp3, *p.Retry)
				assert.Nil(t, p.Timeout)
				assert.Nil(t, p.Recover)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := tc.build()
			p := action.ResolvePolicy(a)
			tc.assert(t, p, a)
		})
	}
}
