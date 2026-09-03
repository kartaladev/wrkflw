package runtime

import (
	"time"

	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

const (
	// timerJobKind is the scheduler.JobKind under which the runtime arms
	// process-instance timers (intermediate catches, boundaries, deadlines,
	// reminders, retries). The driver's owned default scheduler registers a
	// [scheduler.JobStore] for this kind (see NewJobStore) so armed timers are
	// rehydrated on start.
	timerJobKind scheduler.JobKind = "wrkflw.timer"

	// startTimerJobKind is the scheduler.JobKind for timer-start events. No
	// JobStore is registered for it: a timer-start has no instance yet and
	// re-derives its arms purely from registered definitions
	// on boot ([ProcessDriver.RehydrateStartTimers]), so its jobs are
	// in-memory-only by design.
	startTimerJobKind scheduler.JobKind = "wrkflw.start-timer"
)

// timerJob is the runtime's concrete [scheduler.Job] for a process-instance
// timer. It is ALWAYS [scheduler.ActivationManual]: durable persistence rides
// the runtime's own jobStore INSIDE the state-commit transaction
// (direct-Save), so the runtime never asks the scheduler to persist — it arms
// post-commit via [scheduler.Scheduler.Activate] only.
type timerJob struct {
	// spec is the typed descriptor of the durable timer row this job mirrors.
	spec kernel.JobSpec
	// trig is spec.Trigger converted to the scheduler's own vocabulary.
	trig scheduler.Trigger
	// fn wraps the engine's timerFireFunc fire callback.
	fn scheduler.JobFunc
	// data carries the timer's identifying fields for observability-minded
	// consumers of the job at fire time.
	data scheduler.DataProvider
}

var _ scheduler.Job = (*timerJob)(nil)

func (j *timerJob) ID() string                           { return j.spec.TimerID }
func (j *timerJob) Kind() scheduler.JobKind              { return timerJobKind }
func (j *timerJob) Activation() scheduler.ActivationType { return scheduler.ActivationManual }
func (j *timerJob) Trigger() scheduler.Trigger           { return j.trig }
func (j *timerJob) Action() scheduler.JobFunc            { return j.fn }
func (j *timerJob) Data() scheduler.DataProvider         { return j.data }

// descriptor returns the typed kernel.JobSpec this job was built from.
func (j *timerJob) descriptor() kernel.JobSpec { return j.spec }

// scheduledTimerJob wraps a timerJob with its next-fire instant, satisfying
// [scheduler.ScheduledJob]. Build it with newScheduledTimerJob.
type scheduledTimerJob struct {
	*timerJob
	nextRun time.Time
}

var _ scheduler.ScheduledJob = (*scheduledTimerJob)(nil)

func (s *scheduledTimerJob) NextRun() time.Time { return s.nextRun }

// newScheduledTimerJob wraps j with NextRun = j.trig.Next(now), which is the
// instant handed to the scheduler at Activate. It is NOT what gets persisted:
// jobStore.Save writes j's descriptor, carrying the authoritative NextRun its
// caller computed.
//
// Next's ok is discarded here because both callers have already refused a
// trigger that reports not-ok — timerJobsFor for a fresh arm, jobStore.Load
// for a rehydrated row. Before that, this line stamped the zero
// time on the not-ok path and the comment here called that path impossible,
// asserting the scheduler would re-validate the trigger at arm time anyway.
// Both were false: the zero instant was persisted inside the commit
// transaction, and the post-commit Activate that would have rejected it runs
// where failure is deliberately WARN-only.
func newScheduledTimerJob(j *timerJob, now time.Time) *scheduledTimerJob {
	next, _ := j.trig.Next(now)
	return &scheduledTimerJob{timerJob: j, nextRun: next}
}
