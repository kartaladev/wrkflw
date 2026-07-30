package kernel_test

import (
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func TestMemTimerStore(t *testing.T) {
	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	mk := func(id string, at time.Time) kernel.ArmedTimer {
		return kernel.ArmedTimer{InstanceID: "i1", DefID: "d", DefVersion: 1, TimerID: id, NextRun: at, Kind: engine.TimerIntermediate}
	}
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "arm then ListArmed returns it",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				s.Arm(mk("t1", base))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "t1", got[0].TimerID)
				assert.Equal(t, base, got[0].NextRun)
			},
		},
		{
			name: "re-arm same id upserts NextRun (no duplicate)",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				s.Arm(mk("t1", base))
				s.Arm(mk("t1", base.Add(time.Hour)))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, base.Add(time.Hour), got[0].NextRun)
			},
		},
		{
			name: "cancel removes it",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				s.Arm(mk("t1", base))
				s.Cancel("i1", "t1")
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
		{
			name: "cancel unknown is a no-op",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				s.Cancel("i1", "nope")
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t) })
	}
}

// var _ kernel.TimerWriter = (*kernel.MemTimerStore)(nil) is the compile-time
// check that MemTimerStore satisfies the write-side capability (ADR-0134).
var _ kernel.TimerWriter = (*kernel.MemTimerStore)(nil)

// TestMemTimerStoreTimerWriter exercises the TimerWriter capability
// (UpsertJob/DeleteJob/DeleteJobByTimerID) added by ADR-0134: the runtime
// JobStore delegates writes to this port. Kind must round-trip because it is
// a new JobSpec field with no analogue on ArmedTimer's pre-existing Arm/Cancel
// path.
func TestMemTimerStoreTimerWriter(t *testing.T) {
	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	mkSpec := func(instanceID, timerID string, kind engine.TimerKind) kernel.JobSpec {
		return kernel.JobSpec{
			TimerID:    timerID,
			InstanceID: instanceID,
			DefID:      "d",
			DefVersion: 1,
			Trigger:    schedule.At(base.Add(time.Hour)),
			NextRun:    base.Add(time.Hour),
			Kind:       kind,
		}
	}

	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "UpsertJob then ListArmed round-trips Kind",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				require.NoError(t, s.UpsertJob(t.Context(), mkSpec("i1", "t1", engine.TimerDeadline)))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "t1", got[0].TimerID)
				assert.Equal(t, engine.TimerDeadline, got[0].Kind)
			},
		},
		{
			name: "DeleteJob removes by (instanceID, timerID)",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				require.NoError(t, s.UpsertJob(t.Context(), mkSpec("i1", "t1", engine.TimerDeadline)))
				require.NoError(t, s.DeleteJob(t.Context(), "i1", "t1"))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
		{
			name: "DeleteJobByTimerID removes by timerID alone",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				require.NoError(t, s.UpsertJob(t.Context(), mkSpec("i1", "t1", engine.TimerDeadline)))
				require.NoError(t, s.DeleteJobByTimerID(t.Context(), "t1"))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
		{
			name: "DeleteJobByTimerID unknown is a no-op",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				require.NoError(t, s.DeleteJobByTimerID(t.Context(), "nope"))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t) })
	}
}

func TestProcessDriverPersistsAndClearsTimer(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	r := runtimetest.MustProcessDriver(t, action.NewCatalog(nil), store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched), runtime.WithTimerStore(mts))

	def := runtimetest.TimerIntermediateDef() // reuse the helper in runtime/timer_example_test.go (1h intermediate timer)
	_, err := r.Drive(t.Context(), def, "tr-1", nil)
	require.NoError(t, err)

	// Armed after Run parks on the timer.
	armed, err := mts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1, "the pending timer must be persisted")
	assert.Equal(t, "tr-1", armed[0].InstanceID)

	// Fire it; the armed row clears (consumed via TimerFired).
	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched.Tick(t.Context()))
	armed, err = mts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed, "a fired timer must leave the armed set")
}

// armedTimerFixtures returns the seed set shared by the MemTimerStore point-lookup
// contract test. It deliberately mirrors, case for case, the fixtures the SQL
// TimerStore's own conformance suite arms in
// internal/persistence/store/timerstore_conformance_test.go
// (TestTimerStoreArmedTimer): one recurring and one one-shot timer under the same
// instance, so the two contract tests can be read side by side as the two halves
// of the ADR-0159 mem-vs-SQL parity claim.
//
// NextRun values are Truncate(time.Millisecond) because the SQL half round-trips
// through TIMESTAMPTZ / DATETIME(6) / TEXT columns whose resolution stops short of
// nanoseconds; keeping the in-memory fixtures at the same resolution keeps the two
// suites' expectations interchangeable.
func armedTimerFixtures(base time.Time) (recurring, oneshot kernel.ArmedTimer) {
	recurring = kernel.ArmedTimer{
		InstanceID: "i1",
		DefID:      "proc-def",
		DefVersion: 3,
		TimerID:    "recurring-timer",
		NextRun:    base.Add(time.Hour),
		Kind:       engine.TimerIntermediate,
		Trigger:    schedule.Every(time.Minute),
	}
	oneshot = kernel.ArmedTimer{
		InstanceID: "i1",
		DefID:      "proc-def",
		DefVersion: 3,
		TimerID:    "oneshot-timer",
		NextRun:    base.Add(2 * time.Hour),
		Kind:       engine.TimerDeadline,
		Trigger:    schedule.At(base.Add(2 * time.Hour)),
	}
	return recurring, oneshot
}

// TestMemTimerStoreArmedTimer pins the [kernel.TimerStore.ArmedTimer] point-lookup
// contract on the in-memory reference implementation (ADR-0159).
//
// It is the mem-side half of the mem-vs-SQL parity assertion: every case here has
// a counterpart in the SQL store's conformance suite, which runs the identical
// matrix across Postgres, MySQL and SQLite. Paging is deliberately absent —
// MemTimerStore implements neither Stats nor ListArmedPage and is explicitly NOT a
// service.TimerAdmin (see the note at the foot of service/opsadmin.go), so there
// is no paging parity to assert on this side.
func TestMemTimerStoreArmedTimer(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC).Truncate(time.Millisecond)
	recurring, oneshot := armedTimerFixtures(base)
	seeded := []kernel.ArmedTimer{recurring, oneshot}

	type testCase struct {
		name       string
		seed       []kernel.ArmedTimer
		instanceID string
		timerID    string
		assert     func(t *testing.T, got kernel.ArmedTimer, found bool, err error)
	}

	cases := []testCase{
		{
			name:       "recurring timer is found by exact pair with every field projected",
			seed:       seeded,
			instanceID: "i1",
			timerID:    "recurring-timer",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err)
				require.True(t, found)
				assert.Equal(t, recurring, got, "the whole descriptor must survive the lookup")
				assert.True(t, got.Trigger.Recurring(),
					"recurrence is the whole reason this lookup exists")
			},
		},
		{
			name:       "one-shot timer is found and reports non-recurring",
			seed:       seeded,
			instanceID: "i1",
			timerID:    "oneshot-timer",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err)
				require.True(t, found)
				assert.Equal(t, oneshot, got)
				assert.False(t, got.Trigger.Recurring(), "At() is not recurring")
			},
		},
		{
			name:       "absent timer reports not-found, never an error",
			seed:       seeded,
			instanceID: "i1",
			timerID:    "no-such-timer",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err, "not-found is not an error condition")
				assert.False(t, found)
				assert.Zero(t, got, "the zero descriptor accompanies found == false")
			},
		},
		{
			name:       "same timer id under a different instance is not a match",
			seed:       seeded,
			instanceID: "other",
			timerID:    "recurring-timer",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err)
				assert.False(t, found, "the key is the pair, not the timer id alone")
				assert.Zero(t, got)
			},
		},
		{
			name:       "empty instance id against a populated store reports not-found",
			seed:       seeded,
			instanceID: "",
			timerID:    "recurring-timer",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err, "an empty key is not-found, not a validation error")
				assert.False(t, found)
				assert.Zero(t, got)
			},
		},
		{
			name:       "empty timer id against a populated store reports not-found",
			seed:       seeded,
			instanceID: "i1",
			timerID:    "",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err)
				assert.False(t, found)
				assert.Zero(t, got)
			},
		},
		{
			name:       "both keys empty reports not-found",
			seed:       seeded,
			instanceID: "",
			timerID:    "",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err)
				assert.False(t, found)
				assert.Zero(t, got)
			},
		},
		{
			name:       "empty store reports not-found",
			instanceID: "i1",
			timerID:    "recurring-timer",
			assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
				require.NoError(t, err)
				assert.False(t, found)
				assert.Zero(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := kernel.NewMemTimerStore()
			for _, a := range tc.seed {
				s.Arm(a)
			}

			got, found, err := s.ArmedTimer(t.Context(), tc.instanceID, tc.timerID)
			tc.assert(t, got, found, err)
		})
	}
}

// TestMemTimerStoreArmedTimerAgreesWithListArmed asserts the store-side invariant
// documented on [kernel.TimerStore.ArmedTimer]: ArmedTimer(i, t) returns exactly
// the row ListArmed returns for (i, t), when that row is well-formed.
//
// The qualifier matters and is why this cannot be folded into the table above: the
// invariant is scoped to well-formed rows because ListArmed fails wholesale if any
// row is corrupt while ArmedTimer, reading one row, does not. MemTimerStore has no
// corruptible representation — it hands back the struct it was given — so on this
// side the qualifier is vacuous and the agreement is total. The SQL side, where a
// corrupt trigger_payload is genuinely reachable, is where the qualifier bites.
func TestMemTimerStoreArmedTimerAgreesWithListArmed(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC).Truncate(time.Millisecond)
	recurring, oneshot := armedTimerFixtures(base)

	s := kernel.NewMemTimerStore()
	s.Arm(recurring)
	s.Arm(oneshot)
	// A second instance reusing one of the timer ids, so agreement is checked
	// across the full composite key rather than a single-instance table.
	other := recurring
	other.InstanceID = "i2"
	other.NextRun = base.Add(30 * time.Minute)
	s.Arm(other)

	armed, err := s.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 3)

	for _, want := range armed {
		got, found, err := s.ArmedTimer(t.Context(), want.InstanceID, want.TimerID)
		require.NoError(t, err, "%s/%s", want.InstanceID, want.TimerID)
		require.True(t, found, "%s/%s: listed rows must be point-readable", want.InstanceID, want.TimerID)
		assert.Equal(t, want, got, "%s/%s: the two reads must agree field for field",
			want.InstanceID, want.TimerID)
	}

	// The converse direction: a pair ListArmed does not report is not point-readable
	// either, so the two reads agree on absence as well as on presence.
	_, found, err := s.ArmedTimer(t.Context(), "i2", "oneshot-timer")
	require.NoError(t, err)
	assert.False(t, found, "an unlisted pair must not be point-readable")
}
