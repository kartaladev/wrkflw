package runtime_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
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
	d, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Shutdown(context.Background())) })

	st, err := d.Run(t.Context(), timerOnlyDef(), "i-default-sched", nil)
	require.NoError(t, err, "zero-config driver must arm the timer via the default scheduler")
	assert.Equal(t, engine.StatusRunning, st.Status, "instance must park at the timer catch")
}

// TestProcessDriverStart covers ProcessDriver.Start, which starts the driver-owned
// default scheduler and binds its lifetime to the given context.
func TestProcessDriverStart(t *testing.T) {
	t.Run("starts the owned scheduler and is idempotent", func(t *testing.T) {
		d, err := runtime.NewProcessDriver()
		require.NoError(t, err)
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

		require.NoError(t, d.Start(t.Context()))
		require.NoError(t, d.Start(t.Context()), "Start must be idempotent")

		st, err := d.Run(t.Context(), timerOnlyDef(), "i-drv-start", nil)
		require.NoError(t, err)
		assert.Equal(t, engine.StatusRunning, st.Status, "timer must arm under the started scheduler")
	})

	t.Run("delegates to the owned scheduler (errors after Shutdown)", func(t *testing.T) {
		d, err := runtime.NewProcessDriver()
		require.NoError(t, err)
		require.NoError(t, d.Shutdown(context.Background()))

		// The owned scheduler is closed by Shutdown, so a subsequent Start must
		// surface its terminal error rather than silently succeed — proving Start
		// genuinely delegates to the owned scheduler.
		err = d.Start(context.Background())
		require.ErrorIs(t, err, scheduling.ErrSchedulerClosed)
	})

	t.Run("no-op for an injected scheduler", func(t *testing.T) {
		d, err := runtime.NewProcessDriver(runtime.WithScheduler(processtest.NewMemScheduler()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
		require.NoError(t, d.Start(t.Context()), "an injected scheduler is consumer-owned; Start is a no-op")
	})
}

// TestProcessDriverShutdown covers the driver-owned ShutdownGroup teardown.
func TestProcessDriverShutdown(t *testing.T) {
	t.Run("is idempotent", func(t *testing.T) {
		d, err := runtime.NewProcessDriver(runtime.WithScheduler(processtest.NewMemScheduler()))
		require.NoError(t, err)
		require.NoError(t, d.Shutdown(context.Background()))
		require.NoError(t, d.Shutdown(context.Background()), "second Shutdown must be a no-op")
	})

	t.Run("does not close a consumer-injected scheduler", func(t *testing.T) {
		spy := &closeSpyScheduler{}
		d, err := runtime.NewProcessDriver(runtime.WithScheduler(spy))
		require.NoError(t, err)
		require.NoError(t, d.Shutdown(context.Background()))
		assert.False(t, spy.closed.Load(), "an injected scheduler is consumer-owned and must not be closed by the driver")
	})
}
