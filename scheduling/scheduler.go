// Package scheduling is the consumer-facing façade over the internal gocron
// scheduler (ADR-0008, ADR-0009). Consumers import only this root package;
// the concrete gocron implementation stays in internal/scheduling/gocron so
// the vendor dependency is not visible to the library API surface.
package scheduling

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	gocronsched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ErrTimerLockElectorConflict is returned by [NewScheduler] when both
// [WithDistributedTimerLock] and [WithTimerElector] are configured. The two are
// mutually-exclusive multi-replica modes — load-balanced per-timer exclusion vs.
// single-leader firing (ADR-0050, ADR-0059); pick one.
var ErrTimerLockElectorConflict = errors.New(
	"workflow-scheduling: WithDistributedTimerLock and WithTimerElector are mutually exclusive — set only one")

// Scheduler is the production, gocron-backed [runtime.Scheduler]. Construct it
// with [NewScheduler], passing the same [clockwork.Clock] instance used to
// build the runtime so one fake-clock advance drives both engine timestamps and
// timer firing under test (ADR-0003). Call [Close] on shutdown to release the
// underlying gocron goroutine.
type Scheduler struct {
	impl *gocronsched.GocronScheduler

	// elector, when single-leader mode is enabled via WithTimerElector, holds the
	// Postgres-backed leader elector. Close releases it (and its dedicated pooled
	// connection) alongside the gocron scheduler. nil when not in elector mode.
	elector *gocronsched.PostgresElector
}

// Compile-time contract assertions.
var (
	_ runtime.Scheduler = (*Scheduler)(nil)
	_ io.Closer         = (*Scheduler)(nil)
)

// config holds façade-level options.
type config struct {
	clk    clockwork.Clock
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
	pool   *pgxpool.Pool

	// electorEnabled and electorPool/electorOpts capture a WithTimerElector
	// request; the elector is constructed in NewScheduler so its dedicated
	// connection is tied to the Scheduler's lifetime.
	electorEnabled bool
	electorPool    *pgxpool.Pool
	electorOpts    []gocronsched.ElectorOption
}

// Option configures a [Scheduler].
type Option func(*config)

// ElectorOption configures the leader elector created by [WithTimerElector].
type ElectorOption func(*config)

// WithElectorKey overrides the leader-lock key used by [WithTimerElector]
// (default: a fixed well-known constant). Give each independent engine sharing one
// database a distinct key so their leader elections do not contend. An empty value
// is ignored.
func WithElectorKey(key string) ElectorOption {
	return func(c *config) {
		if key != "" {
			c.electorOpts = append(c.electorOpts, gocronsched.WithElectorKey(key))
		}
	}
}

// WithElectorHeartbeatInterval overrides how often the elected leader re-validates
// its dedicated connection (default: an internal sane value, currently 5s). It
// bounds the residual split-brain window to at most one interval (ADR-0061): if the
// leader's connection is severed server-side the heartbeat catches it within one
// interval and the leader steps down so a follower can take over. A non-positive
// value is ignored.
func WithElectorHeartbeatInterval(d time.Duration) ElectorOption {
	return func(c *config) {
		if d > 0 {
			c.electorOpts = append(c.electorOpts, gocronsched.WithHeartbeatInterval(d))
		}
	}
}

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
// track (API parity only — consistent with relay/rest/grpc). A nil value is
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
// track (API parity only — consistent with relay/rest/grpc). A nil value is
// ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) {
		if mp != nil {
			c.mp = mp
		}
	}
}

// WithDistributedTimerLock enables multi-replica timer exclusivity backed by
// Postgres advisory locks (the same database the engine persists to). When set,
// many replicas may arm the same timer but only one runs its fire callback per
// firing — removing the steady-state N×-replica redundant Deliver storm. The
// engine's version-CAS plus in-tx timer-row deletion (ADR-0027) remain the
// exactly-once backstop. A nil pool is ignored. See ADR-0050.
func WithDistributedTimerLock(pool *pgxpool.Pool) Option {
	return func(c *config) {
		if pool != nil {
			c.pool = pool
		}
	}
}

// WithTimerElector enables multi-replica timer firing in single-leader mode,
// backed by a Postgres leader advisory lock (the same database the engine persists
// to). Exactly one replica is elected leader and runs ALL timer fires; the others
// skip. When the leader dies its connection drops and Postgres releases the lock,
// so a follower is elected on its next attempt — natural failover with no lease
// loop. The engine's version-CAS plus in-tx timer-row deletion (ADR-0027) remain
// the exactly-once backstop.
//
// This is the single-leader ALTERNATIVE to [WithDistributedTimerLock]'s load-
// balanced per-timer exclusion; the two are mutually exclusive (requesting both
// returns [ErrTimerLockElectorConflict]). Pass [WithElectorKey] to scope leadership
// when several independent engines share one database. A nil pool is ignored. The
// elector is released by [Scheduler.Close]. See ADR-0059.
func WithTimerElector(pool *pgxpool.Pool, opts ...ElectorOption) Option {
	return func(c *config) {
		if pool == nil {
			return
		}
		c.electorEnabled = true
		c.electorPool = pool
		for _, o := range opts {
			o(c)
		}
	}
}

// NewScheduler constructs and starts a gocron-backed [Scheduler]. Pass
// [WithSchedulerClock] to drive timer scheduling with a specific
// [clockwork.Clock] (default: [clockwork.NewRealClock]). The returned
// scheduler must be closed via [Scheduler.Close] when the application shuts down.
//
// [WithDistributedTimerLock] and [WithTimerElector] are mutually exclusive;
// requesting both returns [ErrTimerLockElectorConflict].
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

	if cfg.pool != nil && cfg.electorEnabled {
		return nil, ErrTimerLockElectorConflict
	}

	var internalOpts []gocronsched.Option
	// Always pass the resolved clock to the internal adapter.
	internalOpts = append(internalOpts, gocronsched.WithClock(clk))
	if cfg.logger != nil {
		internalOpts = append(internalOpts, gocronsched.WithLogger(cfg.logger))
	}
	if cfg.tp != nil {
		internalOpts = append(internalOpts, gocronsched.WithTracerProvider(cfg.tp))
	}
	if cfg.mp != nil {
		internalOpts = append(internalOpts, gocronsched.WithMeterProvider(cfg.mp))
	}
	if cfg.pool != nil {
		internalOpts = append(internalOpts, gocronsched.WithLocker(gocronsched.NewPostgresLocker(cfg.pool)))
	}

	// Construct the leader elector (single-leader mode) before the scheduler so its
	// dedicated connection is owned for the Scheduler's lifetime and released by Close.
	var elector *gocronsched.PostgresElector
	if cfg.electorEnabled {
		// Share the resolved clock so the leadership heartbeat is driven by the same
		// time source as timer firing (ADR-0003, ADR-0069). Prepend so a caller-supplied
		// elector clock option (in cfg.electorOpts) still wins — applied after this one.
		electorOpts := append([]gocronsched.ElectorOption{gocronsched.WithElectorClock(clk)}, cfg.electorOpts...)
		e, err := gocronsched.NewPostgresElector(context.Background(), cfg.electorPool, electorOpts...)
		if err != nil {
			return nil, err
		}
		elector = e
		internalOpts = append(internalOpts, gocronsched.WithElector(elector))
	}

	impl, err := gocronsched.NewGocronScheduler(internalOpts...)
	if err != nil {
		if elector != nil {
			_ = elector.Close()
		}
		return nil, err
	}
	return &Scheduler{impl: impl, elector: elector}, nil
}

// Schedule registers a one-time timer identified by timerID that calls fire at
// or after fireAt. If a timer with the same timerID already exists it is
// replaced.
func (s *Scheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
	s.impl.Schedule(timerID, fireAt, fire)
}

// Cancel removes a pending timer. No-op if the timer is unknown or has already
// fired.
func (s *Scheduler) Cancel(timerID string) {
	s.impl.Cancel(timerID)
}

// Close shuts the underlying gocron scheduler down gracefully and, in single-
// leader mode, releases the leader elector (and its dedicated pooled connection).
// The scheduler cannot be reused after this call.
func (s *Scheduler) Close() error {
	err := s.impl.Close()
	if s.elector != nil {
		// Close is idempotent; combine any elector error with the scheduler's.
		err = errors.Join(err, s.elector.Close())
	}
	return err
}
