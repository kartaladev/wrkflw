package scheduler

import (
	"context"
	"iter"
)

// Scheduler is the port through which jobs are persisted, armed, and
// inspected. The production implementation is [NativeScheduler] (gocron-backed,
// constructed with [NewScheduler]); in-memory test implementations live in the
// processtest package.
//
// Lifecycle note: the port is deliberately job-only. Start/Close belong to the
// concrete implementation ([NativeScheduler.Start], [NativeScheduler.Close])
// because a scheduler's lifetime is owned by whoever constructed it, not by
// the code that schedules jobs on it.
type Scheduler interface {
	// Schedule persists j through the [JobStore] registered for j's kind (when
	// one is registered — an unregistered kind is a supported in-memory-only
	// mode) and, for [ActivationAuto] jobs, arms it. A [ActivationManual] job
	// is persisted only: the scheduler keeps NO record of it until
	// [Scheduler.Activate] — the returned [ScheduledJob] (carrying the
	// computed next run) is for the caller alone.
	Schedule(ctx context.Context, j Job) (ScheduledJob, error)

	// Activate arms j against the live scheduling backend. It is an upsert by
	// job id: activating an id that is already armed replaces the existing
	// registration, never duplicating fires.
	Activate(ctx context.Context, j ScheduledJob) error

	// Deactivate disarms the job with the given id WITHOUT touching its
	// durable record. Unknown id is a no-op returning nil.
	Deactivate(ctx context.Context, id string) error

	// Cancel disarms the job with the given id AND deletes its durable record
	// through its kind's registered [JobStore]. Unknown id is a no-op
	// returning nil.
	Cancel(ctx context.Context, id string) error

	// Scheduled returns the ARMED job with the given id, annotated with its
	// live next-run instant. A job that is not armed — unknown, cancelled,
	// deactivated, a consumed one-shot, or a Manual job that was never
	// activated — reports an error wrapping [ErrJobNotFound].
	Scheduled(ctx context.Context, id string) (ScheduledJob, error)

	// List yields every armed job. Persisted-but-unactivated Manual jobs are
	// not included (they have no scheduler record).
	List(ctx context.Context) iter.Seq[ScheduledJob]
}

// Compile-time contract assertion: the concrete gocron-backed scheduler
// satisfies the port.
var _ Scheduler = (*NativeScheduler)(nil)
