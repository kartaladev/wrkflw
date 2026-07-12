package persistence_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
)

// TestPrunerFacade verifies the public persistence.Pruner surfaces every
// time-cutoff pruner over a real database (ADR-0052). Each method deletes only
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
