package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// noRetryServiceTaskDef returns a process with a single service-task node that
// carries NO node-level RetryPolicy. Used to verify that a default policy
// supplied via WithDefaultRetryPolicy enables retry on this task.
//
//	start → task → end
func noRetryServiceTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "no-node-retry",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{
				ID:     "task",
				Kind:   model.KindServiceTask,
				Action: "a",
				// RetryPolicy intentionally omitted — no node-level policy.
			},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestRunnerDefaultPolicyEnablesRetry verifies that WithDefaultRetryPolicy
// causes a service-task node that declares no RetryPolicy of its own to
// schedule a retry timer when its action fails, instead of failing the instance.
//
// RED proof: before the change (StepOptions{} with nil DefaultRetryPolicy),
// the node has no policy → no retry is scheduled → instance fails (StatusFailed)
// and the recording scheduler captures nothing. So both assertions fail BEFORE
// the implementation and pass AFTER.
func TestRunnerDefaultPolicyEnablesRetry(t *testing.T) {
	cases := []struct {
		name             string
		withDefaultRetry bool
		assert           func(t *testing.T, st engine.InstanceState, sched *recordingScheduler, T time.Time)
	}{
		{
			name:             "with default policy enables retry",
			withDefaultRetry: true,
			assert: func(t *testing.T, st engine.InstanceState, sched *recordingScheduler, T time.Time) {
				t.Helper()
				assert.True(t, sched.scheduled, "expected scheduler to capture a retry timer")
				assert.Equal(t, engine.StatusRunning, st.Status,
					"instance must park (StatusRunning), not fail")
				// attempt 0: backoff = InitialInterval × BackoffCoef^0 = 1s; jitter = 1.0 → 1s
				wantFireAt := T.Add(time.Second)
				assert.True(t, sched.fireAt.Equal(wantFireAt),
					"fireAt must equal T+1s (attempt-0 backoff 1s × jitter 1.0), got %v", sched.fireAt)
			},
		},
		{
			name:             "without default policy fails instance",
			withDefaultRetry: false,
			assert: func(t *testing.T, st engine.InstanceState, sched *recordingScheduler, _ time.Time) {
				t.Helper()
				assert.False(t, sched.scheduled, "scheduler must NOT be called when no retry policy is set")
				assert.Equal(t, engine.StatusFailed, st.Status,
					"instance must fail when no retry policy is configured")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			T := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			clk := clockwork.NewFakeClockAt(T)

			cat := action.NewMapCatalog(map[string]action.ServiceAction{
				"a": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					return nil, errors.New("boom")
				}),
			})

			sched := &recordingScheduler{}

			var opts []runtime.Option
			opts = append(opts, runtime.WithScheduler(sched))
			opts = append(opts, runtime.WithJitterSource(fixedJitter{1.0}))
			if tc.withDefaultRetry {
				opts = append(opts, runtime.WithDefaultRetryPolicy(model.RetryPolicy{
					MaxAttempts:     3,
					InitialInterval: time.Second,
					BackoffCoef:     2,
					MaxInterval:     time.Minute,
				}))
			}

			runner := runtime.NewRunner(cat, clk, runtime.NewMemStore(), opts...)
			def := noRetryServiceTaskDef()

			st, err := runner.Run(t.Context(), def, "p", nil)
			require.NoError(t, err)

			tc.assert(t, st, sched, T)
		})
	}
}
