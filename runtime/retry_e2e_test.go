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

// retryE2EDef returns a process with one service-task node "task" (action "a")
// configured with a RetryPolicy that allows 5 attempts with exponential backoff.
//
//	start → task → end
func retryE2EDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "retry-e2e",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a"), activity.WithRetryPolicy(&model.RetryPolicy{
				MaxAttempts:     5,
				InitialInterval: time.Second,
				BackoffCoef:     2.0,
				MaxInterval:     time.Minute,
			})),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestRetryThenSucceedDrivesToCompletion is a deterministic end-to-end capstone
// test that proves the full retry loop:
//
//	Run (attempt 1, fail) → parks on retry timer (T+1s)
//	Advance 1s → Tick → attempt 2, fail → parks on retry timer (T+3s)
//	Advance 2s → Tick → attempt 3, succeed → StatusCompleted
//
// The fake clock and runtimetest.FixedJitter{F: 1.0} make every fire-at time deterministic.
func TestRetryThenSucceedDrivesToCompletion(t *testing.T) {
	ctx := t.Context()

	T := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(T)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))

	// runtimetest.FixedJitter{F: 1.0}: Fraction() always returns 1.0, so FireAt = clk.Now() + 1.0×Backoff(attempt).
	// Attempt 0: Backoff(0) = InitialInterval×BackoffCoef^0 = 1s×1 = 1s → FireAt = T+1s.
	// Attempt 1: Backoff(1) = InitialInterval×BackoffCoef^1 = 1s×2 = 2s → FireAt = (T+1s)+2s = T+3s.
	jitter := runtimetest.FixedJitter{F: 1.0}

	// attempts counts how many times action "a" has been invoked.
	// We use a plain int because the closure is captured by reference (pointer via &attempts).
	attempts := 0
	cat := action.NewCatalog(map[string]action.Action{
		"a": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("boom")
			}
			return map[string]any{"ok": true}, nil
		}),
	})

	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, cat, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithJitterSource(jitter),
	)

	def := retryE2EDef()
	const instanceID = "p"

	// --- Step 1: Run → attempt 1 fails → parks on retry timer at T+1s ---
	st, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err, "Run must not return a hard error")
	assert.Equal(t, engine.StatusRunning, st.Status,
		"instance must be parked (StatusRunning) after first failure — retry timer scheduled")
	assert.Equal(t, 1, attempts, "action must have been invoked exactly once after Run")

	// --- Step 2: advance to T+1s → Tick fires retry → attempt 2 fails → parks at T+3s ---
	fc.Advance(time.Second) // clock is now T+1s
	require.NoError(t, sched.Tick(ctx), "Tick must not error")
	// After Tick the retry delivered by the timer fires attempt 2, which fails and schedules
	// the next retry. The state is loaded from the store.
	mid, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, mid.Status,
		"instance must still be parked after second failure")
	assert.Equal(t, 2, attempts, "action must have been invoked exactly twice after second attempt")

	// --- Step 3: advance to T+3s → Tick fires retry → attempt 3 succeeds → completed ---
	fc.Advance(2 * time.Second) // clock is now T+3s
	require.NoError(t, sched.Tick(ctx), "Tick must not error")

	// Load final state from the store (Tick's internal ApplyTrigger doesn't surface the state).
	final, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"instance must reach StatusCompleted after third attempt succeeds")
	assert.Equal(t, 3, attempts, "action must have been invoked exactly 3 times total")
}
