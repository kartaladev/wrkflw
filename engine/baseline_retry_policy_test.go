package engine_test

// baseline_retry_policy_test.go — characterization tests for engine-level rows
// of the retry-policy capability spec (openspec/specs/retry-policy) that no
// earlier test asserted: the first retry parks the token without a jitter
// fraction, and the NonRetryableErrors and MaxElapsed terminal conditions with
// their legal neighbours. All rows drive retryDef (retry_test.go):
// start → task(service) → end.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// TestBaselineFirstRetryParksTheToken covers "The first retry parks the token
// and counts the attempt" for both jitter rows; TestStepSchedulesRetryWithJitteredBackoff
// covers only the 0.5 row.
func TestBaselineFirstRetryParksTheToken(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		opts []engine.ActionFailedOption
	}

	cases := []testCase{
		{name: "jitter not set", opts: nil},
		{name: "jitter 0.5", opts: []engine.ActionFailedOption{engine.WithJitter(0.5)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := retryDef(&model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2})
			r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "p"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)

			r2, err := engine.Step(t.Context(), def, r1.State,
				engine.NewActionFailed(at.Add(time.Second), findInvokeActionCmdID(t, r1.Commands), "boom", true, tc.opts...),
				engine.StepOptions{})
			require.NoError(t, err)

			timer, found := findScheduleTimerByKind(r2.Commands, engine.TimerRetry)
			require.True(t, found, "a retryable failure with budget left must schedule a retry timer")
			tok := findTokenByNodeID(t, r2.State.Tokens, "task")
			assert.Equal(t, engine.TokenWaiting, tok.State, "the token is parked")
			assert.Equal(t, timer.TimerID, tok.AwaitCommand, "the token waits on the scheduled retry timer")
			assert.Equal(t, 1, tok.RetryAttempts, "one retry attempt is counted")
		})
	}
}

// TestBaselineRetryTerminalConditions covers the NonRetryableErrors and
// MaxElapsed rows of "Terminal conditions end retrying" and "The legal
// neighbours of each terminal condition are retried".
//
// A row with a positive `after` first fails retryably at t0, which schedules the
// first retry and anchors the elapsed budget there, then fires that retry timer
// and fails again `after` past t0. The assertion reads the commands of that last
// failure. Every row is under MaxAttempts 5, so the attempt budget never decides.
func TestBaselineRetryTerminalConditions(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		policy model.RetryPolicy
		// after, when positive, is how long after the first retry was scheduled
		// the asserted failure occurs; zero means the asserted failure is the first.
		after  time.Duration
		errMsg string
		assert func(t *testing.T, r engine.StepResult)
	}

	terminal := func(t *testing.T, r engine.StepResult) {
		t.Helper()
		_, found := findScheduleTimerByKind(r.Commands, engine.TimerRetry)
		assert.False(t, found, "a terminal condition must schedule no retry timer")
		assert.Len(t, r.State.Incidents, 1, "with no catch flow or boundary the terminal failure parks as an incident")
	}
	retried := func(t *testing.T, r engine.StepResult) {
		t.Helper()
		_, found := findScheduleTimerByKind(r.Commands, engine.TimerRetry)
		assert.True(t, found, "a legal neighbour of a terminal condition must arm a retry timer")
		assert.Empty(t, r.State.Incidents)
	}

	nonRetryableErrors := model.RetryPolicy{
		MaxAttempts: 5, InitialInterval: time.Second, BackoffCoef: 2,
		NonRetryableErrors: []string{"validation"},
	}
	maxElapsed := model.RetryPolicy{
		MaxAttempts: 5, InitialInterval: time.Second, BackoffCoef: 2,
		MaxElapsed: time.Minute,
	}

	cases := []testCase{
		{
			name:   "NonRetryableErrors validation, error input validation failed is terminal",
			policy: nonRetryableErrors,
			errMsg: "input validation failed",
			assert: terminal,
		},
		{
			name:   "NonRetryableErrors validation, error timeout is retried",
			policy: nonRetryableErrors,
			errMsg: "timeout",
			assert: retried,
		},
		{
			name:   "MaxElapsed 1m, 199s after the first retry is terminal",
			policy: maxElapsed,
			after:  199 * time.Second,
			errMsg: "boom",
			assert: terminal,
		},
		{
			name:   "MaxElapsed 1m, 29s after the first retry is retried",
			policy: maxElapsed,
			after:  29 * time.Second,
			errMsg: "boom",
			assert: retried,
		},
		{
			name:   "MaxElapsed 1m, exactly 60s after the first retry is retried",
			policy: maxElapsed,
			after:  60 * time.Second,
			errMsg: "boom",
			assert: retried,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := tc.policy
			def := retryDef(&policy)
			r, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "p"},
				engine.NewStartInstance(t0, nil), engine.StepOptions{})
			require.NoError(t, err)

			if tc.after > 0 {
				r, err = engine.Step(t.Context(), def, r.State,
					engine.NewActionFailed(t0, findInvokeActionCmdID(t, r.Commands), "first", true),
					engine.StepOptions{})
				require.NoError(t, err)
				timer, found := findScheduleTimerByKind(r.Commands, engine.TimerRetry)
				require.True(t, found, "fixture: the first failure schedules the first retry")
				require.Equal(t, t0, findTokenByNodeID(t, r.State.Tokens, "task").RetryStartedAt,
					"fixture: the elapsed budget is anchored when the first retry is scheduled")

				r, err = engine.Step(t.Context(), def, r.State,
					engine.NewTimerFired(t0.Add(time.Second), timer.TimerID), engine.StepOptions{})
				require.NoError(t, err)
			}

			r, err = engine.Step(t.Context(), def, r.State,
				engine.NewActionFailed(t0.Add(tc.after), findInvokeActionCmdID(t, r.Commands), tc.errMsg, true),
				engine.StepOptions{})
			require.NoError(t, err)
			tc.assert(t, r)
		})
	}
}
