package scheduling_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/scheduling"
)

// TestSchedulerStartExplicitAndFire verifies that an explicitly started scheduler
// arms and fires a timer, and that Start is idempotent.
func TestSchedulerStartExplicitAndFire(t *testing.T) {
	fc := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(scheduling.WithClock(fc))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, s.Start(t.Context()))
	require.NoError(t, s.Start(t.Context()), "Start must be idempotent")

	var wg sync.WaitGroup
	wg.Add(1)
	_, err = s.Schedule(t.Context(), "t1", schedule.At(fc.Now().Add(time.Second)), wg.Done)
	require.NoError(t, err)
	require.NoError(t, fc.BlockUntilContext(t.Context(), 1))
	fc.Advance(time.Second)
	wg.Wait()
}

// TestSchedulerCtxCancelCloses verifies that cancelling the context passed to
// Start stops the scheduler: a subsequent Schedule reports ErrSchedulerClosed.
func TestSchedulerCtxCancelCloses(t *testing.T) {
	fc := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(scheduling.WithClock(fc))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, s.Start(ctx))
	cancel()

	require.Eventually(t, func() bool {
		_, serr := s.Schedule(context.Background(), "x", schedule.At(fc.Now().Add(time.Hour)), func() {})
		return errors.Is(serr, scheduling.ErrSchedulerClosed)
	}, 2*time.Second, 10*time.Millisecond, "ctx cancellation must close the scheduler")
}

// TestSchedulerStartAfterAutoStartInstallsWatcher verifies that an explicit
// Start(ctx) still binds ctx-cancellation to shutdown even when a prior Schedule
// already auto-started the scheduler with a background context (no watcher). This
// guards the documented "cancelling ctx stops the scheduler" contract against the
// common ordering where a timer is armed before Start is called.
func TestSchedulerStartAfterAutoStartInstallsWatcher(t *testing.T) {
	fc := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(scheduling.WithClock(fc))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Auto-start via Schedule (background context, no cancellation watcher).
	_, err = s.Schedule(t.Context(), "t1", schedule.At(fc.Now().Add(time.Hour)), func() {})
	require.NoError(t, err)

	// A subsequent explicit Start(ctx) must install the watcher so ctx drives shutdown.
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, s.Start(ctx))
	cancel()

	require.Eventually(t, func() bool {
		_, serr := s.Schedule(context.Background(), "t2", schedule.At(fc.Now().Add(time.Hour)), func() {})
		return errors.Is(serr, scheduling.ErrSchedulerClosed)
	}, 2*time.Second, 10*time.Millisecond, "Start after auto-start must bind ctx cancellation to shutdown")
}

// TestSchedulerScheduleAfterCloseErrors verifies Schedule after Close is a
// terminal error rather than a panic or silent no-op.
func TestSchedulerScheduleAfterCloseErrors(t *testing.T) {
	s, err := scheduling.NewScheduler(scheduling.WithClock(clockwork.NewFakeClock()))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	require.NoError(t, s.Close())

	_, err = s.Schedule(t.Context(), "y", schedule.At(time.Now().Add(time.Hour)), func() {})
	require.ErrorIs(t, err, scheduling.ErrSchedulerClosed)
}
