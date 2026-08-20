package engine_test

// step_retry_backoff_test.go — backlog 40. handleActionFailed computed the retry
// delay as
//
//	delay := time.Duration(t.JitterFraction * float64(eff.Backoff(attempt)))
//
// an UNCONDITIONAL multiplication. [engine.NewActionFailed] leaves
// JitterFraction at its zero value and WithJitter is opt-in, so a consumer
// driving the engine directly — the library's primary use — got
// AfterDuration(0): an immediate retry with no backoff at all, exactly the
// retry-storm the policy exists to prevent.
//
// The shipped runtime was never affected: runtime/processdriver_action.go passes
// engine.WithJitter(driver.jitter.Fraction()) — full jitter, correct by design —
// which is why the defect could sit behind a green suite. This is public library
// API, so the ZERO VALUE has to be the safe one.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// retryBackoffDef is a single service task carrying an explicit retry policy:
// first retry at 1s, doubling, up to 5 attempts.
//
//	start → work[ServiceTask, retry 1s ×2] → end
func retryBackoffDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-retry-backoff", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("work",
				activity.WithTaskAction("do-work"),
				activity.WithRetryPolicy(&model.RetryPolicy{
					MaxAttempts:     5,
					InitialInterval: time.Second,
					BackoffCoef:     2.0,
					MaxInterval:     time.Minute,
				}),
			),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "work"},
			{ID: "f2", Source: "work", Target: "end"},
		},
	}
}

// TestActionFailedRetryBackoffDefaultsToFullInterval pins the delay the retry
// timer is armed with.
//
// What makes the first case fail before the fix: JitterFraction is 0, and the
// old expression multiplied the backoff by it unconditionally, yielding
// AfterDuration(0). The WithJitter cases are the regression control — a sampled
// fraction must still scale the backoff exactly as before.
func TestActionFailedRetryBackoffDefaultsToFullInterval(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		// opts are the trigger options applied to NewActionFailed.
		opts []engine.ActionFailedOption
		want time.Duration
	}

	cases := []testCase{
		{
			name: "no jitter yields the full backoff interval",
			opts: nil,
			want: time.Second,
		},
		{
			name: "explicit jitter scales the backoff",
			opts: []engine.ActionFailedOption{engine.WithJitter(0.5)},
			want: 500 * time.Millisecond,
		},
		{
			// A jitter source that happens to sample exactly 0.0 now yields the
			// full backoff rather than an immediate retry — strictly safer, and
			// the one behaviour delta this change makes to the runtime path.
			name: "a sampled fraction of exactly zero yields the full backoff",
			opts: []engine.ActionFailedOption{engine.WithJitter(0)},
			want: time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := retryBackoffDef()

			started, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)

			var cmdID string
			for _, cmd := range started.Commands {
				if ia, ok := cmd.(engine.InvokeAction); ok {
					cmdID = ia.CommandID
				}
			}
			require.NotEmpty(t, cmdID, "the fixture must invoke a service action")

			res, err := engine.Step(t.Context(), def, started.State,
				engine.NewActionFailed(at.Add(time.Minute), cmdID, "boom", true, tc.opts...),
				engine.StepOptions{})
			require.NoError(t, err)

			var retry *engine.ScheduleTimer
			for _, cmd := range res.Commands {
				if st, ok := cmd.(engine.ScheduleTimer); ok && st.Kind == engine.TimerRetry {
					retry = &st
				}
			}
			require.NotNil(t, retry, "a non-terminal failure under a retry policy must arm a retry timer")
			assert.Equal(t, schedule.AfterDuration(tc.want), retry.Trigger,
				"retry backoff delay")
		})
	}
}
