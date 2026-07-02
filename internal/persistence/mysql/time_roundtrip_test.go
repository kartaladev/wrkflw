package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestTimerFireAtRehydratesUTC asserts that fire_at timestamps read back from
// wrkflw_timers via TimerStore.ListArmed are UTC-located (zone offset == 0),
// regardless of the process time zone. Run under TZ=Asia/Jakarta to catch
// time.Local leakage from DATETIME(6) without explicit UTC normalization.
// MySQL with loc=UTC typically returns UTC already; this test acts as a
// documented regression guard ensuring the .UTC() normalization is always
// applied after the fix.
func TestTimerFireAtRehydratesUTC(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	store := mypkg.NewStore(db)
	ts := mypkg.NewTimerStore(db)

	// A known fire_at instant stored in UTC.
	want := time.Date(2031, 3, 4, 5, 6, 7, 0, time.UTC) // MySQL DATETIME(6) precision; no sub-second ns

	base := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "tr-my-utc-1",
		DefID:      "def-utc",
		DefVersion: 1,
		Status:     engine.StatusRunning,
		StartedAt:  base,
	}
	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(base, nil),
		TimerArms: []runtime.ArmedTimer{{
			InstanceID: "tr-my-utc-1",
			DefID:      "def-utc",
			DefVersion: 1,
			TimerID:    "fire-utc",
			FireAt:     want,
			Kind:       engine.TimerIntermediate,
		}},
	})
	require.NoError(t, err)

	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1)

	got := armed[0].FireAt
	_, off := got.Zone()
	if off != 0 {
		t.Fatalf("FireAt zone offset = %d, want 0 (UTC); got %v", off, got)
	}
	if !got.Equal(want) {
		t.Fatalf("FireAt = %v, want %v", got, want)
	}
}
