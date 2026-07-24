package scheduler_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kartaladev/wrkflw/scheduler"
)

func TestNewScheduler_SatisfiesPortAndFires(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := scheduler.NewScheduler(scheduler.WithClock(fakeClock))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Runtime checks that the façade satisfies the required contracts.
	var _ scheduler.Scheduler = s
	var _ io.Closer = s

	var wg sync.WaitGroup
	wg.Add(1)
	_, err = s.Schedule(t.Context(), mustJob(t, "t1", surfaceKind,
		scheduler.At(fakeClock.Now().Add(3*time.Second)), func() { wg.Done() }))
	require.NoError(t, err)
	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
	fakeClock.Advance(3 * time.Second)
	wg.Wait()
}

// TestNativeSchedulerCalendarTriggers proves that Daily/Weekly/Monthly
// triggers survive the full façade→internal-gocron conversion path
// (triggerDef → scheduleClockTimes) and actually fire once armed — not just
// that Trigger.Next computes the right next instant in isolation (that is
// already covered by TestTrigger_Next's calendar cases). Following the
// internal gocron package's own calendar-fire pattern
// (scheduler/internal/gocron/trigger_test.go): read the job's live NextRun
// back and advance the fake clock exactly that far, rather than relying on
// BlockUntilContext, whose fake-clock waiter count is not reliably 1 for
// gocron's calendar-job internals.
func TestNativeSchedulerCalendarTriggers(t *testing.T) {
	t.Parallel()

	// refTime is a Thursday; wantFire is independently computed (not derived
	// through scheduleClockTimes) so the assertion actually exercises the
	// hour/minute/second passthrough rather than trivially matching whatever
	// the conversion produces.
	//
	// Timezone: the live scheduler now resolves calendar at-times in UTC by
	// default (ADR-0136) — see the timezone note on [scheduler.Daily]'s
	// godoc. refTime and wantFire are built in time.UTC here to match that
	// default, while still exercising the hour/minute/second passthrough
	// (wantFire is independently computed above, not derived through
	// scheduleClockTimes).
	refTime := time.Date(2026, time.January, 1, 8, 0, 0, 0, time.UTC)
	wantFire := time.Date(2026, time.January, 1, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name    string
		trigger func(at scheduler.ClockTime) scheduler.Trigger
	}

	cases := []testCase{
		{
			name: "Daily",
			trigger: func(at scheduler.ClockTime) scheduler.Trigger {
				return scheduler.Daily(1, at)
			},
		},
		{
			name: "Weekly",
			trigger: func(at scheduler.ClockTime) scheduler.Trigger {
				return scheduler.Weekly(1, []time.Weekday{refTime.Weekday()}, at)
			},
		},
		{
			name: "Monthly",
			trigger: func(at scheduler.ClockTime) scheduler.Trigger {
				return scheduler.Monthly(1, []int{refTime.Day()}, at)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeClock := clockwork.NewFakeClockAt(refTime)
			s, err := scheduler.NewScheduler(scheduler.WithClock(fakeClock))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			id := "calendar-" + tc.name
			at := scheduler.ClockTime{
				Hour:   uint(wantFire.Hour()),
				Minute: uint(wantFire.Minute()),
				Second: uint(wantFire.Second()),
			}

			var fired atomic.Bool
			_, err = s.Schedule(t.Context(), mustJob(t, id, surfaceKind,
				tc.trigger(at), func() { fired.Store(true) }))
			require.NoError(t, err)

			sj, err := s.Scheduled(t.Context(), id)
			require.NoError(t, err)
			require.False(t, sj.NextRun().IsZero())
			require.Truef(t, sj.NextRun().Equal(wantFire),
				"NextRun = %v, want %v (scheduleClockTimes must pass the hour/minute/second through unchanged)",
				sj.NextRun(), wantFire)

			fakeClock.Advance(wantFire.Sub(fakeClock.Now()) + time.Millisecond)
			require.Eventually(t, fired.Load, time.Second, 5*time.Millisecond)
		})
	}
}

func TestScheduler_Cancel_NoOp(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := scheduler.NewScheduler(scheduler.WithClock(fakeClock))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Cancel on an unknown ID must not panic or error.
	require.NoError(t, s.Cancel(t.Context(), "nonexistent"))

	// Schedule then cancel — callback must NOT fire.
	fired := false
	_, err = s.Schedule(t.Context(), mustJob(t, "t2", surfaceKind,
		scheduler.At(fakeClock.Now().Add(1*time.Second)), func() { fired = true }))
	require.NoError(t, err)
	require.NoError(t, s.Cancel(t.Context(), "t2"))

	// Advance past the would-be fire time; nothing should fire.
	// Drain any remaining waiters on the fake clock.
	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 0))
	fakeClock.Advance(2 * time.Second)
	require.False(t, fired, "cancelled timer must not fire")
}

// TestNewScheduler_WithLogger verifies that the WithLogger façade option is
// accepted and that the resulting scheduler constructs and fires correctly.
func TestNewScheduler_WithLogger(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []tc{
		{
			name: "WithLogger propagates custom logger without error",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				logger := slog.New(slog.Default().Handler())
				s, err := scheduler.NewScheduler(scheduler.WithClock(clk), scheduler.WithLogger(logger))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })

				// Verify scheduler still fires correctly with injected logger.
				var wg sync.WaitGroup
				wg.Add(1)
				_, err = s.Schedule(t.Context(), mustJob(t, "wl-t1", surfaceKind,
					scheduler.At(clk.Now().Add(time.Second)), func() { wg.Done() }))
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second)
				wg.Wait()
			},
		},
		{
			name: "WithLogger nil is a no-op — construction succeeds",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				s, err := scheduler.NewScheduler(scheduler.WithClock(clk), scheduler.WithLogger(nil))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				require.NotNil(t, s)
			},
		},
		{
			name: "no options still constructs correctly",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				s, err := scheduler.NewScheduler(scheduler.WithClock(clk))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				require.NotNil(t, s)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.assert(t)
		})
	}
}

// TestNewScheduler_ObservabilityOptions verifies that WithTracerProvider and
// WithMeterProvider are accepted by the façade and that the resulting scheduler
// constructs without panicking. Per spec §4, the scheduler holds these options
// for parity with other components (relay, runtime, transports); no spans or
// metrics are emitted in this track.
func TestNewScheduler_ObservabilityOptions(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []tc{
		{
			name: "WithTracerProvider constructs without panic",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				tp := sdktrace.NewTracerProvider()
				t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
				s, err := scheduler.NewScheduler(scheduler.WithClock(clk), scheduler.WithTracerProvider(tp))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				require.NotNil(t, s)
			},
		},
		{
			name: "WithMeterProvider constructs without panic",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				mp := sdkmetric.NewMeterProvider()
				t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })
				s, err := scheduler.NewScheduler(scheduler.WithClock(clk), scheduler.WithMeterProvider(mp))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				require.NotNil(t, s)
			},
		},
		{
			name: "all three options together construct without panic",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				tp := sdktrace.NewTracerProvider()
				t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
				mp := sdkmetric.NewMeterProvider()
				t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })
				l := slog.New(slog.Default().Handler())
				s, err := scheduler.NewScheduler(
					scheduler.WithClock(clk),
					scheduler.WithTracerProvider(tp),
					scheduler.WithMeterProvider(mp),
					scheduler.WithLogger(l),
				)
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				require.NotNil(t, s)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.assert(t)
		})
	}
}

// TestScheduler_CloseWithContext verifies the façade's context-aware shutdown:
// it bounds the underlying gocron shutdown by ctx (returning its error while a
// job is still running) and is idempotent like Close.
func TestScheduler_CloseWithContext(t *testing.T) {
	t.Run("honors an expired ctx while a job is running", func(t *testing.T) {
		s, err := scheduler.NewScheduler() // real clock: an At(now) job fires immediately
		require.NoError(t, err)

		enter := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		_, err = s.Schedule(t.Context(), mustJob(t, "blocker", surfaceKind, scheduler.At(time.Now()), func() {
			once.Do(func() { close(enter) })
			<-release
		}))
		require.NoError(t, err)
		select {
		case <-enter:
		case <-time.After(2 * time.Second):
			t.Fatal("blocking job did not start")
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		err = s.CloseWithContext(ctx)
		assert.Less(t, time.Since(start), 2*time.Second,
			"CloseWithContext must honor ctx, not block on gocron's stop timeout")
		assert.ErrorIs(t, err, context.Canceled)

		close(release) // let the job finish so gocron fully shuts down (goleak)
	})

	t.Run("is idempotent", func(t *testing.T) {
		s, err := scheduler.NewScheduler(scheduler.WithClock(clockwork.NewFakeClock()))
		require.NoError(t, err)
		require.NoError(t, s.CloseWithContext(context.Background()))
		require.NoError(t, s.CloseWithContext(context.Background()), "second CloseWithContext must no-op")
		require.NoError(t, s.Close(), "Close after CloseWithContext must also no-op")
	})
}
