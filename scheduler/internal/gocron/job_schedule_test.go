package gocron_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// ctxObservation is what the one-shot task records about the ctx gocron handed
// it, captured synchronously from inside the task body. Both facts travel on ONE
// channel so a single receive delivers everything the case asserts on: two
// channels is two signals, and a wait that observes only one of them is the
// defect #86 catalogues.
type ctxObservation struct {
	nonNil bool
	live   bool
}

// TestGocronScheduler_ScheduleJob covers the Job-shaped scheduling entry
// point (ScheduleJob): a zero-parameter `func(context.Context) error` task
// registered against a TriggerDef, upsert-by-id semantics, singleton overrun
// protection, and the invalid-trigger error path. See TestGocronScheduler_
// Behaviour in scheduler_test.go for the identical per-case assert-closure
// shape this mirrors.
func TestGocronScheduler_ScheduleJob(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock)
	}

	cases := []tc{
		{
			name: "one-shot fires exactly once with a live, non-nil ctx",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				// Snapshot ctx's liveness synchronously, from inside the task,
				// before it returns. gocron cancels a completed one-shot job's
				// ctx asynchronously shortly after the task function returns
				// (its #925 fix defers that cancellation past task completion,
				// but does not delay it further) — inspecting the ctx object
				// itself from the test goroutine after receiving it would race
				// against that cleanup, so the task records what it observed
				// at fire time instead.
				obsCh := make(chan ctxObservation, 1)
				task := func(ctx context.Context) error {
					fired.Add(1)
					obsCh <- ctxObservation{nonNil: ctx != nil, live: ctx != nil && ctx.Err() == nil}
					return nil
				}

				next, err := s.ScheduleJob(t.Context(), "job-one-shot", sched.After(5*time.Second), task, false)
				require.NoError(t, err)
				require.False(t, next.IsZero(), "ScheduleJob must return the live first-run time")

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(6 * time.Second)

				// Wait for the observation itself, not for `fired`. `fired.Add(1)`
				// runs BEFORE the send, so a wait on the counter is satisfied while
				// obsCh is still empty — and the non-blocking receive that used to
				// follow took its `default` branch and failed a green run. Widening
				// that window with a 200ms sleep between the two writes made it
				// deterministic; gating on the value the assertions read removes it.
				// obs is written by the condition closure on testify's goroutine and
				// read here: ordered, because Eventually keeps one condition
				// goroutine in flight and the receive that ends the wait orders the
				// write before this read. It relies on require (not assert): on the
				// timeout path a straggler closure can still write obs, and only
				// FailNow stops this goroutine from reading it concurrently.
				var obs ctxObservation
				require.Eventually(t, func() bool {
					select {
					case obs = <-obsCh:
						return true
					default:
						return false
					}
				}, eventuallyBudget, 5*time.Millisecond,
					"the one-shot must fire and record the ctx it observed")

				assert.True(t, obs.nonNil, "task must receive a non-nil ctx from gocron")
				assert.True(t, obs.live, "ctx must not already be done at fire time")

				require.Never(t, func() bool { return fired.Load() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
					"one-shot must fire exactly once")
			},
		},
		{
			name: "upsert-by-id replaces the prior registration; only the second fires",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var firstFired, secondFired atomic.Int32
				first := func(context.Context) error { firstFired.Add(1); return nil }
				second := func(context.Context) error { secondFired.Add(1); return nil }

				_, err := s.ScheduleJob(t.Context(), "job-upsert", sched.At(clk.Now().Add(5*time.Second)), first, false)
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				_, err = s.ScheduleJob(t.Context(), "job-upsert", sched.At(clk.Now().Add(10*time.Second)), second, false)
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				clk.Advance(5 * time.Second) // old T+5 fire time — must NOT fire (replaced)
				require.Never(t, func() bool { return firstFired.Load() > 0 }, 150*time.Millisecond, 10*time.Millisecond,
					"the replaced registration must never fire")

				clk.Advance(5 * time.Second) // now at T+10 — the replacement fires
				require.Eventually(t, func() bool { return secondFired.Load() >= 1 }, eventuallyBudget, 5*time.Millisecond)

				assert.EqualValues(t, 0, firstFired.Load())
				assert.EqualValues(t, 1, secondFired.Load())
			},
		},
		{
			name: "singleton=true recurring job never overlaps a slow run",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var running, maxConcurrent atomic.Int32
				gate := make(chan struct{})

				task := func(context.Context) error {
					n := running.Add(1)
					defer running.Add(-1)
					for {
						cur := maxConcurrent.Load()
						if n <= cur || maxConcurrent.CompareAndSwap(cur, n) {
							break
						}
					}
					<-gate
					return nil
				}

				_, err := s.ScheduleJob(t.Context(), "job-singleton", sched.Every(time.Second), task, true)
				require.NoError(t, err)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second) // first due instant — starts the run, blocks on gate

				require.Eventually(t, func() bool { return running.Load() >= 1 }, eventuallyBudget, 5*time.Millisecond)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second) // second due instant while the first run is still blocked

				require.Never(t, func() bool { return running.Load() > 1 }, 200*time.Millisecond, 10*time.Millisecond,
					"singleton mode must never run two fires concurrently")

				close(gate) // release the blocked run
				require.Eventually(t, func() bool { return running.Load() == 0 }, eventuallyBudget, 5*time.Millisecond)

				assert.LessOrEqual(t, maxConcurrent.Load(), int32(1))
			},
		},
		{
			// Regression for a review Minor: WithSingletonMode used to be
			// appended even for one-shot jobs, which is meaningless (a
			// one-shot already runs at most once via WithLimitedRuns(1) —
			// there is nothing for it to overlap). ScheduleJob now guards the
			// option with singleton && !oneShot. This case only proves the
			// combination is still accepted without error and still fires
			// exactly once; it does not distinguish "guard applied" from
			// "gocron silently tolerates the redundant option" — see the
			// package doc note on this test for that caveat.
			name: "singleton=true is a no-op for one-shot triggers",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				task := func(context.Context) error { fired.Add(1); return nil }

				next, err := s.ScheduleJob(t.Context(), "job-singleton-oneshot", sched.After(5*time.Second), task, true)
				require.NoError(t, err, "singleton=true combined with a one-shot trigger must not error")
				require.False(t, next.IsZero())

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(6 * time.Second)

				require.Eventually(t, func() bool { return fired.Load() >= 1 }, eventuallyBudget, 5*time.Millisecond)
				require.Never(t, func() bool { return fired.Load() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
					"one-shot must still fire exactly once regardless of singleton")
			},
		},
		{
			name: "zero TriggerDef is rejected wrapping the engine's unsupported-trigger sentinel",
			assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
				next, err := s.ScheduleJob(t.Context(), "job-invalid", sched.TriggerDef{}, func(context.Context) error { return nil }, false)
				require.Error(t, err)
				require.ErrorIs(t, err, sched.ErrUnsupportedTrigger)
				assert.True(t, next.IsZero())
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(sched.WithClock(clk))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			c.assert(t, s, clk)
		})
	}
}

// TestGocronScheduleJob_AfterClose_ReturnsSentinelNotFalseFire is a
// regression test for a false positive the race fix introduced: the
// fireImmediately branch reported a fire time (s.clk.Now(), nil error) for a
// past-due one-shot even after Close, because Close only shuts gocron down —
// gocron's own NewJob succeeds silently on a shut-down scheduler, and
// ScheduleJob had no closed-state check of its own. Not reachable through
// the public scheduler.NativeScheduler (which guards with its own
// scheduler.ErrSchedulerClosed before ever reaching this internal package),
// but the CONTRACT of this internal API is what is being hardened, not only
// its one current caller.
func TestGocronScheduleJob_AfterClose_ReturnsSentinelNotFalseFire(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s, err := sched.NewGocronScheduler(sched.WithClock(clk))
	require.NoError(t, err)
	require.NoError(t, s.Close())

	// A past-due one-shot: exactly the fireImmediately shape that used to
	// report a false fire time after Close.
	fireAt := clk.Now().Add(-1 * time.Second)
	next, err := s.ScheduleJob(t.Context(), "job-after-close", sched.At(fireAt), func(context.Context) error { return nil }, false)
	require.Error(t, err)
	require.ErrorIs(t, err, sched.ErrSchedulerClosed)
	assert.True(t, next.IsZero(), "a closed scheduler must not report a fire time for a job it will never run")
}

// TestGocronScheduleJob_PastDueDurationOneShot is the regression test for a
// past-due one-shot expressed as a DURATION (After(-d), After(0)) that used
// to be refused outright. jobDefinition's duration branch mapped it to
// gocron.OneTimeJobStartDateTime(now.Add(d)) with no past-due guard — unlike
// the hardened absolute-time (At) branch —
// so gocron rejected it and the raw string
// "gocron: OneTimeJob: start must not be in the past" escaped ScheduleJob
// with no workflow-scheduler: sentinel wrap, alongside a zero next-run.
//
// What made these cases fail before the fix (observed, not reasoned):
// require.NoError failed on the first line with exactly that gocron string.
// Deliberately asserts on the error being absent and on next's VALUE, never
// on gocron's message text.
func TestGocronScheduleJob_PastDueDurationOneShot(t *testing.T) {
	type tc struct {
		name   string
		trig   sched.TriggerDef
		assert func(t *testing.T, clk *clockwork.FakeClock, fired func() int32, next time.Time, err error)
	}

	cases := []tc{
		{
			name: "After(-1m) fires immediately instead of being refused",
			trig: sched.After(-1 * time.Minute),
			assert: func(t *testing.T, clk *clockwork.FakeClock, fired func() int32, next time.Time, err error) {
				require.NoError(t, err, "a past-due one-shot expressed as a duration must not be refused")
				require.False(t, next.IsZero(), "a job that will fire must not report a zero next-run")
				// The fake clock is never advanced in this case, so the
				// fire-immediately branch must report exactly clk.Now() —
				// "not zero" alone would not distinguish a correct answer
				// from now.Add(-1m), which is what the unguarded branch
				// handed to gocron.
				assert.True(t, next.Equal(clk.Now()),
					"want exactly the fake clock's current time %v, got %v", clk.Now(), next)
				require.Eventually(t, func() bool { return fired() >= 1 }, eventuallyBudget, 5*time.Millisecond,
					"a past-due one-shot must fire without any clock advance")
				// Refutes the re-arm livelock this guard could plausibly have
				// introduced (an arm whose next-run is still past-due,
				// re-firing forever). WithLimitedRuns(1) must
				// retire it after one fire. The Eventually above is this
				// Never's liveness precondition — the job demonstrably fired.
				require.Never(t, func() bool { return fired() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
					"a past-due one-shot must fire ONCE, not re-arm itself into a loop")
			},
		},
		{
			name: "After(0) fires immediately instead of being refused",
			trig: sched.After(0),
			assert: func(t *testing.T, clk *clockwork.FakeClock, fired func() int32, next time.Time, err error) {
				require.NoError(t, err, "a zero-duration one-shot must not be refused")
				require.False(t, next.IsZero())
				assert.True(t, next.Equal(clk.Now()),
					"want exactly the fake clock's current time %v, got %v", clk.Now(), next)
				require.Eventually(t, func() bool { return fired() >= 1 }, eventuallyBudget, 5*time.Millisecond,
					"a zero-duration one-shot must fire without any clock advance")
				require.Never(t, func() bool { return fired() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
					"a past-due one-shot must fire ONCE, not re-arm itself into a loop")
			},
		},
		{
			// Control: the future duration form must keep its ordinary
			// behaviour — armed, not fired, until the clock reaches it. Without
			// this row a fix that made EVERY duration one-shot fire immediately
			// would pass the two rows above.
			name: "After(+5s) still waits for the clock",
			trig: sched.After(5 * time.Second),
			assert: func(t *testing.T, clk *clockwork.FakeClock, fired func() int32, next time.Time, err error) {
				require.NoError(t, err)
				require.False(t, next.IsZero())
				assert.True(t, next.After(clk.Now()), "a future one-shot must report a future next-run, got %v", next)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				assert.Zero(t, fired(), "a future one-shot must not have fired before its due instant")
				clk.Advance(6 * time.Second)
				require.Eventually(t, func() bool { return fired() >= 1 }, eventuallyBudget, 5*time.Millisecond)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(sched.WithClock(clk))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			var fired atomic.Int32
			task := func(context.Context) error { fired.Add(1); return nil }

			next, scheduleErr := s.ScheduleJob(t.Context(), "job-pastdue-duration", c.trig, task, false)
			c.assert(t, clk, fired.Load, next, scheduleErr)
		})
	}
}

// TestGocronScheduleJob_NewJobErrorIsWrapped is the second half of backlog
// 49: every error ScheduleJob returns must carry this engine's
// "workflow-scheduler:" prefix, so a raw vendor string never reaches a
// consumer as if it were our own vocabulary. jobDefinition validates only the
// trigger KIND — a well-formed kind carrying a nonsense value (Every(0), a
// malformed cron expression) is rejected later, by gocron's own NewJob, and
// that error used to be returned verbatim.
//
// What made these cases fail before the fix (observed, not reasoned): the
// returned errors were exactly "gocron: DurationJob: time interval is 0" and
// "gocron: CronJob: crontab parse failure…", neither of which contains
// "workflow-scheduler:". The underlying cause must still be reachable, so
// each case also asserts the vendor text survives the wrap.
func TestGocronScheduleJob_NewJobErrorIsWrapped(t *testing.T) {
	type tc struct {
		name  string
		trig  sched.TriggerDef
		cause string // vendor text that must survive the wrap
	}

	cases := []tc{
		{name: "zero recurring interval", trig: sched.Every(0), cause: "DurationJob"},
		{name: "malformed cron expression", trig: sched.Cron("not a cron"), cause: "CronJob"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(sched.WithClock(clk))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			next, scheduleErr := s.ScheduleJob(t.Context(), "job-newjob-error", c.trig, func(context.Context) error { return nil }, false)
			require.Error(t, scheduleErr)
			assert.True(t, next.IsZero(), "a job that was never registered must not report a fire time")
			assert.ErrorContains(t, scheduleErr, "workflow-scheduler: ScheduleJob \"job-newjob-error\"",
				"a vendor error must not escape without this engine's own vocabulary")
			assert.ErrorContains(t, scheduleErr, c.cause, "the wrap must preserve the underlying cause")
		})
	}
}

// TestGocronScheduleJob_PastDueOneShot_NextRunNeverZero is a regression test
// for a race between ScheduleJob's own return value and gocron's internal
// firing of a past-due one-shot job. jobDefinition maps a past-due At trigger
// to gocron.OneTimeJob(gocron.OneTimeJobStartImmediately()) — gocron starts
// that job on its own goroutine as soon as NewJob registers it. Because
// WithLimitedRuns(1) is also set, the job can fire and self-retire from
// gocron's own bookkeeping before ScheduleJob's subsequent job.NextRun()
// call runs, in which case NextRun() truthfully reports "no next run" as the
// zero time — but ScheduleJob returns that zero time alongside a NIL error,
// silently claiming no fire is scheduled for a timer that fired correctly.
//
// A single call reproduces the race at a rate that depends on the test
// mode: measured (fresh re-derivation against reverted production code, 7
// runs × 1,000 arms each) **~12 % of arms without `-race`** (848/7,000) and
// **~0.9 % under `-race`** (63/7,000) — `-race`'s added synchronization
// narrows, but does not close, the window. Because the two rates differ by
// roughly 13×, a loop count picked against the higher (`-race`) rate is not
// safe for a plain `go test` run: 200 iterations at ~12 % gave a false-green
// pass on reverted code in roughly 1 run of 5 under `-race` (measured: 3 of
// 5 runs failed, 2 passed on definitely-broken code — an unacceptable
// regression-guard reliability). Looping 5,000 times instead was measured
// 6/6 reliably RED against the same reverted code, and its own runtime on
// the fixed (green) path is ~0.2-0.4s — negligible against the package's
// test budget.
//
// This stays a standalone Test rather than folding into
// TestGocronScheduler_ScheduleJob's assert-closure table: every other case
// in that table runs once, while this one is a 5,000-iteration statistical
// loop whose cost and shape (a single tight loop, no clock advances, no
// goroutine synchronization) do not fit the table's per-case setup/teardown
// pattern.
func TestGocronScheduleJob_PastDueOneShot_NextRunNeverZero(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s, err := sched.NewGocronScheduler(sched.WithClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	const iterations = 5000
	for i := range iterations {
		id := fmt.Sprintf("race-pastdue-%d", i)
		// -1s (not -10m): still exceeds no time-skew tolerance boundary check
		// (!at.After(now) is satisfied by any past instant) so the
		// fireImmediately branch is still taken, but stays well under the
		// default 5-minute skew tolerance so no WARN is logged per
		// iteration — 5,000 WARN lines would otherwise flood test output.
		fireAt := clk.Now().Add(-1 * time.Second)
		next, err := s.ScheduleJob(t.Context(), id, sched.At(fireAt), func(context.Context) error { return nil }, false)
		require.NoError(t, err)
		require.Falsef(t, next.IsZero(),
			"iteration %d: ScheduleJob returned a zero next-run (nil error) for a past-due one-shot that fires immediately", i)
		// Pins the actual VALUE, not merely its non-zero-ness: the clock is
		// fake and never advanced in this loop, so the fire-immediately
		// branch must report exactly clk.Now(). "Not zero" alone does not
		// distinguish a correct answer from e.g. an arbitrarily offset one.
		require.Truef(t, next.Equal(clk.Now()),
			"iteration %d: ScheduleJob returned %v, want exactly the fake clock's current time %v", i, next, clk.Now())
	}
}
