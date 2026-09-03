package persistence_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
)

// TestPrunerFacade verifies the public persistence.Pruner surfaces every
// time-cutoff pruner over a real database. Each method deletes only
// the eligible old row and reports the count.
func TestPrunerFacade(t *testing.T) {
	t.Parallel()

	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	p, err := persistence.NewPruner(pool)
	require.NoError(t, err)

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	type pruneCase struct {
		name   string
		seed   func(t *testing.T)
		prune  func(t *testing.T) (int64, error)
		assert func(t *testing.T, deleted int64, err error)
	}

	cases := []pruneCase{
		{
			name: "outbox published before cutoff",
			seed: func(t *testing.T) {
				_, err := pool.Exec(t.Context(),
					`INSERT INTO wrkflw_outbox
					   (instance_id, topic, payload, dedup_key, created_at, published_at, status)
					 VALUES ('i','t','{}','ob1',$1,$1,'published')`, old)
				require.NoError(t, err)
			},
			prune: func(t *testing.T) (int64, error) { return p.PruneOutbox(t.Context(), cutoff) },
			assert: func(t *testing.T, deleted int64, err error) {
				require.NoError(t, err)
				assert.Equal(t, int64(1), deleted)
			},
		},
		{
			name: "call links notified before cutoff",
			seed: func(t *testing.T) {
				_, err := pool.Exec(t.Context(),
					`INSERT INTO wrkflw_call_links
					   (child_instance_id, parent_instance_id, parent_command_id,
					    parent_def_id, parent_def_version, depth, status, created_at, notified_at)
					 VALUES ('c1','p','cmd','d',1,1,'notified',$1,$1)`, old)
				require.NoError(t, err)
			},
			prune: func(t *testing.T) (int64, error) { return p.PruneCallLinks(t.Context(), cutoff) },
			assert: func(t *testing.T, deleted int64, err error) {
				require.NoError(t, err)
				assert.Equal(t, int64(1), deleted)
			},
		},
		{
			name: "chain links created before cutoff",
			seed: func(t *testing.T) {
				_, err := pool.Exec(t.Context(),
					`INSERT INTO wrkflw_chain_links
					   (predecessor_instance_id, outcome, successor_instance_id, created_at)
					 VALUES ('p1','completed','s1',$1)`, old)
				require.NoError(t, err)
			},
			prune: func(t *testing.T) (int64, error) { return p.PruneChainLinks(t.Context(), cutoff) },
			assert: func(t *testing.T, deleted int64, err error) {
				require.NoError(t, err)
				assert.Equal(t, int64(1), deleted)
			},
		},
		{
			name: "processed messages before cutoff",
			seed: func(t *testing.T) {
				_, err := pool.Exec(t.Context(),
					`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
					 VALUES ('s','m1',$1)`, old)
				require.NoError(t, err)
			},
			prune: func(t *testing.T) (int64, error) { return p.PruneProcessedMessages(t.Context(), cutoff) },
			assert: func(t *testing.T, deleted int64, err error) {
				require.NoError(t, err)
				assert.Equal(t, int64(1), deleted)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.seed(t)
			deleted, err := tc.prune(t)
			tc.assert(t, deleted, err)
		})
	}
}

// TestPruner_PruneTimers_ThroughInterface verifies that PruneTimers is reachable
// through the public persistence.Pruner interface — the method must be part of the
// interface contract, not just on the concrete type. Seeds two wrkflw_timers rows
// (one before, one after the cutoff) and asserts only the pre-cutoff row is deleted.
func TestPruner_PruneTimers_ThroughInterface(t *testing.T) {
	t.Parallel()

	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	// NewPruner returns the interface type; p is already persistence.Pruner —
	// calling PruneTimers through it validates the method is on the interface.
	p, err := persistence.NewPruner(pool)
	require.NoError(t, err)

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	before := cutoff.Add(-1 * time.Hour) // strictly before cutoff → should be pruned
	after := cutoff.Add(1 * time.Hour)   // after cutoff → must survive

	_, err = pool.Exec(t.Context(),
		`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version)
		 VALUES ('inst-prune','timer-old',$1,1,'def1',1)`, before)
	require.NoError(t, err)

	_, err = pool.Exec(t.Context(),
		`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version)
		 VALUES ('inst-prune','timer-new',$1,1,'def1',1)`, after)
	require.NoError(t, err)

	n, err := p.PruneTimers(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the pre-cutoff timer is pruned")

	// Verify the post-cutoff row survived.
	var remaining int
	row := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_timers WHERE instance_id='inst-prune'`)
	require.NoError(t, row.Scan(&remaining))
	assert.Equal(t, 1, remaining, "post-cutoff timer must survive")
}

// TestNeverDueTimerReclaimerCapability pins the reachability decision: the
// orphan sweep must be callable from consumer wiring.
//
// Every public pruner constructor returns the persistence.Pruner *interface*,
// and internal/persistence/store is unimportable by a consumer, so a method
// living only on the concrete store type would be dead code. This test
// therefore exercises exactly the assertion a consumer writes — through the
// interface type the constructor actually returns — and never touches the
// concrete type.
//
// It then calls the method against a seeded fixture rather than only asserting
// the assertion succeeds: a capability that type-asserts but is wired to
// nothing would still return (0, nil). The orphan must go and the sub-epoch
// one-shot control (reachable by PruneTimers, and the guard on the two sweeps
// staying disjoint) must survive.
//
// SQLite is the backend here because it is the only one that can hold the
// fixture: MySQL rejects a zero next_run outright (Error 1292), and it is
// container-free (dbtest.RunTestSQLite, pure Go).
func TestNeverDueTimerReclaimerCapability(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.RunTestSQLite(t)

	// Fixed-width nine-digit-fraction RFC3339 — the encoding SQLite timestamp
	// columns are written in.
	const textTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
	zero := time.Time{}.UTC().Format(textTimeLayout)

	seed := func(instanceID, nextRun string, kind schedule.Kind) {
		t.Helper()
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version, trigger_kind)
			 VALUES (?, 't1', ?, 0, 'd', 1, ?)`,
			instanceID, nextRun, int16(kind))
		require.NoError(t, err, "seed timer row %s", instanceID)
	}
	seed("orphan-recurring", zero, schedule.KindDuration)
	seed("control-suboneshot", zero, schedule.KindOneTime)

	// A consumer only ever holds this interface — NewSQLitePruner, NewMySQLPruner
	// and NewPruner all return it.
	var pruner persistence.Pruner
	pruner, err := persistence.NewSQLitePruner(db)
	require.NoError(t, err)

	reclaimer, ok := pruner.(persistence.NeverDueTimerReclaimer)
	require.True(t, ok, "a Pruner from persistence.NewSQLitePruner must satisfy NeverDueTimerReclaimer")

	n, err := reclaimer.ReclaimNeverDueTimers(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the sub-epoch recurring orphan is reclaimed")

	var survivor string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT instance_id FROM wrkflw_timers`).Scan(&survivor))
	assert.Equal(t, "control-suboneshot", survivor,
		"a sub-epoch one-shot is PruneTimers' business, not the orphan sweep's")
}
