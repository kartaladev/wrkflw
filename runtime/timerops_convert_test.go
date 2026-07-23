package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/scheduler"
)

// TestConvertTrigger locks convertTrigger's total mapping over ALL 10
// schedule.Kind values: the 7 executable kinds convert to a scheduler.Trigger
// that is value-equivalent to the directly-constructed one (asserted through
// Trigger.Next on a fixed anchor plus Recurring), and the 3 non-executable
// kinds (Unset, Expr, EveryExpr — engine-resolved before arming) are rejected
// wrapping scheduler.ErrUnsupportedTrigger.
func TestConvertTrigger(t *testing.T) {
	t.Parallel()

	anchor := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)

	type testCase struct {
		name   string
		in     schedule.TriggerSpec
		assert func(t *testing.T, got scheduler.Trigger, err error)
	}

	// equiv builds an assert closure that checks got behaves identically to
	// want through the Trigger value surface: same Next at the fixed anchor
	// and the same recurrence flag.
	equiv := func(want scheduler.Trigger) func(t *testing.T, got scheduler.Trigger, err error) {
		return func(t *testing.T, got scheduler.Trigger, err error) {
			t.Helper()
			require.NoError(t, err)
			wantNext, wantOK := want.Next(anchor)
			gotNext, gotOK := got.Next(anchor)
			require.Equal(t, wantOK, gotOK, "Next ok mismatch")
			assert.True(t, gotNext.Equal(wantNext), "Next mismatch: want %v got %v", wantNext, gotNext)
			assert.Equal(t, want.Recurring(), got.Recurring(), "Recurring mismatch")
		}
	}

	unsupported := func(t *testing.T, _ scheduler.Trigger, err error) {
		t.Helper()
		require.ErrorIs(t, err, scheduler.ErrUnsupportedTrigger)
	}

	cases := []testCase{
		{
			name:   "KindOneTime absolute (At)",
			in:     schedule.At(anchor.Add(time.Hour)),
			assert: equiv(scheduler.At(anchor.Add(time.Hour))),
		},
		{
			name:   "KindOneTime duration (AfterDuration)",
			in:     schedule.AfterDuration(90 * time.Minute),
			assert: equiv(scheduler.After(90 * time.Minute)),
		},
		{
			name:   "KindDuration (Every)",
			in:     schedule.Every(15 * time.Minute),
			assert: equiv(scheduler.Every(15 * time.Minute)),
		},
		{
			name:   "KindDurationRand (EveryRandom)",
			in:     schedule.EveryRandom(time.Minute, 5*time.Minute),
			assert: equiv(scheduler.EveryRandom(time.Minute, 5*time.Minute)),
		},
		{
			name:   "KindCron",
			in:     schedule.Cron("0 9 * * *"),
			assert: equiv(scheduler.Cron("0 9 * * *")),
		},
		{
			name:   "KindDaily",
			in:     schedule.Daily(2, schedule.ClockTime{Hour: 9}),
			assert: equiv(scheduler.Daily(2, scheduler.ClockTime{Hour: 9})),
		},
		{
			name:   "KindWeekly",
			in:     schedule.Weekly(1, []time.Weekday{time.Monday, time.Friday}, schedule.ClockTime{Hour: 8}),
			assert: equiv(scheduler.Weekly(1, []time.Weekday{time.Monday, time.Friday}, scheduler.ClockTime{Hour: 8})),
		},
		{
			name:   "KindMonthly",
			in:     schedule.Monthly(1, []int{15}, schedule.ClockTime{Hour: 7, Minute: 30}),
			assert: equiv(scheduler.Monthly(1, []int{15}, scheduler.ClockTime{Hour: 7, Minute: 30})),
		},
		{
			name:   "KindUnset is rejected",
			in:     schedule.TriggerSpec{},
			assert: unsupported,
		},
		{
			name:   "KindExpr is rejected (engine resolves it before arming)",
			in:     schedule.AfterExpr(`"1h"`),
			assert: unsupported,
		},
		{
			name:   "KindEveryExpr is rejected (engine resolves it before arming)",
			in:     schedule.EveryExpr(`"1m"`),
			assert: unsupported,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := convertTrigger(tc.in)
			tc.assert(t, got, err)
		})
	}
}
