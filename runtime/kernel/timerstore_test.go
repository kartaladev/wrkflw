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
