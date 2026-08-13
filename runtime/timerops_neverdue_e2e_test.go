package runtime_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

// neverDueTimerDef returns start → timer-catch(trig) → end, so driving it parks
// on a node that arms exactly one timer from trig.
func neverDueTimerDef(trig schedule.TriggerSpec) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "never-due-timer",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait", event.WithCatchTimer(trig)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}
}

// TestDriveDoesNotWedgeOnALivelockingTimerArm is the ADR-0176 regression test
// for a whole-process availability defect in shipped code: arming
// Monthly(12, []int{31}) while the scheduler's clock sits in a month with no
// 31st made gocron v2.22.0's monthlyJob.next spin forever inside gocron's
// single selectNewJob goroutine, so EVERY job's arm, cancel and rehydrate in
// the process blocked behind it — not just this instance's.
//
// What makes it fail without the arm guard: Drive commits the step, then calls
// the post-commit sched.Activate, which never returns. The assertion is
// therefore "Drive returns at all", and the drive runs on its own goroutine
// with an explicit timeout — a naive call would hang the whole package run
// until go test's global timeout.
//
// ⚠ Clock-month dependent. With interval 12 only months congruent to the
// anchor month qualify, so the wedge window is the five months without a 31st.
// The February clock below is load-bearing: on an August clock this same
// definition arms cleanly and the test cannot fail.
func TestDriveDoesNotWedgeOnALivelockingTimerArm(t *testing.T) {
	ctx := t.Context()

	// February 2026: no 31st, so no month on the 12-month grid ever has one.
	fc := clockwork.NewFakeClockAt(time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC))

	sched, err := scheduler.NewScheduler(scheduler.WithClock(fc))
	require.NoError(t, err)
	t.Cleanup(func() {
		// Close on its own goroutine: a wedged gocron never returns from it.
		go func() { _ = sched.Close() }()
	})

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(store), runtime.WithClock(fc), runtime.WithScheduler(sched))
	require.NoError(t, err)

	type result struct {
		state engine.InstanceState
		err   error
	}
	done := make(chan result, 1)
	go func() {
		st, derr := driver.Drive(ctx, neverDueTimerDef(schedule.Monthly(12, []int{31})), "livelock-1", nil)
		done <- result{state: st, err: derr}
	}()

	select {
	case got := <-done:
		require.NoError(t, got.err, "a never-due arm is skipped with a WARN, not an error")
		assert.Equal(t, engine.StatusRunning, got.state.Status)
		require.Len(t, got.state.Tokens, 1)
		assert.Equal(t, "wait", got.state.Tokens[0].NodeID,
			"the token still parks on the timer node — it simply has no armed timer")
	case <-time.After(10 * time.Second):
		t.Fatal("Drive did not return within 10s: the never-due arm reached gocron's unbounded monthly search")
	}
}

// clockAdvancingStore advances a fake clock exactly once, immediately after
// the instance-state write inside the commit transaction. That lands the
// advance between the arm guard (which runs in timerJobsFor, BEFORE the
// transaction) and newScheduledTimerJob (which re-reads the clock inside it),
// which is the window the guard cannot see.
type clockAdvancingStore struct {
	*kernel.MemInstanceStore
	clk  *clockwork.FakeClock
	by   time.Duration
	once sync.Once
}

func (s *clockAdvancingStore) Create(ctx context.Context, step kernel.AppliedStep) (kernel.Version, error) {
	v, err := s.MemInstanceStore.Create(ctx, step)
	s.once.Do(func() { s.clk.Advance(s.by) })
	return v, err
}

// TestDriveDoesNotWedgeWhenTheClockCrossesTheGuard covers the gap between the
// arm guard and the arm itself: the guard evaluates Trigger.Next at the step's
// clock reading, but the scheduler re-derives the fire time from the trigger at
// ITS OWN, later reading (activateJob discards the ScheduledJob's NextRun
// entirely). A trigger can be armable at the first instant and never-due at the
// second.
//
// Monthly(12, {31}) is exactly that: armable from 2026-01-31T23:59:59Z (it
// finds 2027-01-31) and never-due one second later, because the anchor month
// becomes February. Without the post-commit re-check, that step passes the
// guard and then hands gocron the anchor its unbounded monthly search never
// escapes — restoring the whole-process wedge for every job behind it.
//
// ⚠ The window is small in production but the consequence is total, and the
// re-check does not eliminate it: the scheduler still reads the clock once
// more, after this check. It narrows the window from "the whole commit" to a
// few instructions. Documented as a residual in ADR-0176.
func TestDriveDoesNotWedgeWhenTheClockCrossesTheGuard(t *testing.T) {
	ctx := t.Context()

	// One second before February: the trigger is armable HERE.
	fc := clockwork.NewFakeClockAt(time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC))
	trig := schedule.Monthly(12, []int{31})
	next, ok := scheduler.Monthly(12, []int{31}).Next(fc.Now())
	require.True(t, ok, "fixture check: the trigger must pass the guard at the pre-commit clock")
	require.False(t, next.IsZero())
	after, okAfter := scheduler.Monthly(12, []int{31}).Next(fc.Now().Add(2 * time.Second))
	require.False(t, okAfter, "fixture check: and must be never-due at the post-advance clock")
	require.True(t, after.IsZero())

	sched, err := scheduler.NewScheduler(scheduler.WithClock(fc))
	require.NoError(t, err)
	t.Cleanup(func() { go func() { _ = sched.Close() }() })

	mem, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	store := &clockAdvancingStore{MemInstanceStore: mem, clk: fc, by: 2 * time.Second}

	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(store), runtime.WithClock(fc), runtime.WithScheduler(sched))
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, derr := driver.Drive(ctx, neverDueTimerDef(trig), "toctou-1", nil)
		done <- derr
	}()

	select {
	case derr := <-done:
		assert.NoError(t, derr)
	case <-time.After(10 * time.Second):
		t.Fatal("Drive did not return within 10s: a trigger that went never-due mid-commit reached Activate")
	}
}
