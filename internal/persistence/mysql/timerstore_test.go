package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestTimerStore_ListArmed verifies that ListArmed returns armed timers ordered
// by fire_at ascending, with DefID and DefVersion populated.
func TestTimerStore_ListArmed(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	store := mypkg.NewStore(db)
	ts := mypkg.NewTimerStore(db)

	base := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "ts-ord-1",
		DefID:      "proc-def",
		DefVersion: 2,
		Status:     engine.StatusRunning,
		StartedAt:  base,
	}

	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(base, nil),
		TimerArms: []runtime.ArmedTimer{
			{
				InstanceID: "ts-ord-1",
				DefID:      "proc-def",
				DefVersion: 2,
				TimerID:    "later-timer",
				FireAt:     base.Add(2 * time.Hour),
				Kind:       engine.TimerIntermediate,
			},
			{
				InstanceID: "ts-ord-1",
				DefID:      "proc-def",
				DefVersion: 2,
				TimerID:    "sooner-timer",
				FireAt:     base.Add(time.Hour),
				Kind:       engine.TimerIntermediate,
			},
		},
	})
	require.NoError(t, err)

	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 2)

	assert.Equal(t, "sooner-timer", armed[0].TimerID, "ordered by FireAt ascending")
	assert.Equal(t, "later-timer", armed[1].TimerID)
	assert.Equal(t, "proc-def", armed[0].DefID)
	assert.Equal(t, 2, armed[0].DefVersion)
	assert.Equal(t, engine.TimerIntermediate, armed[0].Kind)
	assert.Equal(t, "ts-ord-1", armed[0].InstanceID)
}
