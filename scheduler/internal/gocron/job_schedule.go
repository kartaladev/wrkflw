package gocron

import (
	"context"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

// ScheduleJob registers a Job-shaped scheduling entry: task runs whenever
// trig fires. task's signature is func(context.Context) error and is
// registered with gocron.NewTask(task) — NO extra parameters are supplied
// here, because gocron detects that the task's first parameter is a
// context.Context and automatically injects the job's own per-run,
// shutdown-linked context ahead of any explicit parameters (see gocron's
// NewTask doc and internalJob.addOrUpdateJob's context-injection branch in
// go-co-op/gocron/v2). The ctx parameter ScheduleJob itself accepts is
// reserved for future cancellation propagation and is currently unused.
//
// UPSERT BY ID: any existing registration under id is removed first
// (remove-then-add) so repeated calls under the same id (rehydration,
// re-Activate) always leave exactly one live registration. A past-due
// one-shot fires immediately (never dropped), with a WARN when its lateness
// exceeds the timeskew tolerance, and one-shots carry WithLimitedRuns(1) plus
// self-removal from the tracking map after firing.
//
// When singleton is true AND the job is recurring (not one-shot),
// gocron.WithSingletonMode(gocron.LimitModeReschedule) is applied: a fire
// that is still running when its trigger becomes due again is never run
// concurrently with itself — the overlapping due instant is rescheduled
// (skipped, not queued) rather than run in parallel or piled up. singleton
// is a no-op for one-shot jobs (WithLimitedRuns(1) already guarantees at
// most one run, so there is nothing to overlap) — the option is not even
// appended to gocron in that case.
//
// Returns the live first-run time from gocron. A zero time is returned only
// on error (e.g. an invalid TriggerDef — see jobDefinition/ErrUnsupportedTrigger).
func (s *GocronScheduler) ScheduleJob(_ context.Context, id string, trig TriggerDef, task func(context.Context) error, singleton bool) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.jobs[id]; ok {
		_ = s.sched.RemoveJob(existing) // ignore ErrJobNotFound: already fired/pruned
		delete(s.jobs, id)
	}

	now := s.clk.Now()
	def, oneShot, err := jobDefinition(trig, now)
	if err != nil {
		return time.Time{}, err
	}

	// Past-due skew check: only applies to one-shot triggers with an absolute
	// fire time that has already elapsed. Timers are NEVER dropped — within
	// tolerance they fire silently; beyond tolerance they still fire and a
	// WARN is logged.
	if oneShot {
		if at, ok := trig.AbsTime(); ok && !at.After(now) {
			lateness := now.Sub(at)
			if lateness > s.timeSkew {
				s.tel.Logger.Warn("workflow-scheduler: past-due timer exceeds time-skew tolerance; firing immediately",
					"timer_id", id,
					"fire_time", at,
					"lateness", lateness,
				)
			}
		}
	}

	opts := []gocron.JobOption{
		gocron.WithName(id),
		gocron.WithEventListeners(gocron.AfterJobRuns(func(jobID uuid.UUID, _ string) {
			s.mu.Lock()
			if oneShot {
				// One-shots remove themselves from the tracking map after firing.
				if cur, ok := s.jobs[id]; ok && cur == jobID {
					delete(s.jobs, id)
				}
			}
			s.mu.Unlock()
		})),
	}
	if oneShot {
		opts = append(opts, gocron.WithLimitedRuns(1))
	}
	if singleton && !oneShot {
		opts = append(opts, gocron.WithSingletonMode(gocron.LimitModeReschedule))
	}

	job, err := s.sched.NewJob(def, gocron.NewTask(task), opts...)
	if err != nil {
		return time.Time{}, err
	}
	s.jobs[id] = job.ID()
	next, _ := job.NextRun()
	return next, nil
}

// RemoveJob removes a job registered via ScheduleJob, keyed by the same id
// ScheduleJob was called with. It is a no-op if id is unknown or the job has
// already fired.
//
// RemoveJob is a Job-vocabulary alias for [GocronScheduler.Cancel]: both
// operate on the identical id -> gocron-job-ID tracking map, so there is
// exactly one removal code path (Cancel's) underneath. RemoveJob exists so
// callers working in ScheduleJob's Job vocabulary aren't forced to reach for
// a "Cancel a timer" name; pick whichever name reads better at the call site.
func (s *GocronScheduler) RemoveJob(ctx context.Context, id string) {
	s.Cancel(ctx, id)
}
