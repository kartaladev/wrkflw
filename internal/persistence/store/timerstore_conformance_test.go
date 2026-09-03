package store_test

import (
	"context"
	"fmt"
	"math"
	"strings"
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
// the durable-jobs write path. It satisfies the FK constraint that
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

		// FireAt UTC location: must survive round-trip at the same instant
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
// timestamps survive the round-trip on all dialects (precision guard).
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

// TestTimerStoreArmedTimer verifies the PK-exact point lookup on
// every backend: it returns the row for the exact (instance_id, timer_id) pair,
// reports not-found without an error, and does not treat a bare timer id as a
// key. It also pins the ListArmed/ArmedTimer invariant for a well-formed row.
func TestTimerStoreArmedTimer(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)
		var _ kernel.TimerStore = ts // compile-time interface check

		// Millisecond-truncated: Postgres TIMESTAMPTZ and MySQL DATETIME(6)
		// round to microseconds, so a nanosecond fixture would not compare equal
		// to the value read back.
		base := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC).Truncate(time.Millisecond)

		seedTimerInstance(t, s, ts, "ts-pt-1", base, []kernel.ArmedTimer{
			{
				InstanceID: "ts-pt-1",
				DefID:      "proc-def",
				DefVersion: 3,
				TimerID:    "recurring-timer",
				NextRun:    base.Add(time.Hour),
				Kind:       engine.TimerIntermediate,
				Trigger:    schedule.Every(time.Minute),
			},
			{
				InstanceID: "ts-pt-1",
				DefID:      "proc-def",
				DefVersion: 3,
				TimerID:    "oneshot-timer",
				NextRun:    base.Add(2 * time.Hour),
				Kind:       engine.TimerIntermediate,
				Trigger:    schedule.At(base.Add(2 * time.Hour)),
			},
		})

		type testCase struct {
			name       string
			instanceID string
			timerID    string
			// ctx derives the context handed to the SUT; nil means t.Context()
			// unchanged. ArmedTimer runs on the timer-fire hot path and is
			// therefore context-sensitive, so the table carries the modifier and
			// at least one case cancels.
			ctx    func(ctx context.Context) context.Context
			assert func(t *testing.T, got kernel.ArmedTimer, found bool, err error)
		}

		cases := []testCase{
			{
				name:       "recurring timer is found and its trigger survives",
				instanceID: "ts-pt-1",
				timerID:    "recurring-timer",
				assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
					require.NoError(t, err, "%s", b.name)
					require.True(t, found, "%s: recurring-timer must be found", b.name)
					assert.Equal(t, "recurring-timer", got.TimerID, "%s", b.name)
					assert.Equal(t, "proc-def", got.DefID, "%s", b.name)
					assert.Equal(t, 3, got.DefVersion, "%s", b.name)
					assert.True(t, got.Trigger.Recurring(),
						"%s: recurrence is the whole reason this lookup exists", b.name)
					assert.Equal(t, base.Add(time.Hour).UTC(), got.NextRun.UTC(), "%s: NextRun", b.name)
				},
			},
			{
				name:       "one-shot timer is found and reports non-recurring",
				instanceID: "ts-pt-1",
				timerID:    "oneshot-timer",
				assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
					require.NoError(t, err, "%s", b.name)
					require.True(t, found, "%s", b.name)
					assert.False(t, got.Trigger.Recurring(), "%s: At() is not recurring", b.name)
				},
			},
			{
				name:       "absent timer id reports not-found, never an error",
				instanceID: "ts-pt-1",
				timerID:    "no-such-timer",
				assert: func(t *testing.T, _ kernel.ArmedTimer, found bool, err error) {
					require.NoError(t, err, "%s: not-found is not an error", b.name)
					assert.False(t, found, "%s", b.name)
				},
			},
			{
				name:       "same timer id under a different instance is not a match",
				instanceID: "ts-pt-other",
				timerID:    "recurring-timer",
				assert: func(t *testing.T, _ kernel.ArmedTimer, found bool, err error) {
					require.NoError(t, err, "%s", b.name)
					assert.False(t, found, "%s: the key is the pair, not the timer id", b.name)
				},
			},
			{
				name:       "absent instance reports not-found",
				instanceID: "",
				timerID:    "",
				assert: func(t *testing.T, _ kernel.ArmedTimer, found bool, err error) {
					require.NoError(t, err, "%s", b.name)
					assert.False(t, found, "%s", b.name)
				},
			},
			{
				// The row exists and every other case proves this exact pair is
				// found, so the only difference here is the dead context — which
				// makes the error the assertion's subject rather than the fixture.
				//
				// found MUST stay false alongside the error. An implementation
				// that swallowed a query failure into (zero, false, nil) would
				// satisfy every other row in this table, and the fire path would
				// then read a transient connection blip as "not recurring" and
				// permanently cancel a recurring in-wait reminder loop — the exact
				// failure the point lookup exists to prevent.
				name:       "a failed read is an error, never a silent not-found",
				instanceID: "ts-pt-1",
				timerID:    "recurring-timer",
				ctx: func(ctx context.Context) context.Context {
					cctx, cancel := context.WithCancel(ctx)
					cancel() // pre-cancel: the query never reaches the server
					return cctx
				},
				assert: func(t *testing.T, got kernel.ArmedTimer, found bool, err error) {
					require.Error(t, err, "%s: a cancelled context must surface as an error", b.name)
					assert.False(t, found,
						"%s: an errored read must not claim not-found — the caller would treat it as non-recurring", b.name)
					assert.Zero(t, got, "%s: an errored read returns the zero timer", b.name)
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := t.Context()
				if tc.ctx != nil {
					ctx = tc.ctx(ctx)
				}

				got, found, err := ts.ArmedTimer(ctx, tc.instanceID, tc.timerID)
				tc.assert(t, got, found, err)
			})
		}

		// The stated invariant: for a well-formed row, ArmedTimer agrees with
		// what ListArmed reports for the same pair.
		t.Run("agrees with ListArmed for a well-formed row", func(t *testing.T) {
			armed, err := ts.ListArmed(t.Context())
			require.NoError(t, err, "%s", b.name)
			require.NotEmpty(t, armed, "%s", b.name)
			for _, want := range armed {
				got, found, err := ts.ArmedTimer(t.Context(), want.InstanceID, want.TimerID)
				require.NoError(t, err, "%s: %s/%s", b.name, want.InstanceID, want.TimerID)
				require.True(t, found, "%s: %s/%s", b.name, want.InstanceID, want.TimerID)
				assert.Equal(t, want.TimerID, got.TimerID, "%s", b.name)
				assert.Equal(t, want.NextRun.UTC(), got.NextRun.UTC(), "%s", b.name)
				assert.Equal(t, want.Kind, got.Kind, "%s", b.name)
			}
		})
	})
}

// corruptTriggerPayload overwrites one row's trigger_payload with bytes every
// backend ACCEPTS but [decodeTriggerPayload] cannot decode, and returns the
// payload it wrote.
//
// Finding a vector that works on all three dialects took some doing, so the
// reasoning is recorded rather than rediscovered:
//
//   - A malformed next_run is NOT usable as a shared vector. Postgres TIMESTAMPTZ
//     and MySQL DATETIME(6) reject a non-timestamp at write time, so only SQLite's
//     TEXT column could hold one.
//   - Invalid JSON is NOT usable either: Postgres JSONB and MySQL JSON validate on
//     write and reject it. Only SQLite's TEXT column would take it.
//   - [model.ReadTrigger] never fails — an unrecognised "kind" falls through to
//     the zero TriggerSpec — so a wrong-VALUE payload like {"kind":"nonsense"}
//     decodes cleanly and corrupts nothing.
//
// What does work everywhere is a payload that is VALID JSON of the wrong SHAPE:
// `[]` is a JSON array, so JSONB and JSON both store it happily, and
// json.Unmarshal into the model.TriggerWire STRUCT then fails with "cannot
// unmarshal array into Go value of type model.TriggerWire". That is the one
// decode error reachable on postgres, mysql and sqlite alike.
func corruptTriggerPayload(t *testing.T, b backend, s *store.Store, instanceID, timerID string) string {
	t.Helper()

	const payload = `[]`
	res, err := s.QuerierForTest(t.Context()).Exec(t.Context(),
		b.dialect.Rebind(`UPDATE wrkflw_timers SET trigger_payload = '`+payload+`' WHERE instance_id = ? AND timer_id = ?`),
		instanceID, timerID)
	require.NoError(t, err, "%s: corrupt %q/%q: every backend must ACCEPT this payload", b.name, instanceID, timerID)

	affected, err := res.RowsAffected()
	require.NoError(t, err, "%s: rows affected", b.name)
	require.Equal(t, int64(1), affected,
		"%s: corruption must land on exactly one row, else the test proves nothing", b.name)

	return payload
}

// TestTimerStoreCorruptTriggerPayload pins the behaviour delta claimed for
// the point lookup, against a real database on every backend:
//
//	(a) ListArmed aborts WHOLESALE when any row in the table is corrupt, and
//	(b) ArmedTimer is UNAFFECTED by a corrupt SIBLING row.
//
// Both halves previously rested on a runtime-level hand-written double in which
// they held by construction, so nothing measured them against real driver and
// column semantics — and [store.TimerStore.scanArmedTimer]'s decode-error branch
// was never executed at all. The delta is the whole reason the fire path reads
// one row instead of the table, so it is worth a real database.
func TestTimerStoreCorruptTriggerPayload(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC).Truncate(time.Microsecond)

		// Two rows on one instance. The corrupt one sorts FIRST so ListArmed
		// meets it immediately; the well-formed one is its sibling.
		seedTimerInstance(t, s, ts, "corrupt-inst", base, []kernel.ArmedTimer{
			{
				InstanceID: "corrupt-inst", DefID: "proc-def", DefVersion: 1,
				TimerID: "bad-timer", NextRun: base, Kind: engine.TimerIntermediate,
				Trigger: schedule.Every(time.Minute),
			},
			{
				InstanceID: "corrupt-inst", DefID: "proc-def", DefVersion: 1,
				TimerID: "good-timer", NextRun: base.Add(time.Hour), Kind: engine.TimerIntermediate,
				Trigger: schedule.Every(2 * time.Minute),
			},
		})

		// Precondition: both rows read cleanly BEFORE the corruption, so a
		// failure below is the corruption and not the fixture.
		before, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed before corruption", b.name)
		require.Len(t, before, 2, "%s", b.name)

		corruptTriggerPayload(t, b, s, "corrupt-inst", "bad-timer")

		type testCase struct {
			name  string
			probe func(t *testing.T)
		}

		cases := []testCase{
			{
				name: "ListArmed aborts wholesale rather than serving a partial table",
				probe: func(t *testing.T) {
					got, err := ts.ListArmed(t.Context())
					require.Error(t, err, "%s: one corrupt row must fail the whole listing", b.name)
					assert.Contains(t, err.Error(), "trigger payload",
						"%s: the error names the decode that failed", b.name)
					assert.Empty(t, got,
						"%s: a failed listing must not hand back the rows it managed to scan", b.name)
				},
			},
			{
				name: "ArmedTimer is unaffected by a corrupt sibling row",
				probe: func(t *testing.T) {
					got, found, err := ts.ArmedTimer(t.Context(), "corrupt-inst", "good-timer")
					require.NoError(t, err,
						"%s: the point lookup reads ONE row — a corrupt sibling is not its business", b.name)
					require.True(t, found, "%s", b.name)
					assert.Equal(t, "good-timer", got.TimerID, "%s", b.name)
					assert.True(t, got.Trigger.Recurring(),
						"%s: recurrence still resolves while ListArmed is dead", b.name)
				},
			},
			{
				name: "ArmedTimer on the corrupt row itself errors and never reports not-found",
				probe: func(t *testing.T) {
					// The row IS present, so a decode failure must not be
					// laundered into (zero, false, nil) — that is precisely the
					// "recurring timer silently cancelled" failure mode.
					got, found, err := ts.ArmedTimer(t.Context(), "corrupt-inst", "bad-timer")
					require.Error(t, err, "%s: an undecodable row is an error", b.name)
					assert.False(t, found, "%s: an errored read must not claim not-found", b.name)
					assert.Zero(t, got, "%s", b.name)
				},
			},
			{
				name: "ListArmedPage aborts on the corrupt row like ListArmed does",
				probe: func(t *testing.T) {
					// The paged read shares scanArmedTimer, so the admin listing
					// must not diverge from the full listing on a corrupt row.
					_, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 10})
					require.Error(t, err, "%s", b.name)
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) { tc.probe(t) })
		}
	})
}

// TestTimerStoreListArmedPage verifies the paged read: keyset ordering by the
// full (next_run, instance_id, timer_id) triple, HasMore derived by fetching
// one extra row, cursor continuation that neither skips nor repeats, and
// IncludeTotal gating the count query.
//
// The broad matrix — ties, zero next_run, clamping, sub-second precision —
// lands separately; this is the minimum that pins the symbol's contract on all
// three backends.
func TestTimerStoreListArmedPage(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)

		// Five timers on one instance, one minute apart, so the expected page
		// order is unambiguous.
		armed := make([]kernel.ArmedTimer, 0, 5)
		for i := range 5 {
			armed = append(armed, kernel.ArmedTimer{
				InstanceID: "ts-page-1",
				DefID:      "proc-def",
				DefVersion: 1,
				TimerID:    fmt.Sprintf("ts-page-1-tm%d", i),
				Trigger:    schedule.AfterDuration(time.Duration(i+1) * time.Minute),
				NextRun:    base.Add(time.Duration(i) * time.Minute),
				Kind:       engine.TimerIntermediate,
			})
		}
		seedTimerInstance(t, s, ts, "ts-page-1", base, armed)

		t.Run("first page reports HasMore and a continuation cursor", func(t *testing.T) {
			page, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 2})
			require.NoError(t, err, "%s", b.name)
			require.Len(t, page.Items, 2, "%s", b.name)
			assert.Equal(t, "ts-page-1-tm0", page.Items[0].TimerID, "%s", b.name)
			assert.Equal(t, "ts-page-1-tm1", page.Items[1].TimerID, "%s", b.name)
			assert.True(t, page.HasMore, "%s", b.name)
			assert.NotEmpty(t, page.NextCursor, "%s", b.name)
			assert.Zero(t, page.TotalCount, "%s: IncludeTotal defaults off", b.name)
		})

		t.Run("paging the whole table yields every row exactly once", func(t *testing.T) {
			var (
				seen   []string
				cursor string
			)
			for range 10 { // generous bound; 5 rows at 2/page needs 3 iterations
				page, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Cursor: cursor, Limit: 2})
				require.NoError(t, err, "%s", b.name)
				for _, it := range page.Items {
					seen = append(seen, it.TimerID)
				}
				if !page.HasMore {
					assert.Empty(t, page.NextCursor, "%s: no cursor on the last page", b.name)
					break
				}
				require.NotEmpty(t, page.NextCursor, "%s: HasMore requires a cursor", b.name)
				cursor = page.NextCursor
			}
			assert.Equal(t, []string{
				"ts-page-1-tm0", "ts-page-1-tm1", "ts-page-1-tm2", "ts-page-1-tm3", "ts-page-1-tm4",
			}, seen, "%s: keyset paging must neither skip nor repeat", b.name)
		})

		t.Run("IncludeTotal reports the table total, not the page size", func(t *testing.T) {
			page, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 2, IncludeTotal: true})
			require.NoError(t, err, "%s", b.name)
			assert.Len(t, page.Items, 2, "%s", b.name)
			assert.Equal(t, 5, page.TotalCount, "%s", b.name)
		})

		t.Run("a malformed cursor is an error, never a silent reset to page one", func(t *testing.T) {
			_, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Cursor: "!!!not-base64!!!"})
			require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor, "%s", b.name)
		})

		t.Run("agrees with ListArmed over the full set", func(t *testing.T) {
			want, err := ts.ListArmed(t.Context())
			require.NoError(t, err, "%s", b.name)
			page, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 200})
			require.NoError(t, err, "%s", b.name)
			require.Len(t, page.Items, len(want), "%s", b.name)
			for i := range want {
				assert.Equal(t, want[i].TimerID, page.Items[i].TimerID, "%s: index %d", b.name, i)
				assert.Equal(t, want[i].NextRun.UTC(), page.Items[i].NextRun.UTC(), "%s: index %d", b.name, i)
				assert.Equal(t, want[i].Trigger, page.Items[i].Trigger, "%s: index %d", b.name, i)
			}
			assert.False(t, page.HasMore, "%s", b.name)
		})
	})
}

// wipeTimers removes every wrkflw_timers row so a paging case sees a table
// holding only its own fixture. [store.TimerStore.ListArmedPage] reads the
// WHOLE table — there is no instance predicate — so scoping assertions by id
// prefix alone cannot make HasMore/NextCursor/TotalCount meaningful. Instances
// are deliberately left in place: they carry no timers once this returns, and
// re-Creating an existing instance id is a duplicate-key error.
func wipeTimers(t *testing.T, s *store.Store) {
	t.Helper()
	_, err := s.QuerierForTest(t.Context()).Exec(t.Context(), `DELETE FROM wrkflw_timers`)
	require.NoError(t, err, "wipe wrkflw_timers")
}

// seedTimersGrouped creates the wrkflw_instances row for every distinct
// InstanceID in timers (once each) and then arms every timer through the
// TimerWriter path, so a fixture may span several instances — which is what
// makes a next_run tie broken by instance_id expressible at all.
func seedTimersGrouped(t *testing.T, s *store.Store, ts *store.TimerStore, base time.Time, timers []kernel.ArmedTimer) {
	t.Helper()
	seenInstance := map[string]bool{}
	for _, tm := range timers {
		if !seenInstance[tm.InstanceID] {
			seenInstance[tm.InstanceID] = true
			seedTimerWriterInstance(t, s, tm.InstanceID, base)
		}
		err := ts.UpsertJob(t.Context(), kernel.JobSpec{
			TimerID:    tm.TimerID,
			InstanceID: tm.InstanceID,
			DefID:      tm.DefID,
			DefVersion: tm.DefVersion,
			Trigger:    tm.Trigger,
			NextRun:    tm.NextRun,
			Kind:       tm.Kind,
		})
		require.NoError(t, err, "seedTimersGrouped: arm %q/%q", tm.InstanceID, tm.TimerID)
	}
}

// bulkSeedTimersGrouped seeds timers the same way [seedTimersGrouped] does —
// one wrkflw_instances row per distinct InstanceID — but arms every timer with a
// SINGLE multi-row INSERT instead of one UpsertJob transaction per row.
//
// It exists for the fixtures that need hundreds of rows to make the 200-row
// Limit cap observable at all: at three dialects apiece, a couple of hundred
// round-trip transactions would dominate the package's runtime while measuring
// nothing the smaller fixtures do not already measure.
//
// next_run MUST go through [store.TimeArgForDialectValue], the same encoder the
// production write path uses. On SQLite the column is fixed-width RFC3339 TEXT;
// binding a raw time.Time there makes the driver stringify it
// non-ISO8601, the keyset predicate then matches nothing, and the test would
// silently measure an empty table. trigger_kind and trigger_payload are left to
// their column defaults (0 / NULL): the paging matrix sorts on
// (next_run, instance_id, timer_id) only and never inspects the trigger.
func bulkSeedTimersGrouped(t *testing.T, b backend, s *store.Store, base time.Time, timers []kernel.ArmedTimer) {
	t.Helper()
	require.NotEmpty(t, timers, "bulkSeedTimersGrouped: nothing to seed")

	seenInstance := map[string]bool{}
	for _, tm := range timers {
		if !seenInstance[tm.InstanceID] {
			seenInstance[tm.InstanceID] = true
			seedTimerWriterInstance(t, s, tm.InstanceID, base)
		}
	}

	var sb strings.Builder
	sb.WriteString(`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version) VALUES `)
	args := make([]any, 0, len(timers)*6)
	for i, tm := range timers {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?,?,?,?,?,?)")
		args = append(args,
			tm.InstanceID, tm.TimerID,
			store.TimeArgForDialectValue(b.dialect, tm.NextRun),
			int16(tm.Kind), tm.DefID, tm.DefVersion,
		)
	}

	_, err := s.QuerierForTest(t.Context()).Exec(t.Context(), b.dialect.Rebind(sb.String()), args...)
	require.NoError(t, err, "%s: bulk-arm %d timers", b.name, len(timers))
}

// pageTimer builds one fixture row. Kind and DefID are constant across the
// paging matrix — only the sort key (next_run, instance_id, timer_id) matters.
func pageTimer(instanceID, timerID string, nextRun time.Time) kernel.ArmedTimer {
	return kernel.ArmedTimer{
		InstanceID: instanceID,
		DefID:      "proc-def",
		DefVersion: 1,
		TimerID:    timerID,
		NextRun:    nextRun,
		Kind:       engine.TimerIntermediate,
	}
}

// armedKey renders the full sort key of a row as a comparable string, so a
// paging walk can be asserted as an exact ordered sequence.
func armedKey(a kernel.ArmedTimer) string { return a.InstanceID + "/" + a.TimerID }

// drainPages walks every page from the first with the given limit and returns
// the keys in the order the pages reported them.
//
// maxPages is a hard bound, not a convenience: paging past a row whose next_run
// is the zero value must TERMINATE. A cursor implementation that inferred
// "first page" from a zero next_run instead of from an empty cursor string
// would alias that real row and re-serve page one forever, so a test that
// looped unbounded would hang rather than fail.
func drainPages(t *testing.T, b backend, ts *store.TimerStore, limit, maxPages int) []string {
	t.Helper()
	var (
		seen   []string
		cursor string
	)
	for i := range maxPages {
		page, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Cursor: cursor, Limit: limit})
		require.NoError(t, err, "%s: page %d", b.name, i)
		for _, it := range page.Items {
			seen = append(seen, armedKey(it))
		}
		if !page.HasMore {
			require.Empty(t, page.NextCursor,
				"%s: page %d: NextCursor must be empty exactly when HasMore is false", b.name, i)
			return seen
		}
		require.NotEmpty(t, page.NextCursor, "%s: page %d: HasMore requires a cursor", b.name, i)
		cursor = page.NextCursor
	}
	t.Fatalf("%s: paging did not terminate within %d pages — a value-inferred first-page sentinel loops forever", b.name, maxPages)
	return nil
}

// TestTimerStoreListArmedPageMatrix is the broad paging matrix for
// [store.TimerStore.ListArmedPage], run against every backend.
//
// Each case owns the whole wrkflw_timers table: the fixture is wiped first,
// because HasMore, NextCursor and TotalCount are properties of the table, not
// of one instance, and rows left behind by a sibling case would silently make
// them pass for the wrong reason. Instance ids are still prefixed per case so
// the wrkflw_instances rows the cases create never collide.
func TestTimerStoreListArmedPageMatrix(t *testing.T) {
	// Microsecond-truncated: MySQL stores next_run as DATETIME(6), so a
	// nanosecond fixture would not round-trip and the cursor built from the
	// value read back would not equal the value seeded.
	base := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC).Truncate(time.Microsecond)

	type pageCase struct {
		name string
		// bulk routes arm()'s rows through [bulkSeedTimersGrouped] (one
		// multi-row INSERT) instead of [seedTimersGrouped] (one UpsertJob
		// transaction each). Set it only for fixtures large enough that the
		// per-row round trips would dominate the suite; the write path itself is
		// covered by the smaller cases and by the ListArmed tests above.
		bulk   bool
		arm    func() []kernel.ArmedTimer
		assert func(t *testing.T, b backend, ts *store.TimerStore)
	}

	cases := []pageCase{
		{
			name: "HasMore is true exactly while rows remain and the cursor is empty exactly when it is false",
			arm: func() []kernel.ArmedTimer {
				out := make([]kernel.ArmedTimer, 0, 5)
				for i := range 5 {
					out = append(out, pageTimer("pm-bound", fmt.Sprintf("t%d", i), base.Add(time.Duration(i)*time.Minute)))
				}
				return out
			},
			assert: func(t *testing.T, b backend, ts *store.TimerStore) {
				// 5 rows at 2 per page: pages of 2, 2, 1 — HasMore on the
				// first two, false with an empty cursor on the third.
				wantSizes := []int{2, 2, 1}
				wantMore := []bool{true, true, false}

				var cursor string
				for i, wantSize := range wantSizes {
					page, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Cursor: cursor, Limit: 2})
					require.NoError(t, err, "%s: page %d", b.name, i)
					require.Len(t, page.Items, wantSize, "%s: page %d size", b.name, i)
					assert.Equal(t, wantMore[i], page.HasMore, "%s: page %d HasMore", b.name, i)
					assert.Equal(t, wantMore[i], page.NextCursor != "",
						"%s: page %d: NextCursor is non-empty exactly when HasMore", b.name, i)
					cursor = page.NextCursor
				}
				assert.Empty(t, cursor, "%s: the last page must not hand out a cursor", b.name)

				assert.Equal(t, []string{
					"pm-bound/t0", "pm-bound/t1", "pm-bound/t2", "pm-bound/t3", "pm-bound/t4",
				}, drainPages(t, b, ts, 2, 10), "%s: full walk", b.name)
			},
		},
		{
			name: "a next_run tie split across pages neither skips nor repeats a row",
			arm: func() []kernel.ArmedTimer {
				// Six rows share ONE next_run instant and differ only by
				// instance_id and timer_id. A two-column cursor gets this
				// wrong; the limit of 2 splits the tie group three ways.
				var out []kernel.ArmedTimer
				for _, inst := range []string{"pm-tie-a", "pm-tie-b", "pm-tie-c"} {
					for _, id := range []string{"t1", "t2"} {
						out = append(out, pageTimer(inst, id, base))
					}
				}
				return out
			},
			assert: func(t *testing.T, b backend, ts *store.TimerStore) {
				want := []string{
					"pm-tie-a/t1", "pm-tie-a/t2",
					"pm-tie-b/t1", "pm-tie-b/t2",
					"pm-tie-c/t1", "pm-tie-c/t2",
				}

				// The limits are the point, not a sweep for its own sake. With
				// two timers per instance, a limit of 2 lands EVERY cursor
				// exactly on an instance boundary, so the tie is always resolved
				// by the `instance_id > ?` term and the innermost
				// `(instance_id = ? AND timer_id > ?)` term of the MySQL
				// predicate (dialect/mysql.go ArmedTimerKeysetPredicate) never
				// selects a row — deleting that clause outright would leave a
				// limit-2-only walk green.
				//
				// Limits 1 and 3 park a cursor INSIDE a tie group, mid-instance
				// (after pm-tie-a/t1 and after pm-tie-b/t1 respectively), where
				// resuming requires that innermost term and nothing else can
				// supply the row. Verified by deleting the clause: the walk then
				// skips every second row of each group.
				for _, limit := range []int{1, 2, 3} {
					assert.Equal(t, want, drainPages(t, b, ts, limit, 20),
						"%s: limit %d: a tie on next_run must be broken by the full (instance_id, timer_id) key, not re-served or skipped",
						b.name, limit)
				}
			},
		},
		{
			name: "a zero next_run row sorts first, appears once, and paging past it terminates",
			// The zero row is armed inside the assert closure, not here: MySQL
			// REJECTS it (see below), and that rejection is itself the
			// behaviour worth pinning rather than a fixture failure.
			arm: func() []kernel.ArmedTimer {
				out := make([]kernel.ArmedTimer, 0, 3)
				for i := range 3 {
					out = append(out, pageTimer("pm-zero", fmt.Sprintf("t%d", i), base.Add(time.Duration(i)*time.Minute)))
				}
				return out
			},
			assert: func(t *testing.T, b backend, ts *store.TimerStore) {
				zero := pageTimer("pm-zero", "t-zero", time.Time{})
				err := ts.UpsertJob(t.Context(), kernel.JobSpec{
					TimerID:    zero.TimerID,
					InstanceID: zero.InstanceID,
					DefID:      zero.DefID,
					DefVersion: zero.DefVersion,
					NextRun:    zero.NextRun,
					Kind:       zero.Kind,
				})

				if b.name == "mysql" {
					// go-sql-driver serialises a zero time.Time as the literal
					// '0000-00-00', which DATETIME(6) rejects under MySQL 8's
					// default strict mode. So a zero next_run is NOT persistable
					// on MySQL. A zero next_run is therefore NOT genuinely
					// persisted on all three backends. The cursor sentinel
					// argument is unaffected — a sentinel safe on only some
					// backends is no sentinel — and this row pins the divergence
					// so it cannot regress unnoticed.
					require.Error(t, err, "%s: a zero next_run must not silently become some other instant", b.name)
					assert.Contains(t, err.Error(), "next_run", "%s: the rejection names the column", b.name)

					assert.Equal(t, []string{"pm-zero/t0", "pm-zero/t1", "pm-zero/t2"},
						drainPages(t, b, ts, 1, 20), "%s: paging still terminates", b.name)
					return
				}

				require.NoError(t, err, "%s: a zero next_run is genuinely persisted", b.name)

				// Limit 1 forces the very first cursor to encode the zero
				// next_run — the value a sentinel-by-value cursor would alias,
				// re-serving page one forever. drainPages is bounded, so that
				// failure surfaces as a failure rather than a hang.
				assert.Equal(t, []string{
					"pm-zero/t-zero", "pm-zero/t0", "pm-zero/t1", "pm-zero/t2",
				}, drainPages(t, b, ts, 1, 20),
					"%s: the zero next_run row sorts first and is served exactly once", b.name)

				first, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 1})
				require.NoError(t, err, "%s", b.name)
				require.Len(t, first.Items, 1, "%s", b.name)
				assert.True(t, first.Items[0].NextRun.IsZero(),
					"%s: want a zero next_run read back, got %v", b.name, first.Items[0].NextRun)
			},
		},
		{
			name: "Limit is clamped, never rejected: 0 means 50 and MaxInt means 200",
			bulk: true,
			arm: func() []kernel.ArmedTimer {
				// 205 rows, and the count is load-bearing twice over.
				//
				// It must exceed 200 so the CAP is observable: at 55 rows a
				// MaxInt page returns the whole fixture, `len(Items) <= 200`
				// holds trivially, and the cap could have been 10,000 without
				// any assertion noticing.
				//
				// It must exceed 200 for the OVERFLOW guard too. MaxInt+1 (the
				// HasMore probe row) wraps to MinInt, and SQLite reads a negative
				// LIMIT as NO limit — so on SQLite an unclamped MaxInt returns
				// the entire table with HasMore false (`55 > math.MaxInt` is
				// false). Under a 55-row fixture that is indistinguishable from
				// correct behaviour and only Postgres and MySQL caught the
				// regression, incidentally, by rejecting the negative LIMIT at
				// the driver. At 205 rows an unclamped MaxInt yields 205 items
				// on SQLite where 200 are required, so the backend the comment
				// names is finally the backend the case can fail on.
				//
				// 205 also still exceeds the default 50, which is what makes
				// Limit 0 and Limit -1 distinguishable from "no limit".
				out := make([]kernel.ArmedTimer, 0, 205)
				for i := range 205 {
					out = append(out, pageTimer("pm-clamp", fmt.Sprintf("t%03d", i), base.Add(time.Duration(i)*time.Minute)))
				}
				return out
			},
			assert: func(t *testing.T, b backend, ts *store.TimerStore) {
				zero, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 0})
				require.NoError(t, err, "%s: Limit 0", b.name)
				assert.Len(t, zero.Items, 50, "%s: Limit 0 takes the default 50", b.name)
				assert.True(t, zero.HasMore, "%s: 205 rows at the default 50 leaves more", b.name)

				maxed, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: math.MaxInt})
				require.NoError(t, err, "%s: Limit math.MaxInt must be clamped, not passed through", b.name)
				assert.Len(t, maxed.Items, 200,
					"%s: Limit is capped at EXACTLY 200 — more means the clamp never ran, fewer means it over-clamped", b.name)
				assert.True(t, maxed.HasMore, "%s: 205 rows under a 200 cap leaves more", b.name)
				assert.NotEmpty(t, maxed.NextCursor, "%s: a capped page still pages on", b.name)

				negative, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: -1})
				require.NoError(t, err, "%s: a negative Limit is clamped, not rejected", b.name)
				assert.Len(t, negative.Items, 50, "%s: a negative Limit takes the default 50", b.name)
				assert.True(t, negative.HasMore, "%s", b.name)

				// The cap must not lose rows: resuming from the capped page's
				// cursor reaches the remaining 5.
				rest, err := ts.ListArmedPage(t.Context(),
					kernel.ArmedTimerFilter{Cursor: maxed.NextCursor, Limit: math.MaxInt})
				require.NoError(t, err, "%s", b.name)
				assert.Len(t, rest.Items, 5, "%s: the 5 rows past the cap are still reachable", b.name)
				assert.False(t, rest.HasMore, "%s", b.name)
				assert.Equal(t, "pm-clamp/t200", armedKey(rest.Items[0]),
					"%s: the second page resumes exactly where the cap stopped", b.name)
			},
		},
		{
			name: "a cursor round-trips at sub-second precision inside one second",
			arm: func() []kernel.ArmedTimer {
				// MySQL's next_run is DATETIME(6) — microsecond precision — so
				// the fixture granularity is a microsecond, not a nanosecond.
				// A nanosecond fixture would not round-trip on MySQL and the
				// test would be measuring the fixture, not the cursor.
				sec := base.Truncate(time.Second)
				out := make([]kernel.ArmedTimer, 0, 4)
				for i := range 4 {
					out = append(out, pageTimer("pm-usec", fmt.Sprintf("t%d", i), sec.Add(time.Duration(i+1)*time.Microsecond)))
				}
				return out
			},
			assert: func(t *testing.T, b backend, ts *store.TimerStore) {
				assert.Equal(t, []string{"pm-usec/t0", "pm-usec/t1", "pm-usec/t2", "pm-usec/t3"},
					drainPages(t, b, ts, 1, 20),
					"%s: a cursor that lost sub-second precision would skip or repeat within the second", b.name)

				// Pin the precision itself: a truncated cursor timestamp would
				// still page correctly here by accident if the values collapsed
				// onto the same second AND the tiebreakers rescued it.
				all, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 10})
				require.NoError(t, err, "%s", b.name)
				require.Len(t, all.Items, 4, "%s", b.name)
				sec := base.Truncate(time.Second)
				for i, it := range all.Items {
					want := sec.Add(time.Duration(i+1) * time.Microsecond)
					assert.True(t, it.NextRun.Equal(want),
						"%s: item %d next_run: want %v got %v", b.name, i, want, it.NextRun)
				}
			},
		},
		{
			name: "IncludeTotal gates the count query and reports the table total",
			arm: func() []kernel.ArmedTimer {
				out := make([]kernel.ArmedTimer, 0, 5)
				for i := range 5 {
					out = append(out, pageTimer("pm-total", fmt.Sprintf("t%d", i), base.Add(time.Duration(i)*time.Minute)))
				}
				return out
			},
			assert: func(t *testing.T, b backend, ts *store.TimerStore) {
				off, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 2})
				require.NoError(t, err, "%s", b.name)
				assert.Zero(t, off.TotalCount, "%s: IncludeTotal off leaves TotalCount at zero", b.name)

				on, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 2, IncludeTotal: true})
				require.NoError(t, err, "%s", b.name)
				require.Len(t, on.Items, 2, "%s", b.name)
				assert.Equal(t, 5, on.TotalCount, "%s: TotalCount is the table total, not len(Items)", b.name)
				assert.NotEqual(t, len(on.Items), on.TotalCount, "%s", b.name)
			},
		},
	}

	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		// Cases share one database and are therefore NOT parallel: each owns
		// the whole wrkflw_timers table for the duration of its assertions.
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				wipeTimers(t, s)
				if tc.bulk {
					bulkSeedTimersGrouped(t, b, s, base, tc.arm())
				} else {
					seedTimersGrouped(t, s, ts, base, tc.arm())
				}
				tc.assert(t, b, ts)
			})
		}
	})
}

// TestTimerStoreListArmedPageContext covers the failure branches of
// [store.TimerStore.ListArmedPage] on every backend. The happy paths are pinned
// by the matrix above; what is pinned here is that an infrastructure failure
// SURFACES rather than degrading into an empty-looking page, on which an
// operator paging a large table would silently conclude "no armed timers".
//
// Reachability note, stated rather than papered over: the separate
// `SELECT count(*)` issued under IncludeTotal is sequenced AFTER the main query
// and reads through the same context, so no black-box caller can make the count
// fail while the main query succeeds — a dead context kills the main query
// first, and the main query's rows are fully drained before the count runs, so
// connection-starvation tricks do not separate them either. The IncludeTotal
// case below therefore pins that IncludeTotal does not SWALLOW the failure (and
// leaves TotalCount at zero), not that the count statement itself was reached.
// Reaching that one line would require injecting a fault between the two
// statements, which the port deliberately offers no seam for.
func TestTimerStoreListArmedPageContext(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC).Truncate(time.Microsecond)

		armed := make([]kernel.ArmedTimer, 0, 4)
		for i := range 4 {
			armed = append(armed, pageTimer("pc-ctx", fmt.Sprintf("t%d", i), base.Add(time.Duration(i)*time.Minute)))
		}
		seedTimerInstance(t, s, ts, "pc-ctx", base, armed)

		// A real cursor, minted from a live page, so the cancelled cursor case
		// below exercises the keyset branch of the statement builder rather than
		// short-circuiting on a decode error.
		first, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Limit: 2})
		require.NoError(t, err, "%s: mint a cursor", b.name)
		require.NotEmpty(t, first.NextCursor, "%s: mint a cursor", b.name)
		liveCursor := first.NextCursor

		cancelled := func(ctx context.Context) context.Context {
			cctx, cancel := context.WithCancel(ctx)
			cancel() // pre-cancel: the query never reaches the server
			return cctx
		}

		type testCase struct {
			name   string
			filter kernel.ArmedTimerFilter
			// ctx derives the context handed to the SUT; nil means t.Context()
			// unchanged.
			ctx    func(ctx context.Context) context.Context
			assert func(t *testing.T, page kernel.ArmedTimerPage, err error)
		}

		cases := []testCase{
			{
				// The control row. Without it, every assertion below could pass
				// against a store that failed for some reason of its own.
				name:   "the same filter on a live context returns a page",
				filter: kernel.ArmedTimerFilter{Limit: 2, IncludeTotal: true},
				assert: func(t *testing.T, page kernel.ArmedTimerPage, err error) {
					require.NoError(t, err, "%s", b.name)
					assert.Len(t, page.Items, 2, "%s", b.name)
					assert.Equal(t, 4, page.TotalCount, "%s", b.name)
				},
			},
			{
				name:   "a cancelled context fails the main query rather than returning an empty page",
				filter: kernel.ArmedTimerFilter{Limit: 2},
				ctx:    cancelled,
				assert: func(t *testing.T, page kernel.ArmedTimerPage, err error) {
					require.Error(t, err, "%s: a cancelled context must surface as an error", b.name)
					assert.Empty(t, page.Items,
						"%s: a failed page must be empty, not partially populated", b.name)
					assert.False(t, page.HasMore,
						"%s: a failed page must not claim more rows remain", b.name)
					assert.Empty(t, page.NextCursor, "%s: a failed page hands out no cursor", b.name)
					assert.Zero(t, page.TotalCount, "%s", b.name)
				},
			},
			{
				name:   "IncludeTotal does not swallow the failure",
				filter: kernel.ArmedTimerFilter{Limit: 2, IncludeTotal: true},
				ctx:    cancelled,
				assert: func(t *testing.T, page kernel.ArmedTimerPage, err error) {
					require.Error(t, err, "%s", b.name)
					assert.Zero(t, page.TotalCount,
						"%s: a failed page must not report a total it never counted", b.name)
					assert.Empty(t, page.Items, "%s", b.name)
				},
			},
			{
				name:   "a cancelled context on a cursor page fails instead of silently restarting at page one",
				filter: kernel.ArmedTimerFilter{Cursor: liveCursor, Limit: 2},
				ctx:    cancelled,
				assert: func(t *testing.T, page kernel.ArmedTimerPage, err error) {
					require.Error(t, err, "%s", b.name)
					assert.Empty(t, page.Items,
						"%s: resuming must not degrade into an empty page an operator reads as 'done'", b.name)
				},
			},
			{
				// Guards the ordering of the two failure modes: a bad cursor is
				// rejected by the statement builder BEFORE any query is issued,
				// so it reports ErrBadArmedTimerCursor even though the context is
				// also dead. A build that queried first would surface the context
				// error instead and lose the actionable diagnosis.
				name:   "a malformed cursor is diagnosed before the context is ever used",
				filter: kernel.ArmedTimerFilter{Cursor: "!!!not-base64!!!", Limit: 2},
				ctx:    cancelled,
				assert: func(t *testing.T, page kernel.ArmedTimerPage, err error) {
					require.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor, "%s", b.name)
					assert.Empty(t, page.Items, "%s", b.name)
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := t.Context()
				if tc.ctx != nil {
					ctx = tc.ctx(ctx)
				}

				page, err := ts.ListArmedPage(ctx, tc.filter)
				tc.assert(t, page, err)
			})
		}
	})
}
