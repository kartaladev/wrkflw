package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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
				assert.True(t, arms[0].NextRun.Equal(at.Add(time.Hour)),
					"one-shot AfterDuration NextRun must be now+duration (truthful, crash-safe): want %v got %v", at.Add(time.Hour), arms[0].NextRun)
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
				assert.True(t, arms[0].NextRun.Equal(at.Add(15*time.Minute)),
					"recurring Every persists a truthful first-fire next_run (now+interval) for Stats; rehydration still re-arms from Trigger: want %v got %v", at.Add(15*time.Minute), arms[0].NextRun)
				assert.Empty(t, cancels)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arms, cancels := timerOpsFor(tc.cmds, tc.trg, "d", 1, "i1", at, tc.armedFn)
			tc.assert(t, arms, cancels)
		})
	}
}

func TestNextRunFor(t *testing.T) {
	now := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	absTime := time.Date(2026, 6, 22, 15, 30, 0, 0, time.UTC)
	cases := []struct {
		name string
		trig schedule.TriggerSpec
		want time.Time
	}{
		{
			name: "At one-shot returns the absolute time (UTC)",
			trig: schedule.At(absTime.In(time.FixedZone("x", 3600))),
			want: absTime,
		},
		{
			name: "AfterDuration one-shot returns now + duration",
			trig: schedule.AfterDuration(2 * time.Hour),
			want: now.Add(2 * time.Hour),
		},
		{
			name: "recurring Every returns now + interval (truthful for Stats)",
			trig: schedule.Every(15 * time.Minute),
			want: now.Add(15 * time.Minute),
		},
		{
			name: "cron recurring returns zero (interim: rehydrated from Trigger)",
			trig: schedule.Cron("0 9 * * *"),
			want: time.Time{},
		},
		{
			name: "unset trigger returns zero",
			trig: schedule.TriggerSpec{},
			want: time.Time{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextRunFor(tc.trig, now)
			assert.True(t, got.Equal(tc.want), "want %v got %v", tc.want, got)
			if !tc.want.IsZero() {
				assert.Equal(t, time.UTC, got.Location(), "next run must be UTC-located")
			}
		})
	}
}

func TestRehydrateTrigger(t *testing.T) {
	nextRun := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		armed  kernel.ArmedTimer
		assert func(t *testing.T, got schedule.TriggerSpec)
	}{
		{
			name:  "non-recurring with NextRun re-arms via At(NextRun)",
			armed: kernel.ArmedTimer{Trigger: schedule.AfterDuration(time.Hour), NextRun: nextRun},
			assert: func(t *testing.T, got schedule.TriggerSpec) {
				at, ok := got.AbsTime()
				assert.True(t, ok, "must be an At trigger")
				assert.True(t, at.Equal(nextRun), "At time must equal persisted NextRun")
			},
		},
		{
			name:  "recurring re-arms via its Trigger (scheduler recomputes)",
			armed: kernel.ArmedTimer{Trigger: schedule.Every(15 * time.Minute), NextRun: nextRun},
			assert: func(t *testing.T, got schedule.TriggerSpec) {
				assert.Equal(t, schedule.KindDuration, got.Kind(), "recurring keeps its Trigger")
				assert.True(t, got.Recurring())
			},
		},
		{
			name:  "non-recurring without NextRun falls back to its Trigger",
			armed: kernel.ArmedTimer{Trigger: schedule.AfterDuration(time.Hour)},
			assert: func(t *testing.T, got schedule.TriggerSpec) {
				d, ok := got.Duration()
				assert.True(t, ok, "falls back to the AfterDuration trigger")
				assert.Equal(t, time.Hour, d)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, rehydrateTrigger(tc.armed))
		})
	}
}
