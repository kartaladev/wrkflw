package scheduler_test

// The test functions below cover the scheduler.Scheduler port surface on the
// concrete NativeScheduler. They are grouped per concern (Schedule / Activate /
// Deactivate+Cancel / Scheduled+List / rehydration) as standalone functions
// with subtests rather than one table: each concern's cases require
// structurally different setup (fake-clock advances, store wiring, slog
// capture, Start sequencing), so the shared-call-shape table assumption breaks
// down.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

// recordingJobStore is a scheduler.JobStore fake that records Save/Delete ids
// and Load invocations (including the ctx each Load was called with).
type recordingJobStore struct {
	mu       sync.Mutex
	saves    []string
	deletes  []string
	loadFn   func(ctx context.Context) ([]scheduler.ScheduledJob, error)
	loadCtxs []context.Context
}

func (s *recordingJobStore) Load(ctx context.Context) ([]scheduler.ScheduledJob, error) {
	s.mu.Lock()
	s.loadCtxs = append(s.loadCtxs, ctx)
	fn := s.loadFn
	s.mu.Unlock()
	if fn == nil {
		return nil, nil
	}
	return fn(ctx)
}

func (s *recordingJobStore) Save(_ context.Context, j scheduler.ScheduledJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves = append(s.saves, j.ID())
	return nil
}

func (s *recordingJobStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, id)
	return nil
}

func (s *recordingJobStore) savedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.saves...)
}

func (s *recordingJobStore) deletedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deletes...)
}

func (s *recordingJobStore) loadContexts() []context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]context.Context(nil), s.loadCtxs...)
}

// mustJob builds a Job whose action invokes fire (when non-nil). It is the
// shared job-construction helper for the port-surface tests and the ported
// elector/locker/clock/timeskew façade tests.
func mustJob(t *testing.T, id string, kind scheduler.JobKind, trig scheduler.Trigger, fire func(), opts ...scheduler.JobOption) scheduler.Job {
	t.Helper()
	j, err := scheduler.NewJobWithID(id, kind, trig,
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

// listIDs collects the ids yielded by List.
func listIDs(ctx context.Context, s scheduler.Scheduler) []string {
	var ids []string
	for sj := range s.List(ctx) {
		ids = append(ids, sj.ID())
	}
	return ids
}

const surfaceKind scheduler.JobKind = "test.kind"

func TestNativeSchedulerSchedule(t *testing.T) {
	t.Run("auto job persists via the registered store AND arms", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		store := &recordingJobStore{}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		fired := make(chan struct{}, 1)
		trig := scheduler.At(clk.Now().Add(3 * time.Second))
		sj, err := s.Schedule(t.Context(), mustJob(t, "auto-1", surfaceKind, trig, func() { fired <- struct{}{} }))
		require.NoError(t, err)
		require.NotNil(t, sj)
		assert.Equal(t, []string{"auto-1"}, store.savedIDs(), "auto Schedule must persist through the kind's store")

		wantNext, ok := trig.Next(clk.Now())
		require.True(t, ok)
		assert.True(t, sj.NextRun().Equal(wantNext), "returned NextRun must be trig.Next(now)")

		require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
		clk.Advance(3 * time.Second)
		select {
		case <-fired:
		case <-time.After(2 * time.Second):
			t.Fatal("auto job must be armed and fire on clock advance")
		}
	})

	t.Run("manual job persists but leaves NO scheduler record", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		store := &recordingJobStore{}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		var fired atomic.Int32
		trig := scheduler.At(clk.Now().Add(3 * time.Second))
		sj, err := s.Schedule(t.Context(),
			mustJob(t, "manual-1", surfaceKind, trig, func() { fired.Add(1) }, scheduler.WithManualActivation()))
		require.NoError(t, err)
		assert.Equal(t, []string{"manual-1"}, store.savedIDs(), "manual Schedule must still persist")

		// The returned value carries the computed NextRun for the CALLER only.
		wantNext, ok := trig.Next(clk.Now())
		require.True(t, ok)
		assert.True(t, sj.NextRun().Equal(wantNext))

		// No scheduler record: Scheduled errors, List is empty.
		_, err = s.Scheduled(t.Context(), "manual-1")
		require.ErrorIs(t, err, scheduler.ErrJobNotFound)
		assert.Empty(t, listIDs(t.Context(), s))

		// Not armed: advancing past the due instant must not fire.
		clk.Advance(5 * time.Second)
		assert.Never(t, func() bool { return fired.Load() > 0 }, 150*time.Millisecond, 10*time.Millisecond,
			"a manual job must not fire before Activate")
	})

	t.Run("unregistered kind schedules in-memory only without WARN", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		h := &captureHandlerFacade{}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithLogger(slog.New(h)),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		fired := make(chan struct{}, 1)
		_, err = s.Schedule(t.Context(),
			mustJob(t, "mem-only-1", "unregistered.kind", scheduler.At(clk.Now().Add(time.Second)), func() { fired <- struct{}{} }))
		require.NoError(t, err)

		require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
		clk.Advance(time.Second)
		select {
		case <-fired:
		case <-time.After(2 * time.Second):
			t.Fatal("an unregistered-kind auto job must still arm in-memory")
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		for _, r := range h.records {
			assert.NotEqual(t, slog.LevelWarn, r.Level,
				"scheduling an unregistered kind is a supported in-memory mode — no WARN expected, got: %s", r.Message)
		}
	})
}

func TestNativeSchedulerActivate(t *testing.T) {
	t.Run("arms a manual job and upserts by id (double-Activate fires once)", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		s, err := scheduler.NewScheduler(scheduler.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		var fired atomic.Int32
		trig := scheduler.At(clk.Now().Add(3 * time.Second))
		sj, err := s.Schedule(t.Context(),
			mustJob(t, "act-1", surfaceKind, trig, func() { fired.Add(1) }, scheduler.WithManualActivation()))
		require.NoError(t, err)

		require.NoError(t, s.Activate(t.Context(), sj))
		require.NoError(t, s.Activate(t.Context(), sj), "Activate must be an idempotent upsert by id")

		got, err := s.Scheduled(t.Context(), "act-1")
		require.NoError(t, err, "an activated job must have a scheduler record")
		assert.Equal(t, "act-1", got.ID())

		require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
		clk.Advance(4 * time.Second)
		require.Eventually(t, func() bool { return fired.Load() >= 1 }, 2*time.Second, 5*time.Millisecond)
		assert.Never(t, func() bool { return fired.Load() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
			"double-Activate must leave exactly ONE live registration (one fire per due instant)")
	})
}

func TestNativeSchedulerDeactivateCancel(t *testing.T) {
	t.Run("Deactivate disarms without Delete", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		store := &recordingJobStore{}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		var fired atomic.Int32
		_, err = s.Schedule(t.Context(),
			mustJob(t, "deact-1", surfaceKind, scheduler.At(clk.Now().Add(3*time.Second)), func() { fired.Add(1) }))
		require.NoError(t, err)

		require.NoError(t, s.Deactivate(t.Context(), "deact-1"))
		assert.Empty(t, store.deletedIDs(), "Deactivate must NOT touch the durable store")

		_, err = s.Scheduled(t.Context(), "deact-1")
		require.ErrorIs(t, err, scheduler.ErrJobNotFound, "a deactivated job has no scheduler record")

		clk.Advance(5 * time.Second)
		assert.Never(t, func() bool { return fired.Load() > 0 }, 150*time.Millisecond, 10*time.Millisecond,
			"a deactivated job must not fire")
	})

	t.Run("Deactivate of an unknown id is nil", func(t *testing.T) {
		s, err := scheduler.NewScheduler(scheduler.WithClock(clockwork.NewFakeClock()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		require.NoError(t, s.Deactivate(t.Context(), "nope"))
	})

	t.Run("Cancel deletes from the store and disarms", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		store := &recordingJobStore{}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		var fired atomic.Int32
		_, err = s.Schedule(t.Context(),
			mustJob(t, "cancel-1", surfaceKind, scheduler.At(clk.Now().Add(3*time.Second)), func() { fired.Add(1) }))
		require.NoError(t, err)

		require.NoError(t, s.Cancel(t.Context(), "cancel-1"))
		assert.Equal(t, []string{"cancel-1"}, store.deletedIDs(), "Cancel must Delete the durable record")

		_, err = s.Scheduled(t.Context(), "cancel-1")
		require.ErrorIs(t, err, scheduler.ErrJobNotFound)

		clk.Advance(5 * time.Second)
		assert.Never(t, func() bool { return fired.Load() > 0 }, 150*time.Millisecond, 10*time.Millisecond,
			"a cancelled job must not fire")
	})

	t.Run("Cancel of an unknown id is nil and touches no store", func(t *testing.T) {
		store := &recordingJobStore{}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clockwork.NewFakeClock()),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		require.NoError(t, s.Cancel(t.Context(), "nope"))
		assert.Empty(t, store.deletedIDs())
	})
}

func TestNativeSchedulerScheduledAndList(t *testing.T) {
	t.Run("Scheduled unknown id reports ErrJobNotFound", func(t *testing.T) {
		s, err := scheduler.NewScheduler(scheduler.WithClock(clockwork.NewFakeClock()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		_, err = s.Scheduled(t.Context(), "ghost")
		require.ErrorIs(t, err, scheduler.ErrJobNotFound)
	})

	t.Run("List yields exactly the armed jobs", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		s, err := scheduler.NewScheduler(scheduler.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		far := scheduler.At(clk.Now().Add(time.Hour))
		_, err = s.Schedule(t.Context(), mustJob(t, "armed-a", surfaceKind, far, nil))
		require.NoError(t, err)
		_, err = s.Schedule(t.Context(), mustJob(t, "armed-b", surfaceKind, far, nil))
		require.NoError(t, err)
		// A manual job leaves no record and must not appear.
		_, err = s.Schedule(t.Context(), mustJob(t, "manual-c", surfaceKind, far, nil, scheduler.WithManualActivation()))
		require.NoError(t, err)

		assert.ElementsMatch(t, []string{"armed-a", "armed-b"}, listIDs(t.Context(), s))
	})

	t.Run("after Close, Scheduled and List report no armed jobs", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		s, err := scheduler.NewScheduler(scheduler.WithClock(clk))
		require.NoError(t, err)

		far := scheduler.At(clk.Now().Add(time.Hour))
		_, err = s.Schedule(t.Context(), mustJob(t, "closed-a", surfaceKind, far, nil))
		require.NoError(t, err)

		require.NoError(t, s.Close())

		_, err = s.Scheduled(t.Context(), "closed-a")
		require.ErrorIs(t, err, scheduler.ErrJobNotFound,
			"Close must clear the armed map so Scheduled reports not-found")
		assert.Empty(t, listIDs(t.Context(), s),
			"Close must clear the armed map so List yields nothing")
	})
}

// startCtxKey is a context key used to prove rehydration does NOT run on the
// Start caller's context.
type startCtxKey struct{}

func TestNativeSchedulerRehydration(t *testing.T) {
	// manualScheduled builds a Manual ScheduledJob as a JobStore.Load would.
	manualScheduled := func(t *testing.T, id string, trig scheduler.Trigger, fire func()) scheduler.ScheduledJob {
		t.Helper()
		j := mustJob(t, id, surfaceKind, trig, fire, scheduler.WithManualActivation())
		next, ok := trig.Next(time.Now())
		require.True(t, ok)
		sj, err := scheduler.NewScheduledJob(j, next)
		require.NoError(t, err)
		return sj
	}

	t.Run("Start activates every loaded job exactly once, on a background ctx", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		store := &recordingJobStore{}
		far := scheduler.At(clk.Now().Add(time.Hour))
		store.loadFn = func(context.Context) ([]scheduler.ScheduledJob, error) {
			return []scheduler.ScheduledJob{
				manualScheduled(t, "rehy-1", far, nil),
				manualScheduled(t, "rehy-2", far, nil),
			}, nil
		}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		startCtx := context.WithValue(t.Context(), startCtxKey{}, "caller")
		require.NoError(t, s.Start(startCtx))

		_, err = s.Scheduled(t.Context(), "rehy-1")
		require.NoError(t, err, "rehydrated job rehy-1 must be armed after Start")
		_, err = s.Scheduled(t.Context(), "rehy-2")
		require.NoError(t, err, "rehydrated job rehy-2 must be armed after Start")

		// A second Start must not duplicate registrations (Load runs once).
		require.NoError(t, s.Start(startCtx))
		assert.ElementsMatch(t, []string{"rehy-1", "rehy-2"}, listIDs(t.Context(), s))
		require.Len(t, store.loadContexts(), 1, "Load must run exactly once across Starts")

		// Rehydration I/O must run on a background-derived ctx, never the
		// Start caller's (possibly transaction-scoped) ctx.
		loadCtx := store.loadContexts()[0]
		assert.Nil(t, loadCtx.Value(startCtxKey{}),
			"rehydration Load must NOT receive the Start caller's ctx")
	})

	t.Run("unresolved-definitions Load error is non-fatal and WARN-logged", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		h := &captureHandlerFacade{}
		store := &recordingJobStore{}
		far := scheduler.At(clk.Now().Add(time.Hour))
		store.loadFn = func(context.Context) ([]scheduler.ScheduledJob, error) {
			return []scheduler.ScheduledJob{manualScheduled(t, "partial-1", far, nil)},
				fmt.Errorf("2 jobs skipped: %w", scheduler.ErrUnresolvedTimerDefinitions)
		}
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithLogger(slog.New(h)),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		require.NoError(t, s.Start(t.Context()), "unresolved definitions must be non-fatal")

		_, err = s.Scheduled(t.Context(), "partial-1")
		require.NoError(t, err, "the partial (resolvable) jobs must still be armed")

		h.mu.Lock()
		defer h.mu.Unlock()
		var warned bool
		for _, r := range h.records {
			if r.Level == slog.LevelWarn {
				warned = true
			}
		}
		assert.True(t, warned, "unresolved definitions must be WARN-logged")
	})

	t.Run("an infrastructure Load error is fatal to Start", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		store := &recordingJobStore{}
		infraErr := errors.New("db down")
		store.loadFn = func(context.Context) ([]scheduler.ScheduledJob, error) { return nil, infraErr }
		s, err := scheduler.NewScheduler(
			scheduler.WithClock(clk),
			scheduler.WithJobStore(surfaceKind, func() scheduler.JobStore { return store }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })

		require.ErrorIs(t, s.Start(t.Context()), infraErr,
			"a genuine store failure must surface to the explicit Start caller")
	})
}
