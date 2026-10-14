package model_test

// baseline_retry_policy_test.go — characterization tests for model-level rows of
// the retry-policy capability spec (openspec/specs/retry-policy) that no earlier
// test asserted: the empty NonRetryableErrors substring, the Normalize rows
// beyond InitialInterval and BackoffCoef, and the Validate rows other than
// "InitialInterval 1s, BackoffCoef -1".

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestBaselineRetryPolicyEmptySubstringMatchesNothing covers the [""] row of
// "Substring matching of NonRetryableErrors": an empty entry would match every
// text if it were honoured, so it is ignored.
func TestBaselineRetryPolicyEmptySubstringMatchesNothing(t *testing.T) {
	t.Parallel()

	p := model.RetryPolicy{NonRetryableErrors: []string{""}}
	assert.False(t, p.IsNonRetryable("timeout"), `an empty substring must not mark "timeout" non-retryable`)
}

// TestBaselineRetryPolicyNormalize covers the rows of "Normalize fills unset
// fields and keeps set ones" that TestRetryPolicyNormalizeFillsZeros does not.
func TestBaselineRetryPolicyNormalize(t *testing.T) {
	t.Parallel()

	keptPolicy := model.RetryPolicy{
		MaxAttempts: 5, InitialInterval: 5 * time.Second, BackoffCoef: 1.0, MaxInterval: time.Minute,
	}

	type testCase struct {
		name   string
		policy model.RetryPolicy
		assert func(t *testing.T, got model.RetryPolicy)
	}

	cases := []testCase{
		{
			name:   "MaxAttempts 5 fills MaxInterval with 100s",
			policy: model.RetryPolicy{MaxAttempts: 5},
			assert: func(t *testing.T, got model.RetryPolicy) {
				assert.Equal(t, 100*time.Second, got.MaxInterval)
			},
		},
		{
			name:   "BackoffCoef 0.99 is replaced with 2.0",
			policy: model.RetryPolicy{MaxAttempts: 5, BackoffCoef: 0.99},
			assert: func(t *testing.T, got model.RetryPolicy) {
				assert.InDelta(t, 2.0, got.BackoffCoef, 0)
			},
		},
		{
			name:   "a usable InitialInterval 5s is kept",
			policy: keptPolicy,
			assert: func(t *testing.T, got model.RetryPolicy) {
				assert.Equal(t, 5*time.Second, got.InitialInterval)
			},
		},
		{
			name:   "a usable BackoffCoef 1.0 is kept",
			policy: keptPolicy,
			assert: func(t *testing.T, got model.RetryPolicy) {
				assert.InDelta(t, 1.0, got.BackoffCoef, 0)
			},
		},
		{
			name:   "a usable MaxInterval 1m is kept",
			policy: keptPolicy,
			assert: func(t *testing.T, got model.RetryPolicy) {
				assert.Equal(t, time.Minute, got.MaxInterval)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.policy.Normalize())
		})
	}
}

// TestBaselineValidateRetryPolicyRanges covers the rows of "Retry policy
// validation by field values" other than "InitialInterval 1s, BackoffCoef -1",
// which TestValidateRejectsBadRetryPolicy covers.
func TestBaselineValidateRetryPolicyRanges(t *testing.T) {
	t.Parallel()

	rejected := func(t *testing.T, err error) {
		t.Helper()
		require.ErrorIs(t, err, model.ErrInvalidRetryPolicy)
	}
	accepted := func(t *testing.T, err error) {
		t.Helper()
		require.NoError(t, err)
	}

	type testCase struct {
		name   string
		policy model.RetryPolicy
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:   "InitialInterval 1s, BackoffCoef 0.99 is refused",
			policy: model.RetryPolicy{InitialInterval: time.Second, BackoffCoef: 0.99},
			assert: rejected,
		},
		{
			name:   "MaxAttempts -1 is refused",
			policy: model.RetryPolicy{MaxAttempts: -1},
			assert: rejected,
		},
		{
			name:   "InitialInterval -1s is refused",
			policy: model.RetryPolicy{InitialInterval: -time.Second},
			assert: rejected,
		},
		{
			name:   "MaxInterval -1s is refused",
			policy: model.RetryPolicy{MaxInterval: -time.Second},
			assert: rejected,
		},
		{
			name:   "MaxAttempts 3, InitialInterval 1s, BackoffCoef 1.0 is accepted",
			policy: model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 1.0},
			assert: accepted,
		},
		{
			name:   "MaxAttempts 3, InitialInterval 0, BackoffCoef 0.5 is accepted",
			policy: model.RetryPolicy{MaxAttempts: 3, BackoffCoef: 0.5},
			assert: accepted,
		},
		{
			name: "MaxAttempts 3, InitialInterval 1s, BackoffCoef 2, MaxInterval 1m is accepted",
			policy: model.RetryPolicy{
				MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2, MaxInterval: time.Minute,
			},
			assert: accepted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := tc.policy
			def := &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("a"), activity.WithRetryPolicy(&policy)),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
				},
			}
			tc.assert(t, model.Validate(def))
		})
	}
}
