package runtime

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/scheduler"
)

// febAnchor is a clock instant inside a month with no 31st — the anchor that
// makes Monthly(12, []int{31}) never-due, and makes arming it livelock gocron.
var febAnchor = time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC)

// TestNeverDueNextRun pins the arm predicate itself, both halves separately.
//
// The ok=false half is also covered end-to-end by
// TestTimerJobsForRefusesANeverDueArm. The next.IsZero() half is pinned HERE
// and only here, deliberately: after the scheduler fix no Trigger shape
// reports (zero, true) any more — the 30-February cron that used to is now
// ok=false — so the second half of the predicate has no reachable trigger to
// drive it. It is kept as the direct statement of the invariant ("a zero
// next_run must never be persisted") and as the guard that survives if
// Trigger.Next ever regresses to reporting a zero instant as fireable.
func TestNeverDueNextRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		next time.Time
		ok   bool
		want bool
	}{
		{name: "a real instant reported ok is armable", next: febAnchor, ok: true, want: false},
		{name: "ok=false is never due — the half a trigger can reach", next: time.Time{}, ok: false, want: true},
		{name: "ok=false even carrying an instant is never due", next: febAnchor, ok: false, want: true},
		{name: "a zero instant reported ok is never due — the half no trigger reaches today", next: time.Time{}, ok: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, neverDueNextRun(tc.next, tc.ok))
		})
	}
}

// TestTimerJobsForRefusesANeverDueArm covers the arm site that both the in-tx
// durable write and the post-commit Activate flow from: refusing here produces
// no timer row AND no Activate call, which is what makes the gocron monthly
// livelock unreachable.
//
// Without the guard every case below appends an arm carrying a ZERO NextRun —
// which MySQL rejects outright (Error 1292), failing the whole step so the
// instance can never advance past the node, while Postgres and SQLite persist
// it as 0001-01-01 and hang the instance forever.
func TestTimerJobsForRefusesANeverDueArm(t *testing.T) {
	cases := []struct {
		name string
		trig schedule.TriggerSpec
		want int // expected arms
	}{
		{
			name: "a zero recurring interval",
			trig: schedule.Every(0),
		},
		{
			name: "a day-of-month no month on the interval grid can hold",
			// The livelock shape: never-due from a February anchor, and the
			// arm gocron searches for without a bound.
			trig: schedule.Monthly(12, []int{31}),
		},
		{
			name: "a cron expression that parses but matches nothing",
			// 30 February. robfig/cron gives up after five years; before the
			// scheduler fix this reported ok=true with a zero instant, so it
			// defeated every ok-keyed gate and still wrote a zero row.
			trig: schedule.Cron("0 0 30 2 *"),
		},
		{
			name: "an empty weekday set still arms — the scheduler substitutes Sunday",
			trig: schedule.Weekly(1, nil),
			want: 1,
		},
		{
			name: "a negative day-of-month still arms — it counts back from month end",
			trig: schedule.Monthly(1, []int{-1}),
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver, err := NewProcessDriver(WithClock(clockwork.NewFakeClockAt(febAnchor)))
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

			cmds := []engine.Command{engine.ScheduleTimer{
				TimerID: "t1",
				Trigger: tc.trig,
				Kind:    engine.TimerInWait,
			}}
			arms, cancels := driver.timerJobsFor(t.Context(), timerOpsDef(), cmds, nil, "i1", noRecurring)

			assert.Empty(t, cancels)
			require.Len(t, arms, tc.want)
			for _, arm := range arms {
				assert.False(t, arm.descriptor().NextRun.IsZero(),
					"an armed timer must never carry a zero next_run")
			}
		})
	}
}

// ungatedScheduler is a consumer-supplied [scheduler.Scheduler] that does NOT
// refuse a never-due trigger. It exists because the runtime consumes the
// Scheduler PORT, not a specific implementation: both schedulers this repo
// ships (NativeScheduler and processtest.MemScheduler) reject an ok=false
// trigger in their own Schedule, but nothing in the port requires that, and a
// consumer's implementation is free to arm it — measured returning a job with
// a zero next run and a nil error.
type ungatedScheduler struct {
	now   time.Time
	armed []string
	nexts []time.Time
}

func (s *ungatedScheduler) Schedule(_ context.Context, j scheduler.Job) (scheduler.ScheduledJob, error) {
	next, _ := j.Trigger().Next(s.now)
	s.armed = append(s.armed, j.ID())
	s.nexts = append(s.nexts, next)
	return scheduler.NewScheduledJob(j, next)
}
func (s *ungatedScheduler) Activate(context.Context, scheduler.ScheduledJob) error { return nil }
func (s *ungatedScheduler) Deactivate(context.Context, string) error               { return nil }
func (s *ungatedScheduler) Cancel(context.Context, string) error                   { return nil }
func (s *ungatedScheduler) Scheduled(context.Context, string) (scheduler.ScheduledJob, error) {
	return nil, errors.New("not implemented")
}
func (s *ungatedScheduler) List(context.Context) iter.Seq[scheduler.ScheduledJob] {
	return func(func(scheduler.ScheduledJob) bool) {}
}

// TestScheduleStartTimerJobRefusesANeverDueTrigger covers the third arm path:
// a timer START event, armed raw from the registered definition with no
// instance and no durable row behind it (RehydrateStartTimers → armStartTimer
// → scheduleStartTimerJob).
//
// What makes it fail without the guard: the driver hands the job straight to
// whatever Scheduler the consumer supplied. Against the ungating one above,
// the never-due job is armed with a zero next run and no error — so the
// refusal has to be the RUNTIME's, not borrowed from one implementation of the
// port.
func TestScheduleStartTimerJobRefusesANeverDueTrigger(t *testing.T) {
	cases := []struct {
		name      string
		trig      schedule.TriggerSpec
		wantArmed bool
	}{
		{name: "a zero recurring interval", trig: schedule.Every(0)},
		{name: "the livelocking day-of-month", trig: schedule.Monthly(12, []int{31})},
		{name: "a cron that matches nothing", trig: schedule.Cron("0 0 30 2 *")},
		{name: "an empty weekday set still arms", trig: schedule.Weekly(1, nil), wantArmed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sched := &ungatedScheduler{now: febAnchor}
			driver, err := NewProcessDriver(
				WithClock(clockwork.NewFakeClockAt(febAnchor)), WithScheduler(sched))
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

			sj, err := driver.scheduleStartTimerJob(t.Context(), timerOpsDef(), "n1", "t1", tc.trig)
			if !tc.wantArmed {
				require.Error(t, err, "a never-due timer-start must be refused")
				assert.ErrorIs(t, err, scheduler.ErrUnsupportedTrigger)
				assert.Nil(t, sj)
				assert.Empty(t, sched.armed, "nothing may reach the scheduler")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, sj)
			assert.False(t, sj.NextRun().IsZero())
			assert.Equal(t, []string{"t1"}, sched.armed)
		})
	}
}
