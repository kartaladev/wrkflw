package scheduling_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// captureHandlerFacade records slog records for assertions in the scheduling_test package.
// (captureHandler is already defined in the internal gocron test package; this is a
// separate, package-local copy for the façade tests.)
type captureHandlerFacade struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandlerFacade) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandlerFacade) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandlerFacade) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandlerFacade) WithGroup(_ string) slog.Handler      { return h }

// TestScheduler_WithTimeSkew verifies the public WithTimeSkew façade option:
//   - The option is accepted by NewScheduler without error.
//   - The scheduler still fires past-due timers correctly.
//   - Beyond-tolerance fires produce a WARN; within-tolerance do not.
func TestScheduler_WithTimeSkew(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		fireAt func() time.Time
		skew   time.Duration
		assert func(t *testing.T, fired bool, records []slog.Record)
	}

	cases := []testCase{
		{
			name:   "within tolerance: accepted, fires, no WARN",
			fireAt: func() time.Time { return startTime.Add(-1 * time.Minute) },
			skew:   5 * time.Minute,
			assert: func(t *testing.T, fired bool, records []slog.Record) {
				t.Helper()
				assert.True(t, fired, "timer must fire within tolerance")
				for _, r := range records {
					assert.NotEqual(t, slog.LevelWarn, r.Level, "no WARN expected within tolerance")
				}
			},
		},
		{
			name:   "beyond tolerance: fires (never-drop) AND emits WARN",
			fireAt: func() time.Time { return startTime.Add(-10 * time.Minute) },
			skew:   5 * time.Minute,
			assert: func(t *testing.T, fired bool, records []slog.Record) {
				t.Helper()
				assert.True(t, fired, "timer must fire even beyond tolerance (never-drop invariant)")
				var warnFound bool
				for _, r := range records {
					if r.Level == slog.LevelWarn {
						warnFound = true
						break
					}
				}
				assert.True(t, warnFound, "WARN must be logged when lateness exceeds tolerance")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := clockwork.NewFakeClockAt(startTime)
			h := &captureHandlerFacade{}
			logger := slog.New(h)

			s, err := scheduling.NewScheduler(
				scheduling.WithClock(clk),
				scheduling.WithLogger(logger),
				scheduling.WithTimeSkew(tc.skew),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			var mu sync.Mutex
			fired := false
			_, err = s.Schedule(t.Context(), "facade-skew-timer", schedule.At(tc.fireAt()), func() {
				mu.Lock()
				fired = true
				mu.Unlock()
			})
			require.NoError(t, err)

			require.Eventually(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return fired
			}, 2*time.Second, 5*time.Millisecond, "past-due timer must fire immediately")

			mu.Lock()
			f := fired
			mu.Unlock()

			h.mu.Lock()
			records := make([]slog.Record, len(h.records))
			copy(records, h.records)
			h.mu.Unlock()

			tc.assert(t, f, records)
		})
	}
}
