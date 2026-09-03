package store_test

// The two tests in this file both drive Pruner.ReclaimNeverDueTimers but are
// deliberately NOT folded into one table: they observe different SUTs (the
// surviving row set versus TimerStore.Stats) over structurally different
// fixtures, so the shared setup-call-assert shape the table form exists for
// does not hold. See .claude/skills/table-test.
//
// Neither test uses forEachDialect, because the three backends are not
// interchangeable for this fixture:
//
//   - SQLite carries the SQLite-only half. next_run is TEXT there, so it is the
//     only backend where the legacy trimmed encoding exists at all, and
//     the only one where the threshold is a lexicographic comparison. It is also
//     pure Go, so the bulk of the coverage costs no container.
//   - Postgres has its own test below. ⚠ It is NOT excluded by the fixture:
//     next_run is TIMESTAMPTZ with no CHECK, and timestamptz reaches back to
//     4713 BC, so a zero next_run stores fine (measured: postgres accepted). It
//     is also the primary production backend and the one where the legacy
//     orphan population actually lives, so the destructive DELETE must be
//     exercised there and not merely assumed to generalise through
//     dialect.Rebind.
//   - MySQL is the only backend that genuinely cannot hold the fixture: it
//     rejects a zero next_run outright under default strict mode (Error 1292,
//     measured), so its orphan population is empty and the sweep is a no-op
//     there. There is nothing to seed and nothing to assert.
//
// An earlier revision of this comment claimed SQLite was "the only backend that
// can hold the fixture at all". That is false — Postgres holds it too, as
// measured above — and it argued against the Postgres coverage below.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/persistence"
)

// textTimeLayout mirrors the unexported store constant of the same name: the
// fixed-width nine-digit-fraction RFC3339 encoding SQLite timestamp columns are
// written in. Declared locally because package store cannot be
// imported by an in-package test file here — internal/dbtest imports store, so
// a `package store` test that used dbtest would be an import cycle.
const textTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// seedTimerRow inserts one wrkflw_timers row with next_run written verbatim, so
// the stored TEXT encoding is under the test's control rather than the store's.
func seedTimerRow(ctx context.Context, t *testing.T, db *sql.DB, instanceID, nextRun string, kind schedule.Kind) {
	t.Helper()

	_, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version, trigger_kind)
		 VALUES (?, 't1', ?, 0, 'd', 1, ?)`,
		instanceID, nextRun, int16(kind))
	require.NoError(t, err, "seed timer row %s", instanceID)
}

// armedInstanceIDs returns the instance_id of every surviving wrkflw_timers row.
func armedInstanceIDs(ctx context.Context, t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(ctx, `SELECT instance_id FROM wrkflw_timers ORDER BY instance_id`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

// TestPrunerReclaimNeverDueTimers pins the orphan sweep: rows whose
// next_run is sub-epoch AND whose trigger_kind is recurring are reclaimed, and
// nothing else is.
//
// Every seeded row is load-bearing:
//   - the three fixed-width orphans are the population the sweep exists for;
//   - orphan-legacy-trimmed is what fails if the predicate is written as
//     `next_run = <zero>` instead of a threshold — the legacy trimmed
//     encoding is not byte-equal to the fixed-width zero, so equality deletes 4
//     of 5 and reports success (measured);
//   - control-past-recurring is a regression guard: an expired but
//     still-armed recurring row. It guards the THRESHOLD clause — measured, it
//     dies when the epoch sentinel is replaced by a wall-clock cutoff, and
//     survives every widening of the trigger_kind IN-list (at 2020 it is not
//     sub-epoch, so the kind clause never gets to decide it);
//   - control-suboneshot guards the TRIGGER_KIND clause, and is the only seeded
//     row that can. It satisfies the sub-epoch half of the predicate but not the
//     kind half, so it is what dies if the IN-list is dropped or widened to the
//     non-recurring kinds — measured. It must survive: a sub-epoch one-shot is
//     already reachable by PruneTimers, and the two sweeps are correct only
//     while their predicates stay disjoint.
//   - control-at-epoch pins the threshold as STRICTLY less-than: a recurring row
//     sitting exactly on the sentinel survives. Without it a mutation of `<` to
//     `<=` passes every other assertion here. The boundary is semantically
//     arbitrary — no recurring arm can land on 1970-01-01T00:00:00Z, since
//     next_run is computed strictly forward from the arming instant — so this
//     row pins a documented choice, not a behaviour anyone depends on.
func TestPrunerReclaimNeverDueTimers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.RunTestSQLite(t)

	zeroFixed := time.Time{}.UTC().Format(textTimeLayout)     // "0001-01-01T00:00:00.000000000Z"
	zeroTrimmed := time.Time{}.UTC().Format(time.RFC3339Nano) // "0001-01-01T00:00:00Z"
	past := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC).Format(textTimeLayout)
	future := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC).Format(textTimeLayout)
	atEpoch := time.Unix(0, 0).UTC().Format(textTimeLayout) // exactly the sentinel

	// Orphans — never-due recurring rows, in both TEXT encodings.
	seedTimerRow(ctx, t, db, "orphan-duration", zeroFixed, schedule.KindDuration)
	seedTimerRow(ctx, t, db, "orphan-monthly", zeroFixed, schedule.KindMonthly)
	seedTimerRow(ctx, t, db, "orphan-everyexpr", zeroFixed, schedule.KindEveryExpr)
	seedTimerRow(ctx, t, db, "orphan-legacy-trimmed", zeroTrimmed, schedule.KindDaily)

	// Controls — none of these may be touched.
	seedTimerRow(ctx, t, db, "control-future-recurring", future, schedule.KindDuration)
	seedTimerRow(ctx, t, db, "control-past-recurring", past, schedule.KindWeekly)
	seedTimerRow(ctx, t, db, "control-expired-oneshot", past, schedule.KindOneTime)
	seedTimerRow(ctx, t, db, "control-suboneshot", zeroFixed, schedule.KindOneTime)
	seedTimerRow(ctx, t, db, "control-at-epoch", atEpoch, schedule.KindDuration)

	require.Len(t, armedInstanceIDs(ctx, t, db), 9, "fixture")

	p, err := store.NewPruner(db, dialect.NewSQLite())
	require.NoError(t, err)

	n, err := p.ReclaimNeverDueTimers(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(4), n, "reclaimed count")

	assert.Equal(t, []string{
		"control-at-epoch",
		"control-expired-oneshot",
		"control-future-recurring",
		"control-past-recurring",
		"control-suboneshot",
	}, armedInstanceIDs(ctx, t, db), "survivors")

	// The sweep is idempotent: a second pass finds nothing left to reclaim.
	again, err := p.ReclaimNeverDueTimers(ctx)
	require.NoError(t, err)
	assert.Zero(t, again, "second sweep")
}

// TestPrunerNeverDueStats pins that reclaiming an orphan frees
// TimerStats.NextFireAt. An orphan sorts first in the keyset index, so
// MIN(next_run) reports 0001-01-01 and the operator-facing "next timer fires
// at" reading is pinned there for as long as the row exists.
func TestPrunerNeverDueStats(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.RunTestSQLite(t)

	wantNext := time.Date(2099, 1, 2, 3, 4, 5, 123456789, time.UTC)
	seedTimerRow(ctx, t, db, "orphan", time.Time{}.UTC().Format(textTimeLayout), schedule.KindDuration)
	seedTimerRow(ctx, t, db, "healthy-future", wantNext.Format(textTimeLayout), schedule.KindDuration)

	ts, err := store.NewTimerStore(db, dialect.NewSQLite())
	require.NoError(t, err)
	p, err := store.NewPruner(db, dialect.NewSQLite())
	require.NoError(t, err)

	before, err := ts.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), before.Armed)
	require.NotNil(t, before.NextFireAt)
	assert.True(t, before.NextFireAt.IsZero(),
		"before the sweep NextFireAt is pinned at the orphan, got %s", before.NextFireAt)

	n, err := p.ReclaimNeverDueTimers(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	after, err := ts.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), after.Armed)
	require.NotNil(t, after.NextFireAt)
	assert.Equal(t, wantNext, *after.NextFireAt, "after the sweep NextFireAt is the healthy row")
}

// TestPrunerReclaimNeverDueTimersPostgres runs the orphan sweep against the
// primary production backend. Postgres is where the legacy orphan
// population actually lives, so the destructive DELETE is exercised here rather
// than assumed to generalise from SQLite through dialect.Rebind — a regression
// in the ?→$n rewrite or in the epoch bind would otherwise ship undetected.
//
// The fixture is the SQLite one minus the trimmed-encoding row, which has no
// meaning on a TIMESTAMPTZ column: next_run is bound as a time.Time and there is
// no second textual encoding to tolerate. Both clause guards survive the trip —
// control-past-recurring for the threshold, control-suboneshot for trigger_kind.
//
// ⚠ Needs Docker (testcontainers).
func TestPrunerReclaimNeverDueTimersPostgres(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	pool := dbtest.RunTestDatabase(t) // bare pool — no migrations yet
	require.NoError(t, persistence.Migrate(ctx, pool), "migrate postgres")

	seed := func(instanceID string, nextRun time.Time, kind schedule.Kind) {
		t.Helper()
		_, err := pool.Exec(ctx,
			`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version, trigger_kind)
			 VALUES ($1, 't1', $2, 0, 'd', 1, $3)`,
			instanceID, nextRun, int16(kind))
		require.NoError(t, err, "seed timer row %s", instanceID)
	}
	surviving := func() []string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT instance_id FROM wrkflw_timers ORDER BY instance_id`)
		require.NoError(t, err)
		defer rows.Close()

		var ids []string
		for rows.Next() {
			var id string
			require.NoError(t, rows.Scan(&id))
			ids = append(ids, id)
		}
		require.NoError(t, rows.Err())
		return ids
	}

	zero := time.Time{}.UTC()
	past := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	future := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)

	seed("orphan-duration", zero, schedule.KindDuration)
	seed("orphan-monthly", zero, schedule.KindMonthly)
	seed("control-future-recurring", future, schedule.KindDuration)
	seed("control-past-recurring", past, schedule.KindWeekly) // guards the THRESHOLD clause
	seed("control-suboneshot", zero, schedule.KindOneTime)    // guards the TRIGGER_KIND clause
	seed("control-at-epoch", time.Unix(0, 0).UTC(), schedule.KindDuration)

	require.Len(t, surviving(), 6, "fixture")

	p, err := store.NewPruner(pool, dialect.NewPostgres())
	require.NoError(t, err)

	n, err := p.ReclaimNeverDueTimers(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "reclaimed count")

	assert.Equal(t, []string{
		"control-at-epoch",
		"control-future-recurring",
		"control-past-recurring",
		"control-suboneshot",
	}, surviving(), "survivors")

	again, err := p.ReclaimNeverDueTimers(ctx)
	require.NoError(t, err)
	assert.Zero(t, again, "second sweep")
}
