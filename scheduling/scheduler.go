// Package scheduling is the consumer-facing façade over the internal gocron
// scheduler (ADR-0008, ADR-0009, ADR-0102). Consumers import only this root
// package; the concrete gocron implementation stays in internal/scheduling/gocron
// so the vendor dependency is not visible to the library API surface.
//
// The façade is neutral of any database driver: multi-replica coordination is
// supplied through the [Locker] and [Elector] interfaces (see [WithLocker] /
// [WithElector]). Database-backed implementations live in
// scheduling/backend/{postgres,mysql} and the persistence-lock bridge, keeping
// pgx / database/sql out of this package entirely.
package scheduling

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	gocronsched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// ErrTimerLockElectorConflict is returned by [NewScheduler] when both a [Locker]
// and an [Elector] are configured. Load-balanced per-timer exclusion
// ([WithLocker]) and single-leader firing ([WithElector]) are mutually exclusive
// (ADR-0059, ADR-0102); pick exactly one.
var ErrTimerLockElectorConflict = errors.New(
	"workflow-scheduling: a Locker and an Elector are mutually exclusive — set only one")

// Scheduler is the production, gocron-backed [kernel.Scheduler]. Construct it
// with [NewScheduler]; supply the same [clockwork.Clock] instance used to build
// the runtime via [WithSchedulerClock] so one fake-clock advance drives both
// engine timestamps and timer firing under test (ADR-0003). When the clock
// option is omitted, a real clock is used. Call [Close] on shutdown to release
// the underlying gocron goroutine.
type Scheduler struct {
	impl *gocronsched.GocronScheduler

	// elector, when single-leader mode is enabled via WithElector, holds the
	// neutral leader elector. If it also implements io.Closer, Close closes it
	// alongside the gocron scheduler as a convenience (ADR-0102). nil when not in
	// elector mode.
	elector Elector
}

// Compile-time contract assertions.
var (
	_ kernel.Scheduler = (*Scheduler)(nil)
	_ io.Closer        = (*Scheduler)(nil)
)

// config holds façade-level options. It carries no database-driver state — only
// the neutral locker/elector seams and observability wiring.
type config struct {
	clk      clockwork.Clock
	logger   *slog.Logger
	tp       trace.TracerProvider
	mp       metric.MeterProvider
	locker   Locker
	elector  Elector
	timeSkew *time.Duration // nil = use internal default (5 minutes)
}

// Option configures a [Scheduler].
type Option func(*config)

// WithSchedulerClock sets the [clockwork.Clock] that drives timer scheduling
// (default: [clockwork.NewRealClock]). Pass a fake clock in tests so that a
// single clock.Advance drives both engine timestamps and timer firing (ADR-0003,
// ADR-0069). A nil value is ignored (falls back to the default real clock).
func WithSchedulerClock(clk clockwork.Clock) Option {
	return func(c *config) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// WithClock is an alias for [WithSchedulerClock] — it sets the [clockwork.Clock]
// that drives timer scheduling. A nil value is ignored.
func WithClock(clk clockwork.Clock) Option { return WithSchedulerClock(clk) }

// WithLogger sets the scheduler's structured logger (default: [slog.Default]).
// A nil value is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithTracerProvider sets the OTel TracerProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no spans in this
// track (API parity only — consistent with the relay and HTTP transport). A nil value is
// ignored.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) {
		if tp != nil {
			c.tp = tp
		}
	}
}

// WithMeterProvider sets the OTel MeterProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no metrics in this
// track (API parity only — consistent with the relay and HTTP transport). A nil value is
// ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) {
		if mp != nil {
			c.mp = mp
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
// Default when this option is omitted: 5 minutes. Pass 0 to warn on any
// past-due timer; pass a very large value to effectively silence the warning.
func WithTimeSkew(d time.Duration) Option {
	return func(c *config) {
		c.timeSkew = &d
	}
}

// NewScheduler constructs and starts a gocron-backed [Scheduler]. Pass
// [WithSchedulerClock] to drive timer scheduling with a specific
// [clockwork.Clock] (default: [clockwork.NewRealClock]). The returned
// scheduler must be closed via [Scheduler.Close] when the application shuts down.
//
// With no [WithLocker] / [WithElector] option the scheduler runs in single-node
// mode: every armed timer fires locally. [WithLocker] and [WithElector] are
// mutually exclusive; requesting both returns [ErrTimerLockElectorConflict].
func NewScheduler(opts ...Option) (*Scheduler, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	// Resolve the effective clock once: option-provided or real-clock default.
	clk := cfg.clk
	if clk == nil {
		clk = clockwork.NewRealClock()
	}

	// Mutual-exclusion: at most one multi-replica mode may be active.
	if cfg.locker != nil && cfg.elector != nil {
		return nil, ErrTimerLockElectorConflict
	}

	internalOpts := []gocronsched.Option{gocronsched.WithClock(clk)}
	if cfg.logger != nil {
		internalOpts = append(internalOpts, gocronsched.WithLogger(cfg.logger))
	}
	if cfg.tp != nil {
		internalOpts = append(internalOpts, gocronsched.WithTracerProvider(cfg.tp))
	}
	if cfg.mp != nil {
		internalOpts = append(internalOpts, gocronsched.WithMeterProvider(cfg.mp))
	}
	if cfg.timeSkew != nil {
		internalOpts = append(internalOpts, gocronsched.WithTimeSkew(*cfg.timeSkew))
	}
	if cfg.locker != nil {
		internalOpts = append(internalOpts, gocronsched.WithLocker(gocronsched.AdaptLocker(neutralLockerBridge{cfg.locker})))
	}
	if cfg.elector != nil {
		internalOpts = append(internalOpts, gocronsched.WithElector(gocronsched.AdaptElector(cfg.elector)))
	}

	impl, err := gocronsched.NewGocronScheduler(internalOpts...)
	if err != nil {
		return nil, err
	}
	return &Scheduler{impl: impl, elector: cfg.elector}, nil
}

// neutralLockerBridge adapts the public scheduling.Locker (whose Lock returns a
// scheduling.Lock) to the internal gocronsched.NeutralLocker shape (whose Lock
// returns a gocronsched.NeutralLock). The two interfaces are structurally
// identical apart from the named return type, so this one-hop bridge lets the
// façade pass its neutral Locker to AdaptLocker without importing gocron.
type neutralLockerBridge struct{ inner Locker }

func (b neutralLockerBridge) Lock(ctx context.Context, key string) (gocronsched.NeutralLock, error) {
	l, err := b.inner.Lock(ctx, key)
	if err != nil {
		return nil, err
	}
	return l, nil // scheduling.Lock satisfies gocronsched.NeutralLock structurally
}

// Schedule registers a timer identified by timerID whose firing schedule is
// described by trig, invoking fire when it becomes due. It returns the next
// computed run time (the first fire for recurring triggers), or an error if the
// trigger kind cannot be honoured. If a timer with the same timerID already
// exists it is replaced.
func (s *Scheduler) Schedule(ctx context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (time.Time, error) {
	return s.impl.Schedule(ctx, timerID, trig, fire)
}

// Cancel removes a pending timer. No-op if the timer is unknown or has already
// fired.
func (s *Scheduler) Cancel(ctx context.Context, timerID string) {
	s.impl.Cancel(ctx, timerID)
}

// NextRun returns the next scheduled run time of the timer identified by timerID
// and true, or the zero time and false when no such timer is pending.
func (s *Scheduler) NextRun(timerID string) (time.Time, bool) {
	return s.impl.NextRun(timerID)
}

// Close shuts the underlying gocron scheduler down gracefully. If the configured
// [Elector] also implements [io.Closer] (e.g. a backend elector holding a
// dedicated database connection), it is closed as a convenience — its error is
// joined with the scheduler's. The scheduler cannot be reused after this call.
func (s *Scheduler) Close() error {
	err := s.impl.Close()
	if closer, ok := s.elector.(io.Closer); ok {
		err = errors.Join(err, closer.Close())
	}
	return err
}
