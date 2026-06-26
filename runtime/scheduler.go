package runtime

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zakyalvan/krtlwrkflw/clock"
)

// Scheduler is the port through which the runtime registers and cancels timers.
// Implementations may be in-memory (for tests), gocron-backed (production),
// or any other backing store.
type Scheduler interface {
	// Schedule registers a timer with the given timerID that calls fire at or
	// after fireAt. If a timer with the same timerID already exists it is
	// replaced.
	Schedule(timerID string, fireAt time.Time, fire func())

	// Cancel removes a pending timer. It is a no-op if the timer does not exist
	// or has already fired.
	Cancel(timerID string)
}

// Compile-time interface check.
var _ Scheduler = (*MemScheduler)(nil)

// pendingTimer is one entry in the MemScheduler's internal table.
type pendingTimer struct {
	timerID string
	fireAt  time.Time
	fire    func()
}

// MemScheduler is a clock-driven, concurrency-safe Scheduler for tests and
// reference wiring. It holds pending timers in memory and fires those whose
// FireAt <= clock.Now() when Tick is called.
//
// Determinism guarantee: Tick fires pending-at-tick-start timers in
// (FireAt, TimerID) lexicographic order. Timers scheduled inside a fire
// callback during a Tick are NOT fired in that same Tick; they fire only on a
// subsequent Tick call. This prevents surprising infinite loops when a reminder
// reschedules itself.
type MemScheduler struct {
	clk     clock.Clock
	mu      sync.Mutex
	pending map[string]pendingTimer
}

// MemSchedulerOption configures a MemScheduler.
type MemSchedulerOption func(*MemScheduler)

// WithMemSchedulerClock sets the time source used to evaluate timer due-ness.
// Default: clock.System(). A nil clock is ignored. Inject a fake clock in tests.
func WithMemSchedulerClock(clk clock.Clock) MemSchedulerOption {
	return func(s *MemScheduler) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// NewMemScheduler constructs a MemScheduler. The time source defaults to
// clock.System(); override it with WithMemSchedulerClock (e.g. a fake clock in tests).
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

// Schedule registers a timer. Replaces any existing timer with the same timerID.
func (s *MemScheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[timerID] = pendingTimer{timerID: timerID, fireAt: fireAt, fire: fire}
}

// Cancel removes a pending timer. No-op if absent.
func (s *MemScheduler) Cancel(timerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, timerID)
}

// Tick fires all timers whose FireAt <= clock.Now() in deterministic
// (FireAt, TimerID) order. Timers scheduled inside a fire callback are NOT
// eligible to fire during this Tick — only timers present at the moment Tick
// begins are considered.
//
// ctx is reserved for future use (e.g. cancellation); currently ignored.
func (s *MemScheduler) Tick(_ context.Context) error {
	now := s.clk.Now()

	// Snapshot the timers that are due at this instant, then remove them from
	// the pending map before invoking any callbacks. This ensures that a newly
	// scheduled timer (added inside a fire callback) cannot fire in this Tick.
	s.mu.Lock()
	var due []pendingTimer
	for _, pt := range s.pending {
		if !pt.fireAt.After(now) { // fireAt <= now
			due = append(due, pt)
			delete(s.pending, pt.timerID)
		}
	}
	s.mu.Unlock()

	// Sort due timers deterministically: primary FireAt (earlier first),
	// secondary TimerID (lexicographic).
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
