package runtime

import (
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// noRecurring is an armedRecurring lookup that reports every timer as unknown
// (non-recurring), i.e. today's safe default: a fired timer is always consumed.
func noRecurring(string) bool { return false }

// timerOpsDef is a minimal definition carrier for timerJobsFor (which only
// reads ID/Version and builds fire callbacks from it).
func timerOpsDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "d",
		Version: 1,
		Nodes:   []model.Node{event.NewStart("start"), event.NewEnd("end")},
	}
}

// TestTimerJobsFor covers the single derivation site for timer side-effects
// (ADR-0134): ScheduleTimer commands become Manual timerJobs whose
// spec.NextRun is the converted trigger's Next(now) in UTC (subsuming the
// retired nextRunFor — including the UTC and original-instant guarantees);
// CancelTimer commands and consumed TimerFired triggers become PK-exact
// cancelKeys.
func TestTimerJobsFor(t *testing.T) {
	at := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	absTime := time.Date(2026, 6, 22, 15, 30, 0, 0, time.UTC)
	oneShot := schedule.AfterDuration(time.Hour)
	recurring := schedule.Every(15 * time.Minute)

	cases := []struct {
		name    string
		cmds    []engine.Command
		trg     engine.Trigger
		armedFn func(string) bool
		assert  func(t *testing.T, arms []*timerJob, cancels []cancelKey)
	}{
		{
			name:    "ScheduleTimer becomes a Manual arm job carrying its Trigger",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: oneShot, Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				assert.Equal(t, "t1", spec.TimerID)
				assert.Equal(t, "i1", spec.InstanceID)
				assert.Equal(t, "d", spec.DefID)
				assert.Equal(t, 1, spec.DefVersion)
				assert.Equal(t, oneShot, spec.Trigger)
				assert.Equal(t, engine.TimerIntermediate, spec.Kind)
				assert.True(t, spec.NextRun.Equal(at.Add(time.Hour)),
					"one-shot AfterDuration NextRun must be now+duration (original instant, crash-safe): want %v got %v", at.Add(time.Hour), spec.NextRun)
				assert.Equal(t, time.UTC, spec.NextRun.Location(), "next run must be UTC-located")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "At one-shot arm persists the absolute time (UTC) even when built in another zone",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: schedule.At(absTime.In(time.FixedZone("x", 3600))), Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				assert.True(t, spec.NextRun.Equal(absTime), "At one-shot must persist its absolute instant: want %v got %v", absTime, spec.NextRun)
				assert.Equal(t, time.UTC, spec.NextRun.Location(), "next run must be UTC-located")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "cron arm persists the REAL next occurrence (ADR-0134 closes the interim zero-NextRun gap)",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: schedule.Cron("0 9 * * *"), Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				want := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC) // next 09:00 after 2026-06-22 11:00 UTC
				assert.True(t, spec.NextRun.Equal(want),
					"cron next_run must be the trigger's real next occurrence: want %v got %v", want, spec.NextRun)
				assert.Equal(t, time.UTC, spec.NextRun.Location(), "next run must be UTC-located")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "unset trigger is unschedulable: skipped entirely (no arm, no row)",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: schedule.TriggerSpec{}, Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms, "an unconvertible trigger must be WARN-skipped, never armed")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "CancelTimer becomes a PK-exact cancel key",
			cmds:    []engine.Command{engine.CancelTimer{TimerID: "t1"}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "t1"}}, cancels)
			},
		},
		{
			name:    "TimerFired of a non-recurring timer cancels (consumes) it",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "t1"),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "t1"}}, cancels)
			},
		},
		{
			name: "TimerFired of a RECURRING timer does NOT cancel it (survives fire)",
			cmds: nil,
			trg:  engine.NewTimerFired(at, "rec-1"),
			armedFn: func(id string) bool {
				return id == "rec-1"
			},
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Empty(t, cancels, "a recurring timer must survive its fire; the native scheduler re-arms it")
			},
		},
		{
			name:    "TimerFired of an unknown timer defaults to cancel (safe)",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "gone"),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "gone"}}, cancels)
			},
		},
		{
			name:    "TimerFired with NIL armedRecurring (no timer store) is left alone",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "t1"),
			armedFn: nil,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Empty(t, cancels,
					"without a timer store recurrence is undeterminable: never deactivate a possibly-recurring native job")
			},
		},
		{
			name:    "explicit CancelTimer overrides recurrence (scope-exit stops a recurring timer)",
			cmds:    []engine.Command{engine.CancelTimer{TimerID: "rec-1"}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: func(id string) bool { return id == "rec-1" },
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "rec-1"}}, cancels,
					"an explicit CancelTimer must always cancel, recurring or not")
			},
		},
		{
			name:    "arm carries a recurring Trigger with a truthful first-fire next_run",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "rec-2", Trigger: recurring, Kind: engine.TimerInWait}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				assert.Equal(t, recurring, spec.Trigger)
				assert.True(t, spec.Trigger.Recurring())
				assert.Equal(t, engine.TimerInWait, spec.Kind)
				assert.True(t, spec.NextRun.Equal(at.Add(15*time.Minute)),
					"recurring Every persists a truthful first-fire next_run (now+interval) for Stats: want %v got %v", at.Add(15*time.Minute), spec.NextRun)
				assert.Empty(t, cancels)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver, err := NewProcessDriver(WithClock(clockwork.NewFakeClockAt(at)))
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

			arms, cancels := driver.timerJobsFor(t.Context(), timerOpsDef(), tc.cmds, tc.trg, "i1", tc.armedFn)
			tc.assert(t, arms, cancels)
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
