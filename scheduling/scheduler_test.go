package scheduling_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

func TestNewScheduler_SatisfiesPortAndFires(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(fakeClock))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Runtime checks that the façade satisfies the required contracts.
	var _ runtime.Scheduler = s
	var _ io.Closer = s

	var wg sync.WaitGroup
	wg.Add(1)
	s.Schedule("t1", fakeClock.Now().Add(3*time.Second), func() { wg.Done() })
	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
	fakeClock.Advance(3 * time.Second)
	wg.Wait()
}

func TestScheduler_Cancel_NoOp(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(fakeClock))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Cancel on an unknown ID must not panic or error.
	s.Cancel("nonexistent")

	// Schedule then cancel — callback must NOT fire.
	fired := false
	s.Schedule("t2", fakeClock.Now().Add(1*time.Second), func() { fired = true })
	s.Cancel("t2")

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
				s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk), scheduling.WithLogger(logger))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })

				// Verify scheduler still fires correctly with injected logger.
				var wg sync.WaitGroup
				wg.Add(1)
				s.Schedule("wl-t1", clk.Now().Add(time.Second), func() { wg.Done() })
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second)
				wg.Wait()
			},
		},
		{
			name: "WithLogger nil is a no-op — construction succeeds",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk), scheduling.WithLogger(nil))
				require.NoError(t, err)
				t.Cleanup(func() { _ = s.Close() })
				require.NotNil(t, s)
			},
		},
		{
			name: "no options still constructs correctly",
			assert: func(t *testing.T) {
				clk := clockwork.NewFakeClock()
				s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk))
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
				s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk), scheduling.WithTracerProvider(tp))
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
				s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk), scheduling.WithMeterProvider(mp))
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
				s, err := scheduling.NewScheduler(
					scheduling.WithSchedulerClock(clk),
					scheduling.WithTracerProvider(tp),
					scheduling.WithMeterProvider(mp),
					scheduling.WithLogger(l),
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
