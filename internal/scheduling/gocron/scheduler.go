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

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/internal/observability"
)

// defaultTimeSkew is the out-of-the-box tolerance for the past-due one-shot
// path. A timer whose fire time is up to 5 minutes in the past fires
// immediately and silently (expected clock skew or brief downtime). If the
// lateness exceeds this value a WARN is logged — the timer still fires
// (never-drop invariant). Override with [WithTimeSkew].
const defaultTimeSkew = 5 * time.Minute

// GocronScheduler is a production kernel.Scheduler backed by gocron v2. It
// shares the engine's clockwork time source so one fake-clock advance drives
// both engine timestamps and timer firing (ADR-0003, ADR-0009).
type GocronScheduler struct {
	sched gocron.Scheduler
	clk   clockwork.Clock

	// timeSkew is the maximum lateness that is accepted silently for a
	// past-due one-shot timer. Lateness beyond this threshold emits a WARN
	// (the timer still fires — no timer is ever dropped).
	timeSkew time.Duration

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
// leader elector (see scheduling/backend/{postgres,mysql}) for multi-replica
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
		"github.com/kartaladev/wrkflw/scheduling",
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

	now := s.clk.Now()
	def, oneShot, err := jobDefinition(trig, now)
	if err != nil {
		return time.Time{}, err
	}

	// Past-due skew check: only applies to one-shot triggers with an absolute
	// fire time that has already elapsed (the branch that resolves to
	// OneTimeJobStartImmediately). Timers are NEVER dropped — within tolerance
	// they fire silently; beyond tolerance they still fire and a WARN is logged.
	if oneShot {
		if at, ok := trig.AbsTime(); ok && !at.After(now) {
			lateness := now.Sub(at)
			if lateness > s.timeSkew {
				s.tel.Logger.Warn("workflow-scheduler: past-due timer exceeds time-skew tolerance; firing immediately",
					"timer_id", timerID,
					"fire_time", at,
					"lateness", lateness,
				)
			}
		}
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
