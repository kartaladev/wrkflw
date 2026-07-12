package runtimetest

import (
	"context"
	"time"

	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Compile-time interface check.
var _ kernel.Scheduler = (*RecordingScheduler)(nil)

// RecordingScheduler is a [kernel.Scheduler] stub that records the first
// scheduled fire-at time WITHOUT firing the callback. This lets tests inspect
// the jittered delay without triggering retry loops.
//
// Clock is the time source used to resolve a fixed-duration trigger to an
// absolute fire-at (now+duration); when nil it defaults to clock.System().
// Inject a fake clock to make duration triggers deterministic.
type RecordingScheduler struct {
	Clock     clock.Clock
	FireAt    time.Time
	Scheduled bool
}

// Schedule records the computed fire-at time and marks the scheduler as invoked.
// It never runs the callback. For an absolute-time trigger it records that time;
// for a fixed-duration trigger it records now+duration (per Clock).
func (s *RecordingScheduler) Schedule(_ context.Context, _ string, trig schedule.TriggerSpec, _ func()) (time.Time, error) {
	if at, ok := trig.AbsTime(); ok {
		s.FireAt = at
	} else if d, ok := trig.Duration(); ok {
		clk := s.Clock
		if clk == nil {
			clk = clock.System()
		}
		s.FireAt = clk.Now().Add(d)
	} else {
		return time.Time{}, kernel.ErrUnsupportedTrigger
	}
	s.Scheduled = true
	return s.FireAt, nil
}

// Cancel is a no-op.
func (s *RecordingScheduler) Cancel(context.Context, string) {}

// NextRun reports the recorded fire-at time when a timer has been scheduled.
func (s *RecordingScheduler) NextRun(string) (time.Time, bool) {
	return s.FireAt, s.Scheduled
}

// FixedJitter is a deterministic JitterSource that always returns the same
// fraction. It satisfies kernel.JitterSource.
type FixedJitter struct{ F float64 }

// Fraction returns the fixed fraction.
func (j FixedJitter) Fraction() float64 { return j.F }
