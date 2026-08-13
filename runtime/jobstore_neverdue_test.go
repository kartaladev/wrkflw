package runtime

import (
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

// armedRow builds one persisted armed-timer row against timerOpsDef.
func armedRow(instanceID, timerID string, trig schedule.TriggerSpec, nextRun time.Time) kernel.ArmedTimer {
	def := timerOpsDef()
	return kernel.ArmedTimer{
		InstanceID: instanceID,
		DefID:      def.ID,
		DefVersion: def.Version,
		TimerID:    timerID,
		Trigger:    trig,
		NextRun:    nextRun,
		Kind:       engine.TimerInWait,
	}
}

// loadJobIDs runs jobStore.Load against a store seeded with rows and returns
// the timer ids that survived, with each survivor's arm instant.
func loadJobIDs(t *testing.T, now time.Time, rows ...kernel.ArmedTimer) ([]string, map[string]time.Time) {
	t.Helper()

	store := kernel.NewMemTimerStore()
	for _, r := range rows {
		store.Arm(r)
	}
	driver, err := NewProcessDriver(
		WithClock(clockwork.NewFakeClockAt(now)),
		WithTimerStore(store),
		WithDefinitions(kernel.NewMapDefinitionRegistry(timerOpsDef())),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

	jobs, err := newJobStore(driver).Load(t.Context())
	require.NoError(t, err)

	ids := make([]string, 0, len(jobs))
	nexts := make(map[string]time.Time, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID())
		nexts[j.ID()] = j.NextRun()
	}
	return ids, nexts
}

// TestJobStoreLoadSkipsARowItCannotArm covers rehydration, the arm path that
// bypasses every other guard: it re-arms from persisted rows, and
// rehydrateTrigger hands a RECURRING trigger back verbatim — which every
// never-due calendar kind is — so the row is re-armed at boot from the trigger
// itself, not from the stored next_run.
//
// What makes it fail without the guard: the row below re-arms to an ok=false
// trigger, so the job reaches the scheduler carrying a zero next run and
// gocron searches for a 31st that no month on its 12-month grid has, without
// a bound. The persisted next_run does NOT have to be zero for that: a row
// armed in a month WITH a 31st carries a perfectly valid instant and still
// wedges a boot that happens in February.
func TestJobStoreLoadSkipsARowItCannotArm(t *testing.T) {
	feb := time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC)
	healthy := armedRow("i-ok", "t-ok", schedule.Every(time.Hour), feb.Add(time.Hour))

	cases := []struct {
		name string
		row  kernel.ArmedTimer
		want bool // is the row expected to survive Load?
	}{
		{
			name: "a livelocking calendar row armed in a month that HAS a 31st",
			row:  armedRow("i1", "t1", schedule.Monthly(12, []int{31}), time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)),
		},
		{
			name: "a legacy row poisoned with a zero next_run and a never-due trigger",
			row:  armedRow("i2", "t2", schedule.Every(0), time.Time{}),
		},
		{
			name: "a legacy row poisoned with a zero next_run whose trigger still fires re-arms",
			// The stored value is unusable but the trigger is not, and the arm
			// instant is recomputed from the trigger — so this row heals
			// rather than being stranded.
			row:  armedRow("i3", "t3", schedule.Every(time.Hour), time.Time{}),
			want: true,
		},
		{
			name: "an ordinary recurring row is untouched",
			row:  armedRow("i4", "t4", schedule.Weekly(1, []time.Weekday{time.Monday}), feb.AddDate(0, 0, 6)),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids, nexts := loadJobIDs(t, feb, healthy, tc.row)

			assert.Contains(t, ids, healthy.TimerID, "an unrelated healthy row must never be dropped")
			if tc.want {
				assert.Contains(t, ids, tc.row.TimerID)
				assert.False(t, nexts[tc.row.TimerID].IsZero(),
					"a rehydrated job must never be armed at the zero instant")
				return
			}
			assert.NotContains(t, ids, tc.row.TimerID)
		})
	}
}

// TestRehydrateTimersDoesNotWedgeOnALivelockingRow is the boot-path companion
// to TestDriveDoesNotWedgeOnALivelockingTimerArm: the same gocron defect, but
// reached through startup rehydration rather than a fresh arm. It runs against
// the REAL scheduler, so without the guard RehydrateTimers never returns and
// the process comes up with its scheduler goroutine permanently wedged.
//
// ⚠ Runs on its own goroutine with an explicit timeout — a direct call would
// hang the package run until go test's global timeout.
func TestRehydrateTimersDoesNotWedgeOnALivelockingRow(t *testing.T) {
	feb := time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(feb)

	store := kernel.NewMemTimerStore()
	store.Arm(armedRow("i1", "t1", schedule.Monthly(12, []int{31}), time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)))

	sched, err := scheduler.NewScheduler(scheduler.WithClock(fc))
	require.NoError(t, err)
	t.Cleanup(func() { go func() { _ = sched.Close() }() })

	driver, err := NewProcessDriver(
		WithClock(fc),
		WithScheduler(sched),
		WithTimerStore(store),
		WithDefinitions(kernel.NewMapDefinitionRegistry(timerOpsDef())),
	)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- driver.RehydrateTimers(t.Context()) }()

	select {
	case rerr := <-done:
		assert.NoError(t, rerr, "an unarmable row is skipped with a WARN, not an error")
	case <-time.After(10 * time.Second):
		t.Fatal("RehydrateTimers did not return within 10s: a persisted row reached gocron's unbounded monthly search")
	}
}
