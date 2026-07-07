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
	"sync"
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

// ErrSchedulerClosed is returned by [Scheduler.Start] and [Scheduler.Schedule]
// after the scheduler has been closed (via [Scheduler.Close] or cancellation of
// the context passed to [Scheduler.Start]). A closed scheduler cannot be reused.
var ErrSchedulerClosed = errors.New("workflow-scheduling: scheduler is closed")

// Scheduler is the production, gocron-backed [kernel.Scheduler]. Construct it
// with [NewScheduler]; supply the same [clockwork.Clock] instance used to build
// the runtime via [WithClock] so one fake-clock advance drives both
// engine timestamps and timer firing under test (ADR-0003). When the clock
// option is omitted, a real clock is used.
//
// Lifecycle (ADR-0102): [NewScheduler] is goroutine-free — the underlying gocron
// scheduler (and its background goroutine) is not created until the scheduler is
// started. Call [Scheduler.Start] with a long-lived context to start it
// explicitly; cancelling that context stops the scheduler. As a convenience the
// scheduler also auto-starts (with a background context) on the first
// [Scheduler.Schedule] call, so timer-only consumers need no explicit Start.
// Call [Scheduler.Close] on shutdown to release the gocron goroutine.
type Scheduler struct {
	// cfg holds the resolved façade options; the underlying gocron scheduler is
	// built from it lazily on first start.
	cfg config

	// mu guards impl, closed, and stopCh across concurrent Start/Schedule/Close.
	mu sync.Mutex
	// impl is the underlying gocron scheduler. nil before the first start and
	// again after Close.
	impl *gocronsched.GocronScheduler
	// closed is set by Close (or context cancellation); a closed scheduler is
	// terminal and cannot be restarted.
	closed bool
	// stopCh, when non-nil, terminates the context-cancellation watcher started
	// by an explicit Start(ctx). Close closes it so the watcher exits.
	stopCh chan struct{}
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

// WithClock sets the [clockwork.Clock] that drives timer scheduling
// (default: [clockwork.NewRealClock]). Pass a fake clock in tests so that a
// single clock.Advance drives both engine timestamps and timer firing (ADR-0003,
// ADR-0069). A nil value is ignored (falls back to the default real clock).
func WithClock(clk clockwork.Clock) Option {
	return func(c *config) {
		if clk != nil {
			c.clk = clk
		}
	}
}

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

// NewScheduler constructs a gocron-backed [Scheduler]. Pass [WithClock]
// to drive timer scheduling with a specific [clockwork.Clock] (default:
// [clockwork.NewRealClock]).
//
// Construction is goroutine-free: the underlying gocron scheduler is not created
// until the scheduler is started (ADR-0102). Start it explicitly with
// [Scheduler.Start] to bind its lifetime to a context, or simply call
// [Scheduler.Schedule] — the first Schedule auto-starts it with a background
// context. Either way, call [Scheduler.Close] on shutdown to release the gocron
// goroutine.
//
// With no [WithLocker] / [WithElector] option the scheduler runs in single-node
// mode: every armed timer fires locally. [WithLocker] and [WithElector] are
// mutually exclusive; requesting both returns [ErrTimerLockElectorConflict].
func NewScheduler(opts ...Option) (*Scheduler, error) {
	cfg := config{}
	for _, o := range opts {
		o(&cfg)
	}

	// Mutual-exclusion: at most one multi-replica mode may be active. Validated
	// eagerly at construction so misconfiguration fails fast, before any start.
	if cfg.locker != nil && cfg.elector != nil {
		return nil, ErrTimerLockElectorConflict
	}

	return &Scheduler{cfg: cfg}, nil
}

// internalOpts builds the internal gocron options from the resolved façade
// config. The effective clock is resolved here (option-provided or real-clock
// default) so a fake clock supplied via WithClock drives gocron.
func (s *Scheduler) internalOpts() []gocronsched.Option {
	clk := s.cfg.clk
	if clk == nil {
		clk = clockwork.NewRealClock()
	}

	opts := []gocronsched.Option{gocronsched.WithClock(clk)}
	if s.cfg.logger != nil {
		opts = append(opts, gocronsched.WithLogger(s.cfg.logger))
	}
	if s.cfg.tp != nil {
		opts = append(opts, gocronsched.WithTracerProvider(s.cfg.tp))
	}
	if s.cfg.mp != nil {
		opts = append(opts, gocronsched.WithMeterProvider(s.cfg.mp))
	}
	if s.cfg.timeSkew != nil {
		opts = append(opts, gocronsched.WithTimeSkew(*s.cfg.timeSkew))
	}
	if s.cfg.locker != nil {
		opts = append(opts, gocronsched.WithLocker(gocronsched.AdaptLocker(neutralLockerBridge{s.cfg.locker})))
	}
	if s.cfg.elector != nil {
		opts = append(opts, gocronsched.WithElector(gocronsched.AdaptElector(s.cfg.elector)))
	}
	return opts
}

// Start starts the underlying gocron scheduler if it is not already running,
// creating its background goroutine. Cancelling ctx stops the scheduler (it is
// closed as if [Scheduler.Close] were called), tying the scheduler's lifetime to
// ctx. Start is idempotent: calling it on an already-started scheduler is a
// no-op returning nil. It returns [ErrSchedulerClosed] if the scheduler has
// already been closed.
//
// Passing a non-cancellable context (e.g. [context.Background]) starts the
// scheduler without a cancellation watcher; use [Scheduler.Close] to stop it.
func (s *Scheduler) Start(ctx context.Context) error {
	_, err := s.ensureStarted(ctx)
	return err
}

// ensureStarted lazily creates and starts the underlying gocron scheduler,
// returning it. It is called by Start (with the caller's context) and by
// Schedule (with a background context) so a timer-only consumer needs no explicit
// Start. Subsequent calls return the already-running scheduler without touching
// the existing cancellation watcher.
func (s *Scheduler) ensureStarted(ctx context.Context) (*gocronsched.GocronScheduler, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrSchedulerClosed
	}
	if s.impl != nil {
		// Already running. If this is an explicit Start(ctx) carrying a
		// cancellable context and no cancellation watcher is installed yet (e.g. a
		// prior Schedule auto-started the scheduler with a background context),
		// install the watcher now so cancelling ctx still stops the scheduler, as
		// Start documents. Without this, a timer armed before Start would leave
		// Start's ctx binding silently unhonoured.
		if s.stopCh == nil && ctx != nil {
			if done := ctx.Done(); done != nil {
				stop := make(chan struct{})
				s.stopCh = stop
				go s.watchContext(done, stop)
			}
		}
		return s.impl, nil
	}

	impl, err := gocronsched.NewGocronScheduler(s.internalOpts()...)
	if err != nil {
		return nil, err
	}
	s.impl = impl

	// Wire ctx cancellation to Close only when ctx can actually be cancelled, so
	// a background-context auto-start spawns no watcher goroutine.
	if ctx != nil {
		if done := ctx.Done(); done != nil {
			stop := make(chan struct{})
			s.stopCh = stop
			go s.watchContext(done, stop)
		}
	}
	return impl, nil
}

// watchContext closes the scheduler when the start context is cancelled, or
// exits quietly when Close closes stop first.
func (s *Scheduler) watchContext(done <-chan struct{}, stop <-chan struct{}) {
	select {
	case <-done:
		_ = s.Close()
	case <-stop:
	}
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
//
// Schedule auto-starts the scheduler (with a background context) on first use, so
// an explicit [Scheduler.Start] is optional. It returns [ErrSchedulerClosed] if
// the scheduler has been closed.
func (s *Scheduler) Schedule(ctx context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (time.Time, error) {
	impl, err := s.ensureStarted(context.Background())
	if err != nil {
		return time.Time{}, err
	}
	return impl.Schedule(ctx, timerID, trig, fire)
}

// Cancel removes a pending timer. No-op if the timer is unknown, has already
// fired, or the scheduler has not been started.
func (s *Scheduler) Cancel(ctx context.Context, timerID string) {
	s.mu.Lock()
	impl := s.impl
	s.mu.Unlock()
	if impl == nil {
		return
	}
	impl.Cancel(ctx, timerID)
}

// NextRun returns the next scheduled run time of the timer identified by timerID
// and true, or the zero time and false when no such timer is pending or the
// scheduler has not been started.
func (s *Scheduler) NextRun(timerID string) (time.Time, bool) {
	s.mu.Lock()
	impl := s.impl
	s.mu.Unlock()
	if impl == nil {
		return time.Time{}, false
	}
	return impl.NextRun(timerID)
}

// Close shuts the underlying gocron scheduler down gracefully and stops any
// context-cancellation watcher started by [Scheduler.Start]. If the configured
// [Elector] also implements [io.Closer] (e.g. a backend elector holding a
// dedicated database connection), it is closed as a convenience — its error is
// joined with the scheduler's. Close is idempotent and safe to call on a
// never-started scheduler; the scheduler cannot be reused after this call.
func (s *Scheduler) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	impl := s.impl
	s.impl = nil
	stop := s.stopCh
	s.stopCh = nil
	elector := s.cfg.elector
	s.mu.Unlock()

	if stop != nil {
		close(stop) // wake the Start watcher so it exits
	}
	var err error
	if impl != nil {
		err = impl.Close()
	}
	if closer, ok := elector.(io.Closer); ok {
		err = errors.Join(err, closer.Close())
	}
	return err
}
