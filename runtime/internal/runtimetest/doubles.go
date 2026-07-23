package runtimetest

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/scheduler"
)

// Compile-time interface check.
var _ scheduler.Scheduler = (*RecordingScheduler)(nil)

// RecordingScheduler is a [scheduler.Scheduler] stub that records the first
// armed job's fire-at time WITHOUT ever firing its action. This lets tests
// inspect the jittered retry delay without triggering retry loops.
//
// A job is recorded when it is armed: on [RecordingScheduler.Activate], or on
// [RecordingScheduler.Schedule] of an [scheduler.ActivationAuto] job. FireAt
// is computed as the job's Trigger().Next(clock.Now()); Armed reports whether
// any job was armed. All other port methods are inert no-ops.
//
// Clock is the time source used to resolve the trigger's next occurrence
// (e.g. now+duration for a fixed-delay trigger); when nil it defaults to
// clock.System(). Inject a fake clock to make duration triggers deterministic.
type RecordingScheduler struct {
	Clock  clock.Clock
	FireAt time.Time
	Armed  bool
}

// now resolves the effective clock.
func (s *RecordingScheduler) now() time.Time {
	clk := s.Clock
	if clk == nil {
		clk = clock.System()
	}
	return clk.Now()
}

// record captures j's next fire instant and marks the scheduler as armed.
func (s *RecordingScheduler) record(j scheduler.Job) {
	if next, ok := j.Trigger().Next(s.now()); ok {
		s.FireAt = next
	}
	s.Armed = true
}

// Schedule records an [scheduler.ActivationAuto] job (auto jobs arm on
// Schedule); a Manual job is not recorded — it would only arm via Activate.
// The returned ScheduledJob carries the trigger's computed next occurrence.
// It never runs the job's action.
func (s *RecordingScheduler) Schedule(_ context.Context, j scheduler.Job) (scheduler.ScheduledJob, error) {
	if j == nil {
		return nil, fmt.Errorf("runtimetest: Schedule requires a non-nil Job")
	}
	next, _ := j.Trigger().Next(s.now())
	if j.Activation() != scheduler.ActivationManual {
		s.record(j)
	}
	return scheduler.NewScheduledJob(j, next)
}

// Activate records the job's fire-at time. It never runs the job's action.
func (s *RecordingScheduler) Activate(_ context.Context, j scheduler.ScheduledJob) error {
	if j == nil {
		return fmt.Errorf("runtimetest: Activate requires a non-nil ScheduledJob")
	}
	s.record(j)
	return nil
}

// Deactivate is a no-op.
func (s *RecordingScheduler) Deactivate(context.Context, string) error { return nil }

// Cancel is a no-op.
func (s *RecordingScheduler) Cancel(context.Context, string) error { return nil }

// Scheduled always reports scheduler.ErrJobNotFound — the stub keeps no
// per-job records.
func (s *RecordingScheduler) Scheduled(_ context.Context, id string) (scheduler.ScheduledJob, error) {
	return nil, fmt.Errorf("runtimetest: job %q: %w", id, scheduler.ErrJobNotFound)
}

// List yields nothing.
func (s *RecordingScheduler) List(context.Context) iter.Seq[scheduler.ScheduledJob] {
	return func(func(scheduler.ScheduledJob) bool) {}
}

// FixedJitter is a deterministic JitterSource that always returns the same
// fraction. It satisfies kernel.JitterSource.
type FixedJitter struct{ F float64 }

// Fraction returns the fixed fraction.
func (j FixedJitter) Fraction() float64 { return j.F }
