package runtime_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/scheduling"
)

// closeSpyScheduler is a kernel.Scheduler that also implements io.Closer and
// records whether Close was invoked. It lets a test assert that the driver does
// NOT close a consumer-injected scheduler on Shutdown (owned-vs-injected).
type closeSpyScheduler struct {
	closed atomic.Bool
}

func (s *closeSpyScheduler) Schedule(_ context.Context, _ string, _ schedule.TriggerSpec, _ func()) (time.Time, error) {
	return time.Time{}, nil
}
func (s *closeSpyScheduler) Cancel(context.Context, string)   {}
func (s *closeSpyScheduler) NextRun(string) (time.Time, bool) { return time.Time{}, false }
func (s *closeSpyScheduler) Close() error                     { s.closed.Store(true); return nil }

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
// supplied via WithScheduler — including a TYPED nil (e.g. a (*scheduling.Scheduler)(nil)
// variable, whose interface value is non-nil) — is treated as "not provided" and
// falls back to the owned in-process default, consistent with how WithInstanceStore(nil)
// and WithActionCatalog(nil) are ignored. Without the guard a typed nil would slip
// past the driver.sched != nil check and panic on the first timer.
func TestProcessDriverNilSchedulerFallsBackToDefault(t *testing.T) {
	var typedNil *scheduling.Scheduler // typed nil: non-nil kernel.Scheduler interface, nil concrete pointer

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
		require.ErrorIs(t, err, scheduling.ErrSchedulerClosed)
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
