package processtest

import (
	"context"
	"fmt"
	"iter"
	"sort"
	"sync"
	"time"

	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/scheduler"
)

// Compile-time interface check.
var _ scheduler.Scheduler = (*MemScheduler)(nil)

// pendingTimer is one ARMED entry in the MemScheduler's internal table.
// recurEvery is zero for one-shot jobs and the re-arm interval for recurring
// (Every) jobs.
type pendingTimer struct {
	job        scheduler.Job
	kind       scheduler.JobKind
	timerID    string
	fireAt     time.Time
	recurEvery time.Duration
}

// MemScheduler is a clock-driven, concurrency-safe [scheduler.Scheduler] for
// tests and reference wiring. It holds armed jobs in memory — keyed by the
// job id, which for runtime timers IS the engine timer id — and fires those
// whose fire time is <= clock.Now() when [MemScheduler.Tick] is called.
//
// It mirrors the production no-manual-pen semantics: [MemScheduler.Schedule]
// persists a job through the [scheduler.JobStore] registered for its kind (see
// [MemScheduler.RegisterJobStore]) and arms only [scheduler.ActivationAuto]
// jobs; a [scheduler.ActivationManual] job stays unarmed (invisible to
// Pending/NextFireAt/Scheduled/List) until [MemScheduler.Activate].
//
// It is a test-only double, not a production default: it understands only
// one-shot triggers ([scheduler.At], [scheduler.After]) and the fixed-interval
// recurring [scheduler.Every] (re-arm at last+d on each Tick). Any other
// trigger kind (cron, calendar, random) yields
// [scheduler.ErrUnsupportedTrigger] from Schedule and Activate.
//
// Determinism guarantee: Tick fires pending-at-tick-start jobs in
// (fireAt, id) lexicographic order. Jobs armed inside a fire callback during a
// Tick — and recurring jobs re-armed by this Tick — are NOT fired again in
// that same Tick; they fire only on a subsequent Tick call. This prevents
// surprising infinite loops when a reminder reschedules itself.
type MemScheduler struct {
	clk     clock.Clock
	mu      sync.Mutex
	pending map[string]pendingTimer
	stores  map[scheduler.JobKind]scheduler.JobStore
}

// MemSchedulerOption configures a [MemScheduler].
type MemSchedulerOption func(*MemScheduler)

// WithMemSchedulerClock sets the time source used to evaluate timer due-ness.
// Default: clock.System(). A nil clock is ignored. Inject a fake clock (e.g.
// [FakeClock]) in tests.
func WithMemSchedulerClock(clk clock.Clock) MemSchedulerOption {
	return func(s *MemScheduler) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// NewMemScheduler constructs a [MemScheduler]. The time source defaults to
// clock.System(); override it with [WithMemSchedulerClock] (e.g. a [FakeClock]
// in tests).
func NewMemScheduler(opts ...MemSchedulerOption) *MemScheduler {
	s := &MemScheduler{
		clk:     clock.System(),
		pending: make(map[string]pendingTimer),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// RegisterJobStore registers store as the [scheduler.JobStore] used for jobs
// of the given kind, mirroring [scheduler.WithJobStore]'s routing semantics:
// Schedule persists through it and Cancel deletes through it. A nil store or
// empty kind is ignored; registering the same kind again keeps the last store.
func (s *MemScheduler) RegisterJobStore(kind scheduler.JobKind, store scheduler.JobStore) {
	if kind == "" || store == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stores == nil {
		s.stores = make(map[scheduler.JobKind]scheduler.JobStore)
	}
	s.stores[kind] = store
}

// storeFor returns the store registered for kind, or nil.
func (s *MemScheduler) storeFor(kind scheduler.JobKind) scheduler.JobStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stores[kind]
}

// resolve computes the fire instant (via Trigger.Next at the scheduler's now)
// and the re-arm interval for j's trigger, or reports
// [scheduler.ErrUnsupportedTrigger] for the kinds this double does not
// understand (anything but At/After/Every, detected through the Trigger
// accessors).
func (s *MemScheduler) resolve(j scheduler.Job) (fireAt time.Time, recurEvery time.Duration, err error) {
	trig := j.Trigger()
	next, ok := trig.Next(s.clk.Now())
	if !ok {
		return time.Time{}, 0, fmt.Errorf("processtest: job %q trigger can never fire: %w", j.ID(), scheduler.ErrUnsupportedTrigger)
	}
	if _, isAbs := trig.AbsTime(); isAbs {
		return next, 0, nil // At: one-shot at the absolute instant
	}
	if d, isDur := trig.Duration(); isDur {
		if trig.Recurring() {
			return next, d, nil // Every: recurring, re-armed at last+d on Tick
		}
		return next, 0, nil // After: one-shot at now+d
	}
	return time.Time{}, 0, fmt.Errorf("processtest: job %q trigger kind not supported by MemScheduler: %w",
		j.ID(), scheduler.ErrUnsupportedTrigger)
}

// arm upserts j as an armed pending entry.
func (s *MemScheduler) arm(j scheduler.Job, fireAt time.Time, recurEvery time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[j.ID()] = pendingTimer{
		job:        j,
		kind:       j.Kind(),
		timerID:    j.ID(),
		fireAt:     fireAt,
		recurEvery: recurEvery,
	}
}

// Schedule implements [scheduler.Scheduler]: it persists j through the store
// registered for j's kind (when any) and arms it when it is
// [scheduler.ActivationAuto]. A [scheduler.ActivationManual] job is persisted
// only — no pending entry exists until [MemScheduler.Activate]. The returned
// [scheduler.ScheduledJob] carries the fire instant computed via
// Trigger.Next(clock.Now()).
func (s *MemScheduler) Schedule(ctx context.Context, j scheduler.Job) (scheduler.ScheduledJob, error) {
	if j == nil {
		return nil, fmt.Errorf("processtest: Schedule requires a non-nil Job")
	}
	fireAt, recurEvery, err := s.resolve(j)
	if err != nil {
		return nil, err
	}
	sj, err := scheduler.NewScheduledJob(j, fireAt)
	if err != nil {
		return nil, err
	}
	if store := s.storeFor(j.Kind()); store != nil {
		if serr := store.Save(ctx, sj); serr != nil {
			return nil, fmt.Errorf("processtest: persist job %q: %w", j.ID(), serr)
		}
	}
	if j.Activation() == scheduler.ActivationManual {
		return sj, nil
	}
	s.arm(j, fireAt, recurEvery)
	return sj, nil
}

// Activate implements [scheduler.Scheduler]: it arms j (an upsert by job id),
// recomputing the fire instant from j's trigger at the scheduler's now — for a
// rehydrated one-shot re-armed via an absolute-time trigger this is the
// faithful original instant.
func (s *MemScheduler) Activate(_ context.Context, j scheduler.ScheduledJob) error {
	if j == nil {
		return fmt.Errorf("processtest: Activate requires a non-nil ScheduledJob")
	}
	fireAt, recurEvery, err := s.resolve(j)
	if err != nil {
		return err
	}
	s.arm(j, fireAt, recurEvery)
	return nil
}

// Deactivate implements [scheduler.Scheduler]: it disarms the job with the
// given id without touching any registered store. Unknown id is a no-op.
func (s *MemScheduler) Deactivate(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
	return nil
}

// Cancel implements [scheduler.Scheduler]: it disarms the job with the given
// id and deletes its durable record through its kind's registered store.
// Unknown id is a no-op returning nil.
func (s *MemScheduler) Cancel(ctx context.Context, id string) error {
	s.mu.Lock()
	pt, ok := s.pending[id]
	delete(s.pending, id)
	var store scheduler.JobStore
	if ok {
		store = s.stores[pt.kind]
	}
	s.mu.Unlock()
	if store != nil {
		if err := store.Delete(ctx, id); err != nil {
			return fmt.Errorf("processtest: delete job %q: %w", id, err)
		}
	}
	return nil
}

// Scheduled implements [scheduler.Scheduler]: it returns the ARMED job with
// the given id annotated with its pending fire instant, or an error wrapping
// [scheduler.ErrJobNotFound] when no such job is armed (unknown, cancelled,
// fired one-shot, or a manual job never activated).
func (s *MemScheduler) Scheduled(_ context.Context, id string) (scheduler.ScheduledJob, error) {
	s.mu.Lock()
	pt, ok := s.pending[id]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("processtest: job %q: %w", id, scheduler.ErrJobNotFound)
	}
	return scheduler.NewScheduledJob(pt.job, pt.fireAt)
}

// List implements [scheduler.Scheduler]: it yields every armed job in
// deterministic (fireAt, id) order, annotated with its pending fire instant.
func (s *MemScheduler) List(_ context.Context) iter.Seq[scheduler.ScheduledJob] {
	return func(yield func(scheduler.ScheduledJob) bool) {
		s.mu.Lock()
		entries := make([]pendingTimer, 0, len(s.pending))
		for _, pt := range s.pending {
			entries = append(entries, pt)
		}
		s.mu.Unlock()
		sortPending(entries)
		for _, pt := range entries {
			sj, err := scheduler.NewScheduledJob(pt.job, pt.fireAt)
			if err != nil {
				continue
			}
			if !yield(sj) {
				return
			}
		}
	}
}

// NextFireAt returns the fire time of the earliest ARMED job and true, or the
// zero time and false when none are armed. It lets a test harness advance a
// fake clock to exactly the next due timer before calling Tick, without
// needing visibility into the (unexported) per-instance timer bookkeeping. It
// is concrete on MemScheduler and not part of the [scheduler.Scheduler] port.
func (s *MemScheduler) NextFireAt() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		earliest time.Time
		found    bool
	)
	for _, pt := range s.pending {
		if !found || pt.fireAt.Before(earliest) {
			earliest = pt.fireAt
			found = true
		}
	}
	return earliest, found
}

// Pending returns the fire time of the ARMED job with the given id and true,
// or the zero time and false if no such job is armed. Job ids ARE engine timer
// ids for runtime-armed timers, so a test harness can tell whether a specific
// parked token's awaited timer is armed (matching the token's command id
// against a job id) without scanning all timers.
func (s *MemScheduler) Pending(timerID string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pt, ok := s.pending[timerID]
	if !ok {
		return time.Time{}, false
	}
	return pt.fireAt, true
}

// sortPending orders entries deterministically: primary fireAt (earlier
// first), secondary id (lexicographic).
func sortPending(entries []pendingTimer) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].fireAt.Equal(entries[j].fireAt) {
			return entries[i].timerID < entries[j].timerID
		}
		return entries[i].fireAt.Before(entries[j].fireAt)
	})
}

// Tick fires all armed jobs whose fireAt <= clock.Now() in deterministic
// (fireAt, id) order. A one-shot job is removed after firing; a recurring
// (Every) job is re-armed at fireAt+recurEvery instead of removed. Jobs armed
// inside a fire callback — and recurring jobs re-armed by this Tick — are NOT
// eligible to fire during this Tick; only jobs armed at the moment Tick begins
// are considered.
//
// Each due job's action runs with a background context and its own
// DataProvider, mirroring the production engine's self-contained fire
// contract; an action error is ignored (a test double has no retry policy).
//
// ctx is reserved for future use (e.g. cancellation); currently ignored.
func (s *MemScheduler) Tick(_ context.Context) error {
	now := s.clk.Now()

	// Snapshot the jobs that are due at this instant, then remove one-shots and
	// re-arm recurring ones BEFORE invoking any callbacks. This ensures that a
	// newly armed job (added inside a fire callback) — and a re-armed recurring
	// job — cannot fire in this Tick.
	s.mu.Lock()
	var due []pendingTimer
	for _, pt := range s.pending {
		if !pt.fireAt.After(now) { // fireAt <= now
			due = append(due, pt)
			if pt.recurEvery > 0 {
				rearmed := pt
				rearmed.fireAt = pt.fireAt.Add(pt.recurEvery)
				s.pending[pt.timerID] = rearmed
			} else {
				delete(s.pending, pt.timerID)
			}
		}
	}
	s.mu.Unlock()

	sortPending(due)

	// Invoke fire callbacks outside the lock so Schedule/Activate/Cancel can be
	// called from within a callback without deadlocking.
	for _, pt := range due {
		fn, data := pt.job.Action(), pt.job.Data()
		_ = fn(context.Background(), data)
	}
	return nil
}
