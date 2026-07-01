package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestTimerFireAtRehydratesUTC asserts that fire_at timestamps read back from
// wrkflw_timers via TimerStore.ListArmed are UTC-located (zone offset == 0),
// regardless of the process time zone. Run under TZ=Asia/Jakarta to catch
// time.Local leakage from TIMESTAMPTZ without explicit UTC normalization.
func TestTimerFireAtRehydratesUTC(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	store := pg.NewStore(pool)
	ts := pg.NewTimerStore(pool)

	// A known fire_at instant stored in UTC.
	want := time.Date(2031, 3, 4, 5, 6, 7, 890000000, time.UTC)

	base := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "tr-pg-utc-1",
		DefID:      "def-utc",
		DefVersion: 1,
		Status:     engine.StatusRunning,
		StartedAt:  base,
	}
	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(base, nil),
		TimerArms: []runtime.ArmedTimer{{
			InstanceID: "tr-pg-utc-1",
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
