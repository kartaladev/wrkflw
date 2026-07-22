package gocron_test

import (
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// TestBumpRegression_OneShotFiresExactlyOnce locks the WithLimitedRuns(1)
// semantics of a one-shot timer across the gocron v2.22.0 bump (ADR-0135):
// exactly one fire, and NextRun reports the timer as consumed (gone)
// afterwards. This characterizes CURRENT behaviour under v2.21.2 first (a
// regression lock, not a red-cycle symbol) so the bump can be verified to
// preserve it.
func TestBumpRegression_OneShotFiresExactlyOnce(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s, err := sched.NewGocronScheduler(sched.WithClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	wg.Add(1)
	var n int
	var mu sync.Mutex
	_, err = s.Schedule(t.Context(), "bump-t1", schedule.AfterDuration(time.Minute), func() {
		mu.Lock()
		n++
		mu.Unlock()
		wg.Done()
	})
	require.NoError(t, err)

	// MANDATORY barrier: wait until gocron armed its timer before advancing,
	// else Advance can outrun the arm and the timer never fires.
	require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
	clk.Advance(time.Minute + time.Second)
	wg.Wait() // executor goroutine actually ran the task

	// Confirm it never fires a second time.
	require.Never(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return n > 1
	}, 150*time.Millisecond, 10*time.Millisecond)

	// The consumed one-shot's map-cleanup runs via an async AfterJobRuns
	// listener — poll rather than asserting immediately.
	require.Eventually(t, func() bool {
		_, ok := s.NextRun("bump-t1")
		return !ok
	}, 2*time.Second, 10*time.Millisecond, "consumed one-shot must report NextRun ok=false")
}
