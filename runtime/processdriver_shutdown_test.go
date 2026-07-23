package runtime_test

import (
	"context"
	"fmt"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/scheduler"
)

// closeSpyScheduler is a scheduler.Scheduler that also implements io.Closer
// and records whether Close was invoked. It lets a test assert that the
// driver does NOT close a consumer-injected scheduler on Shutdown
// (owned-vs-injected).
type closeSpyScheduler struct {
	closed atomic.Bool
}

var _ scheduler.Scheduler = (*closeSpyScheduler)(nil)

func (s *closeSpyScheduler) Schedule(_ context.Context, j scheduler.Job) (scheduler.ScheduledJob, error) {
	if j == nil {
		return nil, fmt.Errorf("closeSpyScheduler: Schedule requires a non-nil Job")
	}
	return scheduler.NewScheduledJob(j, time.Time{})
}
func (s *closeSpyScheduler) Activate(context.Context, scheduler.ScheduledJob) error { return nil }
func (s *closeSpyScheduler) Deactivate(context.Context, string) error               { return nil }
func (s *closeSpyScheduler) Cancel(context.Context, string) error                   { return nil }
func (s *closeSpyScheduler) Scheduled(_ context.Context, id string) (scheduler.ScheduledJob, error) {
	return nil, fmt.Errorf("closeSpyScheduler: job %q: %w", id, scheduler.ErrJobNotFound)
}
func (s *closeSpyScheduler) List(context.Context) iter.Seq[scheduler.ScheduledJob] {
	return func(func(scheduler.ScheduledJob) bool) {}
}
func (s *closeSpyScheduler) Close() error { s.closed.Store(true); return nil }

// TestProcessDriverDefaultScheduler verifies that a zero-config ProcessDriver
// wires an in-process default scheduler, so a process reaching a timer node arms
// the timer and parks instead of failing with "no Scheduler configured". The
// driver-owned scheduler goroutine is released by Shutdown (proven implicitly by
// this package's goleak TestMain).
func TestProcessDriverDefaultScheduler(t *testing.T) {
	driver, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, driver.Shutdown(context.Background())) })

	st, err := driver.Drive(t.Context(), timerOnlyDef(), "i-default-sched", nil)
	require.NoError(t, err, "zero-config driver must arm the timer via the default scheduler")
	assert.Equal(t, engine.StatusRunning, st.Status, "instance must park at the timer catch")
}

// TestProcessDriverNilSchedulerFallsBackToDefault verifies that a nil scheduler
// supplied via WithScheduler — including a TYPED nil (e.g. a (*scheduler.Scheduler)(nil)
// variable, whose interface value is non-nil) — is treated as "not provided" and
// falls back to the owned in-process default, consistent with how WithInstanceStore(nil)
// and WithActionCatalog(nil) are ignored. Without the guard a typed nil would slip
// past the driver.sched != nil check and panic on the first timer.
func TestProcessDriverNilSchedulerFallsBackToDefault(t *testing.T) {
	var typedNil *scheduler.NativeScheduler // typed nil: non-nil scheduler.Scheduler interface, nil concrete pointer

	driver, err := runtime.NewProcessDriver(runtime.WithScheduler(typedNil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	st, err := driver.Drive(t.Context(), timerOnlyDef(), "i-nil-sched", nil)
	require.NoError(t, err, "a typed-nil scheduler must fall back to the default, not panic")
	assert.Equal(t, engine.StatusRunning, st.Status, "timer must arm via the fallback default scheduler")
}

// TestProcessDriverStart covers ProcessDriver.Start, which starts the driver-owned
// default scheduler and binds its lifetime to the given context.
func TestProcessDriverStart(t *testing.T) {
	t.Run("starts the owned scheduler and is idempotent", func(t *testing.T) {
		driver, err := runtime.NewProcessDriver()
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

		require.NoError(t, driver.Start(t.Context()))
		require.NoError(t, driver.Start(t.Context()), "Start must be idempotent")

		st, err := driver.Drive(t.Context(), timerOnlyDef(), "i-drv-start", nil)
		require.NoError(t, err)
		assert.Equal(t, engine.StatusRunning, st.Status, "timer must arm under the started scheduler")
	})

	t.Run("delegates to the owned scheduler (errors after Shutdown)", func(t *testing.T) {
		driver, err := runtime.NewProcessDriver()
		require.NoError(t, err)
		require.NoError(t, driver.Shutdown(context.Background()))

		// The owned scheduler is closed by Shutdown, so a subsequent Start must
		// surface its terminal error rather than silently succeed — proving Start
		// genuinely delegates to the owned scheduler.
		err = driver.Start(context.Background())
		require.ErrorIs(t, err, scheduler.ErrSchedulerClosed)
	})

	t.Run("no-op for an injected scheduler", func(t *testing.T) {
		driver, err := runtime.NewProcessDriver(runtime.WithScheduler(processtest.NewMemScheduler()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })
		require.NoError(t, driver.Start(t.Context()), "an injected scheduler is consumer-owned; Start is a no-op")
	})
}

// TestProcessDriverShutdown covers the driver-owned ShutdownGroup teardown.
func TestProcessDriverShutdown(t *testing.T) {
	t.Run("is idempotent", func(t *testing.T) {
		driver, err := runtime.NewProcessDriver(runtime.WithScheduler(processtest.NewMemScheduler()))
		require.NoError(t, err)
		require.NoError(t, driver.Shutdown(context.Background()))
		require.NoError(t, driver.Shutdown(context.Background()), "second Shutdown must be a no-op")
	})

	t.Run("does not close a consumer-injected scheduler", func(t *testing.T) {
		spy := &closeSpyScheduler{}
		driver, err := runtime.NewProcessDriver(runtime.WithScheduler(spy))
		require.NoError(t, err)
		require.NoError(t, driver.Shutdown(context.Background()))
		assert.False(t, spy.closed.Load(), "an injected scheduler is consumer-owned and must not be closed by the driver")
	})
}

// TestShutdownHonoursCtxDeadline guards the Finding-3 contract: with the owned gocron
// scheduler started, Shutdown given an already-expired ctx returns PROMPTLY and surfaces
// the ctx deadline error (joined), rather than blocking on gocron's internal stop
// timeout. The scheduler is closed via gocron's native ShutdownWithContext (through
// scheduler.Scheduler.CloseWithContext), which honors ctx directly — no manual
// close-race goroutine.
//
// Caveat on isolation: an empty owned gocron scheduler closes in well under 500ms, and
// the drain wait also surfaces the deadline, so this test asserts the observable Shutdown
// contract (prompt return + ctx error) rather than isolating the scheduler-close path — a
// slow-closing OWNED scheduler cannot be constructed in a unit test (a consumer-injected
// slow scheduler is never registered in the ShutdownGroup, ADR-0054). Stable across
// repeated runs: the already-expired ctx wins the close/drain selects in practice.
func TestShutdownHonoursCtxDeadline(t *testing.T) {
	driver, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	require.NoError(t, driver.Start(t.Context())) // start the owned gocron scheduler

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // ensure ctx is already expired

	start := time.Now()
	err = driver.Shutdown(ctx)
	assert.Less(t, time.Since(start), 500*time.Millisecond,
		"Shutdown must return promptly on an expired ctx, not block on gocron's stop timeout")
	// The scheduler close raced against ctx (and/or the drain wait) surfaces the deadline.
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
