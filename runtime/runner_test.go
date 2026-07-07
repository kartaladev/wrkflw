package runtime_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// errStore is an InstanceStore whose Create and Commit always fail with a concurrency error.
// It embeds *kernel.MemInstanceStore so that Load still works for Deliver-based tests
// that need an initial state.
type errStore struct{ *kernel.MemInstanceStore }

func (errStore) Create(_ context.Context, _ kernel.AppliedStep) (kernel.Version, error) {
	return 0, kernel.ErrConcurrentUpdate
}

func (errStore) Commit(_ context.Context, _ kernel.Version, _ kernel.AppliedStep) (kernel.Version, error) {
	return 0, kernel.ErrConcurrentUpdate
}

// commitErrStore is a Store whose Create succeeds but Commit always fails
// with ErrConcurrentUpdate. Used to test the Commit failure path independently.
type commitErrStore struct{ *kernel.MemInstanceStore }

func (s *commitErrStore) Commit(_ context.Context, _ kernel.Version, _ kernel.AppliedStep) (kernel.Version, error) {
	return 0, kernel.ErrConcurrentUpdate
}

// TestRunnerUnknownActionFailsInstance verifies that a catalog with no actions
// causes the runner to produce FailInstance (recorded in the store's outbox).
func TestRunnerUnknownActionFailsInstance(t *testing.T) {
	cat := action.NewMapCatalog(nil)
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, cat, store)

	final, err := driver.Drive(t.Context(), linearDef(), "i1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusFailed, final.Status)

	evs := store.Events()
	require.Len(t, evs, 1)
	assert.Equal(t, "instance.failed", evs[0].Topic)
}

func TestRunnerActionErrorFailsInstance(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, errors.New("greet exploded")
		}),
	})
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, cat, store)

	final, err := driver.Drive(t.Context(), linearDef(), "i1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusFailed, final.Status)

	evs := store.Events()
	require.Len(t, evs, 1)
	assert.Equal(t, "instance.failed", evs[0].Topic)
}

// TestRunnerStoreCreateErrorPropagates verifies that a Create failure from the
// store is surfaced as a hard error from Run (wrapping ErrConcurrentUpdate).
func TestRunnerStoreCreateErrorPropagates(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, nil
		}),
	})
	driver := runtimetest.MustRunner(t, cat, errStore{runtimetest.MustMemStore(t)})

	_, err := driver.Drive(t.Context(), linearDef(), "i1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow-runtime: commit:")
}

// TestRunnerStoreCommitErrorPropagates verifies that a Commit failure is surfaced
// as a hard error from Run for subsequent steps (after Create succeeds).
func TestRunnerStoreCommitErrorPropagates(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, nil
		}),
	})
	// commitErrStore: Create succeeds (first step), Commit fails (second step when
	// ActionCompleted is delivered).
	driver := runtimetest.MustRunner(t, cat, &commitErrStore{runtimetest.MustMemStore(t)})

	_, err := driver.Drive(t.Context(), linearDef(), "i1", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, kernel.ErrConcurrentUpdate,
		"ErrConcurrentUpdate from Commit must be surfaced via errors.Is")
	assert.Contains(t, err.Error(), "workflow-runtime: commit:")
}

// userTaskOnlyDef returns a process with a single user-task node: start → userTask → end.
func userTaskOnlyDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "user-task-only",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("task1", []string{"manager"}),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task1"},
			{ID: "f2", Source: "task1", Target: "end"},
		},
	}
}

// TestRunnerUserTaskWithoutDepsErrors verifies that a Runner constructed without
// human-task dependencies (nil resolver and nil TaskStore) returns a descriptive
// error — rather than panicking — when it reaches an AwaitHuman command.
func TestRunnerUserTaskWithoutDepsErrors(t *testing.T) {
	// Build a Runner with no human-task option (nil resolver and nil tasks).
	driver := runtimetest.MustRunner(t, action.NewMapCatalog(nil), runtimetest.MustMemStore(t))
	// WithHumanTasks intentionally omitted to test error path.

	_, err := driver.Drive(t.Context(), userTaskOnlyDef(), "i1", nil)
	require.Error(t, err, "Run must fail with a descriptive error, not panic")
	assert.Contains(t, err.Error(), "ActorResolver", "error must mention the missing ActorResolver")
}

// timerDef returns: start → timer-catch("1h") → end, used to exercise
// ScheduleTimer / CancelTimer perform paths in the runner.
func timerOnlyDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "timer-only",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewCatch("wait", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}
}

// TestRunnerZeroConfigUsesDefaultScheduler verifies that a driver built without an
// explicit WithScheduler still arms timers: NewProcessDriver supplies an in-process
// default scheduler, so a process reaching a timer-catch node schedules the timer
// and parks (StatusRunning) rather than failing with "no Scheduler configured".
// (The perform nil-guard remains as defensive code but is unreachable through the
// constructor, which always resolves a scheduler.) MustRunner tears the driver
// down via t.Cleanup, releasing the default scheduler goroutine.
func TestRunnerZeroConfigUsesDefaultScheduler(t *testing.T) {
	driver := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t))
	// WithScheduler intentionally omitted — the default scheduler must handle it.

	st, err := driver.Drive(t.Context(), timerOnlyDef(), "i1", nil)
	require.NoError(t, err, "zero-config driver must arm the timer via the default scheduler")
	assert.Equal(t, engine.StatusRunning, st.Status, "instance must park at the timer catch")
}

// onceConflictStore wraps *kernel.MemInstanceStore and injects a single ErrConcurrentUpdate
// on the first Commit call whose step.Trigger is an engine.TimerFired. All other
// calls (before or after the triggered conflict) delegate to the inner store.
//
// This lets TestTimerFireRetriesOnCASConflict drive a deterministic CAS conflict on
// the timer-fire path without any concurrency or timing gymnastics.
type onceConflictStore struct {
	inner     *kernel.MemInstanceStore
	triggered atomic.Bool
}

func (s *onceConflictStore) Create(ctx context.Context, step kernel.AppliedStep) (kernel.Version, error) {
	return s.inner.Create(ctx, step)
}

func (s *onceConflictStore) Load(ctx context.Context, id string) (engine.InstanceState, kernel.Version, error) {
	return s.inner.Load(ctx, id)
}

func (s *onceConflictStore) Commit(ctx context.Context, expected kernel.Version, step kernel.AppliedStep) (kernel.Version, error) {
	if _, ok := step.Trigger.(engine.TimerFired); ok && s.triggered.CompareAndSwap(false, true) {
		// First TimerFired Commit → simulate CAS conflict.
		return 0, kernel.ErrConcurrentUpdate
	}
	return s.inner.Commit(ctx, expected, step)
}

// conflictTimerDef returns: start → timer-catch("10s") → end.
// No service tasks; the timer catch is the only external wait.
func conflictTimerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "conflict-timer",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewCatch("wait10s", event.WithCatchTimer(schedule.AfterExpr(`"10s"`))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait10s"},
			{ID: "f2", Source: "wait10s", Target: "end"},
		},
	}
}

// TestTimerFireRetriesOnCASConflict verifies that the runner retries Deliver on
// ErrConcurrentUpdate during the timer-fire callback, so a single CAS conflict
// never silently drops the TimerFired trigger.
//
// Without the bounded-retry fix the runner logs the error and returns, leaving the
// instance parked forever (StatusRunning). With the fix the instance reaches
// StatusCompleted after the retry re-delivers the trigger.
func TestTimerFireRetriesOnCASConflict(t *testing.T) {
	ctx := t.Context()

	startAt := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	inner := runtimetest.MustMemStore(t)
	store := &onceConflictStore{inner: inner}
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))

	driver := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc), runtime.WithScheduler(sched))

	def := conflictTimerDef()
	const instanceID = "conflict-timer-1"

	// Run → parks at the intermediate-catch timer node.
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked.Status,
		"instance must park at the timer node")

	// Advance clock past the 10-second FireAt and Tick → fires the timer callback.
	// The callback calls Deliver → Commit, which returns ErrConcurrentUpdate on the
	// first attempt (injected by onceConflictStore). The retry loop must succeed on
	// the second attempt.
	fc.Advance(11 * time.Second)
	require.NoError(t, sched.Tick(ctx))

	// Assert the instance completed (not parked) — proves the retry re-delivered.
	final, _, err := inner.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"instance must reach StatusCompleted after retry on CAS conflict")
	assert.Empty(t, final.Tokens, "no tokens remain after completion")
	// Self-certifying: confirm the CAS-conflict injection path was actually taken.
	assert.True(t, store.triggered.Load(), "onceConflictStore must have injected a CAS conflict")
}

// TestDeliverLoopPropagatesConcurrentUpdate verifies that when the Store's Create
// returns ErrConcurrentUpdate, deliverLoop surfaces it wrapped so errors.Is matches.
func TestDeliverLoopPropagatesConcurrentUpdate(t *testing.T) {
	// Use a simple linear def (start → greet → end) with a succeeding action.
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})
	driver := runtimetest.MustRunner(t, cat, errStore{runtimetest.MustMemStore(t)})
	_, err := driver.Drive(t.Context(), linearDef(), "i1", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, kernel.ErrConcurrentUpdate,
		"ErrConcurrentUpdate from Create must be surfaced via errors.Is")
}

// TestNewRunnerDefaultUsesSystemClock verifies that a Runner constructed without a
// clock option stamps instance StartedAt from the system clock (within a real-time bracket).
func TestNewRunnerDefaultUsesSystemClock(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		}),
	})
	before := time.Now()
	driver := runtimetest.MustRunner(t, cat, runtimetest.MustMemStore(t))
	st, err := driver.Drive(t.Context(), linearDef(), "i-sys-1", nil)
	after := time.Now()
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
	// StartedAt is set from driver.clk.Now() inside the engine's StartInstance handler.
	assert.False(t, st.StartedAt.Before(before) || st.StartedAt.After(after),
		"StartedAt must be within [before, after] wall-clock bracket")
}

// TestNewRunnerWithClockOption verifies that WithClock injects a fake clock
// whose time flows into the engine's StartedAt stamp (behavioral assertion).
func TestNewRunnerWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		}),
	})
	driver := runtimetest.MustRunner(t, cat, runtimetest.MustMemStore(t), runtime.WithClock(fake))
	st, err := driver.Drive(t.Context(), linearDef(), "i-fake-1", nil)
	require.NoError(t, err)
	// StartedAt is stamped from driver.clk.Now() = fake.Now() = time.Unix(1000, 0).
	assert.Equal(t, time.Unix(1000, 0), st.StartedAt,
		"StartedAt must equal fake clock's epoch")
}

// TestNewProcessDriverAlwaysSucceeds verifies that NewProcessDriver always
// constructs a valid driver regardless of whether WithActionCatalog /
// WithInstanceStore are supplied, because nil options are silently ignored
// and sensible in-memory defaults apply.
func TestNewProcessDriverAlwaysSucceeds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		opts   []runtime.Option
		assert func(t *testing.T, driver *runtime.ProcessDriver, err error)
	}{
		{
			name: "zero args — defaults apply",
			opts: nil,
			assert: func(t *testing.T, driver *runtime.ProcessDriver, err error) {
				require.NoError(t, err)
				require.NotNil(t, driver)
			},
		},
		{
			name: "WithActionCatalog(nil) — ignored, defaults apply",
			opts: []runtime.Option{runtime.WithActionCatalog(nil)},
			assert: func(t *testing.T, driver *runtime.ProcessDriver, err error) {
				require.NoError(t, err)
				require.NotNil(t, driver)
			},
		},
		{
			name: "WithInstanceStore(nil) — ignored, defaults apply",
			opts: []runtime.Option{runtime.WithInstanceStore(nil)},
			assert: func(t *testing.T, driver *runtime.ProcessDriver, err error) {
				require.NoError(t, err)
				require.NotNil(t, driver)
			},
		},
		{
			name: "explicit catalog and store",
			opts: []runtime.Option{
				runtime.WithActionCatalog(action.NewMapCatalog(nil)),
				runtime.WithInstanceStore(runtimetest.MustMemStore(t)),
			},
			assert: func(t *testing.T, driver *runtime.ProcessDriver, err error) {
				require.NoError(t, err)
				require.NotNil(t, driver)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driver, err := runtime.NewProcessDriver(tc.opts...)
			if driver != nil {
				t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })
			}
			tc.assert(t, driver, err)
		})
	}
}
