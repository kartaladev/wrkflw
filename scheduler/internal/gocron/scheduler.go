// Package gocron is the concrete gocron v2-backed Scheduler implementation
// (ADR-0009). It is internal: consumers reach it only through the module-root
// scheduler façade. gocron and clockwork are imported here only — never from
// engine/runtime/model code.
package gocron

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/scheduler/internal/obs"
)

// defaultTimeSkew is the out-of-the-box tolerance for the past-due one-shot
// path. A timer whose fire time is up to 5 minutes in the past fires
// immediately and silently (expected clock skew or brief downtime). If the
// lateness exceeds this value a WARN is logged — the timer still fires
// (never-drop invariant). Override with [WithTimeSkew].
const defaultTimeSkew = 5 * time.Minute

// GocronScheduler is the production scheduling engine backed by gocron v2,
// consumed by the parent scheduler façade through ScheduleJob/RemoveJob. It
// shares the engine's clockwork time source so one fake-clock advance drives
// both engine timestamps and timer firing (ADR-0003, ADR-0009).
type GocronScheduler struct {
	sched gocron.Scheduler
	clk   clockwork.Clock

	// loc is the timezone the scheduler resolves calendar at-times and cron
	// expressions against. nil means "unset"; NewGocronScheduler resolves an
	// unset loc to time.UTC (it never falls through to gocron's time.Local
	// default). Set via WithLocation. See ADR-0136.
	loc *time.Location

	// timeSkew is the maximum lateness that is accepted silently for a
	// past-due one-shot timer. Lateness beyond this threshold emits a WARN
	// (the timer still fires — no timer is ever dropped).
	timeSkew time.Duration

	// staged telemetry option values; assembled into tel after all Options
	// have been applied in NewGocronScheduler.
	logOpt obs.Option
	tpOpt  obs.Option
	mpOpt  obs.Option

	tel obs.Telemetry

	// locker, when set, is passed to gocron as a distributed locker so that across
	// replicas only one runs each timer's fire callback (the lock key is the
	// timerID, set via gocron.WithName). nil = no distributed locking.
	locker gocron.Locker

	// elector, when set, is passed to gocron as a distributed elector so that
	// across replicas only the elected leader runs ALL timer fires (single-leader
	// mode). It is the mutually-exclusive alternative to locker (ADR-0059): setting
	// both is a construction error. nil = no leader election.
	elector gocron.Elector

	mu   sync.Mutex
	jobs map[string]uuid.UUID // timerID -> gocron job ID
}

// ErrLockerElectorConflict is returned by NewGocronScheduler when both a Locker
// and an Elector are configured. They are mutually-exclusive distributed modes
// (load-balanced per-timer exclusion vs. single-leader); pick one.
var ErrLockerElectorConflict = errors.New(
	"workflow-scheduler: a distributed locker and elector are mutually exclusive — set only one")

// Option configures a [GocronScheduler].
type Option func(*GocronScheduler)

// WithLogger sets the structured logger used by the scheduler (default:
// [slog.Default]). A nil value is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(s *GocronScheduler) {
		s.logOpt = obs.WithLogger(l)
	}
}

// WithTracerProvider sets the OTel TracerProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no spans in this
// track (API parity only — consistent with the relay and HTTP transport).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *GocronScheduler) {
		s.tpOpt = obs.WithTracerProvider(tp)
	}
}

// WithMeterProvider sets the OTel MeterProvider for the scheduler. Default:
// the OTel global provider. The scheduler emits the
// wrkflw_scheduler_job_runs_total counter and
// wrkflw_scheduler_job_duration_seconds histogram through it, driven by
// gocron's native MonitorStatus hook (ADR-0134 production item ① — see
// monitor.go).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(s *GocronScheduler) {
		s.mpOpt = obs.WithMeterProvider(mp)
	}
}

// WithLocker configures a distributed locker so that, across replicas, only one
// instance runs each timer's fire callback. The lock key is the timerID. A nil
// value is ignored. Pair with the persistence advisory-lock bridge (see
// persistence.NewSchedulerLocker) for multi-replica deployments.
func WithLocker(l gocron.Locker) Option {
	return func(s *GocronScheduler) {
		if l != nil {
			s.locker = l
		}
	}
}

// WithElector configures a distributed elector so that, across replicas, only the
// elected leader runs timer fires (single-leader mode). It is the mutually-
// exclusive alternative to WithLocker (setting both errors at construction — see
// ErrLockerElectorConflict). A nil value is ignored. Pair with a database-backed
// leader elector (see scheduler/backend/{postgres,mysql}) for multi-replica
// deployments.
func WithElector(e gocron.Elector) Option {
	return func(s *GocronScheduler) {
		if e != nil {
			s.elector = e
		}
	}
}

// WithClock sets the [clockwork.Clock] that drives timer scheduling (default:
// [clockwork.NewRealClock]). Pass a fake clock in tests so that a single
// clock.Advance drives both engine timestamps and timer firing (ADR-0003,
// ADR-0069). A nil value is ignored (falls back to the default real clock).
func WithClock(clk clockwork.Clock) Option {
	return func(s *GocronScheduler) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// WithLocation sets the timezone in which the scheduler resolves calendar
// at-times (Daily/Weekly/Monthly) and cron expressions. Default: [time.UTC],
// which matches the pure scheduler.Trigger.Next reference. A nil value is
// ignored (the default UTC is used). The nil-guard matters for two reasons:
// (1) gocron's own default when no location is pinned is time.Local, so an
// unset location must be resolved to UTC before construction; and (2)
// gocron.WithLocation(nil) returns ErrWithLocationNil and would fail scheduler
// construction, so nil must never be forwarded. Pass time.Local for host-local
// resolution, or any named zone. Named zones with DST resolve at-times per that
// zone's DST rules on the live scheduler; the UTC reference does not observe
// DST, so the two diverge across DST boundaries. In a multi-replica deployment
// (WithLocker/WithElector) every replica must use the same location. See
// ADR-0136.
func WithLocation(loc *time.Location) Option {
	return func(s *GocronScheduler) {
		if loc != nil {
			s.loc = loc
		}
	}
}

// WithTimeSkew sets the maximum past-due lateness that is accepted silently
// for a one-shot timer whose absolute fire time has already elapsed at
// schedule time (e.g. after a restart or DB↔process clock skew).
//
// Behaviour:
//   - Lateness ≤ d  → fire immediately, no log output.
//   - Lateness >  d → fire immediately (the timer is NEVER dropped) and emit
//     a WARN via the configured logger with timer_id, fire_time, and lateness.
//
// Default when this option is omitted: [defaultTimeSkew] (5 minutes).
// Pass 0 to warn on any past-due timer, however small.
// Pass a very large value (e.g. math.MaxInt64) to silence the warning entirely.
func WithTimeSkew(d time.Duration) Option {
	return func(s *GocronScheduler) {
		s.timeSkew = d
	}
}

// filterNilOpts returns only the non-nil obs.Option values from opts.
func filterNilOpts(opts ...obs.Option) []obs.Option {
	out := opts[:0]
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}

// NewGocronScheduler constructs and starts a gocron-backed scheduler. Pass
// [WithClock] to drive timer scheduling with a specific [clockwork.Clock]
// (default: [clockwork.NewRealClock]). The caller must Close it to avoid
// leaking gocron's executor goroutine.
func NewGocronScheduler(opts ...Option) (*GocronScheduler, error) {
	s := &GocronScheduler{
		jobs:     make(map[string]uuid.UUID),
		timeSkew: defaultTimeSkew, // sentinel: options override this below
	}
	// Apply options first so locker, elector, clock (and telemetry) are known
	// before the gocron scheduler is constructed.
	for _, o := range opts {
		o(s)
	}

	// Resolve the effective clock: option-provided or real-clock default.
	if s.clk == nil {
		s.clk = clockwork.NewRealClock()
	}

	if s.locker != nil && s.elector != nil {
		return nil, ErrLockerElectorConflict
	}

	// Build the Telemetry value before constructing the gocron engine: the
	// MonitorStatus and EventListeners options wired in below (ADR-0134
	// production item ①) both need the resolved logger/meter, so this must
	// happen ahead of gocron.NewScheduler rather than after gs.Start() as
	// before.
	s.tel = obs.New(
		"github.com/kartaladev/wrkflw/scheduler",
		filterNilOpts(s.logOpt, s.tpOpt, s.mpOpt)...,
	)

	// Resolve the effective location: option-provided or UTC default. This is
	// pinned explicitly so gocron never falls back to its own time.Local
	// default (ADR-0136).
	loc := s.loc
	if loc == nil {
		loc = time.UTC
	}

	gocronOpts := []gocron.SchedulerOption{
		gocron.WithLocation(loc), // ADR-0136: pin location (default UTC)
		gocron.WithClock(s.clk),
		gocron.WithMonitorStatus(newMonitorStatus(s.tel)),
		gocron.WithGlobalJobOptions(gocron.WithEventListeners(
			// AfterJobRunsWithError also fires for a recovered panic: gocron
			// wraps the panic as an error (wrapping gocron.ErrPanicRecovered)
			// and still routes it through this listener in addition to
			// AfterJobRunsWithPanic below.
			gocron.AfterJobRunsWithError(func(jobID uuid.UUID, jobName string, err error) {
				s.tel.Logger.Error("gocron: job run failed",
					"job_id", jobID, "job_name", jobName, "error", err)
			}),
			// AfterJobRunsWithPanic is gocron's signal that a job's task
			// panicked; registering it is also what makes gocron install
			// panic recovery around the task in the first place (see
			// executor.runJob) — without any listener here, a panicking task
			// crashes the executor goroutine instead of being recovered.
			gocron.AfterJobRunsWithPanic(func(jobID uuid.UUID, jobName string, recoverData any) {
				s.tel.Logger.Error("gocron: job run panicked",
					"job_id", jobID, "job_name", jobName,
					"panic", recoverData, "stack", string(debug.Stack()))
			}),
			gocron.AfterLockError(func(jobID uuid.UUID, jobName string, err error) {
				s.tel.Logger.Warn("gocron: distributed lock acquisition failed",
					"job_id", jobID, "job_name", jobName, "error", err)
			}),
		)),
	}
	if s.locker != nil {
		gocronOpts = append(gocronOpts, gocron.WithDistributedLocker(s.locker))
	}
	if s.elector != nil {
		gocronOpts = append(gocronOpts, gocron.WithDistributedElector(s.elector))
	}
	gs, err := gocron.NewScheduler(gocronOpts...)
	if err != nil {
		return nil, err
	}
	gs.Start() // non-blocking
	s.sched = gs

	return s, nil
}

// Cancel removes a pending timer. No-op if the timer is unknown or already fired.
//
// ctx is reserved for future cancellation propagation and is currently unused.
func (s *GocronScheduler) Cancel(_ context.Context, timerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.jobs[timerID]
	if !ok {
		return // unknown id: safe no-op
	}
	delete(s.jobs, timerID)
	if err := s.sched.RemoveJob(id); err != nil && !errors.Is(err, gocron.ErrJobNotFound) {
		s.tel.Logger.Error("gocron: cancel timer failed", "timerID", timerID, "error", err)
	}
}

// NextRun returns the next scheduled fire time of the timer identified by
// timerID. Returns (time.Time{}, false) if the timer is unknown, has already
// fired (one-shot disarmed), or has been cancelled.
func (s *GocronScheduler) NextRun(timerID string) (time.Time, bool) {
	s.mu.Lock()
	id, ok := s.jobs[timerID]
	s.mu.Unlock()
	if !ok {
		return time.Time{}, false
	}

	for _, job := range s.sched.Jobs() {
		if job.ID() == id {
			next, err := job.NextRun()
			if err != nil || next.IsZero() {
				return time.Time{}, false
			}
			return next, true
		}
	}
	return time.Time{}, false
}

// Close shuts gocron down gracefully. The scheduler cannot be reused afterward.
func (s *GocronScheduler) Close() error {
	return s.sched.Shutdown()
}

// CloseWithContext shuts gocron down gracefully, honoring ctx's deadline: it stops
// dispatch immediately (gocron's shutdownCancel fires first) and waits for running
// jobs, returning ctx.Err() if ctx expires first. The scheduler cannot be reused
// afterward. Unlike Close (which uses gocron's internal stop timeout), the caller's
// ctx bounds the wait.
func (s *GocronScheduler) CloseWithContext(ctx context.Context) error {
	return s.sched.ShutdownWithContext(ctx)
}
