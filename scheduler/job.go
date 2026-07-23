package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// JobKind names the flavor of work a [Job] performs (e.g. a timer callback vs
// a reminder action). It is opaque to the scheduler: the value is only ever
// compared or logged, never interpreted.
type JobKind string

// ActivationType selects when a scheduled [Job] is armed against the live
// scheduling backend once persisted. See [ActivationAuto] and
// [ActivationManual].
type ActivationType int

const (
	// ActivationAuto arms the job against the scheduling backend as part of
	// persisting it — the default.
	ActivationAuto ActivationType = iota

	// ActivationManual defers arming: the job is persisted but not armed
	// until the owner calls the scheduler's explicit activation entry point.
	// This lets a consumer persist a [Schedule] inside its own transaction
	// and only arm it after that transaction commits, avoiding a fire-before
	// -commit race.
	ActivationManual
)

// DataProvider supplies the data a [Job]'s [JobFunc] reads when it runs.
// Implementations must be safe for concurrent use: a recurring job may be
// running and about to run at the same time (see [WithoutOverrunProtection]).
type DataProvider interface {
	// Get returns the data available to the job at fire time. Implementations
	// that read no external I/O should still honor ctx cancellation if they
	// perform any work; static implementations ignore it.
	Get(ctx context.Context) (map[string]any, error)

	// Static reports whether Get performs no I/O and always returns
	// equivalent data — a hint the caller may use to skip re-fetching on
	// every fire of a recurring job.
	Static() bool
}

// JobFunc is the work a [Job] performs when its [Trigger] fires. data is the
// [Job]'s own [DataProvider], already resolved for this call.
type JobFunc = func(ctx context.Context, data DataProvider) error

// Job is a unit of scheduled work: a [Trigger] describing when it fires, a
// [JobFunc] describing what runs, and a [DataProvider] describing what data
// that func sees. Build one with [NewJob] or [NewJobWithID]; consumers may
// also implement Job directly to plug in their own storage-backed shape.
type Job interface {
	// ID uniquely identifies this job.
	ID() string

	// Kind names the flavor of work this job performs.
	Kind() JobKind

	// Activation reports when this job is armed against the scheduling
	// backend relative to being persisted; see [ActivationAuto] and
	// [ActivationManual].
	Activation() ActivationType

	// Trigger describes when this job fires.
	Trigger() Trigger

	// Action is the work this job performs when its Trigger fires.
	Action() JobFunc

	// Data supplies the data Action sees at fire time.
	Data() DataProvider
}

// ScheduledJob is a [Job] annotated with its next scheduled fire time.
// Build one with [NewScheduledJob].
type ScheduledJob interface {
	Job

	// NextRun reports the next instant this job is due to fire.
	NextRun() time.Time
}

// jobConfig holds the resolved options for [NewJob] / [NewJobWithID].
type jobConfig struct {
	activation ActivationType
	noOverrun  bool
}

// JobOption configures optional [NewJob] / [NewJobWithID] behavior.
type JobOption func(*jobConfig)

// WithManualActivation switches a job's [ActivationType] to
// [ActivationManual] instead of the default [ActivationAuto].
func WithManualActivation() JobOption {
	return func(c *jobConfig) { c.activation = ActivationManual }
}

// WithoutOverrunProtection opts a recurring job out of the default singleton
// mode (production item ③): its fires may overlap. It has no effect on
// one-shot jobs, which are never singleton by default.
func WithoutOverrunProtection() JobOption {
	return func(c *jobConfig) { c.noOverrun = true }
}

// job is the unexported concrete [Job] built by [NewJob] / [NewJobWithID].
type job struct {
	id         string
	kind       JobKind
	activation ActivationType
	trig       Trigger
	fn         JobFunc
	data       DataProvider
	noOverrun  bool
}

var _ Job = (*job)(nil)

func (j *job) ID() string                 { return j.id }
func (j *job) Kind() JobKind              { return j.kind }
func (j *job) Activation() ActivationType { return j.activation }
func (j *job) Trigger() Trigger           { return j.trig }
func (j *job) Action() JobFunc            { return j.fn }
func (j *job) Data() DataProvider         { return j.data }

// singleton reports whether concurrent fires of this job must be serialized
// (production item ③, overrun protection). It defaults to true for jobs
// built from a [Trigger.Recurring] trigger — a slow fire must not overlap
// its own next fire — and is false for one-shot jobs, which have nothing to
// overlap. [WithoutOverrunProtection] forces it to false regardless.
//
// It is deliberately unexported: Job is public API a consumer may implement
// directly, but this flag is an internal signal the in-package façade
// (Tasks 5-11) reads back via a private interface assertion
// (interface{ singleton() bool }) — only code inside package scheduler can
// spell that assertion, since the method name is unexported. Consumer-
// implemented Jobs that don't satisfy it are simply treated as non-singleton
// by the façade.
func (j *job) singleton() bool {
	if j.noOverrun {
		return false
	}
	return j.trig.Recurring()
}

// scheduledJob is the unexported concrete [ScheduledJob] built by
// [NewScheduledJob]. It embeds the wrapped Job so all Job methods (including
// the unexported singleton assertion above) delegate transparently.
type scheduledJob struct {
	Job
	nextRun time.Time
}

var _ ScheduledJob = (*scheduledJob)(nil)

func (s *scheduledJob) NextRun() time.Time { return s.nextRun }

// singleton delegates the overrun-protection flag to the wrapped Job. Interface
// embedding does NOT promote the concrete job's unexported singleton method
// (it is not part of the Job interface's method set), so without this explicit
// delegation the façade's private assertion would silently miss a wrapped
// in-package job's WithoutOverrunProtection setting.
func (s *scheduledJob) singleton() bool { return jobSingleton(s.Job) }

// NewJob builds a [Job] with an auto-generated UUID id. See [NewJobWithID]
// for the validation rules and the meaning of opts.
func NewJob(kind JobKind, trig Trigger, fn JobFunc, data DataProvider, opts ...JobOption) (Job, error) {
	return NewJobWithID(uuid.NewString(), kind, trig, fn, data, opts...)
}

// NewJobWithID builds a [Job] with an explicit id. It errors if id or kind
// is empty, trig is the zero [Trigger] ([Trigger.IsZero]), fn is nil, or
// data is nil. Activation defaults to [ActivationAuto]; apply
// [WithManualActivation] to change it.
func NewJobWithID(id string, kind JobKind, trig Trigger, fn JobFunc, data DataProvider, opts ...JobOption) (Job, error) {
	if id == "" {
		return nil, fmt.Errorf("workflow-scheduler: job id must not be empty")
	}
	if kind == "" {
		return nil, fmt.Errorf("workflow-scheduler: job kind must not be empty")
	}
	if trig.IsZero() {
		return nil, fmt.Errorf("workflow-scheduler: job trigger must not be the zero Trigger")
	}
	if fn == nil {
		return nil, fmt.Errorf("workflow-scheduler: job action func must not be nil")
	}
	if data == nil {
		return nil, fmt.Errorf("workflow-scheduler: job data provider must not be nil")
	}

	cfg := jobConfig{activation: ActivationAuto}
	for _, opt := range opts {
		opt(&cfg)
	}

	return &job{
		id:         id,
		kind:       kind,
		activation: cfg.activation,
		trig:       trig,
		fn:         fn,
		data:       data,
		noOverrun:  cfg.noOverrun,
	}, nil
}

// NewScheduledJob wraps j with a next-fire instant, producing a
// [ScheduledJob]. It errors if j is nil.
func NewScheduledJob(j Job, nextRun time.Time) (ScheduledJob, error) {
	if j == nil {
		return nil, fmt.Errorf("workflow-scheduler: scheduled job requires a non-nil Job")
	}
	return &scheduledJob{Job: j, nextRun: nextRun}, nil
}
