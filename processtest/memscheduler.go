package processtest

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// Compile-time interface check.
var _ kernel.Scheduler = (*MemScheduler)(nil)

// pendingTimer is one entry in the MemScheduler's internal table. recurEvery is
// zero for one-shot timers and the re-arm interval for recurring (Every) timers.
type pendingTimer struct {
	timerID    string
	fireAt     time.Time
	fire       func()
	recurEvery time.Duration
}

// MemScheduler is a clock-driven, concurrency-safe [kernel.Scheduler] for tests
// and reference wiring. It holds pending timers in memory and fires those whose
// fire time is <= clock.Now() when [MemScheduler.Tick] is called.
//
// It is a test-only double, not a production default: it understands only
// [schedule.KindOneTime] (fire once at now+d, or at an absolute time) and
// [schedule.KindDuration] (Every — re-arm at last+d on each Tick). Any other
// trigger kind (cron, calendar, random) yields [kernel.ErrUnsupportedTrigger].
//
// Determinism guarantee: Tick fires pending-at-tick-start timers in
// (fireAt, timerID) lexicographic order. Timers scheduled inside a fire
// callback during a Tick — and recurring timers re-armed by this Tick — are NOT
// fired again in that same Tick; they fire only on a subsequent Tick call. This
// prevents surprising infinite loops when a reminder reschedules itself.
type MemScheduler struct {
	clk     clock.Clock
	mu      sync.Mutex
	pending map[string]pendingTimer
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

// Schedule registers a timer from a [schedule.TriggerSpec], replacing any
// existing timer with the same timerID. It supports KindOneTime (fire once at
// the absolute time, or at now+duration) and KindDuration (Every — re-arm at
// last+duration on each Tick). It returns the next computed fire time, or
// [kernel.ErrUnsupportedTrigger] for any other kind.
func (s *MemScheduler) Schedule(_ context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var next time.Time
	switch trig.Kind() {
	case schedule.KindOneTime:
		if at, ok := trig.AbsTime(); ok {
			next = at
		} else {
			d, _ := trig.Duration()
			next = s.clk.Now().Add(d)
		}
		s.pending[timerID] = pendingTimer{timerID: timerID, fireAt: next, fire: fire, recurEvery: 0}
	case schedule.KindDuration:
		d, _ := trig.Duration()
		next = s.clk.Now().Add(d)
		s.pending[timerID] = pendingTimer{timerID: timerID, fireAt: next, fire: fire, recurEvery: d}
	default:
		return time.Time{}, kernel.ErrUnsupportedTrigger
	}
	return next, nil
}

// NextFireAt returns the fire time of the earliest pending timer and true, or
// the zero time and false when no timers are pending. It lets a test harness
// advance a fake clock to exactly the next due timer before calling Tick, without
// needing visibility into the (unexported) per-instance timer bookkeeping. It is
// concrete on MemScheduler and not part of the [kernel.Scheduler] interface.
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

// Pending returns the fire time of the pending timer with the given id and true,
// or the zero time and false if no such timer is pending. It lets a test harness
// tell whether a specific parked token's awaited timer is armed (matching the
// token's command id against a scheduled timer id) without scanning all timers.
func (s *MemScheduler) Pending(timerID string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pt, ok := s.pending[timerID]
	if !ok {
		return time.Time{}, false
	}
	return pt.fireAt, true
}

// NextRun returns the next scheduled run time of the timer with the given id and
// true, or the zero time and false if no such timer is pending. It is the
// [kernel.Scheduler] port method and is an alias of [MemScheduler.Pending].
func (s *MemScheduler) NextRun(timerID string) (time.Time, bool) {
	return s.Pending(timerID)
}

// Cancel removes a pending timer. No-op if absent. The context is ignored.
func (s *MemScheduler) Cancel(_ context.Context, timerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, timerID)
}

// Tick fires all timers whose fireAt <= clock.Now() in deterministic
// (fireAt, timerID) order. A one-shot timer is removed after firing; a recurring
// (Every) timer is re-armed at fireAt+recurEvery instead of removed. Timers
// scheduled inside a fire callback — and recurring timers re-armed by this Tick —
// are NOT eligible to fire during this Tick; only timers present at the moment
// Tick begins are considered.
//
// ctx is reserved for future use (e.g. cancellation); currently ignored.
func (s *MemScheduler) Tick(_ context.Context) error {
	now := s.clk.Now()

	// Snapshot the timers that are due at this instant, then remove one-shots and
	// re-arm recurring ones BEFORE invoking any callbacks. This ensures that a
	// newly scheduled timer (added inside a fire callback) — and a re-armed
	// recurring timer — cannot fire in this Tick.
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

	// Sort due timers deterministically: primary fireAt (earlier first),
	// secondary timerID (lexicographic).
	sort.Slice(due, func(i, j int) bool {
		if due[i].fireAt.Equal(due[j].fireAt) {
			return due[i].timerID < due[j].timerID
		}
		return due[i].fireAt.Before(due[j].fireAt)
	})

	// Invoke fire callbacks outside the lock so Schedule/Cancel can be called
	// from within a callback without deadlocking.
	for _, pt := range due {
		pt.fire()
	}
	return nil
}
