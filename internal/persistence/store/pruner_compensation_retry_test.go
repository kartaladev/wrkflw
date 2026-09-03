package store_test

// This file holds one half of the PruneTimers coverage: the retention
// sweep must not delete a compensation-retry backoff row.
//
// It is one test over one fixture and one SUT call, not a table: the cases are
// rows of a single DELETE, not repeated invocations of the same call with
// varying inputs, so the setup-call-assert shape the table form exists for does
// not hold. See .claude/skills/table-test.
//
// It uses dbtest.RunTestSQLite directly rather than forEachDialect. The
// behaviour under test is a WHERE-clause term on an INTEGER column, identical on
// all three backends after dialect.Rebind, and SQLite is pure Go — the
// cross-dialect encoding risk that justifies the Postgres coverage in
// pruner_neverdue_test.go (a TEXT-vs-TIMESTAMPTZ timestamp threshold) has no
// analogue here. The dialect sweep over PruneTimers itself already runs in
// pruner_conformance_test.go.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// seedTimerRowOfKind is seedTimerRow (pruner_neverdue_test.go) with the timer
// kind opened up as a parameter. It is separate rather than a widening of that
// helper because the two fixtures vary on different axes: there the kind column
// is irrelevant and pinned at zero, here it is the axis under test.
func seedTimerRowOfKind(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	instanceID, nextRun string,
	triggerKind schedule.Kind,
	timerKind engine.TimerKind,
) {
	t.Helper()

	_, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version, trigger_kind)
		 VALUES (?, 't1', ?, ?, 'd', 1, ?)`,
		instanceID, nextRun, int16(timerKind), int16(triggerKind))
	require.NoError(t, err, "seed timer row %s", instanceID)
}

// TestPruneTimersSparesCompensationRetry pins the retention exclusion: a
// TimerCompensationRetry row is never deleted by [store.Pruner.PruneTimers],
// however far past its next_run is.
//
// Such a row is the only thing that will resume its compensation walk. Between
// the compensation action's failure and the backoff firing the walk makes no
// forward progress and holds no token of its own, and the retry budget's
// exhaustion is reachable only by the timer firing — so a retention job that
// deletes the row strands the walk.
//
// Every seeded row is load-bearing:
//   - comp-retry-expired is the row the exclusion exists for. Before the
//     exclusion it is deleted: the backoff is armed with schedule.AfterDuration,
//     whose schedule.Kind is KindOneTime, which is inside
//     nonRecurringTriggerKinds — the IN-list PruneTimers treats as eligible.
//   - control-retry-expired is the point of the fixture. It is identical to the
//     row above on every axis PruneTimers looks at EXCEPT kind, so it is what
//     dies if the exclusion is written on trigger_kind, or widened, or if
//     pruning is broken outright — a test with only the surviving row passes
//     when PruneTimers deletes nothing at all.
//   - control-intermediate-expired carries the ZERO value of engine.TimerKind
//     under a second, different member of the IN-list (KindExpr). It pins that
//     the exclusion narrows to one kind rather than sparing untagged rows, which
//     is what every row written before the exclusion, and every
//     non-compensation timer, is.
func TestPruneTimersSparesCompensationRetry(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.RunTestSQLite(t)

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	past := cutoff.Add(-72 * time.Hour).Format(textTimeLayout)

	seedTimerRowOfKind(ctx, t, db, "comp-retry-expired", past, schedule.KindOneTime, engine.TimerCompensationRetry)
	seedTimerRowOfKind(ctx, t, db, "control-retry-expired", past, schedule.KindOneTime, engine.TimerRetry)
	seedTimerRowOfKind(ctx, t, db, "control-intermediate-expired", past, schedule.KindExpr, engine.TimerIntermediate)

	require.Len(t, armedInstanceIDs(ctx, t, db), 3, "fixture")

	p, err := store.NewPruner(db, dialect.NewSQLite())
	require.NoError(t, err)

	n, err := p.PruneTimers(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "deleted count: both controls, and only them")

	assert.Equal(t, []string{"comp-retry-expired"}, armedInstanceIDs(ctx, t, db), "survivors")

	// The sweep is idempotent, and the spared row stays spared on a second pass:
	// a retention job runs on a schedule, so sparing it once is not enough.
	again, err := p.PruneTimers(ctx, cutoff)
	require.NoError(t, err)
	assert.Zero(t, again, "second sweep")
	assert.Equal(t, []string{"comp-retry-expired"}, armedInstanceIDs(ctx, t, db), "survivors after second sweep")
}
