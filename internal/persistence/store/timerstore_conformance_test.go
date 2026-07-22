package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// seedTimerInstance creates a bare instance via [seedTimerWriterInstance],
// then arms each of timers through ts's TimerWriter capability (UpsertJob) —
// the durable-jobs write path (ADR-0134). It satisfies the FK constraint that
// wrkflw_timers.instance_id must reference an existing wrkflw_instances row.
func seedTimerInstance(
	t *testing.T,
	s *store.Store,
	ts *store.TimerStore,
	id string,
	base time.Time,
	timers []kernel.ArmedTimer,
) {
	t.Helper()
	seedTimerWriterInstance(t, s, id, base)
	for _, tm := range timers {
		err := ts.UpsertJob(t.Context(), kernel.JobSpec{
			TimerID:    tm.TimerID,
			InstanceID: tm.InstanceID,
			DefID:      tm.DefID,
			DefVersion: tm.DefVersion,
			Trigger:    tm.Trigger,
			NextRun:    tm.NextRun,
			Kind:       tm.Kind,
		})
		require.NoError(t, err, "seedTimerInstance %q: arm %q", id, tm.TimerID)
	}
}

// TestTimerStoreListArmed verifies that NewTimerStore.ListArmed returns all
// armed timers ordered by (fire_at ASC, instance_id ASC, timer_id ASC) on all
// three dialects, with correct field projection and UTC-normalised FireAt.
func TestTimerStoreListArmed(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)
		var _ kernel.TimerStore = ts // compile-time interface check

		base := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)

		seedTimerInstance(t, s, ts, "ts-ord-1", base, []kernel.ArmedTimer{
			{
				InstanceID: "ts-ord-1",
				DefID:      "proc-def",
				DefVersion: 2,
				TimerID:    "later-timer",
				NextRun:    base.Add(2 * time.Hour),
				Kind:       engine.TimerIntermediate,
			},
			{
				InstanceID: "ts-ord-1",
				DefID:      "proc-def",
				DefVersion: 2,
				TimerID:    "sooner-timer",
				NextRun:    base.Add(time.Hour),
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
		assert.True(t, armed[0].NextRun.Equal(wantSooner),
			"%s: FireAt round-trip: want %v got %v", b.name, wantSooner, armed[0].NextRun)
		assert.Equal(t, time.UTC, armed[0].NextRun.Location(),
			"%s: FireAt must be UTC-located", b.name)
	})
}

// TestTimerStoreListArmedEmpty verifies that ListArmed returns a nil/empty
// slice (not an error) when the wrkflw_timers table is empty.
func TestTimerStoreListArmedEmpty(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

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
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)

		// Two instances each with one timer; inst-a fires later than inst-b.
		seedTimerInstance(t, s, ts, "inst-a", base, []kernel.ArmedTimer{
			{
				InstanceID: "inst-a", DefID: "d", DefVersion: 1,
				TimerID: "ta", NextRun: base.Add(2 * time.Hour), Kind: engine.TimerIntermediate,
			},
		})
		seedTimerInstance(t, s, ts, "inst-b", base, []kernel.ArmedTimer{
			{
				InstanceID: "inst-b", DefID: "d", DefVersion: 1,
				TimerID: "tb", NextRun: base.Add(time.Hour), Kind: engine.TimerIntermediate,
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
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)
		var _ kernel.TimerStatsReader = ts // compile-time interface check

		// Stats on empty table.
		stats, err := ts.Stats(t.Context())
		require.NoError(t, err, "%s: Stats empty", b.name)
		assert.Equal(t, int64(0), stats.Armed, "%s: empty Armed must be 0", b.name)
		assert.Nil(t, stats.NextFireAt, "%s: empty NextFireAt must be nil", b.name)

		base := time.Date(2026, 6, 22, 16, 0, 0, 0, time.UTC)
		sooner := base.Add(time.Hour)
		later := base.Add(2 * time.Hour)

		seedTimerInstance(t, s, ts, "stats-inst", base, []kernel.ArmedTimer{
			{
				InstanceID: "stats-inst", DefID: "d", DefVersion: 1,
				TimerID: "t-later", NextRun: later, Kind: engine.TimerIntermediate,
			},
			{
				InstanceID: "stats-inst", DefID: "d", DefVersion: 1,
				TimerID: "t-sooner", NextRun: sooner, Kind: engine.TimerIntermediate,
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

// TestTimerStoreDescriptorRoundTrip verifies that an armed timer's TriggerSpec
// descriptor and NextRun survive a persist → ListArmed round-trip on all three
// dialects. This is the durability contract that lets RehydrateTimers re-arm a
// SQL-backed one-shot timer at its original absolute fire time after a restart
// (the regression this task closes). The trigger_payload column holds the
// JSON-encoded descriptor; trigger_kind is the query-convenience discriminator.
func TestTimerStoreDescriptorRoundTrip(t *testing.T) {
	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)

	type descCase struct {
		timerID string
		trigger schedule.TriggerSpec
		nextRun time.Time
		assert  func(t *testing.T, got kernel.ArmedTimer)
	}

	cases := []descCase{
		{
			timerID: "cron-timer",
			trigger: schedule.Cron("0 9 * * *"),
			nextRun: base.Add(24 * time.Hour),
			assert: func(t *testing.T, got kernel.ArmedTimer) {
				t.Helper()
				assert.Equal(t, schedule.KindCron, got.Trigger.Kind(), "cron kind survives")
				expr, ok := got.Trigger.CronExpr()
				assert.True(t, ok, "cron expr present")
				assert.Equal(t, "0 9 * * *", expr, "cron expr survives")
				assert.True(t, got.Trigger.Recurring(), "cron is recurring")
			},
		},
		{
			timerID: "after-timer",
			trigger: schedule.AfterDuration(90 * time.Minute),
			nextRun: base.Add(90 * time.Minute),
			assert: func(t *testing.T, got kernel.ArmedTimer) {
				t.Helper()
				assert.Equal(t, schedule.KindOneTime, got.Trigger.Kind(), "one-time kind survives")
				d, ok := got.Trigger.Duration()
				assert.True(t, ok, "duration present for AfterDuration")
				assert.Equal(t, 90*time.Minute, d, "duration survives")
				assert.False(t, got.Trigger.Recurring(), "AfterDuration is non-recurring")
				assert.True(t, got.NextRun.Equal(base.Add(90*time.Minute)),
					"NextRun survives: want %v got %v", base.Add(90*time.Minute), got.NextRun)
			},
		},
		{
			timerID: "at-timer",
			trigger: schedule.At(base.Add(3 * time.Hour)),
			nextRun: base.Add(3 * time.Hour),
			assert: func(t *testing.T, got kernel.ArmedTimer) {
				t.Helper()
				assert.Equal(t, schedule.KindOneTime, got.Trigger.Kind(), "one-time kind survives")
				at, ok := got.Trigger.AbsTime()
				assert.True(t, ok, "abs time present for At")
				assert.True(t, at.Equal(base.Add(3*time.Hour)), "At time survives: want %v got %v", base.Add(3*time.Hour), at)
				assert.False(t, got.Trigger.Recurring(), "At is non-recurring")
			},
		},
	}

	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		arms := make([]kernel.ArmedTimer, 0, len(cases))
		for _, c := range cases {
			arms = append(arms, kernel.ArmedTimer{
				InstanceID: "desc-inst",
				DefID:      "d",
				DefVersion: 1,
				TimerID:    c.timerID,
				Trigger:    c.trigger,
				NextRun:    c.nextRun,
				Kind:       engine.TimerIntermediate,
			})
		}
		seedTimerInstance(t, s, ts, "desc-inst", base, arms)

		armed, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed", b.name)
		require.Len(t, armed, len(cases), "%s: want %d timers", b.name, len(cases))

		byID := make(map[string]kernel.ArmedTimer, len(armed))
		for _, a := range armed {
			byID[a.TimerID] = a
		}
		for _, c := range cases {
			got, ok := byID[c.timerID]
			require.True(t, ok, "%s: timer %q present", b.name, c.timerID)
			t.Run(b.name+"/"+c.timerID, func(t *testing.T) {
				c.assert(t, got)
			})
		}
	})
}

// TestTimerStoreFireAtSubSecond verifies that sub-second (microsecond) fire_at
// timestamps survive the round-trip on all dialects (ADR-0080 precision guard).
// Postgres TIMESTAMPTZ and MySQL DATETIME(6) both have microsecond precision;
// SQLite TEXT(RFC3339Nano) preserves nanoseconds. The test uses microsecond
// precision so the assertion holds on all three backends.
func TestTimerStoreFireAtSubSecond(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		// Use microsecond precision: Postgres TIMESTAMPTZ and MySQL DATETIME(6)
		// store at most 6 decimal places; nanosecond digits are truncated/rounded.
		at := time.Date(2026, 6, 22, 12, 0, 0, 123456000, time.UTC) // 123456 µs

		seedTimerInstance(t, s, ts, "sub-sec-inst", at, []kernel.ArmedTimer{
			{
				InstanceID: "sub-sec-inst", DefID: "d", DefVersion: 1,
				TimerID: "t-usec", NextRun: at, Kind: engine.TimerIntermediate,
			},
		})

		armed, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed sub-second", b.name)
		require.Len(t, armed, 1, "%s: want 1 timer", b.name)

		assert.True(t, armed[0].NextRun.Equal(at),
			"%s: FireAt sub-second round-trip: want %v got %v", b.name, at, armed[0].NextRun)
		assert.Equal(t, time.UTC, armed[0].NextRun.Location(),
			"%s: FireAt must be UTC-located", b.name)
	})
}
