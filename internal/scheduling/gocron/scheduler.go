// Package gocron is the concrete gocron v2-backed Scheduler implementation
// (ADR-0009). It is internal: consumers reach it only through the module-root
// scheduling façade. gocron and clockwork are imported here only — never from
// engine/runtime/model code.
package gocron

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
)

// GocronScheduler is a production kernel.Scheduler backed by gocron v2. It
// shares the engine's clockwork time source so one fake-clock advance drives
// both engine timestamps and timer firing (ADR-0003, ADR-0009).
type GocronScheduler struct {
	sched gocron.Scheduler
	clk   clockwork.Clock

	// staged telemetry option values; assembled into tel after all Options
	// have been applied in NewGocronScheduler.
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	tel observability.Telemetry

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
	"workflow-scheduling: a distributed locker and elector are mutually exclusive — set only one")

// Option configures a [GocronScheduler].
type Option func(*GocronScheduler)

// WithLogger sets the structured logger used by the scheduler (default:
// [slog.Default]). A nil value is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(s *GocronScheduler) {
		s.logOpt = observability.WithLogger(l)
	}
}

// WithTracerProvider sets the OTel TracerProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no spans in this
// track (API parity only — consistent with the relay and HTTP transport).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *GocronScheduler) {
		s.tpOpt = observability.WithTracerProvider(tp)
	}
}

// WithMeterProvider sets the OTel MeterProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no metrics in this
// track (API parity only — consistent with the relay and HTTP transport).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(s *GocronScheduler) {
		s.mpOpt = observability.WithMeterProvider(mp)
	}
}

// WithLocker configures a distributed locker so that, across replicas, only one
// instance runs each timer's fire callback. The lock key is the timerID. A nil
// value is ignored. Pair with a Postgres-backed locker (NewPostgresLocker) for
// multi-replica deployments.
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
// ErrLockerElectorConflict). A nil value is ignored. Pair with a Postgres-backed
// elector (NewPostgresElector) for multi-replica deployments.
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

// filterNilOpts returns only the non-nil observability.Option values from opts.
func filterNilOpts(opts ...observability.Option) []observability.Option {
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
		jobs: make(map[string]uuid.UUID),
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

	gocronOpts := []gocron.SchedulerOption{gocron.WithClock(s.clk)}
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

	// Build the Telemetry value after all options have been applied so that any
	// subset of logger/tracer/meter providers can be set independently.
	s.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/scheduling",
		filterNilOpts(s.logOpt, s.tpOpt, s.mpOpt)...,
	)
	return s, nil
}

// Schedule registers a timer according to trig that calls fire each time it
// fires. If a timer with the same timerID already exists it is replaced.
// Returns the authoritative next scheduled run time from gocron (the first
// fire for recurring triggers). A zero time is returned only on error.
//
// ctx is reserved for future cancellation propagation and is currently unused.
func (s *GocronScheduler) Schedule(_ context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.jobs[timerID]; ok {
		_ = s.sched.RemoveJob(existing) // ignore ErrJobNotFound: already fired/pruned
		delete(s.jobs, timerID)
	}

	def, oneShot, err := jobDefinition(trig, s.clk.Now())
	if err != nil {
		return time.Time{}, err
	}

	opts := []gocron.JobOption{
		gocron.WithName(timerID),
		gocron.WithEventListeners(gocron.AfterJobRuns(func(jobID uuid.UUID, _ string) {
			s.mu.Lock()
			if oneShot {
				// One-shots remove themselves from the tracking map after firing.
				if cur, ok := s.jobs[timerID]; ok && cur == jobID {
					delete(s.jobs, timerID)
				}
			}
			s.mu.Unlock()
		})),
	}
	if oneShot {
		opts = append(opts, gocron.WithLimitedRuns(1))
	}

	job, err := s.sched.NewJob(def, gocron.NewTask(fire), opts...)
	if err != nil {
		return time.Time{}, err
	}
	s.jobs[timerID] = job.ID()
	next, _ := job.NextRun()
	return next, nil
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
