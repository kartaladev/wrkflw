package processtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/scheduler"
)

// TestReview_AdvanceTimersNoBackwardClock covers finding #5: advancing to a timer
// whose fireAt is already in the past must not rewind the shared fake clock.
func TestReview_AdvanceTimersNoBackwardClock(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h, err := processtest.New(processtest.WithClockStart(base))
	require.NoError(t, err)

	def := signalDef(t)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	// A past-due timer (fireAt = base-1h) scheduled directly on the shared scheduler.
	pastJob, err := scheduler.NewJobWithID("past", "test-timer", scheduler.At(base.Add(-time.Hour)),
		func(_ context.Context, _ scheduler.DataProvider) error { return nil },
		scheduler.NewEmptyDataProvider())
	require.NoError(t, err)
	if _, err := h.Scheduler().Schedule(t.Context(), pastJob); err != nil {
		t.Fatalf("Schedule past timer: %v", err)
	}

	steps := 0
	_, err = h.DriveToCompletion(t.Context(), def, "i", func(context.Context, processtest.Park) (processtest.Decision, error) {
		steps++
		if steps == 1 {
			return processtest.AdvanceTimers(), nil
		}
		return processtest.Stop(), nil
	})
	require.NoError(t, err)

	assert.False(t, h.Clock().Now().Before(base),
		"AdvanceTimers on a past-due timer must not move the clock backward")
}
