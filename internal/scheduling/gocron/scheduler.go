// Package gocron is the concrete gocron v2-backed Scheduler implementation
// (ADR-0009). It is internal: consumers reach it only through the module-root
// scheduling façade. gocron and clockwork are imported here only — never from
// engine/runtime/model code.
package gocron

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/internal/observability"
)

// GocronScheduler is a production runtime.Scheduler backed by gocron v2. It
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

	mu   sync.Mutex
	jobs map[string]uuid.UUID // timerID -> gocron job ID
}

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
// track (API parity only — consistent with relay/rest/grpc).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *GocronScheduler) {
		s.tpOpt = observability.WithTracerProvider(tp)
	}
}

// WithMeterProvider sets the OTel MeterProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no metrics in this
// track (API parity only — consistent with relay/rest/grpc).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(s *GocronScheduler) {
		s.mpOpt = observability.WithMeterProvider(mp)
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

// NewGocronScheduler constructs and starts a gocron-backed scheduler driven by
// clk. The caller must Close it to avoid leaking gocron's executor goroutine.
func NewGocronScheduler(clk clockwork.Clock, opts ...Option) (*GocronScheduler, error) {
	gs, err := gocron.NewScheduler(gocron.WithClock(clk))
	if err != nil {
		return nil, err
	}
	gs.Start() // non-blocking
	s := &GocronScheduler{
		sched: gs,
		clk:   clk,
		jobs:  make(map[string]uuid.UUID),
	}
	for _, o := range opts {
		o(s)
	}
	// Build the Telemetry value after all options have been applied so that any
	// subset of logger/tracer/meter providers can be set independently.
	s.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/scheduling",
		filterNilOpts(s.logOpt, s.tpOpt, s.mpOpt)...,
	)
	return s, nil
}

// Schedule registers a one-time timer that calls fire at or after fireAt. If a
// timer with the same timerID already exists it is replaced. Best-effort: a
// gocron job-creation error is logged and the timer is not armed.
//
// If fireAt is not in the future per the clock, the timer fires immediately.
func (s *GocronScheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.jobs[timerID]; ok {
		_ = s.sched.RemoveJob(existing) // ignore ErrJobNotFound: already fired/pruned
		delete(s.jobs, timerID)
	}

	var timing gocron.OneTimeJobStartAtOption
	if fireAt.After(s.clk.Now()) {
		timing = gocron.OneTimeJobStartDateTime(fireAt)
	} else {
		// fireAt is in the past or exactly now; fire immediately.
		timing = gocron.OneTimeJobStartImmediately()
	}

	job, err := s.sched.NewJob(
		gocron.OneTimeJob(timing),
		gocron.NewTask(fire),
		gocron.WithEventListeners(gocron.AfterJobRuns(func(jobID uuid.UUID, _ string) {
			s.mu.Lock()
			if cur, ok := s.jobs[timerID]; ok && cur == jobID {
				delete(s.jobs, timerID)
			}
			s.mu.Unlock()
		})),
	)
	if err != nil {
		s.tel.Logger.Error("gocron: schedule timer failed", "timerID", timerID, "error", err)
		return
	}
	s.jobs[timerID] = job.ID()
}

// Cancel removes a pending timer. No-op if the timer is unknown or already fired.
func (s *GocronScheduler) Cancel(timerID string) {
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

// Close shuts gocron down gracefully. The scheduler cannot be reused afterward.
func (s *GocronScheduler) Close() error {
	return s.sched.Shutdown()
}
