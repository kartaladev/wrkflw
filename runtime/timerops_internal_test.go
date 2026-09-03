package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// noRecurring is an armedRecurring lookup that reports every timer as a
// determinable non-recurring timer, i.e. the safe default: a fired timer is
// always consumed.
func noRecurring(string) (bool, bool) { return false, true }

// undeterminable is an armedRecurring lookup that cannot answer — the store
// failed. The fired timer must be left alone.
func undeterminable(string) (bool, bool) { return false, false }

// timerOpsDef is a minimal definition carrier for timerJobsFor (which only
// reads ID/Version and builds fire callbacks from it).
func timerOpsDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "d",
		Version: 1,
		Nodes:   []model.Node{event.NewStart("start"), event.NewEnd("end")},
	}
}

// TestTimerJobsFor covers the single derivation site for timer side-effects:
// ScheduleTimer commands become Manual timerJobs whose
// spec.NextRun is the converted trigger's Next(now) in UTC (subsuming the
// retired nextRunFor — including the UTC and original-instant guarantees);
// CancelTimer commands and consumed TimerFired triggers become PK-exact
// cancelKeys.
func TestTimerJobsFor(t *testing.T) {
	at := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	absTime := time.Date(2026, 6, 22, 15, 30, 0, 0, time.UTC)
	oneShot := schedule.AfterDuration(time.Hour)
	recurring := schedule.Every(15 * time.Minute)

	cases := []struct {
		name    string
		cmds    []engine.Command
		trg     engine.Trigger
		armedFn func(string) (bool, bool)
		assert  func(t *testing.T, arms []*timerJob, cancels []cancelKey)
	}{
		{
			name:    "ScheduleTimer becomes a Manual arm job carrying its Trigger",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: oneShot, Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				assert.Equal(t, "t1", spec.TimerID)
				assert.Equal(t, "i1", spec.InstanceID)
				assert.Equal(t, "d", spec.DefID)
				assert.Equal(t, 1, spec.DefVersion)
				assert.Equal(t, oneShot, spec.Trigger)
				assert.Equal(t, engine.TimerIntermediate, spec.Kind)
				assert.True(t, spec.NextRun.Equal(at.Add(time.Hour)),
					"one-shot AfterDuration NextRun must be now+duration (original instant, crash-safe): want %v got %v", at.Add(time.Hour), spec.NextRun)
				assert.Equal(t, time.UTC, spec.NextRun.Location(), "next run must be UTC-located")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "At one-shot arm persists the absolute time (UTC) even when built in another zone",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: schedule.At(absTime.In(time.FixedZone("x", 3600))), Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				assert.True(t, spec.NextRun.Equal(absTime), "At one-shot must persist its absolute instant: want %v got %v", absTime, spec.NextRun)
				assert.Equal(t, time.UTC, spec.NextRun.Location(), "next run must be UTC-located")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "cron arm persists the REAL next occurrence (closes the interim zero-NextRun gap)",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: schedule.Cron("0 9 * * *"), Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				want := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC) // next 09:00 after 2026-06-22 11:00 UTC
				assert.True(t, spec.NextRun.Equal(want),
					"cron next_run must be the trigger's real next occurrence: want %v got %v", want, spec.NextRun)
				assert.Equal(t, time.UTC, spec.NextRun.Location(), "next run must be UTC-located")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "unset trigger is unschedulable: skipped entirely (no arm, no row)",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "t1", Trigger: schedule.TriggerSpec{}, Kind: engine.TimerIntermediate}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms, "an unconvertible trigger must be WARN-skipped, never armed")
				assert.Empty(t, cancels)
			},
		},
		{
			name:    "CancelTimer becomes a PK-exact cancel key",
			cmds:    []engine.Command{engine.CancelTimer{TimerID: "t1"}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "t1"}}, cancels)
			},
		},
		{
			name:    "TimerFired of a non-recurring timer cancels (consumes) it",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "t1"),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "t1"}}, cancels)
			},
		},
		{
			name: "TimerFired of a RECURRING timer does NOT cancel it (survives fire)",
			cmds: nil,
			trg:  engine.NewTimerFired(at, "rec-1"),
			armedFn: func(id string) (bool, bool) {
				return id == "rec-1", true
			},
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Empty(t, cancels, "a recurring timer must survive its fire; the native scheduler re-arms it")
			},
		},
		{
			name:    "TimerFired whose recurrence is UNDETERMINABLE is left alone, not cancelled",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "t1"),
			armedFn: undeterminable,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Empty(t, cancels,
					"a failed recurrence lookup must never cancel: that would permanently disarm a recurring job")
			},
		},
		{
			name:    "TimerFired of an unknown timer defaults to cancel (safe)",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "gone"),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "gone"}}, cancels)
			},
		},
		{
			name:    "TimerFired with NIL armedRecurring (no timer store) is left alone",
			cmds:    nil,
			trg:     engine.NewTimerFired(at, "t1"),
			armedFn: nil,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Empty(t, cancels,
					"without a timer store recurrence is undeterminable: never deactivate a possibly-recurring native job")
			},
		},
		{
			name:    "explicit CancelTimer overrides recurrence (scope-exit stops a recurring timer)",
			cmds:    []engine.Command{engine.CancelTimer{TimerID: "rec-1"}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: func(id string) (bool, bool) { return id == "rec-1", true },
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				assert.Empty(t, arms)
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "rec-1"}}, cancels,
					"an explicit CancelTimer must always cancel, recurring or not")
			},
		},
		{
			name:    "arm carries a recurring Trigger with a truthful first-fire next_run",
			cmds:    []engine.Command{engine.ScheduleTimer{TimerID: "rec-2", Trigger: recurring, Kind: engine.TimerInWait}},
			trg:     engine.NewStartInstance(at, nil),
			armedFn: noRecurring,
			assert: func(t *testing.T, arms []*timerJob, cancels []cancelKey) {
				require.Len(t, arms, 1)
				spec := arms[0].descriptor()
				assert.Equal(t, recurring, spec.Trigger)
				assert.True(t, spec.Trigger.Recurring())
				assert.Equal(t, engine.TimerInWait, spec.Kind)
				assert.True(t, spec.NextRun.Equal(at.Add(15*time.Minute)),
					"recurring Every persists a truthful first-fire next_run (now+interval) for Stats: want %v got %v", at.Add(15*time.Minute), spec.NextRun)
				assert.Empty(t, cancels)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver, err := NewProcessDriver(WithClock(clockwork.NewFakeClockAt(at)))
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

			arms, cancels := driver.timerJobsFor(t.Context(), timerOpsDef(), tc.cmds, tc.trg, "i1", tc.armedFn)
			tc.assert(t, arms, cancels)
		})
	}
}

// BenchmarkArmedTimerRecurring measures the N→1 read reduction of the point read,
// over a kernel.MemTimerStore holding N armed timers.
//
//   - point-lookup runs the production armedTimerRecurring: one map lookup, flat
//     in N.
//   - list-armed-scan runs legacyArmedRecurring, the superseded algorithm, over
//     the SAME store: MemTimerStore.ListArmed allocates and sorts the whole armed
//     set (O(N log N)) before the linear search finds one row. The SQL store pays
//     N rows plus N trigger decodes for the same answer.
//
// The target is the LAST timer in ListArmed's (next_run, instance_id, timer_id)
// order, so the scan curve is the honest worst case for the linear search.
func BenchmarkArmedTimerRecurring(b *testing.B) {
	at := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	const instanceID = "i1"

	for _, n := range []int{1, 100, 10_000} {
		store := kernel.NewMemTimerStore()
		var targetID string
		for i := range n {
			targetID = fmt.Sprintf("t-%06d", i)
			store.Arm(kernel.ArmedTimer{
				InstanceID: instanceID,
				DefID:      "d",
				DefVersion: 1,
				TimerID:    targetID,
				Trigger:    schedule.Every(15 * time.Minute),
				NextRun:    at.Add(time.Duration(i) * time.Second),
				Kind:       engine.TimerInWait,
			})
		}

		driver, err := NewProcessDriver(WithClock(clockwork.NewFakeClockAt(at)), WithTimerStore(store))
		require.NoError(b, err)
		b.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

		b.Run(fmt.Sprintf("point-lookup/N=%d", n), func(b *testing.B) {
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				if recurring, determinable := driver.armedTimerRecurring(ctx, instanceID, targetID); !recurring || !determinable {
					b.Fatal("benchmark target must be a determinable recurring timer")
				}
			}
		})

		b.Run(fmt.Sprintf("list-armed-scan/N=%d", n), func(b *testing.B) {
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				if !legacyArmedRecurring(ctx, store, instanceID, targetID) {
					b.Fatal("benchmark target must be a recurring timer")
				}
			}
		})
	}
}

func TestRehydrateTrigger(t *testing.T) {
	nextRun := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		armed  kernel.ArmedTimer
		assert func(t *testing.T, got schedule.TriggerSpec)
	}{
		{
			name:  "non-recurring with NextRun re-arms via At(NextRun)",
			armed: kernel.ArmedTimer{Trigger: schedule.AfterDuration(time.Hour), NextRun: nextRun},
			assert: func(t *testing.T, got schedule.TriggerSpec) {
				at, ok := got.AbsTime()
				assert.True(t, ok, "must be an At trigger")
				assert.True(t, at.Equal(nextRun), "At time must equal persisted NextRun")
			},
		},
		{
			name:  "recurring re-arms via its Trigger (scheduler recomputes)",
			armed: kernel.ArmedTimer{Trigger: schedule.Every(15 * time.Minute), NextRun: nextRun},
			assert: func(t *testing.T, got schedule.TriggerSpec) {
				assert.Equal(t, schedule.KindDuration, got.Kind(), "recurring keeps its Trigger")
				assert.True(t, got.Recurring())
			},
		},
		{
			name:  "non-recurring without NextRun falls back to its Trigger",
			armed: kernel.ArmedTimer{Trigger: schedule.AfterDuration(time.Hour)},
			assert: func(t *testing.T, got schedule.TriggerSpec) {
				d, ok := got.Duration()
				assert.True(t, ok, "falls back to the AfterDuration trigger")
				assert.Equal(t, time.Hour, d)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, rehydrateTrigger(tc.armed))
		})
	}
}

// scriptedTimerStore is a kernel.TimerStore whose ArmedTimer outcome is scripted,
// so armedTimerRecurring's three outcomes — recurring, non-recurring, and
// UNDETERMINABLE — can be driven directly without a database.
type scriptedTimerStore struct {
	timer kernel.ArmedTimer
	found bool
	err   error
}

func (scriptedTimerStore) ListArmed(context.Context) ([]kernel.ArmedTimer, error) {
	return nil, nil
}

func (s scriptedTimerStore) ArmedTimer(context.Context, string, string) (kernel.ArmedTimer, bool, error) {
	return s.timer, s.found, s.err
}

// timerRef is one (instanceID, timerID) pair a store double was asked for.
type timerRef struct{ instanceID, timerID string }

// corruptSiblingTimerStore reproduces the store state the point read was
// written against: wrkflw_timers holds one row the driver cannot scan, alongside the
// perfectly readable row the fire path actually wants. The SQL TimerStore's
// ListArmed aborts on the FIRST unscannable row anywhere in the table
// (internal/persistence/store/timerstore.go, the scanArmedTimer error path), so
// the whole scan fails; ArmedTimer reads a single primary-key-exact row and is
// unaffected by the corrupt sibling.
//
// kernel.MemTimerStore cannot express this — its ListArmed can never fail — so
// the fire path needs a purpose-built double here. Reproducing an actually
// corrupt row belongs in the store conformance suite, not in the runtime.
type corruptSiblingTimerStore struct {
	mu         sync.Mutex
	armed      kernel.ArmedTimer
	scans      int
	pointReads []timerRef
}

func (s *corruptSiblingTimerStore) ListArmed(context.Context) ([]kernel.ArmedTimer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scans++
	return nil, errors.New("workflow-store: scan armed timer: invalid trigger payload")
}

func (s *corruptSiblingTimerStore) ArmedTimer(_ context.Context, instanceID, timerID string) (kernel.ArmedTimer, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pointReads = append(s.pointReads, timerRef{instanceID, timerID})
	if instanceID != s.armed.InstanceID || timerID != s.armed.TimerID {
		return kernel.ArmedTimer{}, false, nil
	}
	return s.armed, true, nil
}

func (s *corruptSiblingTimerStore) scanCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scans
}

func (s *corruptSiblingTimerStore) reads() []timerRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.pointReads)
}

// legacyArmedRecurring reproduces the SUPERSEDED recurrence lookup verbatim: a
// full ListArmed scan, a linear search for the (instanceID, timerID) pair, and
// a single boolean in which every failure collapses to "not recurring" — which
// timerJobsFor reads as "cancel the fired timer".
//
// It lives in the test file only: it is the "before" curve of
// BenchmarkArmedTimerRecurring and the reference point for the corrupt-sibling
// delta below. It must never be added back to production code.
func legacyArmedRecurring(ctx context.Context, store kernel.TimerStore, instanceID, timerID string) bool {
	armed, err := store.ListArmed(ctx)
	if err != nil {
		return false
	}
	for _, a := range armed {
		if a.InstanceID == instanceID && a.TimerID == timerID {
			return a.Trigger.Recurring()
		}
	}
	return false
}

// TestCorruptSiblingRowDoesNotDisarmFiredRecurringTimer pins the behaviour delta
// the point read buys beyond the N→1 read count.
//
// Before: the fire path answered recurrence with ListArmed, which aborts on the
// first unscannable row ANYWHERE in wrkflw_timers. One corrupt row therefore
// failed the recurrence lookup for EVERY fire in the deployment, and the
// single-boolean result collapsed that failure into "not recurring" — so
// timerJobsFor cancelled the fired timer, recurring ones included. A recurring
// in-wait reminder loop would be silently disarmed by an unrelated row.
//
// After: the point read returns the requested row regardless of its siblings,
// reports it recurring, and the fired timer survives.
func TestCorruptSiblingRowDoesNotDisarmFiredRecurringTimer(t *testing.T) {
	at := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	newStore := func() *corruptSiblingTimerStore {
		return &corruptSiblingTimerStore{
			armed: kernel.ArmedTimer{
				InstanceID: "i1",
				DefID:      "d",
				DefVersion: 1,
				TimerID:    "rec-1",
				Trigger:    schedule.Every(15 * time.Minute),
				NextRun:    at.Add(15 * time.Minute),
				Kind:       engine.TimerInWait,
			},
		}
	}

	type testCase struct {
		name string
		// lookup builds the armedRecurring closure timerJobsFor is driven with,
		// exactly as ProcessDriver.applyStep does (processdriver.go).
		lookup func(ctx context.Context, driver *ProcessDriver, store *corruptSiblingTimerStore) func(string) (bool, bool)
		assert func(t *testing.T, store *corruptSiblingTimerStore, cancels []cancelKey)
	}

	cases := []testCase{
		{
			name: "legacy ListArmed scan: the corrupt sibling disarms the recurring timer",
			lookup: func(ctx context.Context, _ *ProcessDriver, store *corruptSiblingTimerStore) func(string) (bool, bool) {
				return func(timerID string) (bool, bool) {
					return legacyArmedRecurring(ctx, store, "i1", timerID), true
				}
			},
			assert: func(t *testing.T, store *corruptSiblingTimerStore, cancels []cancelKey) {
				assert.Equal(t, []cancelKey{{instanceID: "i1", timerID: "rec-1"}}, cancels,
					"the regression the point read removes: a failed scan collapsed to non-recurring, so the fire cancelled a recurring timer")
				assert.Equal(t, 1, store.scanCount(), "the old algorithm scanned the whole armed set on every fire")
			},
		},
		{
			name: "point read: the fired recurring timer survives the corrupt sibling",
			lookup: func(ctx context.Context, driver *ProcessDriver, _ *corruptSiblingTimerStore) func(string) (bool, bool) {
				return func(timerID string) (bool, bool) {
					return driver.armedTimerRecurring(ctx, "i1", timerID)
				}
			},
			assert: func(t *testing.T, store *corruptSiblingTimerStore, cancels []cancelKey) {
				assert.Empty(t, cancels,
					"a corrupt sibling row must not disarm the fired recurring timer")
				assert.Zero(t, store.scanCount(), "the fire path must never scan the armed set")
				assert.Equal(t, []timerRef{{"i1", "rec-1"}}, store.reads(),
					"exactly one primary-key-exact read, for the timer that fired")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			driver, err := NewProcessDriver(WithClock(clockwork.NewFakeClockAt(at)), WithTimerStore(store))
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

			ctx := t.Context()
			_, cancels := driver.timerJobsFor(ctx, timerOpsDef(), nil, engine.NewTimerFired(at, "rec-1"), "i1",
				tc.lookup(ctx, driver, store))
			tc.assert(t, store, cancels)
		})
	}
}

// TestArmedTimerRecurring pins the three-state contract.
// The load-bearing case is the store error: it must report UNDETERMINABLE, not
// "non-recurring", because non-recurring means timerJobsFor cancels the fired
// timer — so collapsing an error would let one connection blip permanently
// disarm a recurring job.
func TestArmedTimerRecurring(t *testing.T) {
	at := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		store  kernel.TimerStore
		assert func(t *testing.T, recurring, determinable bool)
	}

	// keyedStore is reached by exactly one case, which inspects what it was
	// asked for after the lookup runs.
	keyedStore := &corruptSiblingTimerStore{
		armed: kernel.ArmedTimer{InstanceID: "other", TimerID: "t1", Trigger: schedule.Every(time.Minute)},
	}

	cases := []testCase{
		{
			name: "recurring armed timer reports recurring and determinable",
			store: scriptedTimerStore{
				timer: kernel.ArmedTimer{InstanceID: "i1", TimerID: "t1", Trigger: schedule.Every(time.Minute)},
				found: true,
			},
			assert: func(t *testing.T, recurring, determinable bool) {
				assert.True(t, determinable, "a successful lookup is determinable")
				assert.True(t, recurring)
			},
		},
		{
			name: "one-shot armed timer reports non-recurring and determinable",
			store: scriptedTimerStore{
				timer: kernel.ArmedTimer{InstanceID: "i1", TimerID: "t1", Trigger: schedule.At(at)},
				found: true,
			},
			assert: func(t *testing.T, recurring, determinable bool) {
				assert.True(t, determinable)
				assert.False(t, recurring, "At() is one-shot, so the fired timer is consumed")
			},
		},
		{
			name:  "absent timer is determinable and non-recurring, so it is consumed",
			store: scriptedTimerStore{found: false},
			assert: func(t *testing.T, recurring, determinable bool) {
				assert.True(t, determinable, "not-found is a real answer, not a failure")
				assert.False(t, recurring)
			},
		},
		{
			name:  "store error is UNDETERMINABLE, never non-recurring",
			store: scriptedTimerStore{err: errors.New("connection reset")},
			assert: func(t *testing.T, recurring, determinable bool) {
				assert.False(t, determinable,
					"an error must not be reported as a recurrence answer: false would cancel the timer")
				assert.False(t, recurring)
			},
		},
		{
			// The scripted store above answers unconditionally, so no other case
			// proves the lookup is keyed at all. Here the armed row belongs to a
			// DIFFERENT instance: a read that ignored its arguments (or reused a
			// scan's first row) would report it recurring.
			name:  "read is primary-key-exact: a row armed for another instance is not found",
			store: keyedStore,
			assert: func(t *testing.T, recurring, determinable bool) {
				assert.True(t, determinable, "a keyed miss is a real answer")
				assert.False(t, recurring, "another instance's recurring timer must not answer for this one")
				assert.Equal(t, []timerRef{{"i1", "t1"}}, keyedStore.reads(),
					"the fired timer's own key must reach the store, once")
				assert.Zero(t, keyedStore.scanCount(), "the fire path must never scan the armed set")
			},
		},
		{
			name:  "nil store is undeterminable",
			store: nil,
			assert: func(t *testing.T, recurring, determinable bool) {
				assert.False(t, determinable)
				assert.False(t, recurring)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []Option{WithClock(clockwork.NewFakeClockAt(at))}
			if tc.store != nil {
				opts = append(opts, WithTimerStore(tc.store))
			}
			driver, err := NewProcessDriver(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

			recurring, determinable := driver.armedTimerRecurring(t.Context(), "i1", "t1")
			tc.assert(t, recurring, determinable)
		})
	}
}
