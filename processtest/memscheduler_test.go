package processtest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/scheduler"
)

// memBase is a fixed deterministic base for fake-clock MemScheduler tests.
var memBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// jobFor builds a scheduler.Job for MemScheduler tests whose action invokes
// fire (when non-nil).
func jobFor(t *testing.T, id string, trig scheduler.Trigger, fire func(), opts ...scheduler.JobOption) scheduler.Job {
	t.Helper()
	j, err := scheduler.NewJobWithID(id, "test.timer", trig,
		func(context.Context, scheduler.DataProvider) error {
			if fire != nil {
				fire()
			}
			return nil
		},
		scheduler.NewEmptyDataProvider(), opts...)
	require.NoError(t, err)
	return j
}

// mustSchedule schedules an Auto job and fails the test if it is rejected. It
// keeps the one-shot/order tests terse.
func mustSchedule(t *testing.T, s *processtest.MemScheduler, ctx context.Context, id string, trig scheduler.Trigger, fire func()) {
	t.Helper()
	if _, err := s.Schedule(ctx, jobFor(t, id, trig, fire)); err != nil {
		t.Fatalf("Schedule(%q): %v", id, err)
	}
}

// TestMemSchedulerTriggers exercises the trigger-kind support surface of the
// MemScheduler: a one-shot (After) fires exactly once, an Every re-arms and
// fires on each Tick, and an unsupported kind (Cron) reports
// scheduler.ErrUnsupportedTrigger.
func TestMemSchedulerTriggers(t *testing.T) {
	clk := processtest.NewFakeClock(memBase)
	s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(clk))
	ctx := t.Context()

	fired := 0
	if _, err := s.Schedule(ctx, jobFor(t, "t1", scheduler.After(time.Hour), func() { fired++ })); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour + time.Second)
	_ = s.Tick(ctx)
	if fired != 1 {
		t.Fatalf("one-shot fired %d", fired)
	}

	rec := 0
	if _, err := s.Schedule(ctx, jobFor(t, "t2", scheduler.Every(time.Minute), func() { rec++ })); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		clk.Advance(time.Minute + time.Second)
		_ = s.Tick(ctx)
	}
	if rec != 3 {
		t.Fatalf("recurring fired %d, want 3", rec)
	}

	_, err := s.Schedule(ctx, jobFor(t, "t3", scheduler.Cron(`0 9 * * *`), nil))
	require.ErrorIs(t, err, scheduler.ErrUnsupportedTrigger, "cron must be unsupported")
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

	mustSchedule(t, sched, ctx, "timer-b", scheduler.At(t1), record("timer-b")) // same fireAt as timer-a, ID sorts after
	mustSchedule(t, sched, ctx, "timer-a", scheduler.At(t2), record("timer-a")) // same fireAt, ID sorts before
	mustSchedule(t, sched, ctx, "timer-c", scheduler.At(t3), record("timer-c"))

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
	mustSchedule(t, sched, ctx, "cancel-me", scheduler.At(memBase.Add(1*time.Second)), func() { fired = true })
	require.NoError(t, sched.Cancel(ctx, "cancel-me"))

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
	mustSchedule(t, sched, ctx, "future", scheduler.At(memBase.Add(10*time.Second)), func() { fired = true })

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
	mustSchedule(t, sched, ctx, "first", scheduler.At(memBase.Add(1*time.Second)), func() {
		// Schedule a new timer that is also already due (at memBase, which < now).
		mustSchedule(t, sched, ctx, "second", scheduler.At(memBase), func() { secondFired = true })
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
	sj, err := sched.Schedule(ctx, jobFor(t, "rec", scheduler.Every(time.Minute), func() { count++ }))
	require.NoError(t, err)
	assert.Equal(t, memBase.Add(time.Minute), sj.NextRun(), "first run is now+interval")

	// Jump far past several intervals; a single Tick fires once and re-arms once.
	fc.Advance(5 * time.Minute)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, 1, count, "a recurring timer fires at most once per Tick")

	// The re-armed run is at the previous fireAt + interval.
	at, ok := sched.Pending("rec")
	require.True(t, ok)
	assert.Equal(t, memBase.Add(2*time.Minute), at)
}

func TestNewMemSchedulerDefaultUsesSystemClock(t *testing.T) {
	// No clock option → uses clock.System(); a past-due timer fires on Tick.
	s := processtest.NewMemScheduler()
	ctx := t.Context()
	fired := false
	mustSchedule(t, s, ctx, "t1", scheduler.At(time.Now().Add(-time.Second)), func() { fired = true })
	require.NoError(t, s.Tick(ctx))
	assert.True(t, fired, "past-due timer should fire under the system clock")
}

func TestNewMemSchedulerWithClockOption(t *testing.T) {
	fake := processtest.NewFakeClock(time.Unix(1000, 0).UTC())
	s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fake))
	ctx := t.Context()
	fired := false
	mustSchedule(t, s, ctx, "t1", scheduler.At(time.Unix(999, 0).UTC()), func() { fired = true }) // fireAt <= fake now
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
				mustSchedule(t, s, t.Context(), "only", scheduler.At(memBase.Add(5*time.Second)), nil)
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, memBase.Add(5*time.Second), at)
			},
		},
		{
			name: "multiple timers report the earliest fire time",
			setup: func(t *testing.T, s *processtest.MemScheduler) {
				mustSchedule(t, s, t.Context(), "late", scheduler.At(memBase.Add(9*time.Second)), nil)
				mustSchedule(t, s, t.Context(), "early", scheduler.At(memBase.Add(2*time.Second)), nil)
				mustSchedule(t, s, t.Context(), "mid", scheduler.At(memBase.Add(4*time.Second)), nil)
			},
			assert: func(t *testing.T, at time.Time, ok bool) {
				require.True(t, ok)
				assert.Equal(t, memBase.Add(2*time.Second), at)
			},
		},
		{
			name: "cancel of the earliest promotes the next",
			setup: func(t *testing.T, s *processtest.MemScheduler) {
				mustSchedule(t, s, t.Context(), "early", scheduler.At(memBase.Add(2*time.Second)), nil)
				mustSchedule(t, s, t.Context(), "late", scheduler.At(memBase.Add(6*time.Second)), nil)
				require.NoError(t, s.Cancel(t.Context(), "early"))
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
	mustSchedule(t, sched, ctx, "known", scheduler.At(memBase.Add(3*time.Second)), nil)

	at, ok := sched.Pending("known")
	require.True(t, ok)
	assert.Equal(t, memBase.Add(3*time.Second), at)

	_, ok = sched.Pending("unknown")
	assert.False(t, ok)

	require.NoError(t, sched.Cancel(ctx, "known"))
	_, ok = sched.Pending("known")
	assert.False(t, ok, "cancelled timer is no longer pending")
}

// recordingMemJobStore records Save/Delete ids for RegisterJobStore routing
// assertions.
type recordingMemJobStore struct {
	mu      sync.Mutex
	saves   []string
	deletes []string
}

func (s *recordingMemJobStore) Load(context.Context) ([]scheduler.ScheduledJob, error) {
	return nil, nil
}

func (s *recordingMemJobStore) Save(_ context.Context, j scheduler.ScheduledJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves = append(s.saves, j.ID())
	return nil
}

func (s *recordingMemJobStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, id)
	return nil
}

// TestMemSchedulerJobSurface covers the scheduler.Scheduler port surface on
// MemScheduler: manual jobs stay unarmed until Activate, RegisterJobStore
// routes persistence by kind, Tick fires only activated jobs, and Pending
// stays keyed by the engine timer id (job ids ARE engine timer ids).
func TestMemSchedulerJobSurface(t *testing.T) {
	t.Run("manual job stays unarmed until Activate", func(t *testing.T) {
		clk := processtest.NewFakeClock(memBase)
		s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(clk))
		ctx := t.Context()

		fired := 0
		sj, err := s.Schedule(ctx, jobFor(t, "man-1", scheduler.At(memBase.Add(time.Second)),
			func() { fired++ }, scheduler.WithManualActivation()))
		require.NoError(t, err)

		_, ok := s.Pending("man-1")
		assert.False(t, ok, "a manual job must not be pending before Activate")

		clk.Advance(2 * time.Second)
		require.NoError(t, s.Tick(ctx))
		assert.Zero(t, fired, "an unactivated manual job must not fire")

		require.NoError(t, s.Activate(ctx, sj))
		at, ok := s.Pending("man-1")
		require.True(t, ok, "an activated job is pending under its engine timer id")
		assert.Equal(t, memBase.Add(time.Second), at)

		require.NoError(t, s.Tick(ctx))
		assert.Equal(t, 1, fired, "the activated job fires on the next Tick")
	})

	t.Run("RegisterJobStore routes Schedule/Cancel persistence by kind", func(t *testing.T) {
		clk := processtest.NewFakeClock(memBase)
		s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(clk))
		ctx := t.Context()

		store := &recordingMemJobStore{}
		s.RegisterJobStore("test.timer", store)

		_, err := s.Schedule(ctx, jobFor(t, "routed-1", scheduler.At(memBase.Add(time.Second)), nil))
		require.NoError(t, err)
		assert.Equal(t, []string{"routed-1"}, store.saves, "Schedule must persist via the registered kind store")

		require.NoError(t, s.Cancel(ctx, "routed-1"))
		assert.Equal(t, []string{"routed-1"}, store.deletes, "Cancel must delete via the registered kind store")
	})

	t.Run("Tick fires only activated jobs", func(t *testing.T) {
		clk := processtest.NewFakeClock(memBase)
		s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(clk))
		ctx := t.Context()

		var fired []string
		_, err := s.Schedule(ctx, jobFor(t, "auto-a", scheduler.At(memBase.Add(time.Second)),
			func() { fired = append(fired, "auto-a") }))
		require.NoError(t, err)
		_, err = s.Schedule(ctx, jobFor(t, "man-b", scheduler.At(memBase.Add(time.Second)),
			func() { fired = append(fired, "man-b") }, scheduler.WithManualActivation()))
		require.NoError(t, err)

		clk.Advance(2 * time.Second)
		require.NoError(t, s.Tick(ctx))
		assert.Equal(t, []string{"auto-a"}, fired, "only the auto (activated) job fires")
	})

	t.Run("NextFireAt enumerates armed jobs only", func(t *testing.T) {
		clk := processtest.NewFakeClock(memBase)
		s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(clk))
		ctx := t.Context()

		_, err := s.Schedule(ctx, jobFor(t, "later", scheduler.At(memBase.Add(9*time.Second)), nil))
		require.NoError(t, err)
		_, err = s.Schedule(ctx, jobFor(t, "earlier-but-manual", scheduler.At(memBase.Add(time.Second)), nil,
			scheduler.WithManualActivation()))
		require.NoError(t, err)

		at, ok := s.NextFireAt()
		require.True(t, ok)
		assert.Equal(t, memBase.Add(9*time.Second), at,
			"NextFireAt must consider armed jobs only, not persisted-but-unactivated manual jobs")
	})

	t.Run("unsupported trigger kinds are rejected on Schedule and Activate", func(t *testing.T) {
		clk := processtest.NewFakeClock(memBase)
		s := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(clk))
		ctx := t.Context()

		_, err := s.Schedule(ctx, jobFor(t, "cron-1", scheduler.Cron("0 9 * * *"), nil))
		require.ErrorIs(t, err, scheduler.ErrUnsupportedTrigger,
			"MemScheduler understands only one-shot and Every triggers")

		manual := jobFor(t, "cron-2", scheduler.Cron("0 9 * * *"), nil, scheduler.WithManualActivation())
		next, ok := scheduler.Cron("0 9 * * *").Next(clk.Now())
		require.True(t, ok)
		msj, err := scheduler.NewScheduledJob(manual, next)
		require.NoError(t, err)
		require.ErrorIs(t, s.Activate(ctx, msj), scheduler.ErrUnsupportedTrigger)
	})
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
			_, _ = sched.Schedule(ctx, jobFor(t, timerID, scheduler.At(memBase.Add(1*time.Second)), nil))
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
