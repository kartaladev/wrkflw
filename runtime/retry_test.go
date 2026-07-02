package runtime_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
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
			model.NewStartEvent("start"),
			// RetryPolicy intentionally omitted — no node-level policy.
			model.NewServiceTask("task", model.WithActionName("a")),
			model.NewEndEvent("end"),
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

			runner := mustRunner(t, cat, mustMemStore(t), append([]runtime.Option{runtime.WithRunnerClock(clk)}, opts...)...)
			def := noRetryServiceTaskDef()

			st, err := runner.Run(t.Context(), def, "p", nil)
			require.NoError(t, err)

			tc.assert(t, st, sched, T)
		})
	}
}

// incidentTaskDef returns a minimal process with one service-task node that
// has MaxAttempts=1, so the first action failure becomes a terminal incident
// without scheduling a retry timer.
//
//	start → task → end
func incidentTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "incident-test",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			// RetryPolicy intentionally omitted — default policy of MaxAttempts=1
			// causes the first failure to exhaust the budget immediately, parking
			// the instance as an incident rather than scheduling a retry.
			model.NewServiceTask("task", model.WithActionName("a")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestRunnerResolveIncident drives an instance to an incident (via a default
// MaxAttempts=1 policy that exhausts on the first failure), then calls
// Runner.ResolveIncident and asserts the incident is cleared and the action
// re-invoked successfully.
//
// The test also verifies that MemStore.List reports IncidentCount==1 while the
// incident is open and IncidentCount==0 after it is resolved.
func TestRunnerResolveIncident(t *testing.T) {
	T := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(T)

	// Counter: action fails on first call (attempt 0), succeeds on second (after resolve).
	var calls atomic.Int32
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"a": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("first call fails")
			}
			return map[string]any{"done": true}, nil
		}),
	})

	store := mustMemStore(t)
	runner := mustRunner(t, cat, store,
		runtime.WithRunnerClock(clk),
		// MaxAttempts=1: first failure parks as incident, no retry timer scheduled.
		runtime.WithDefaultRetryPolicy(model.RetryPolicy{
			MaxAttempts:     1,
			InitialInterval: time.Second,
			BackoffCoef:     1,
			MaxInterval:     time.Minute,
		}),
	)
	def := incidentTaskDef()

	// Run: first attempt fails → incident, instance parks (StatusRunning).
	st, err := runner.Run(t.Context(), def, "p", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status, "instance must park as running with an incident")
	require.Len(t, st.Incidents, 1, "want exactly one incident after first failure")

	incID := st.Incidents[0].ID

	// MemStore lister must report IncidentCount==1 while the incident is open.
	page, err := store.List(t.Context(), runtime.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, 1, page.Items[0].IncidentCount, "MemStore lister: want IncidentCount==1 before resolve")

	// ResolveIncident: grant 2 additional attempts; action now succeeds.
	st2, err := runner.ResolveIncident(t.Context(), def, "p", incID, 2)
	require.NoError(t, err, "ResolveIncident must not return an error")
	assert.Empty(t, st2.Incidents, "incident must be cleared after ResolveIncident")
	assert.Equal(t, engine.StatusCompleted, st2.Status, "instance must complete after resolve+reinvoke")

	// MemStore lister must report IncidentCount==0 after resolve.
	page2, err := store.List(t.Context(), runtime.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	assert.Equal(t, 0, page2.Items[0].IncidentCount, "MemStore lister: want IncidentCount==0 after resolve")
}
