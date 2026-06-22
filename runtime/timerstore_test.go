package runtime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestMemTimerStore(t *testing.T) {
	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	mk := func(id string, at time.Time) runtime.ArmedTimer {
		return runtime.ArmedTimer{InstanceID: "i1", DefID: "d", DefVersion: 1, TimerID: id, FireAt: at, Kind: engine.TimerIntermediate}
	}
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "arm then ListArmed returns it",
			assert: func(t *testing.T) {
				s := runtime.NewMemTimerStore()
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
				s := runtime.NewMemTimerStore()
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
				s := runtime.NewMemTimerStore()
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
				s := runtime.NewMemTimerStore()
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
	mts := runtime.NewMemTimerStore()
	store := runtime.NewMemStoreWithTimers(mts)
	at := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	st := engine.InstanceState{InstanceID: "i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: at}

	// Create with a TimerArm records it.
	tok, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(at, nil),
		TimerArms: []runtime.ArmedTimer{{
			InstanceID: "i1", DefID: "d", DefVersion: 1, TimerID: "t1", FireAt: at.Add(time.Hour), Kind: engine.TimerIntermediate,
		}},
	})
	require.NoError(t, err)
	armed, err := mts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1)

	// Commit with a TimerCancel removes it.
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{
		State:        st,
		Trigger:      engine.NewTimerFired(at.Add(time.Hour), "t1"),
		TimerCancels: []string{"t1"},
	})
	require.NoError(t, err)
	armed, err = mts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed)
}
