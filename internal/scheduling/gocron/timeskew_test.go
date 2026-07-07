package gocron_test

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
)

// TestGocronScheduler_TimeSkew verifies the WithTimeSkew option behaviour for
// the one-shot past-due path:
//
//   - Within tolerance  → fires immediately, no WARN logged.
//   - Beyond tolerance  → STILL fires immediately (never dropped), WARN logged
//     with timer_id, fire_time, and lateness attributes.
func TestGocronScheduler_TimeSkew(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		fireAt func() time.Time // relative to startTime
		skew   time.Duration    // tolerance passed to WithTimeSkew
		assert func(t *testing.T, h *captureHandler, fired bool)
	}

	cases := []testCase{
		{
			name: "within tolerance: fires immediately, no WARN",
			// lateness = 1 minute, tolerance = 5 minutes → within
			fireAt: func() time.Time { return startTime.Add(-1 * time.Minute) },
			skew:   5 * time.Minute,
			assert: func(t *testing.T, h *captureHandler, fired bool) {
				t.Helper()
				assert.True(t, fired, "timer must fire even when past-due within tolerance")
				h.mu.Lock()
				defer h.mu.Unlock()
				for _, r := range h.records {
					assert.NotEqual(t, slog.LevelWarn, r.Level,
						"no WARN must be logged when lateness is within tolerance")
				}
			},
		},
		{
			name: "beyond tolerance: fires immediately AND emits WARN with attrs",
			// lateness = 10 minutes, tolerance = 5 minutes → beyond
			fireAt: func() time.Time { return startTime.Add(-10 * time.Minute) },
			skew:   5 * time.Minute,
			assert: func(t *testing.T, h *captureHandler, fired bool) {
				t.Helper()
				assert.True(t, fired, "timer must STILL fire even when past-due beyond tolerance (never-drop invariant)")

				h.mu.Lock()
				defer h.mu.Unlock()

				var warnFound bool
				var hasTimerID, hasFireTime, hasLateness bool
				for _, r := range h.records {
					if r.Level != slog.LevelWarn {
						continue
					}
					warnFound = true
					r.Attrs(func(a slog.Attr) bool {
						switch a.Key {
						case "timer_id":
							hasTimerID = true
						case "fire_time":
							hasFireTime = true
						case "lateness":
							hasLateness = true
						}
						return true
					})
				}
				assert.True(t, warnFound, "a WARN must be logged when lateness exceeds tolerance")
				assert.True(t, hasTimerID, "WARN must include timer_id attribute")
				assert.True(t, hasFireTime, "WARN must include fire_time attribute")
				assert.True(t, hasLateness, "WARN must include lateness attribute")
			},
		},
		{
			name: "zero tolerance: any past-due fires with WARN",
			// lateness = 1 nanosecond, tolerance = 0 → beyond (0 means warn on ANY past-due)
			fireAt: func() time.Time { return startTime.Add(-1 * time.Nanosecond) },
			skew:   0,
			assert: func(t *testing.T, h *captureHandler, fired bool) {
				t.Helper()
				assert.True(t, fired, "timer must fire with zero tolerance")
				h.mu.Lock()
				defer h.mu.Unlock()
				var warnFound bool
				for _, r := range h.records {
					if r.Level == slog.LevelWarn {
						warnFound = true
						break
					}
				}
				assert.True(t, warnFound, "WARN must be logged for any past-due when tolerance is 0")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := clockwork.NewFakeClockAt(startTime)
			h := &captureHandler{}
			logger := slog.New(h)

			s, err := sched.NewGocronScheduler(
				sched.WithClock(clk),
				sched.WithLogger(logger),
				sched.WithTimeSkew(tc.skew),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			var mu sync.Mutex
			fired := false
			_, err = s.Schedule(t.Context(), "skew-timer", schedule.At(tc.fireAt()), func() {
				mu.Lock()
				fired = true
				mu.Unlock()
			})
			require.NoError(t, err)

			// Past-due timers use OneTimeJobStartImmediately — no clock advance needed.
			require.Eventually(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return fired
			}, 2*time.Second, 5*time.Millisecond, "past-due timer must fire immediately")

			mu.Lock()
			f := fired
			mu.Unlock()
			tc.assert(t, h, f)
		})
	}
}

// TestGocronScheduler_TimeSkew_DefaultSilentWithinFiveMinutes verifies that
// when WithTimeSkew is NOT set the default (5 minutes) applies: a timer that
// is 4 minutes past due fires without a WARN.
func TestGocronScheduler_TimeSkew_DefaultSilentWithinFiveMinutes(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startTime)
	h := &captureHandler{}
	logger := slog.New(h)

	// No WithTimeSkew — default (5m) should apply.
	s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithLogger(logger))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var mu sync.Mutex
	fired := false
	// fireAt is 4 minutes in the past — within the 5-minute default.
	_, err = s.Schedule(t.Context(), "default-skew-timer", schedule.At(startTime.Add(-4*time.Minute)), func() {
		mu.Lock()
		fired = true
		mu.Unlock()
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return fired
	}, 2*time.Second, 5*time.Millisecond, "past-due timer within default tolerance must still fire")

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		assert.NotEqual(t, slog.LevelWarn, r.Level,
			"no WARN expected when lateness is within the default 5-minute tolerance")
	}
}
