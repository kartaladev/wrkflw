package processtest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// memBase is a fixed deterministic base for fake-clock MemScheduler tests.
var memBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// mustSchedule schedules a timer and fails the test if the trigger is rejected.
// It keeps the migrated one-shot/order tests terse now that Schedule returns
// (time.Time, error).
func mustSchedule(t *testing.T, s *processtest.MemScheduler, ctx context.Context, id string, trig schedule.TriggerSpec, fire func()) {
	t.Helper()
	if _, err := s.Schedule(ctx, id, trig, fire); err != nil {
		t.Fatalf("Schedule(%q): %v", id, err)
	}
}

// TestMemSchedulerTriggers exercises the trigger-kind support surface of the
// relocated MemScheduler: a one-shot (KindOneTime) fires exactly once, an Every
// (KindDuration) re-arms and fires on each Tick, and an unsupported kind (Cron)
// returns kernel.ErrUnsupportedTrigger.
func TestMemSchedulerTriggers(t *testing.T) {
	clk := processtest.NewFakeClock(memBase)
	s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(clk))
	ctx := t.Context()

	fired := 0
	if _, err := s.Schedule(ctx, "t1", schedule.AfterDuration(time.Hour), func() { fired++ }); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour + time.Second)
	_ = s.Tick(ctx)
	if fired != 1 {
		t.Fatalf("one-shot fired %d", fired)
	}

	rec := 0
	if _, err := s.Schedule(ctx, "t2", schedule.Every(time.Minute), func() { rec++ }); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		clk.Advance(time.Minute + time.Second)
		_ = s.Tick(ctx)
	}
	if rec != 3 {
		t.Fatalf("recurring fired %d, want 3", rec)
	}

	if _, err := s.Schedule(ctx, "t3", schedule.Cron(`0 9 * * *`), func() {}); !errors.Is(err, kernel.ErrUnsupportedTrigger) {
		t.Fatalf("cron must be unsupported, got %v", err)
	}
}

// TestMemSchedulerTickFiresDue verifies that Tick fires only timers whose
// fireAt <= clock.Now(), in deterministic (fireAt, timerID) order.
func TestMemSchedulerTickFiresDue(t *testing.T) {
	fc := processtest.NewFakeClock(memBase)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	ctx := t.Context()

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
	t1 := memBase.Add(1 * time.Second)
	t2 := memBase.Add(1 * time.Second)
	t3 := memBase.Add(2 * time.Second)

	mustSchedule(t, sched, ctx, "timer-b", schedule.At(t1), record("timer-b")) // same fireAt as timer-a, ID sorts after
	mustSchedule(t, sched, ctx, "timer-a", schedule.At(t2), record("timer-a")) // same fireAt, ID sorts before
	mustSchedule(t, sched, ctx, "timer-c", schedule.At(t3), record("timer-c"))

	// Advance clock to t+1s: timer-a and timer-b should fire, NOT timer-c.
	fc.Advance(1 * time.Second)
	require.NoError(t, sched.Tick(ctx))

	mu.Lock()
	got := append([]string(nil), fired...)
	mu.Unlock()

	require.Len(t, got, 2, "only two timers due at t+1s should fire")
	// Deterministic order: (fireAt same, timerID lexicographic) → timer-a before timer-b.
	assert.Equal(t, []string{"timer-a", "timer-b"}, got)

	// Advance to t+2s: timer-c now fires.
	fc.Advance(1 * time.Second)
	require.NoError(t, sched.Tick(ctx))

	mu.Lock()
	got = append([]string(nil), fired...)
	mu.Unlock()

	require.Len(t, got, 3)
	assert.Equal(t, "timer-c", got[2])
}

// TestMemSchedulerCancelRemovesTimer verifies that Cancel prevents a timer
// from firing on the next Tick even if its fireAt <= clock.Now().
func TestMemSchedulerCancelRemovesTimer(t *testing.T) {
	fc := processtest.NewFakeClock(memBase)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	ctx := t.Context()

	fired := false
	mustSchedule(t, sched, ctx, "cancel-me", schedule.At(memBase.Add(1*time.Second)), func() { fired = true })
	sched.Cancel(ctx, "cancel-me")

	fc.Advance(2 * time.Second) // well past fireAt
	require.NoError(t, sched.Tick(ctx))

	assert.False(t, fired, "cancelled timer must not fire")
}

// TestMemSchedulerNotYetDueDoesNotFire verifies that a timer whose fireAt >
// clock.Now() does NOT fire even after a Tick.
func TestMemSchedulerNotYetDueDoesNotFire(t *testing.T) {
	fc := processtest.NewFakeClock(memBase)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	ctx := t.Context()

	fired := false
	mustSchedule(t, sched, ctx, "future", schedule.At(memBase.Add(10*time.Second)), func() { fired = true })

	fc.Advance(1 * time.Second) // only 1s, not 10s
	require.NoError(t, sched.Tick(ctx))

	assert.False(t, fired, "timer not yet due must not fire")
}

// TestMemSchedulerNewlyScheduledInCallbackNotFiredSameTick verifies the
// same-tick isolation guarantee: if a fire callback schedules a NEW timer that
// would also be due now, that new timer does NOT fire in the same Tick; it fires
// in the next Tick.
func TestMemSchedulerNewlyScheduledInCallbackNotFiredSameTick(t *testing.T) {
	fc := processtest.NewFakeClock(memBase)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	ctx := t.Context()

	secondFired := false
	mustSchedule(t, sched, ctx, "first", schedule.At(memBase.Add(1*time.Second)), func() {
		// Schedule a new timer that is also already due (at memBase, which < now).
		mustSchedule(t, sched, ctx, "second", schedule.At(memBase), func() { secondFired = true })
	})

	fc.Advance(1 * time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.False(t, secondFired, "timer scheduled from a callback must NOT fire in the same Tick")

	// On the next Tick, it fires.
	require.NoError(t, sched.Tick(ctx))
	assert.True(t, secondFired, "timer scheduled from a callback MUST fire in the next Tick")
}

// TestMemSchedulerRecurringReArmNotRefiredSameTick verifies that a recurring
// (Every) timer re-armed by a Tick is not fired again within that same Tick,
// even if the clock has advanced past several intervals.
func TestMemSchedulerRecurringReArmNotRefiredSameTick(t *testing.T) {
	fc := processtest.NewFakeClock(memBase)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	ctx := t.Context()

	count := 0
	next, err := sched.Schedule(ctx, "rec", schedule.Every(time.Minute), func() { count++ })
	require.NoError(t, err)
	assert.Equal(t, memBase.Add(time.Minute), next, "first run is now+interval")

	// Jump far past several intervals; a single Tick fires once and re-arms once.
	fc.Advance(5 * time.Minute)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, 1, count, "a recurring timer fires at most once per Tick")

	// The re-armed run is at the previous fireAt + interval.
	at, ok := sched.NextRun("rec")
	require.True(t, ok)
	assert.Equal(t, memBase.Add(2*time.Minute), at)
}

func TestNewMemSchedulerDefaultUsesSystemClock(t *testing.T) {
	// No clock option → uses clock.System(); a past-due timer fires on Tick.
	s := processtest.NewMemScheduler()
	ctx := t.Context()
	fired := false
	mustSchedule(t, s, ctx, "t1", schedule.At(time.Now().Add(-time.Second)), func() { fired = true })
	require.NoError(t, s.Tick(ctx))
	assert.True(t, fired, "past-due timer should fire under the system clock")
}

func TestNewMemSchedulerWithClockOption(t *testing.T) {
	fake := processtest.NewFakeClock(time.Unix(1000, 0).UTC())
	s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fake))
	ctx := t.Context()
	fired := false
	mustSchedule(t, s, ctx, "t1", schedule.At(time.Unix(999, 0).UTC()), func() { fired = true }) // fireAt <= fake now
	require.NoError(t, s.Tick(ctx))
	assert.True(t, fired, "timer due at the fake clock's now should fire")
}

// TestMemSchedulerNextFireAt verifies NextFireAt reports the earliest pending
// timer's fire time (and false when there are none).
func TestMemSchedulerNextFireAt(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		setup  func(t *testing.T, s *processtest.MemScheduler)
		assert func(t *testing.T, at time.Time, ok bool)
	}

	cases := []testCase{
		{
			name:  "empty scheduler reports no timer",
			setup: func(*testing.T, *processtest.MemScheduler) {},
			assert: func(t *testing.T, at time.Time, ok bool) {
				assert.False(t, ok)
				assert.True(t, at.IsZero())
			},
		},
		{
			name: "single timer reports its fire time",
			setup: func(t *testing.T, s *processtest.MemScheduler) {
				mustSchedule(t, s, t.Context(), "only", schedule.At(memBase.Add(5*time.Second)), func() {})
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, memBase.Add(5*time.Second), at)
			},
		},
		{
			name: "multiple timers report the earliest fire time",
			setup: func(t *testing.T, s *processtest.MemScheduler) {
				mustSchedule(t, s, t.Context(), "late", schedule.At(memBase.Add(9*time.Second)), func() {})
				mustSchedule(t, s, t.Context(), "early", schedule.At(memBase.Add(2*time.Second)), func() {})
				mustSchedule(t, s, t.Context(), "mid", schedule.At(memBase.Add(4*time.Second)), func() {})
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, memBase.Add(2*time.Second), at)
			},
		},
		{
			name: "cancel of the earliest promotes the next",
			setup: func(t *testing.T, s *processtest.MemScheduler) {
				mustSchedule(t, s, t.Context(), "early", schedule.At(memBase.Add(2*time.Second)), func() {})
				mustSchedule(t, s, t.Context(), "late", schedule.At(memBase.Add(6*time.Second)), func() {})
				s.Cancel(t.Context(), "early")
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, memBase.Add(6*time.Second), at)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := processtest.NewFakeClock(memBase)
			sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
			tc.setup(t, sched)

			at, ok := sched.NextFireAt()
			tc.assert(t, at, ok)
		})
	}
}

// TestMemSchedulerPending verifies Pending reports a specific timer's fire time
// by id (and false for absent/cancelled ids).
func TestMemSchedulerPending(t *testing.T) {
	t.Parallel()

	fc := processtest.NewFakeClock(memBase)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	ctx := t.Context()
	mustSchedule(t, sched, ctx, "known", schedule.At(memBase.Add(3*time.Second)), func() {})

	at, ok := sched.Pending("known")
	require.True(t, ok)
	assert.Equal(t, memBase.Add(3*time.Second), at)

	_, ok = sched.Pending("unknown")
	assert.False(t, ok)

	sched.Cancel(ctx, "known")
	_, ok = sched.Pending("known")
	assert.False(t, ok, "cancelled timer is no longer pending")
}

// TestMemSchedulerConcurrentSafe verifies that concurrent Schedule/Cancel calls
// and Tick do not race (exercise with -race).
func TestMemSchedulerConcurrentSafe(t *testing.T) {
	fc := processtest.NewFakeClock(memBase)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	ctx := t.Context()

	var wg sync.WaitGroup
	const n = 50

	fc.Advance(2 * time.Second)

	for i := range n {
		wg.Add(1)
		id := "t" + string(rune('a'+i%26))
		go func(timerID string) {
			defer wg.Done()
			_, _ = sched.Schedule(ctx, timerID, schedule.At(memBase.Add(1*time.Second)), func() {})
		}(id)
	}
	for range n / 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sched.Tick(ctx)
		}()
	}
	wg.Wait()
}
