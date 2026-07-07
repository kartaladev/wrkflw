package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// noRecurring is an armedRecurring lookup that reports every timer as unknown
// (non-recurring), i.e. today's safe default: a fired timer is always consumed.
func noRecurring(string) bool { return false }

func TestTimerOpsFor(t *testing.T) {
	at := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	oneShot := schedule.AfterDuration(time.Hour)
	recurring := schedule.Every(15 * time.Minute)
	cases := []struct {
		name    string
		cmds    []engine.Command
		trg     engine.Trigger
		armedFn func(string) bool
		assert  func(t *testing.T, arms []kernel.ArmedTimer, cancels []string)
	}{
		{
			name:    "ScheduleTimer becomes an arm carrying its Trigger",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: oneShot, Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []kernel.ArmedTimer, cancels []string) {
				assert.Len(t, arms, 1)
				assert.Equal(t, "t1", arms[0].TimerID)
				assert.Equal(t, oneShot, arms[0].Trigger)
				assert.True(t, arms[0].NextRun.IsZero(), "NextRun is populated by Plan 3 persistence; zero here")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "CancelTimer becomes a cancel",
			cmds:    []engine.Command{engine.CancelTimer{TimerID: "t1"}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []kernel.ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"t1"}, cancels)
			},
		},
		{
			name:    "TimerFired of a non-recurring timer cancels (consumes) it",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "t1"),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []kernel.ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"t1"}, cancels)
			},
		},
		{
			name: "TimerFired of a RECURRING timer does NOT cancel it (survives fire)",
			cmds: nil,
			trg:  engine.NewTimerFired(at, "rec-1"),
			armedFn: func(id string) bool {
				return id == "rec-1"
			},
			assert: func(t *testing.T, arms []kernel.ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Empty(t, cancels, "a recurring timer must survive its fire; the native scheduler re-arms it")
			},
		},
		{
			name:    "TimerFired of an unknown timer defaults to cancel (safe)",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "gone"),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []kernel.ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"gone"}, cancels)
			},
		},
		{
			name:    "explicit CancelTimer overrides recurrence (scope-exit stops a recurring timer)",
			cmds:    []engine.Command{engine.CancelTimer{TimerID: "rec-1"}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: func(id string) bool { return id == "rec-1" },
			assert: func(t *testing.T, arms []kernel.ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"rec-1"}, cancels, "an explicit CancelTimer must always cancel, recurring or not")
			},
		},
		{
			name:    "arm carries a recurring Trigger",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "rec-2", Trigger: recurring, Kind: engine.TimerInWait}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []kernel.ArmedTimer, cancels []string) {
				assert.Len(t, arms, 1)
				assert.Equal(t, recurring, arms[0].Trigger)
				assert.True(t, arms[0].Trigger.Recurring())
				assert.Empty(t, cancels)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arms, cancels := timerOpsFor(tc.cmds, tc.trg, "d", 1, "i1", tc.armedFn)
			tc.assert(t, arms, cancels)
		})
	}
}
