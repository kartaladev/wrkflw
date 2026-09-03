package gocron_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// TestBumpRegression_OneShotFiresExactlyOnce locks the WithLimitedRuns(1)
// semantics of a one-shot timer across the gocron v2.22.0 bump:
// exactly one fire, and NextRun reports the timer as consumed (gone)
// afterwards. This characterizes CURRENT behaviour under v2.21.2 first (a
// regression lock, not a red-cycle symbol) so the bump can be verified to
// preserve it.
func TestBumpRegression_OneShotFiresExactlyOnce(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s, err := sched.NewGocronScheduler(sched.WithClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var n atomic.Int32
	_, err = s.ScheduleJob(t.Context(), "bump-t1", sched.After(time.Minute), func(context.Context) error {
		n.Add(1)
		return nil
	}, false)
	require.NoError(t, err)
	// Liveness canary: RECURRING, because the claim under test is "no SECOND
	// fire" and a scheduler that stopped delivering altogether satisfies that
	// too. The canary must reach its own second tick inside the same window in
	// which the one-shot must stay at one. See liveness_test.go.
	canary := newFireCanary(t, s, "bump-t1-canary", sched.Every(time.Minute))

	// MANDATORY barrier: wait until gocron armed BOTH timers before advancing,
	// else Advance can outrun the arm and the timer never fires.
	// (BlockUntilContext is a >= bound, so this must name the true count.)
	require.NoError(t, clk.BlockUntilContext(t.Context(), 2))
	clk.Advance(time.Minute + time.Second)
	// Eventually, not wg.Wait: a wg.Wait that never returns dies as an unnamed
	// 600s binary timeout printing no assertion message at all.
	require.Eventually(t, func() bool { return n.Load() >= 1 }, eventuallyBudget, 5*time.Millisecond,
		"the one-shot must fire at its due instant")
	requireCanaryFired(t, canary, 1)

	// Second due instant: the canary fires again, proving the scheduler is
	// still delivering — the one-shot must not.
	require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
	clk.Advance(time.Minute)
	requireCanaryFired(t, canary, 2)
	require.Never(t, func() bool { return n.Load() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
		"WithLimitedRuns(1) must retire the one-shot though the scheduler demonstrably still fires")

	// The consumed one-shot's map-cleanup runs via an async AfterJobRuns
	// listener — poll rather than asserting immediately.
	require.Eventually(t, func() bool {
		_, ok := s.NextRun("bump-t1")
		return !ok
	}, eventuallyBudget, 10*time.Millisecond, "consumed one-shot must report NextRun ok=false")
}
