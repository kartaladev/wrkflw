package kernel_test

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
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TestJitterSourceInRange verifies that the default JitterSource always produces
// values in [0, 1). It draws 1000 samples to give statistical confidence.
func TestJitterSourceInRange(t *testing.T) {
	s := kernel.NewJitterSource()
	for i := range 1000 {
		f := s.Fraction()
		if f < 0 || f >= 1 {
			t.Fatalf("sample %d: Fraction out of [0,1): %v", i, f)
		}
	}
}

// retryDef builds a process definition with a single service-task node "task"
// whose RetryPolicy will attempt a retry on failure.
//
//	start → task → end
func retryOnceTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "retry-test", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a"), activity.WithRetryPolicy(&model.RetryPolicy{
				MaxAttempts:     3,
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

// TestPerformRecordsJitterInRetryFireAt asserts that when perform builds an
// ActionFailed trigger for a retryable action error, the recorded jitter fraction
// is propagated to the scheduled retry fire-at time.
//
// RED→GREEN proof:
//   - BEFORE the WithJitterSource change, perform calls engine.NewActionFailed(…, true)
//     which internally sets jitter=0, so fireAt == T (zero delay).
//   - AFTER the change, perform calls engine.NewActionFailed(…, true, engine.WithJitter(0.5))
//     so fireAt == T + 0.5×1s = T+500ms.
func TestPerformRecordsJitterInRetryFireAt(t *testing.T) {
	T := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(T)

	cat := action.NewCatalog(map[string]action.Action{
		"a": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, errors.New("boom")
		}),
	})

	sched := &runtimetest.RecordingScheduler{Clock: clk}
	driver := runtimetest.MustProcessDriver(t, cat, runtimetest.MustMemStore(t),
		runtime.WithClock(clk),
		runtime.WithScheduler(sched),
		runtime.WithJitterSource(runtimetest.FixedJitter{F: 0.5}),
	)

	def := retryOnceTaskDef()
	_, err := driver.Drive(t.Context(), def, "p1", nil)
	// Run may return an error or a parked state; either is acceptable as long as
	// the scheduler captured the fireAt. We only care that the scheduler was called.
	_ = err

	require.True(t, sched.Scheduled, "expected the scheduler to have been called for the retry timer")

	// attempt 0 → backoff = InitialInterval × BackoffCoef^0 = 1s; jitter = 0.5 → 500ms
	want := T.Add(500 * time.Millisecond)
	assert.True(t, sched.FireAt.Equal(want),
		"expected fireAt %v, got %v (delta %v)", want, sched.FireAt, sched.FireAt.Sub(want))
}
