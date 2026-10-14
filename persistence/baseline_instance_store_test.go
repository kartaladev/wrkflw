package persistence_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// forEachBaselineInstanceStore runs fn once per SQL dialect against the
// instance store the persistence package's public Open* constructor returns,
// configured with opts, over a fresh migrated database.
func forEachBaselineInstanceStore(t *testing.T, opts []persistence.Option, fn func(t *testing.T, s persistence.InstanceStore)) {
	t.Helper()

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		pool := dbtest.RunTestDatabase(t)
		require.NoError(t, persistence.Migrate(t.Context(), pool))
		s, err := persistence.OpenPostgres(t.Context(), pool, opts...)
		require.NoError(t, err)
		fn(t, s)
	})

	t.Run("mysql", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t) // already migrated
		s, err := persistence.OpenMySQL(t.Context(), db, opts...)
		require.NoError(t, err)
		fn(t, s)
	})

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestSQLite(t) // already migrated
		s, err := persistence.OpenSQLite(t.Context(), db, opts...)
		require.NoError(t, err)
		fn(t, s)
	})
}

// baselineStep builds a minimal running AppliedStep for instance id carrying
// history as its inline node-visit history.
func baselineStep(id string, history []engine.NodeVisit) kernel.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "baseline-instance-store",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
			History:    history,
		},
		Trigger: engine.NewStartInstance(now, nil),
	}
}

// TestBaselineInstanceStoreCreatesAFreshIDBesideAnExistingOne characterizes the
// instance-store scenario "Duplicate and fresh IDs" (openspec/specs/instance-store)
// on every SQL dialect: beside an existing "i1", creating "i1" again is refused
// with ErrInstanceExists and creating the fresh "i9" succeeds with version 1.
func TestBaselineInstanceStoreCreatesAFreshIDBesideAnExistingOne(t *testing.T) {
	forEachBaselineInstanceStore(t, nil, func(t *testing.T, s persistence.InstanceStore) {
		_, err := s.Create(t.Context(), baselineStep("i1", nil))
		require.NoError(t, err)

		cases := []struct {
			name   string
			id     string
			assert func(t *testing.T, v kernel.Version, err error)
		}{
			{
				name: "duplicate i1",
				id:   "i1",
				assert: func(t *testing.T, _ kernel.Version, err error) {
					require.ErrorIs(t, err, persistence.ErrInstanceExists)
				},
			},
			{
				name: "fresh i9",
				id:   "i9",
				assert: func(t *testing.T, v kernel.Version, err error) {
					require.NoError(t, err)
					assert.Equal(t, kernel.Version(1), v)
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				v, err := s.Create(t.Context(), baselineStep(tc.id, nil))
				tc.assert(t, v, err)
			})
		}
	})
}

// TestBaselineInstanceStoreCapsClosedVisits characterizes the instance-store
// scenario "Capping closed visits" (openspec/specs/instance-store) through a
// store configured with persistence.WithHistoryCap on every SQL dialect: after
// a commit, the loaded history keeps every open visit plus the n most recent
// closed visits, in order.
func TestBaselineInstanceStoreCapsClosedVisits(t *testing.T) {
	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	closed := func(node string, at time.Time) engine.NodeVisit {
		left := at.Add(time.Second)
		return engine.NodeVisit{NodeID: node, TokenID: node + "-tok", EnteredAt: at, LeftAt: &left}
	}
	open := func(node string, at time.Time) engine.NodeVisit {
		return engine.NodeVisit{NodeID: node, TokenID: node + "-tok", EnteredAt: at}
	}
	nodeIDs := func(h []engine.NodeVisit) []string {
		ids := make([]string, 0, len(h))
		for _, v := range h {
			ids = append(ids, v.NodeID)
		}
		return ids
	}

	cases := []struct {
		name    string
		n       int
		history []engine.NodeVisit
		assert  func(t *testing.T, got []engine.NodeVisit)
	}{
		{
			name: "n=1 keeps the old open visit and the newest closed one",
			n:    1,
			history: []engine.NodeVisit{
				open("human", base),
				closed("c1", base.Add(1*time.Minute)),
				closed("c2", base.Add(2*time.Minute)),
				closed("c3", base.Add(3*time.Minute)),
			},
			assert: func(t *testing.T, got []engine.NodeVisit) {
				assert.Equal(t, []string{"human", "c3"}, nodeIDs(got))
			},
		},
		{
			name: "n=2 keeps the two newest closed visits around the open one",
			n:    2,
			history: []engine.NodeVisit{
				closed("c1", base.Add(1*time.Minute)),
				closed("c2", base.Add(2*time.Minute)),
				open("o1", base.Add(3*time.Minute)),
				closed("c3", base.Add(4*time.Minute)),
			},
			assert: func(t *testing.T, got []engine.NodeVisit) {
				assert.Equal(t, []string{"c2", "o1", "c3"}, nodeIDs(got))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := []persistence.Option{persistence.WithHistoryCap(tc.n)}
			forEachBaselineInstanceStore(t, opts, func(t *testing.T, s persistence.InstanceStore) {
				tok, err := s.Create(t.Context(), baselineStep("capped", nil))
				require.NoError(t, err)
				_, err = s.Commit(t.Context(), tok, baselineStep("capped", tc.history))
				require.NoError(t, err)

				got, _, err := s.Load(t.Context(), "capped")
				require.NoError(t, err)
				tc.assert(t, got.History)
			})
		})
	}
}

// TestBaselineMigrateSQLiteIsIdempotent characterizes the instance-store
// scenario "Second migration is a no-op" (openspec/specs/instance-store) for
// SQLite: on a database persistence.MigrateSQLite already migrated, running it
// again returns nil.
func TestBaselineMigrateSQLiteIsIdempotent(t *testing.T) {
	t.Parallel()

	dsn := "file:" + filepath.Join(t.TempDir(), "baseline.db") + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, persistence.MigrateSQLite(t.Context(), db), "first migration")
	require.NoError(t, persistence.MigrateSQLite(t.Context(), db), "second migration must be a no-op")
}
