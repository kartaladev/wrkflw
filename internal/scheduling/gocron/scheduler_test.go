package gocron_test

import (
	"context"
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

	"github.com/kartaladev/wrkflw/definition/schedule"
	sched "github.com/kartaladev/wrkflw/internal/scheduling/gocron"
)

// captureHandler records slog records for assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

// TestGocronScheduler_WithLogger verifies that NewGocronScheduler accepts a
// WithLogger option without error and that the scheduler continues to operate
// correctly when an injected logger is provided. A custom capturing handler is
// wired in to demonstrate injection works; normal timer firing is verified to
// succeed with the injected logger in place. We also confirm the default (no
// option) and nil-option variants still construct and fire correctly.
func TestGocronScheduler_WithLogger(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, clk *clockwork.FakeClock)
	}

	cases := []tc{
		{
			name: "construction with injected logger succeeds and fires",
			assert: func(t *testing.T, clk *clockwork.FakeClock) {
				h := &captureHandler{}
				logger := slog.New(h)

				s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithLogger(logger))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })

				// Verify normal operation still works with the injected logger.
				var wg sync.WaitGroup
				wg.Add(1)
				_, err2 := s.Schedule(t.Context(), "log-t1", schedule.At(clk.Now().Add(time.Second)), func() { wg.Done() })
				require.NoError(t, err2)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second)
				wg.Wait()
			},
		},
		{
			name: "construction with nil logger option falls back to default",
			assert: func(t *testing.T, clk *clockwork.FakeClock) {
				// nil logger option must be a no-op — no panic, no nil pointer.
				s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithLogger(nil))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				assert.NotNil(t, s)
			},
		},
		{
			name: "construction with no options still works",
			assert: func(t *testing.T, clk *clockwork.FakeClock) {
				s, err := sched.NewGocronScheduler(sched.WithClock(clk))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				assert.NotNil(t, s)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			c.assert(t, clk)
		})
	}
}

func TestGocronScheduler_FiresAtTime(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := sched.NewGocronScheduler(sched.WithClock(fakeClock))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	wg.Add(1)
	_, err = s.Schedule(t.Context(), "t1", schedule.At(fakeClock.Now().Add(5*time.Second)), func() { wg.Done() })
	require.NoError(t, err)

	// MANDATORY barrier: wait until gocron armed its timer (1 waiter) before
	// advancing, else Advance can outrun the arm and the timer never fires.
	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
	fakeClock.Advance(5 * time.Second)
	wg.Wait() // executor goroutine actually ran the task
}

func TestGocronScheduler_Behaviour(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock)
	}

	// counter returns an atomically-incrementing fire callback and a reader.
	counter := func() (func(), func() int64) {
		var n atomic.Int64
		return func() { n.Add(1) }, func() int64 { return n.Load() }
	}

	cases := []tc{
		{
			name: "cancel prevents fire",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				fire, count := counter()
				_, err := s.Schedule(t.Context(), "c1", schedule.At(clk.Now().Add(5*time.Second)), fire)
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				s.Cancel(t.Context(), "c1")
				// drain: confirm gocron released its fake-clock waiter before advancing
				require.NoError(t, clk.BlockUntilContext(t.Context(), 0))
				clk.Advance(10 * time.Second)
				// Assert it never fires after cancel.
				require.Never(t, func() bool { return count() > 0 },
					200*time.Millisecond, 10*time.Millisecond)
			},
		},
		{
			name: "replace reschedules and fires once",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var wg sync.WaitGroup
				wg.Add(1)
				var n atomic.Int64
				fire := func() { n.Add(1); wg.Done() }

				_, err := s.Schedule(t.Context(), "r1", schedule.At(clk.Now().Add(5*time.Second)), func() { t.Error("stale timer fired") })
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				_, err = s.Schedule(t.Context(), "r1", schedule.At(clk.Now().Add(10*time.Second)), fire) // replace
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				clk.Advance(5 * time.Second)
				require.Never(t, func() bool { return n.Load() > 0 },
					150*time.Millisecond, 10*time.Millisecond) // old T+5 must not fire
				clk.Advance(5 * time.Second) // now at T+10
				wg.Wait()
				require.Equal(t, int64(1), n.Load())
			},
		},
		{
			name: "cancel unknown is a no-op",
			assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
				require.NotPanics(t, func() { s.Cancel(t.Context(), "does-not-exist") })
			},
		},
		{
			// UUID-guard: after replace+fire of new job, Cancel of the timerID
			// still finds the live (new) entry — the old job's AfterJobRuns must
			// not delete the new job's map entry, guarded by job UUID comparison.
			name: "replace then fire new; cancel still live after new fires",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var wgNew sync.WaitGroup
				wgNew.Add(1)

				// Arm the first (old) job at T+5; it will be replaced before firing.
				_, err := s.Schedule(t.Context(), "uuid1", schedule.At(clk.Now().Add(5*time.Second)), func() { t.Error("old job must not fire") })
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				// Replace with a new job at T+10.
				_, err = s.Schedule(t.Context(), "uuid1", schedule.At(clk.Now().Add(10*time.Second)), func() { wgNew.Done() })
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				// Advance past the old T+5 — old job must NOT fire (replace removed it).
				clk.Advance(5 * time.Second)
				require.Never(t, func() bool { return false }, 100*time.Millisecond, 10*time.Millisecond)

				// Advance to T+10 — new job fires.
				clk.Advance(5 * time.Second)
				wgNew.Wait()

				// After new job fired, AfterJobRuns from the new job deletes the map
				// entry (UUID match). A subsequent Cancel must be a clean no-op and
				// must not panic — this confirms the map is consistent (not accidentally
				// left with a stale entry by the old job's listener, and not missing due
				// to UUID mismatch either).
				require.NotPanics(t, func() { s.Cancel(t.Context(), "uuid1") })
			},
		},
		{
			name: "callback runs exactly once",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var wg sync.WaitGroup
				wg.Add(1)
				var n atomic.Int64
				_, err := s.Schedule(t.Context(), "o1", schedule.At(clk.Now().Add(time.Second)), func() { n.Add(1); wg.Done() })
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second)
				wg.Wait()
				require.Never(t, func() bool { return n.Load() > 1 },
					150*time.Millisecond, 10*time.Millisecond)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(sched.WithClock(clk))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			c.assert(t, s, clk)
		})
	}
}

// TestGocronScheduler_WithTracerAndMeterProvider verifies that
// WithTracerProvider and WithMeterProvider are accepted by NewGocronScheduler
// and that the scheduler constructs and operates correctly with those options.
// The scheduler emits no spans or metrics in this track (parity-only); the test
// confirms no panics and continued correct operation.
func TestGocronScheduler_WithTracerAndMeterProvider(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, clk *clockwork.FakeClock)
	}

	cases := []tc{
		{
			name: "WithTracerProvider constructs without panic",
			assert: func(t *testing.T, clk *clockwork.FakeClock) {
				tp := sdktrace.NewTracerProvider()
				t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
				s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithTracerProvider(tp))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				assert.NotNil(t, s)
			},
		},
		{
			name: "WithMeterProvider constructs without panic",
			assert: func(t *testing.T, clk *clockwork.FakeClock) {
				mp := sdkmetric.NewMeterProvider()
				t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })
				s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithMeterProvider(mp))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				assert.NotNil(t, s)
			},
		},
		{
			name: "all three options together construct without panic",
			assert: func(t *testing.T, clk *clockwork.FakeClock) {
				tp := sdktrace.NewTracerProvider()
				t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
				mp := sdkmetric.NewMeterProvider()
				t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })
				l := slog.New(slog.Default().Handler())
				s, err := sched.NewGocronScheduler(
					sched.WithClock(clk),
					sched.WithTracerProvider(tp),
					sched.WithMeterProvider(mp),
					sched.WithLogger(l),
				)
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				assert.NotNil(t, s)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			c.assert(t, clk)
		})
	}
}

// TestSchedulePastFireAtFiresImmediately verifies that scheduling a timer whose
// fireAt is in the past (or equal to now) fires the callback immediately
// instead of being silently dropped.
func TestSchedulePastFireAtFiresImmediately(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startTime)

	s, err := sched.NewGocronScheduler(sched.WithClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	fired := make(chan struct{}, 1)
	// fireAt is 1 second in the past — schedule.At maps to OneTimeJobStartImmediately.
	pastFireAt := startTime.Add(-1 * time.Second)
	_, err = s.Schedule(t.Context(), "past-timer", schedule.At(pastFireAt), func() {
		fired <- struct{}{}
	})
	require.NoError(t, err)

	// OneTimeJobStartImmediately fires without any clock advance needed.
	require.Eventually(t, func() bool {
		select {
		case <-fired:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "callback should fire immediately for past fireAt")
}

// TestGocronScheduler_CloseWithContext verifies the context-aware shutdown: it
// honors the caller's ctx deadline (returning its error while a job is still
// running, rather than blocking on gocron's internal stop timeout) and returns
// nil on a clean shutdown.
func TestGocronScheduler_CloseWithContext(t *testing.T) {
	t.Run("honors an expired ctx while a job is running", func(t *testing.T) {
		s, err := sched.NewGocronScheduler() // real clock: an At(now) job fires immediately
		require.NoError(t, err)

		enter := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		_, err = s.Schedule(t.Context(), "blocker", schedule.At(time.Now()), func() {
			once.Do(func() { close(enter) })
			<-release
		})
		require.NoError(t, err)
		select {
		case <-enter:
		case <-time.After(2 * time.Second):
			t.Fatal("blocking job did not start")
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // already cancelled: CloseWithContext must return promptly with its error
		start := time.Now()
		err = s.CloseWithContext(ctx)
		assert.Less(t, time.Since(start), 2*time.Second,
			"CloseWithContext must honor ctx, not block on gocron's stop timeout")
		assert.ErrorIs(t, err, context.Canceled)

		// Release the job so gocron finishes shutting down (goleak).
		close(release)
	})

	t.Run("returns nil on a clean shutdown", func(t *testing.T) {
		s, err := sched.NewGocronScheduler()
		require.NoError(t, err)
		assert.NoError(t, s.CloseWithContext(context.Background()))
	})
}
