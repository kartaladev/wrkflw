package action_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
)

// TestOptionConstructorsBuildPolicy verifies each WithX option constructor sets its
// own concern (observed through Wrap → ResolvePolicy) and leaves the others unset.
func TestOptionConstructorsBuildPolicy(t *testing.T) {
	t.Parallel()

	bare := action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) { return nil, nil })
	rp := action.RetryPolicy{MaxAttempts: 4, InitialInterval: time.Second, Multiplier: 2, MaxInterval: time.Minute}

	type testCase struct {
		name   string
		opt    action.Option
		assert func(t *testing.T, p action.Policy)
	}

	cases := []testCase{
		{
			name: "WithExecTimeout sets only Timeout",
			opt:  action.WithExecTimeout(15 * time.Second),
			assert: func(t *testing.T, p action.Policy) {
				require.NotNil(t, p.Timeout)
				assert.Equal(t, 15*time.Second, *p.Timeout)
				assert.Nil(t, p.Retry)
				assert.Nil(t, p.Recover)
			},
		},
		{
			name: "WithRetryPolicy sets only Retry",
			opt:  action.WithRetryPolicy(rp),
			assert: func(t *testing.T, p action.Policy) {
				require.NotNil(t, p.Retry)
				assert.Equal(t, rp, *p.Retry)
				assert.Nil(t, p.Timeout)
				assert.Nil(t, p.Recover)
			},
		},
		{
			name: "WithRecover sets only Recover",
			opt:  action.WithRecover(false),
			assert: func(t *testing.T, p action.Policy) {
				require.NotNil(t, p.Recover)
				assert.False(t, *p.Recover)
				assert.Nil(t, p.Timeout)
				assert.Nil(t, p.Retry)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := action.ResolvePolicy(action.Wrap(bare, tc.opt))
			tc.assert(t, p)
		})
	}
}

// TestCapabilityInterfacesAreSatisfiable is a compile-time assertion that a consumer
// type may implement any single capability interface directly.
func TestCapabilityInterfacesAreSatisfiable(t *testing.T) {
	t.Parallel()

	var (
		_ action.TimedAction       = timedOnly{}
		_ action.RetriableAction   = retriableOnly{}
		_ action.RecoverableAction = recoverableOnly{}
	)
	assert.True(t, true)
}

type timedOnly struct{}

func (timedOnly) Do(context.Context, map[string]any) (map[string]any, error) { return nil, nil }
func (timedOnly) ExecTimeout() time.Duration                                 { return time.Second }

type retriableOnly struct{}

func (retriableOnly) Do(context.Context, map[string]any) (map[string]any, error) { return nil, nil }
func (retriableOnly) RetryPolicy() action.RetryPolicy                            { return action.RetryPolicy{} }

type recoverableOnly struct{}

func (recoverableOnly) Do(context.Context, map[string]any) (map[string]any, error) { return nil, nil }
func (recoverableOnly) RecoverPanics() bool                                        { return true }
