package gocron

import (
	"context"
	"fmt"
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
// self-removal from the tracking map after firing. "Past-due" covers BOTH
// one-shot forms — an elapsed absolute time (At) and a non-positive duration
// (After(0), After(-d)) — because oneShotFireTime resolves the due instant
// for both (backlog 49; before that, the duration form was refused by gocron
// with a raw "gocron: OneTimeJob: start must not be in the past" that escaped
// this API unwrapped).
//
// Errors returned from here always carry this engine's "workflow-scheduler:"
// prefix, including the ones gocron's own NewJob raises for a well-formed
// trigger kind carrying a nonsense value (Every(0), a malformed cron
// expression); the vendor cause stays reachable through errors.Is/As.
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
// Returns the live first-run time from gocron, EXCEPT for a past-due
// one-shot: that case fires on gocron's own goroutine immediately upon
// registration and may already have retired by the time gocron is asked, so
// the fire time is reported deterministically as the clock's current time
// (the now captured above) rather than raced out of gocron — see the
// fireImmediately comment below. A zero time is returned only alongside a
// non-nil error (e.g. an invalid TriggerDef — see
// jobDefinition/ErrUnsupportedTrigger — or a call that observes closed
// already true — see ErrSchedulerClosed). Verified: before the closed-state
// guard was added, this was false — ScheduleJob(Every(time.Hour), …) called
// after Close returned (zero, nil) for a job gocron silently accepted but
// will never run.
//
// ⚠ closed does NOT close every window, only the one above: Close/
// CloseWithContext set closed=true under s.mu but call gocron's Shutdown
// OUTSIDE s.mu (see their doc comments). If a ScheduleJob call acquires
// s.mu first, Close blocks on s.mu for the whole ScheduleJob body — closed
// is observed false throughout, the job registers successfully (including
// taking the fireImmediately branch for a past-due one-shot), and
// ScheduleJob returns a non-zero next-run with a NIL error. Shutdown then
// runs immediately after ScheduleJob releases s.mu and can retire the
// underlying gocron scheduler before that job's own goroutine has actually
// fired it, orphaning a call this doc comment would otherwise describe as
// having succeeded. Closing this window is non-trivial: holding Shutdown
// under s.mu risks a deadlock against the AfterJobRuns listener above (which
// also takes s.mu), and re-checking closed after NewJob does not help
// either, since ScheduleJob holds s.mu for its entire body so closed cannot
// change mid-call. See backlog 50 in docs/plans/HANDOVER.md.
func (s *GocronScheduler) ScheduleJob(_ context.Context, id string, trig TriggerDef, task func(context.Context) error, singleton bool) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return time.Time{}, fmt.Errorf("workflow-scheduler: ScheduleJob %q: %w", id, ErrSchedulerClosed)
	}

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
	//
	// fireImmediately mirrors jobDefinition's own past-due branch
	// (!at.After(now)): gocron.OneTimeJobStartImmediately() starts the job on
	// its own goroutine as soon as NewJob registers it below, and with
	// WithLimitedRuns(1) also set, the job can fire and self-retire from
	// gocron's bookkeeping before this function reaches job.NextRun() — at
	// which point NextRun() truthfully reports the zero time ("no next run"),
	// and asking gocron would silently return a zero next-run alongside a nil
	// error for a timer that fired correctly. next is therefore computed
	// deterministically for this case instead of raced out of gocron.
	fireImmediately := false
	if oneShot {
		if at, ok := oneShotFireTime(trig, now); ok && !at.After(now) {
			fireImmediately = true
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
		// jobDefinition validates only the trigger KIND; a well-formed kind
		// carrying a nonsense value (Every(0), a malformed cron expression) is
		// caught here, by gocron. Wrap so no raw vendor string ever reaches a
		// caller as if it were this engine's own vocabulary — the cause stays
		// reachable through errors.Is/As.
		return time.Time{}, fmt.Errorf("workflow-scheduler: ScheduleJob %q: %w", id, err)
	}
	s.jobs[id] = job.ID()

	// The fire-immediately case is reported deterministically as now — the
	// same instant captured above when the past-due decision was made —
	// rather than asked of gocron (see the fireImmediately comment above):
	// the job fires at ~now, not at the past at — reporting the elapsed at
	// would claim a fire time that has already passed. This is unconditional
	// (not a fallback for when NextRun() happens to come back zero) because
	// the race window exists on every call, just with variable width;
	// patching around an observed zero would still leave the same race with
	// a smaller — but nonzero — chance of an incorrect answer.
	if fireImmediately {
		return now, nil
	}

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
