package scheduling_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// timerIntermediateE2EDef returns: start → timer-catch("1h") → service("greet") → end.
// Mirrors the definition in runtime/timer_example_test.go exactly.
func timerIntermediateE2EDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "timer-intermediate",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait1h", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("greet", activity.WithActionName("greet")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "greet"},
			{ID: "f3", Source: "greet", Target: "end"},
		},
	}
}

// TestGocronSchedulerDrivesRunnerToCompletion proves the gocron-backed scheduler
// drives a real Runner identically to MemScheduler: ONE shared fake clock is the
// runner's clock.Clock AND the scheduler's clockwork.Clock. Advancing the shared
// clock past FireAt fires the timer on gocron's executor goroutine, which calls
// runner.ApplyTrigger(TimerFired); the instance must reach StatusCompleted.
//
// Synchronization approach:
//  1. BlockUntilContext(1) ensures gocron has armed exactly one waiter on the fake
//     clock before we advance it — avoids the race between Run returning and the
//     clock advancing before gocron's timer goroutine has started waiting.
//  2. serviceRan channel provides a deterministic signal that the gocron executor
//     goroutine has delivered TimerFired and the service action ran.
//  3. require.Eventually polls the store for StatusCompleted as the final safety
//     net for the async ApplyTrigger path completing its last engine step.
func TestGocronSchedulerDrivesRunnerToCompletion(t *testing.T) {
	ctx := t.Context()

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt) // ONE shared instance — drives both runner and gocron

	serviceRan := make(chan struct{})
	cat := action.NewCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			close(serviceRan)
			return map[string]any{"greeted": true}, nil
		}),
	})

	sched, err := scheduling.NewScheduler(scheduling.WithClock(fc)) // same fc, as clockwork.Clock
	require.NoError(t, err)
	t.Cleanup(func() { _ = sched.Close() })

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store), runtime.WithClock(fc), runtime.WithScheduler(sched)) // same fc, as clock.Clock
	require.NoError(t, err)

	def := timerIntermediateE2EDef()
	const instanceID = "gocron-e2e-1"

	// Run → parks at the intermediate timer node.
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.Tokens, 1)
	assert.Equal(t, "wait1h", parked.Tokens[0].NodeID)

	// Barrier: wait until gocron armed its 1h timer waiter on the fake clock,
	// then advance past FireAt. This guarantees the fake clock advance is observed
	// by the gocron waiter goroutine rather than racing against it.
	require.NoError(t, fc.BlockUntilContext(ctx, 1))
	fc.Advance(1*time.Hour + time.Second)

	// The scheduler fires the timer on its executor goroutine, which Delivers
	// TimerFired and runs the service action asynchronously.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	select {
	case <-serviceRan:
	case <-ctx.Done():
		t.Fatalf("service action did not run after timer fired: %v", ctx.Err())
	}

	// The instance must reach Completed (assert eventually — ApplyTrigger runs async
	// on gocron's executor goroutine, so the final store write may be in-flight
	// even after the service action has run, because the engine still needs to
	// process the greet→end flow and commit the terminal step).
	require.Eventually(t, func() bool {
		final, _, loadErr := store.Load(ctx, instanceID)
		return loadErr == nil && final.Status == engine.StatusCompleted
	}, 2*time.Second, 10*time.Millisecond, "instance must complete after gocron fires the timer")

	final, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, true, final.Variables["greeted"])
	assert.Empty(t, final.Tokens)
}
