package runtime_test

// baseline_retry_policy_test.go — characterization test for the retry-policy
// row "Terminal conditions end retrying" / MaxAttempts 3, a non-retryable error
// (openspec/specs/retry-policy), driven with a PLAIN action.NonRetryable error
// rather than the typed-decode failure that previously stood in for it.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// TestBaselineNonRetryableErrorUnderAPolicyIsTerminal runs both rows under the
// SAME MaxAttempts 3 policy, so the only variable is the error's classification.
// The plain-error row is the control that proves the harness can observe an
// armed retry timer at all.
func TestBaselineNonRetryableErrorUnderAPolicyIsTerminal(t *testing.T) {
	t.Parallel()

	const instanceID = "nr-1"

	type testCase struct {
		name   string
		actErr error
		assert func(t *testing.T, st engine.InstanceState, armed bool)
	}

	cases := []testCase{
		{
			name:   "a plain action.NonRetryable error arms no retry timer",
			actErr: action.NonRetryable(errors.New("4xx")),
			assert: func(t *testing.T, st engine.InstanceState, armed bool) {
				assert.False(t, armed, "a non-retryable error must end retrying at once")
				assert.Equal(t, engine.StatusRunning, st.Status)
				assert.Len(t, st.Incidents, 1,
					"a terminal failure under a retry policy parks the token as an incident")
			},
		},
		{
			name:   "control: a plain error under the same policy arms a retry timer",
			actErr: errors.New("transient"),
			assert: func(t *testing.T, st engine.InstanceState, armed bool) {
				assert.True(t, armed, "a retryable error with budget left must arm a retry timer")
				assert.Equal(t, engine.StatusRunning, st.Status)
				assert.Empty(t, st.Incidents)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := clockwork.NewFakeClockAt(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
			sched := &runtimetest.RecordingScheduler{Clock: clk}
			act := action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
				return nil, tc.actErr
			})
			driver := runtimetest.MustProcessDriver(t,
				action.NewCatalog(map[string]action.Action{"t": act}),
				runtimetest.MustMemStore(t),
				runtime.WithClock(clk),
				runtime.WithScheduler(sched),
				runtime.WithDefaultRetryPolicy(model.RetryPolicy{
					MaxAttempts:     3,
					InitialInterval: time.Second,
					BackoffCoef:     2,
					MaxInterval:     time.Minute,
				}),
			)

			st, err := driver.Drive(t.Context(), typedTaskDef(), instanceID, nil)
			require.NoError(t, err, "an action failure must not surface as a Go error from Drive")
			tc.assert(t, st, sched.Armed)
		})
	}
}
