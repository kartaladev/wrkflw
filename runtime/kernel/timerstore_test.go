package kernel_test

import (
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// timerIntermediateDef returns: start → intermediate-catch(1h timer) → service("greet") → end.
func timerIntermediateDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "timer-intermediate",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewIntermediateCatchEvent("wait1h", model.WithTimerDuration(`"1h"`)),
			model.NewServiceTask("greet", model.WithActionName("greet")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "greet"},
			{ID: "f3", Source: "greet", Target: "end"},
		},
	}
}

func TestMemTimerStore(t *testing.T) {
	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	mk := func(id string, at time.Time) kernel.ArmedTimer {
		return kernel.ArmedTimer{InstanceID: "i1", DefID: "d", DefVersion: 1, TimerID: id, FireAt: at, Kind: engine.TimerIntermediate}
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
				assert.Equal(t, base, got[0].FireAt)
			},
		},
		{
			name: "re-arm same id upserts FireAt (no duplicate)",
			assert: func(t *testing.T) {
				s := kernel.NewMemTimerStore()
				s.Arm(mk("t1", base))
				s.Arm(mk("t1", base.Add(time.Hour)))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, base.Add(time.Hour), got[0].FireAt)
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

func TestMemStoreRecordsTimerOps(t *testing.T) {
	mts := kernel.NewMemTimerStore()
	store := mustMemStore(t, kernel.WithTimers(mts))
	at := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	st := engine.InstanceState{InstanceID: "i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: at}

	// Create with a TimerArm records it.
	tok, err := store.Create(t.Context(), kernel.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(at, nil),
		TimerArms: []kernel.ArmedTimer{{
			InstanceID: "i1", DefID: "d", DefVersion: 1, TimerID: "t1", FireAt: at.Add(time.Hour), Kind: engine.TimerIntermediate,
		}},
	})
	require.NoError(t, err)
	armed, err := mts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1)

	// Commit with a TimerCancel removes it.
	_, err = store.Commit(t.Context(), tok, kernel.AppliedStep{
		State:        st,
		Trigger:      engine.NewTimerFired(at.Add(time.Hour), "t1"),
		TimerCancels: []string{"t1"},
	})
	require.NoError(t, err)
	armed, err = mts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed)
}

func TestRunnerPersistsAndClearsTimer(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := mustMemStore(t, kernel.WithTimers(mts))
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))
	r := mustRunner(t, action.NewMapCatalog(nil), store,
		runtime.WithRunnerClock(fc),
		runtime.WithScheduler(sched), runtime.WithTimerStore(mts))

	def := timerIntermediateDef() // reuse the helper in runtime/timer_example_test.go (1h intermediate timer)
	_, err := r.Run(t.Context(), def, "tr-1", nil)
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
