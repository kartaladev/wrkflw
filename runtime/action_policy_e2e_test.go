package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// policyTaskDef builds start → task("t") → end. nodePolicy, when non-nil, is set as
// the node-level retry policy (used to prove action-retry overrides node-retry).
func policyTaskDef(nodePolicy *model.RetryPolicy) *model.ProcessDefinition {
	taskOpts := []activity.ServiceTaskOption{activity.WithTaskAction("t")}
	if nodePolicy != nil {
		taskOpts = append(taskOpts, activity.WithRetryPolicy(nodePolicy))
	}
	return &model.ProcessDefinition{
		ID: "action-policy", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", taskOpts...),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestActionExecTimeoutOverridesRuntimeDefault proves the per-action timeout is
// respected even when it is LONGER than the runtime default (i.e. it is not min()).
func TestActionExecTimeoutOverridesRuntimeDefault(t *testing.T) {
	t.Parallel()

	var remaining time.Duration
	inner := action.ActionFunc(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
		if dl, ok := ctx.Deadline(); ok {
			remaining = time.Until(dl)
		}
		return nil, nil
	})
	// Action wants 2s; runtime default is a much shorter 50ms.
	cat := action.NewCatalog(map[string]action.Action{
		"t": action.Wrap(inner, action.WithExecTimeout(2*time.Second)),
	})
	r := runtimetest.MustProcessDriver(t, cat, runtimetest.MustMemStore(t),
		runtime.WithClock(clockwork.NewFakeClock()),
		runtime.WithActionTimeout(50*time.Millisecond),
	)

	st, err := r.Drive(t.Context(), policyTaskDef(nil), "p1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
	assert.Greater(t, remaining, 500*time.Millisecond,
		"the action's own 2s timeout must win over the 50ms runtime default (not min())")
}

// TestActionRetryOverridesNodeRetry proves precedence action > node: a node whose
// own retry policy would fail on the first failure (MaxAttempts=1) still completes
// because the action carries a retry policy that overrides it and drives the durable
// retry loop.
func TestActionRetryOverridesNodeRetry(t *testing.T) {
	ctx := t.Context()

	T := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(T)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	jitter := runtimetest.FixedJitter{F: 1.0}

	attempts := 0
	inner := action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("boom")
		}
		return map[string]any{"ok": true}, nil
	})
	// Action policy allows 5 attempts; node policy allows only 1 (would fail fast).
	cat := action.NewCatalog(map[string]action.Action{
		"t": action.Wrap(inner, action.WithRetrySpecs(action.RetrySpecs{
			MaxAttempts: 5, InitialInterval: time.Second, Multiplier: 2, MaxInterval: time.Minute,
		})),
	})
	nodePolicy := &model.RetryPolicy{MaxAttempts: 1, InitialInterval: time.Second, BackoffCoef: 2, MaxInterval: time.Minute}

	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustProcessDriver(t, cat, store,
		runtime.WithClock(fc), runtime.WithScheduler(sched), runtime.WithJitterSource(jitter),
	)
	def := policyTaskDef(nodePolicy)

	// attempt 1 fails → parks on retry timer (proves override beat node MaxAttempts=1).
	st, err := driver.Drive(ctx, def, "p", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status,
		"with node MaxAttempts=1 the instance would fail; the action override must schedule a retry")
	require.Equal(t, 1, attempts)

	// attempt 2 fails → parks again.
	fc.Advance(time.Second)
	require.NoError(t, sched.Tick(ctx))
	mid, _, err := store.Load(ctx, "p")
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, mid.Status)
	require.Equal(t, 2, attempts)

	// attempt 3 succeeds → completed.
	fc.Advance(2 * time.Second)
	require.NoError(t, sched.Tick(ctx))
	final, _, err := store.Load(ctx, "p")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 3, attempts)
}

// TestActionRecoverFalsePropagatesPanic proves WithRecover(false) bypasses the
// runtime's recover-by-default so a panic propagates out of Drive.
func TestActionRecoverFalsePropagatesPanic(t *testing.T) {
	t.Parallel()

	inner := action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
		panic("kaboom")
	})
	cat := action.NewCatalog(map[string]action.Action{
		"t": action.Wrap(inner, action.WithRecover(false)),
	})
	r := runtimetest.MustProcessDriver(t, cat, runtimetest.MustMemStore(t),
		runtime.WithClock(clockwork.NewFakeClock()),
	)

	assert.Panics(t, func() {
		_, _ = r.Drive(t.Context(), policyTaskDef(nil), "p1", nil)
	}, "WithRecover(false) must let the panic propagate rather than converting it to a failure")
}
