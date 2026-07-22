// Package scheduler is the consumer-facing façade over the internal gocron
// scheduler (ADR-0008, ADR-0009, ADR-0102, ADR-0134). Consumers import only
// this root package; the concrete gocron implementation stays in
// scheduler/internal/gocron so the vendor dependency is not visible to the
// library API surface.
//
// The façade is neutral of any database driver: multi-replica coordination is
// supplied through the [Locker] and [Elector] interfaces (see [WithLocker] /
// [WithElector]). Database-backed implementations live in
// scheduler/backend/{postgres,mysql} and the persistence-lock bridge, keeping
// pgx / database/sql out of this package entirely.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	gocronsched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// ErrTimerLockElectorConflict is returned by [NewScheduler] when both a [Locker]
// and an [Elector] are configured. Load-balanced per-timer exclusion
// ([WithLocker]) and single-leader firing ([WithElector]) are mutually exclusive
// (ADR-0059, ADR-0102); pick exactly one.
var ErrTimerLockElectorConflict = errors.New(
	"workflow-scheduler: a Locker and an Elector are mutually exclusive — set only one")

// ErrSchedulerClosed is returned by [NativeScheduler.Start],
// [NativeScheduler.Schedule], and [NativeScheduler.Activate] after the
// scheduler has been closed (via [NativeScheduler.Close] or cancellation of
// the context passed to [NativeScheduler.Start]). A closed scheduler cannot be
// reused.
var ErrSchedulerClosed = errors.New("workflow-scheduler: scheduler is closed")

// NativeScheduler is the production, gocron-backed [Scheduler]. Construct it
// with [NewScheduler]; supply the same [clockwork.Clock] instance used to build
// the runtime via [WithClock] so one fake-clock advance drives both
// engine timestamps and timer firing under test (ADR-0003). When the clock
// option is omitted, a real clock is used.
//
// Lifecycle (ADR-0102): [NewScheduler] is goroutine-free — the underlying gocron
// scheduler (and its background goroutine) is not created until the scheduler is
// started. Call [NativeScheduler.Start] with a long-lived context to start it
// explicitly; cancelling that context stops the scheduler. As a convenience the
// scheduler also auto-starts (with a background context) on the first
// [NativeScheduler.Schedule] / [NativeScheduler.Activate] call that needs to
// arm a job, so timer-only consumers need no explicit Start. Call
// [NativeScheduler.Close] on shutdown to release the gocron goroutine.
//
// Overrun protection: a recurring [Job] built by [NewJob] / [NewJobWithID] is
// serialized against itself by default (a fire still running when its next
// occurrence becomes due is rescheduled, not run concurrently); see
// [WithoutOverrunProtection] to opt out. A consumer-implemented foreign Job
// (defined outside this package) cannot carry that unexported flag, so it
// defaults to the same behavior: serialized when its [Trigger] is recurring,
// unrestricted when one-shot.
type NativeScheduler struct {
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

	// armedMu guards armed.
	armedMu sync.Mutex
	// armed is the scheduler's record of currently-armed jobs, keyed by job id.
	// A Manual job that was scheduled but never activated has NO entry here
	// (the no-manual-pen rule): Scheduled/List reflect armed jobs only. The
	// live engine remains the authority on whether an entry is still pending
	// (a consumed one-shot is pruned lazily on read).
	armed map[string]ScheduledJob

	// storeMu guards stores.
	storeMu sync.Mutex
	// stores caches the JobStore resolved from each kind's registered thunk so
	// the thunk runs at most once per kind.
	stores map[JobKind]JobStore

	// rehydrateOnce ensures self-rehydration (Load + Activate per registered
	// kind store) runs exactly once across the Start-and-auto-start paths,
	// guarding the store I/O outside s.mu. sync.Once must be a stable field —
	// never copied.
	rehydrateOnce sync.Once
	// rehydrateErr is the fatal error (if any) stored by rehydrateOnce.Do and
	// surfaced to an explicit Start call; auto-start paths log it at WARN.
	rehydrateErr error
}

// Compile-time contract assertions.
var (
	_ Scheduler = (*NativeScheduler)(nil)
	_ io.Closer = (*NativeScheduler)(nil)
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

	// jobStores holds the kind-routed JobStore providers registered via
	// WithJobStore. Nil until the first WithJobStore option is applied.
	jobStores map[JobKind]func() JobStore
}

// Option configures a [NativeScheduler].
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

// WithMeterProvider sets the OTel MeterProvider for the scheduler. Default:
// the OTel global provider. The scheduler emits the
// wrkflw_scheduler_job_runs_total counter and
// wrkflw_scheduler_job_duration_seconds histogram through it, driven by
// gocron's native MonitorStatus hook (ADR-0134 production item ① — see
// scheduler/internal/gocron/monitor.go). A nil value is ignored.
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

// NewScheduler constructs a gocron-backed [NativeScheduler]. Pass [WithClock]
// to drive timer scheduling with a specific [clockwork.Clock] (default:
// [clockwork.NewRealClock]).
//
// Construction is goroutine-free: the underlying gocron scheduler is not created
// until the scheduler is started (ADR-0102). Start it explicitly with
// [NativeScheduler.Start] to bind its lifetime to a context, or simply schedule
// jobs — the first arm auto-starts it with a background context. Either way,
// call [NativeScheduler.Close] on shutdown to release the gocron goroutine.
//
// With no [WithLocker] / [WithElector] option the scheduler runs in single-node
// mode: every armed job fires locally. [WithLocker] and [WithElector] are
// mutually exclusive; requesting both returns [ErrTimerLockElectorConflict].
func NewScheduler(opts ...Option) (*NativeScheduler, error) {
	cfg := config{}
	for _, o := range opts {
		o(&cfg)
	}

	// Mutual-exclusion: at most one multi-replica mode may be active. Validated
	// eagerly at construction so misconfiguration fails fast, before any start.
	if cfg.locker != nil && cfg.elector != nil {
		return nil, ErrTimerLockElectorConflict
	}

	return &NativeScheduler{cfg: cfg}, nil
}

// internalOpts builds the internal gocron options from the resolved façade
// config. The effective clock is resolved here (option-provided or real-clock
// default) so a fake clock supplied via WithClock drives gocron.
func (s *NativeScheduler) internalOpts() []gocronsched.Option {
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

// logger resolves the effective structured logger (option-provided or
// slog.Default).
func (s *NativeScheduler) logger() *slog.Logger {
	if s.cfg.logger != nil {
		return s.cfg.logger
	}
	return slog.Default()
}

// now reads the effective clock (option-provided or the wall clock).
func (s *NativeScheduler) now() time.Time {
	if s.cfg.clk != nil {
		return s.cfg.clk.Now()
	}
	return time.Now()
}

// Start starts the underlying gocron scheduler if it is not already running,
// creating its background goroutine. Cancelling ctx stops the scheduler (it is
// closed as if [NativeScheduler.Close] were called), tying the scheduler's
// lifetime to ctx. Start is idempotent: calling it on an already-started
// scheduler is a no-op returning nil. It returns [ErrSchedulerClosed] if the
// scheduler has already been closed.
//
// Passing a non-cancellable context (e.g. [context.Background]) starts the
// scheduler without a cancellation watcher; use [NativeScheduler.Close] to stop it.
//
// When [WithJobStore] registrations are present, Start also triggers
// self-rehydration (exactly once across all Start/arm calls): every registered
// kind's store is Loaded and each returned [ScheduledJob] is activated.
// Rehydration I/O runs on a background-derived context — never ctx, which may
// be request- or transaction-scoped. A Load error wrapping
// [ErrUnresolvedTimerDefinitions] is non-fatal (the resolvable subset is still
// armed, a WARN is logged, and Start returns nil); any other Load error (e.g.
// a DB failure) is returned so the caller can retry.
func (s *NativeScheduler) Start(ctx context.Context) error {
	impl, err := s.ensureStarted(ctx)
	if err != nil {
		return err
	}
	return s.rehydrate(impl)
}

// ensureStarted lazily creates and starts the underlying gocron scheduler,
// returning it. It is called by Start (with the caller's context) and by the
// arming paths (with a background context) so a timer-only consumer needs no
// explicit Start. Subsequent calls return the already-running scheduler without
// touching the existing cancellation watcher.
func (s *NativeScheduler) ensureStarted(ctx context.Context) (*gocronsched.GocronScheduler, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrSchedulerClosed
	}
	if s.impl != nil {
		// Already running. If this is an explicit Start(ctx) carrying a
		// cancellable context and no cancellation watcher is installed yet (e.g. a
		// prior arm auto-started the scheduler with a background context),
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

// storeFor resolves (and caches) the JobStore registered for kind, or nil when
// none is registered or the thunk returned nil. The thunk runs at most once
// per kind.
func (s *NativeScheduler) storeFor(kind JobKind) JobStore {
	provide, registered := s.cfg.jobStores[kind]
	if !registered {
		return nil
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if store, ok := s.stores[kind]; ok {
		return store
	}
	store := provide()
	if s.stores == nil {
		s.stores = make(map[JobKind]JobStore)
	}
	s.stores[kind] = store
	return store
}

// rehydrate Loads every registered kind store and activates each returned job,
// exactly once across the Start-and-auto-start paths. The Load + arm I/O runs
// OUTSIDE s.mu (sync.Once provides the single-execution guarantee) and on a
// background context — NEVER a caller's, which may be transaction-scoped and
// short-lived.
//
// Per-job activation errors are logged at WARN and skipped so one
// unschedulable job never aborts the batch. A Load error wrapping
// [ErrUnresolvedTimerDefinitions] is non-fatal (partial jobs already armed, a
// WARN logged, nothing stored); any other Load error is fatal — stored in
// s.rehydrateErr and returned to the explicit Start caller so the orchestrator
// can retry.
func (s *NativeScheduler) rehydrate(impl *gocronsched.GocronScheduler) error {
	if len(s.cfg.jobStores) == 0 {
		return nil
	}
	s.rehydrateOnce.Do(func() {
		ctx := context.Background()
		logger := s.logger()
		var fatal error
		for _, kind := range slices.Sorted(maps.Keys(s.cfg.jobStores)) {
			store := s.storeFor(kind)
			if store == nil {
				continue
			}
			jobs, err := store.Load(ctx)
			for _, job := range jobs {
				if aerr := s.activateJob(ctx, impl, job); aerr != nil {
					logger.WarnContext(ctx, "workflow-scheduler: rehydrate: failed to re-arm job, skipping",
						slog.String("job_id", job.ID()),
						slog.String("job_kind", string(job.Kind())),
						slog.Any("error", aerr))
				}
			}
			if err != nil {
				// Unresolved-definitions is a non-fatal condition for automatic
				// self-rehydration: the partial result (already armed above) is
				// accepted and startup continues; the operator sees a WARN.
				if errors.Is(err, ErrUnresolvedTimerDefinitions) {
					logger.WarnContext(ctx, "workflow-scheduler: rehydrate: some jobs reference unresolved definitions; skipped (non-fatal)",
						slog.String("job_kind", string(kind)),
						slog.Any("error", err))
					continue
				}
				fatal = errors.Join(fatal, err)
			}
		}
		s.rehydrateErr = fatal
	})
	return s.rehydrateErr
}

// watchContext closes the scheduler when the start context is cancelled, or
// exits quietly when Close closes stop first.
func (s *NativeScheduler) watchContext(done <-chan struct{}, stop <-chan struct{}) {
	select {
	case <-done:
		_ = s.Close()
	case <-stop:
	}
}

// neutralLockerBridge adapts the public scheduler.Locker (whose Lock returns a
// scheduler.Lock) to the internal gocronsched.NeutralLocker shape (whose Lock
// returns a gocronsched.NeutralLock). The two interfaces are structurally
// identical apart from the named return type, so this one-hop bridge lets the
// façade pass its neutral Locker to AdaptLocker without importing gocron.
type neutralLockerBridge struct{ inner Locker }

func (b neutralLockerBridge) Lock(ctx context.Context, key string) (gocronsched.NeutralLock, error) {
	l, err := b.inner.Lock(ctx, key)
	if err != nil {
		return nil, err
	}
	return l, nil // scheduler.Lock satisfies gocronsched.NeutralLock structurally
}

// triggerDef maps a [Trigger] to the internal gocron engine's package-local
// TriggerDef shape. It is total over every Trigger constructor; the zero
// Trigger (no constructor) reports an error wrapping [ErrUnsupportedTrigger].
func triggerDef(t Trigger) (gocronsched.TriggerDef, error) {
	switch t.kind {
	case triggerAt:
		return gocronsched.At(t.at), nil
	case triggerAfter:
		return gocronsched.After(t.dur), nil
	case triggerEvery:
		return gocronsched.Every(t.dur), nil
	case triggerEveryRandom:
		return gocronsched.EveryRandom(t.min, t.max), nil
	case triggerCron:
		return gocronsched.Cron(t.cron), nil
	case triggerDaily:
		return gocronsched.Daily(t.interval, scheduleClockTimes(t.atTimes)...), nil
	case triggerWeekly:
		return gocronsched.Weekly(t.interval, t.weekdays, scheduleClockTimes(t.atTimes)...), nil
	case triggerMonthly:
		return gocronsched.Monthly(t.interval, t.days, scheduleClockTimes(t.atTimes)...), nil
	default:
		return gocronsched.TriggerDef{}, fmt.Errorf(
			"workflow-scheduler: no engine mapping for trigger kind %d: %w", t.kind, ErrUnsupportedTrigger)
	}
}

// scheduleClockTimes converts this package's own ClockTime values (see
// scheduler/trigger.go) to the internal gocron engine's own ClockTime shape
// (scheduler/internal/gocron/trigger.go) its calendar constructors accept —
// the façade boundary conversion that keeps this engine free of any
// dependency on the parent scheduler package.
func scheduleClockTimes(cs []ClockTime) []gocronsched.ClockTime {
	out := make([]gocronsched.ClockTime, len(cs))
	for i, c := range cs {
		out[i] = gocronsched.ClockTime{Hour: c.Hour, Minute: c.Minute, Second: c.Second}
	}
	return out
}

// jobSingleton reads a Job's overrun-protection flag through the in-package
// private assertion (see job.singleton). A FOREIGN Job implementation (defined
// outside this package) cannot satisfy an unexported-method interface, so it
// defaults to the safe equivalent: serialized when its Trigger is recurring,
// unrestricted when one-shot.
func jobSingleton(j Job) bool {
	if s, ok := j.(interface{ singleton() bool }); ok {
		return s.singleton()
	}
	return j.Trigger().Recurring()
}

// Schedule implements [Scheduler]: it persists j through the [JobStore]
// registered for j's kind (a job of an unregistered kind is a supported
// in-memory-only mode — nothing is persisted and nothing is logged) and, for
// [ActivationAuto] jobs, arms it (auto-starting the scheduler on first use).
//
// A [ActivationManual] job is persisted only: the scheduler keeps NO record of
// it until [NativeScheduler.Activate] — [NativeScheduler.Scheduled] and
// [NativeScheduler.List] will not see it. The returned [ScheduledJob] carries
// the next run computed purely from j's [Trigger] at the effective clock's
// now, for the caller alone (e.g. to persist alongside its own state).
//
// It returns [ErrSchedulerClosed] after the scheduler has been closed.
func (s *NativeScheduler) Schedule(ctx context.Context, j Job) (ScheduledJob, error) {
	if j == nil {
		return nil, fmt.Errorf("workflow-scheduler: Schedule requires a non-nil Job")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrSchedulerClosed
	}

	next, ok := j.Trigger().Next(s.now())
	if !ok {
		return nil, fmt.Errorf("workflow-scheduler: job %q trigger can never fire: %w", j.ID(), ErrUnsupportedTrigger)
	}
	sj, err := NewScheduledJob(j, next)
	if err != nil {
		return nil, err
	}

	if store := s.storeFor(j.Kind()); store != nil {
		if serr := store.Save(ctx, sj); serr != nil {
			return nil, fmt.Errorf("workflow-scheduler: persist job %q: %w", j.ID(), serr)
		}
	}

	if j.Activation() == ActivationManual {
		// No-manual-pen rule: persisted only; armed later via Activate.
		return sj, nil
	}
	if err := s.Activate(ctx, sj); err != nil {
		return nil, err
	}
	return sj, nil
}

// Activate implements [Scheduler]: it arms j against the live gocron engine,
// auto-starting the scheduler (with a background context) on first use. It is
// an upsert by job id — activating an id that is already armed replaces the
// existing registration, never duplicating fires. It returns
// [ErrSchedulerClosed] after the scheduler has been closed.
//
// The first arm also triggers self-rehydration (exactly once) when
// [WithJobStore] registrations are present; a rehydration error is logged at
// WARN and does not fail this call.
func (s *NativeScheduler) Activate(ctx context.Context, j ScheduledJob) error {
	if j == nil {
		return fmt.Errorf("workflow-scheduler: Activate requires a non-nil ScheduledJob")
	}
	impl, err := s.ensureStarted(context.Background())
	if err != nil {
		return err
	}
	if rerr := s.rehydrate(impl); rerr != nil {
		s.logger().WarnContext(ctx, "workflow-scheduler: Activate: rehydration had errors (proceeding)",
			slog.Any("error", rerr))
	}
	return s.activateJob(ctx, impl, j)
}

// activateJob registers j on the engine and records it as armed. It is the
// shared worker behind Activate and rehydrate — rehydrate must NOT call the
// public Activate, whose rehydrate call would re-enter the non-reentrant
// sync.Once.
func (s *NativeScheduler) activateJob(ctx context.Context, impl *gocronsched.GocronScheduler, j ScheduledJob) error {
	def, err := triggerDef(j.Trigger())
	if err != nil {
		return err
	}
	fn, data := j.Action(), j.Data()
	task := func(c context.Context) error { return fn(c, data) }
	if _, err := impl.ScheduleJob(ctx, j.ID(), def, task, jobSingleton(j)); err != nil {
		return err
	}
	s.armedMu.Lock()
	if s.armed == nil {
		s.armed = make(map[string]ScheduledJob)
	}
	s.armed[j.ID()] = j
	s.armedMu.Unlock()
	return nil
}

// Deactivate implements [Scheduler]: it disarms the job with the given id
// WITHOUT touching its durable record. Unknown id — or a never-started
// scheduler — is a no-op returning nil.
func (s *NativeScheduler) Deactivate(ctx context.Context, id string) error {
	s.armedMu.Lock()
	delete(s.armed, id)
	s.armedMu.Unlock()

	s.mu.Lock()
	impl := s.impl
	s.mu.Unlock()
	if impl != nil {
		impl.RemoveJob(ctx, id)
	}
	return nil
}

// Cancel implements [Scheduler]: it disarms the job with the given id AND
// deletes its durable record through its kind's registered [JobStore]. An
// unknown id is a no-op returning nil (nothing armed, so no kind to route the
// delete through).
func (s *NativeScheduler) Cancel(ctx context.Context, id string) error {
	s.armedMu.Lock()
	entry, ok := s.armed[id]
	delete(s.armed, id)
	s.armedMu.Unlock()

	s.mu.Lock()
	impl := s.impl
	s.mu.Unlock()
	if impl != nil {
		impl.RemoveJob(ctx, id)
	}
	if !ok {
		return nil
	}
	if store := s.storeFor(entry.Kind()); store != nil {
		if err := store.Delete(ctx, id); err != nil {
			return fmt.Errorf("workflow-scheduler: delete job %q: %w", id, err)
		}
	}
	return nil
}

// Scheduled implements [Scheduler]: it returns the ARMED job with the given
// id, annotated with its live next-run instant from the engine. A job that is
// not armed — unknown, cancelled, deactivated, a consumed one-shot, or a
// Manual job that was never activated — reports an error wrapping
// [ErrJobNotFound].
func (s *NativeScheduler) Scheduled(_ context.Context, id string) (ScheduledJob, error) {
	s.armedMu.Lock()
	entry, ok := s.armed[id]
	s.armedMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("workflow-scheduler: job %q: %w", id, ErrJobNotFound)
	}

	s.mu.Lock()
	impl := s.impl
	s.mu.Unlock()
	if impl == nil {
		return entry, nil
	}
	next, live := impl.NextRun(id)
	if !live {
		// The engine no longer tracks it (a consumed one-shot): prune lazily.
		s.armedMu.Lock()
		delete(s.armed, id)
		s.armedMu.Unlock()
		return nil, fmt.Errorf("workflow-scheduler: job %q: %w", id, ErrJobNotFound)
	}
	return NewScheduledJob(entry, next)
}

// List implements [Scheduler]: it yields every armed job (in ascending job-id
// order for determinism), annotated with its live next-run instant.
// Persisted-but-unactivated Manual jobs and consumed one-shots are not
// included.
func (s *NativeScheduler) List(_ context.Context) iter.Seq[ScheduledJob] {
	return func(yield func(ScheduledJob) bool) {
		s.armedMu.Lock()
		ids := slices.Sorted(maps.Keys(s.armed))
		entries := make([]ScheduledJob, 0, len(ids))
		for _, id := range ids {
			entries = append(entries, s.armed[id])
		}
		s.armedMu.Unlock()

		s.mu.Lock()
		impl := s.impl
		s.mu.Unlock()

		for i, entry := range entries {
			if impl != nil {
				next, live := impl.NextRun(ids[i])
				if !live {
					continue // consumed one-shot: no longer armed
				}
				if refreshed, err := NewScheduledJob(entry, next); err == nil {
					entry = refreshed
				}
			}
			if !yield(entry) {
				return
			}
		}
	}
}

// Close shuts the underlying gocron scheduler down gracefully and stops any
// context-cancellation watcher started by [NativeScheduler.Start]. If the
// configured [Elector] also implements [io.Closer] (e.g. a backend elector
// holding a dedicated database connection), it is closed as a convenience —
// its error is joined with the scheduler's. Close is idempotent and safe to
// call on a never-started scheduler; the scheduler cannot be reused after this
// call.
func (s *NativeScheduler) Close() error {
	return s.closeWith(func(impl *gocronsched.GocronScheduler) error { return impl.Close() })
}

// CloseWithContext behaves like [NativeScheduler.Close] but bounds the underlying gocron
// shutdown by ctx (via gocron's ShutdownWithContext): dispatch stops immediately and
// the wait for running jobs honors ctx's deadline, returning ctx.Err() if it expires
// first. Idempotent and safe on a never-started scheduler; the scheduler cannot be
// reused afterward.
func (s *NativeScheduler) CloseWithContext(ctx context.Context) error {
	return s.closeWith(func(impl *gocronsched.GocronScheduler) error { return impl.CloseWithContext(ctx) })
}

// closeWith performs the idempotent teardown bookkeeping shared by Close and
// CloseWithContext — flip the closed flag, detach impl/stopCh under the lock, wake the
// Start watcher, and join an io.Closer elector — invoking closeImpl to release the
// underlying gocron scheduler (context-aware or not). A second call is a no-op (nil).
func (s *NativeScheduler) closeWith(closeImpl func(*gocronsched.GocronScheduler) error) error {
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

	// Clear the armed record so a post-Close Scheduled/List reports every job
	// as not-found rather than surfacing stale entries for a scheduler that
	// can never fire them again.
	s.armedMu.Lock()
	s.armed = nil
	s.armedMu.Unlock()

	if stop != nil {
		close(stop) // wake the Start watcher so it exits
	}
	var err error
	if impl != nil {
		err = closeImpl(impl)
	}
	if closer, ok := elector.(io.Closer); ok {
		err = errors.Join(err, closer.Close())
	}
	return err
}
