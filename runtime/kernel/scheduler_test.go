package kernel_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"

	"github.com/jonboulle/clockwork"
)

// baseTime is a fixed deterministic base for fake-clock tests.
var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestMemSchedulerTickFiresDue verifies that Tick fires only timers whose
// FireAt <= clock.Now(), in deterministic (FireAt, TimerID) order.
func TestMemSchedulerTickFiresDue(t *testing.T) {
	fc := clockwork.NewFakeClockAt(baseTime)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))

	var mu sync.Mutex
	var fired []string
	record := func(id string) func() {
		return func() {
			mu.Lock()
			defer mu.Unlock()
			fired = append(fired, id)
		}
	}

	// Schedule three timers: two due now+1s, one due now+2s.
	t1 := baseTime.Add(1 * time.Second)
	t2 := baseTime.Add(1 * time.Second)
	t3 := baseTime.Add(2 * time.Second)

	sched.Schedule("timer-b", t1, record("timer-b")) // same FireAt as timer-a, ID sorts after
	sched.Schedule("timer-a", t2, record("timer-a")) // same FireAt, ID sorts before
	sched.Schedule("timer-c", t3, record("timer-c"))

	// Advance clock to t+1s: timer-a and timer-b should fire, NOT timer-c.
	fc.Advance(1 * time.Second)
	require.NoError(t, sched.Tick(context.Background()))

	mu.Lock()
	got := append([]string(nil), fired...)
	mu.Unlock()

	require.Len(t, got, 2, "only two timers due at t+1s should fire")
	// Deterministic order: (FireAt same, TimerID lexicographic) → timer-a before timer-b.
	assert.Equal(t, []string{"timer-a", "timer-b"}, got)

	// Advance to t+2s: timer-c now fires.
	fc.Advance(1 * time.Second)
	require.NoError(t, sched.Tick(context.Background()))

	mu.Lock()
	got = append([]string(nil), fired...)
	mu.Unlock()

	require.Len(t, got, 3)
	assert.Equal(t, "timer-c", got[2])
}

// TestMemSchedulerCancelRemovesTimer verifies that Cancel prevents a timer
// from firing on the next Tick even if its FireAt <= clock.Now().
func TestMemSchedulerCancelRemovesTimer(t *testing.T) {
	fc := clockwork.NewFakeClockAt(baseTime)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))

	fired := false
	sched.Schedule("cancel-me", baseTime.Add(1*time.Second), func() { fired = true })
	sched.Cancel("cancel-me")

	fc.Advance(2 * time.Second) // well past FireAt
	require.NoError(t, sched.Tick(context.Background()))

	assert.False(t, fired, "cancelled timer must not fire")
}

// TestMemSchedulerNotYetDueDoesNotFire verifies that a timer whose FireAt >
// clock.Now() does NOT fire even after a Tick.
func TestMemSchedulerNotYetDueDoesNotFire(t *testing.T) {
	fc := clockwork.NewFakeClockAt(baseTime)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))

	fired := false
	sched.Schedule("future", baseTime.Add(10*time.Second), func() { fired = true })

	fc.Advance(1 * time.Second) // only 1s, not 10s
	require.NoError(t, sched.Tick(context.Background()))

	assert.False(t, fired, "timer not yet due must not fire")
}

// TestMemSchedulerNewlyScheduledInCallbackNotFiredSameTick verifies the
// same-tick isolation guarantee: if a fire callback schedules a NEW timer that
// would also be due now, that new timer does NOT fire in the same Tick; it fires
// in the next Tick.
func TestMemSchedulerNewlyScheduledInCallbackNotFiredSameTick(t *testing.T) {
	fc := clockwork.NewFakeClockAt(baseTime)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))

	secondFired := false
	sched.Schedule("first", baseTime.Add(1*time.Second), func() {
		// Schedule a new timer that is also already due (at baseTime, which < now).
		sched.Schedule("second", baseTime, func() { secondFired = true })
	})

	fc.Advance(1 * time.Second)
	require.NoError(t, sched.Tick(context.Background()))
	assert.False(t, secondFired, "timer scheduled from a callback must NOT fire in the same Tick")

	// On the next Tick, it fires.
	require.NoError(t, sched.Tick(context.Background()))
	assert.True(t, secondFired, "timer scheduled from a callback MUST fire in the next Tick")
}

func TestNewMemSchedulerDefaultUsesSystemClock(t *testing.T) {
	// No clock option → uses clock.System(); a past-due timer fires on Tick.
	s := kernel.NewMemScheduler()
	fired := false
	s.Schedule("t1", time.Now().Add(-time.Second), func() { fired = true })
	require.NoError(t, s.Tick(t.Context()))
	assert.True(t, fired, "past-due timer should fire under the system clock")
}

func TestNewMemSchedulerWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	s := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fake))
	fired := false
	s.Schedule("t1", time.Unix(999, 0), func() { fired = true }) // fireAt <= fake now
	require.NoError(t, s.Tick(t.Context()))
	assert.True(t, fired, "timer due at the fake clock's now should fire")
}

// TestMemSchedulerNextFireAt verifies NextFireAt reports the earliest pending
// timer's fire time (and false when there are none).
func TestMemSchedulerNextFireAt(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		setup  func(s *kernel.MemScheduler)
		assert func(t *testing.T, at time.Time, ok bool)
	}

	cases := []testCase{
		{
			name:  "empty scheduler reports no timer",
			setup: func(*kernel.MemScheduler) {},
			assert: func(t *testing.T, at time.Time, ok bool) {
				assert.False(t, ok)
				assert.True(t, at.IsZero())
			},
		},
		{
			name: "single timer reports its fire time",
			setup: func(s *kernel.MemScheduler) {
				s.Schedule("only", baseTime.Add(5*time.Second), func() {})
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, baseTime.Add(5*time.Second), at)
			},
		},
		{
			name: "multiple timers report the earliest fire time",
			setup: func(s *kernel.MemScheduler) {
				s.Schedule("late", baseTime.Add(9*time.Second), func() {})
				s.Schedule("early", baseTime.Add(2*time.Second), func() {})
				s.Schedule("mid", baseTime.Add(4*time.Second), func() {})
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, baseTime.Add(2*time.Second), at)
			},
		},
		{
			name: "cancel of the earliest promotes the next",
			setup: func(s *kernel.MemScheduler) {
				s.Schedule("early", baseTime.Add(2*time.Second), func() {})
				s.Schedule("late", baseTime.Add(6*time.Second), func() {})
				s.Cancel("early")
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, baseTime.Add(6*time.Second), at)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClockAt(baseTime)
			sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))
			tc.setup(sched)

			at, ok := sched.NextFireAt()
			tc.assert(t, at, ok)
		})
	}
}

// TestMemSchedulerPending verifies Pending reports a specific timer's fire time
// by id (and false for absent/cancelled ids).
func TestMemSchedulerPending(t *testing.T) {
	t.Parallel()

	fc := clockwork.NewFakeClockAt(baseTime)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))
	sched.Schedule("known", baseTime.Add(3*time.Second), func() {})

	at, ok := sched.Pending("known")
	require.True(t, ok)
	assert.Equal(t, baseTime.Add(3*time.Second), at)

	_, ok = sched.Pending("unknown")
	assert.False(t, ok)

	sched.Cancel("known")
	_, ok = sched.Pending("known")
	assert.False(t, ok, "cancelled timer is no longer pending")
}

// TestMemSchedulerConcurrentSafe verifies that concurrent Schedule/Cancel calls
// and Tick do not race (exercise with -race).
func TestMemSchedulerConcurrentSafe(t *testing.T) {
	fc := clockwork.NewFakeClockAt(baseTime)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))

	var wg sync.WaitGroup
	const n = 50

	fc.Advance(2 * time.Second)

	for i := range n {
		wg.Add(1)
		id := "t" + string(rune('a'+i%26))
		go func(timerID string) {
			defer wg.Done()
			sched.Schedule(timerID, baseTime.Add(1*time.Second), func() {})
		}(id)
	}
	for range n / 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sched.Tick(context.Background())
		}()
	}
	wg.Wait()
}
