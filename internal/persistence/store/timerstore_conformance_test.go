package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// seedTimerInstance creates an instance via Store.Create, arming the given
// timers atomically with it. It satisfies the FK constraint that
// wrkflw_timers.instance_id must reference an existing wrkflw_instances row.
func seedTimerInstance(
	t *testing.T,
	s *store.Store,
	id string,
	base time.Time,
	timers []runtime.ArmedTimer,
) {
	t.Helper()
	_, err := s.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  base,
		},
		Trigger:   engine.NewStartInstance(base, nil),
		TimerArms: timers,
	})
	require.NoError(t, err, "seedTimerInstance %q", id)
}

// TestTimerStoreListArmed verifies that NewTimerStore.ListArmed returns all
// armed timers ordered by (fire_at ASC, instance_id ASC, timer_id ASC) on all
// three dialects, with correct field projection and UTC-normalised FireAt.
func TestTimerStoreListArmed(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s := store.New(b.conn, b.dialect)
		ts := store.NewTimerStore(b.conn, b.dialect)
		var _ runtime.TimerStore = ts // compile-time interface check

		base := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)

		seedTimerInstance(t, s, "ts-ord-1", base, []runtime.ArmedTimer{
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
		})

		armed, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed", b.name)
		require.Len(t, armed, 2, "%s: want 2 timers", b.name)

		// Ordering by fire_at ASC.
		assert.Equal(t, "sooner-timer", armed[0].TimerID, "%s: armed[0] must be sooner-timer", b.name)
		assert.Equal(t, "later-timer", armed[1].TimerID, "%s: armed[1] must be later-timer", b.name)

		// Field projection.
		assert.Equal(t, "proc-def", armed[0].DefID, "%s: DefID", b.name)
		assert.Equal(t, 2, armed[0].DefVersion, "%s: DefVersion", b.name)
		assert.Equal(t, engine.TimerIntermediate, armed[0].Kind, "%s: Kind", b.name)
		assert.Equal(t, "ts-ord-1", armed[0].InstanceID, "%s: InstanceID", b.name)

		// FireAt UTC location (ADR-0080): must survive round-trip at the same instant
		// and be UTC-located regardless of the host TZ (TZ=Asia/Jakarta guard).
		wantSooner := base.Add(time.Hour)
		assert.True(t, armed[0].FireAt.Equal(wantSooner),
			"%s: FireAt round-trip: want %v got %v", b.name, wantSooner, armed[0].FireAt)
		assert.Equal(t, time.UTC, armed[0].FireAt.Location(),
			"%s: FireAt must be UTC-located", b.name)
	})
}

// TestTimerStoreListArmedEmpty verifies that ListArmed returns a nil/empty
// slice (not an error) when the wrkflw_timers table is empty.
func TestTimerStoreListArmedEmpty(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ts := store.NewTimerStore(b.conn, b.dialect)

		armed, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed on empty table", b.name)
		assert.Empty(t, armed, "%s: empty table must return empty slice", b.name)
	})
}

// TestTimerStoreListArmedMultiInstance verifies ordering across multiple
// instances: fire_at ASC is the primary sort key, instance_id is secondary,
// timer_id is tertiary.
func TestTimerStoreListArmedMultiInstance(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s := store.New(b.conn, b.dialect)
		ts := store.NewTimerStore(b.conn, b.dialect)

		base := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)

		// Two instances each with one timer; inst-a fires later than inst-b.
		seedTimerInstance(t, s, "inst-a", base, []runtime.ArmedTimer{
			{
				InstanceID: "inst-a", DefID: "d", DefVersion: 1,
				TimerID: "ta", FireAt: base.Add(2 * time.Hour), Kind: engine.TimerIntermediate,
			},
		})
		seedTimerInstance(t, s, "inst-b", base, []runtime.ArmedTimer{
			{
				InstanceID: "inst-b", DefID: "d", DefVersion: 1,
				TimerID: "tb", FireAt: base.Add(time.Hour), Kind: engine.TimerIntermediate,
			},
		})

		armed, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed multi-instance", b.name)
		require.Len(t, armed, 2, "%s: want 2 timers", b.name)

		// inst-b fires sooner → must appear first.
		assert.Equal(t, "inst-b", armed[0].InstanceID, "%s: armed[0] instance", b.name)
		assert.Equal(t, "inst-a", armed[1].InstanceID, "%s: armed[1] instance", b.name)
	})
}

// TestTimerStoreStats verifies that Stats returns the correct armed count and
// NextFireAt (nil when empty, earliest fire_at when non-empty), UTC-located.
func TestTimerStoreStats(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s := store.New(b.conn, b.dialect)
		ts := store.NewTimerStore(b.conn, b.dialect)
		var _ runtime.TimerStatsReader = ts // compile-time interface check

		// Stats on empty table.
		stats, err := ts.Stats(t.Context())
		require.NoError(t, err, "%s: Stats empty", b.name)
		assert.Equal(t, int64(0), stats.Armed, "%s: empty Armed must be 0", b.name)
		assert.Nil(t, stats.NextFireAt, "%s: empty NextFireAt must be nil", b.name)

		base := time.Date(2026, 6, 22, 16, 0, 0, 0, time.UTC)
		sooner := base.Add(time.Hour)
		later := base.Add(2 * time.Hour)

		seedTimerInstance(t, s, "stats-inst", base, []runtime.ArmedTimer{
			{
				InstanceID: "stats-inst", DefID: "d", DefVersion: 1,
				TimerID: "t-later", FireAt: later, Kind: engine.TimerIntermediate,
			},
			{
				InstanceID: "stats-inst", DefID: "d", DefVersion: 1,
				TimerID: "t-sooner", FireAt: sooner, Kind: engine.TimerIntermediate,
			},
		})

		// Stats after arming.
		stats, err = ts.Stats(t.Context())
		require.NoError(t, err, "%s: Stats after arm", b.name)
		assert.Equal(t, int64(2), stats.Armed, "%s: Armed must be 2", b.name)
		require.NotNil(t, stats.NextFireAt, "%s: NextFireAt must not be nil", b.name)

		// NextFireAt must be the earliest fire_at, UTC-located.
		assert.True(t, stats.NextFireAt.Equal(sooner),
			"%s: NextFireAt round-trip: want %v got %v", b.name, sooner, *stats.NextFireAt)
		assert.Equal(t, time.UTC, stats.NextFireAt.Location(),
			"%s: NextFireAt must be UTC-located", b.name)
	})
}

// TestTimerStoreFireAtSubSecond verifies that sub-second (microsecond) fire_at
// timestamps survive the round-trip on all dialects (ADR-0080 precision guard).
// Postgres TIMESTAMPTZ and MySQL DATETIME(6) both have microsecond precision;
// SQLite TEXT(RFC3339Nano) preserves nanoseconds. The test uses microsecond
// precision so the assertion holds on all three backends.
func TestTimerStoreFireAtSubSecond(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s := store.New(b.conn, b.dialect)
		ts := store.NewTimerStore(b.conn, b.dialect)

		// Use microsecond precision: Postgres TIMESTAMPTZ and MySQL DATETIME(6)
		// store at most 6 decimal places; nanosecond digits are truncated/rounded.
		at := time.Date(2026, 6, 22, 12, 0, 0, 123456000, time.UTC) // 123456 µs

		seedTimerInstance(t, s, "sub-sec-inst", at, []runtime.ArmedTimer{
			{
				InstanceID: "sub-sec-inst", DefID: "d", DefVersion: 1,
				TimerID: "t-usec", FireAt: at, Kind: engine.TimerIntermediate,
			},
		})

		armed, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed sub-second", b.name)
		require.Len(t, armed, 1, "%s: want 1 timer", b.name)

		assert.True(t, armed[0].FireAt.Equal(at),
			"%s: FireAt sub-second round-trip: want %v got %v", b.name, at, armed[0].FireAt)
		assert.Equal(t, time.UTC, armed[0].FireAt.Location(),
			"%s: FireAt must be UTC-located", b.name)
	})
}
