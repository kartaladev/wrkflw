package processtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/persistence/cache/hotcache"
	"github.com/kartaladev/wrkflw/processtest"
)

// TestRunTaskStoreConformance runs the exported conformance suite against every
// TaskStore this module bundles. It is the positive leg: each of these stores
// upholds the Upsert contract (ADR-0183), so the suite must report no failure.
// The negative leg — that a non-conforming store is actually CAUGHT — lives in
// taskstoreconformance_internal_test.go, where a recorder captures the failures
// instead of propagating them to this suite.
//
// The cases carry no assert closure because RunTaskStoreConformance returns
// nothing: the SUT reports through t itself, so "this row passed" is the
// assertion.
func TestRunTaskStoreConformance(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// newStore is the factory handed to RunTaskStoreConformance. Its *testing.T
		// is the CASE's: the SQLite leg provisions a database against it, so the
		// handle is closed when that case ends rather than at the end of the run.
		newStore func(t *testing.T) humantask.TaskStore
	}

	cases := []testCase{
		{
			name: "MemTaskStore",
			newStore: func(*testing.T) humantask.TaskStore {
				return humantask.NewMemTaskStore()
			},
		},
		{
			name: "sqlite HumanTaskStore",
			newStore: func(t *testing.T) humantask.TaskStore {
				// Pure-Go SQLite: no Docker daemon involved. A fresh database per
				// call, because the helper documents newStore as returning an
				// EMPTY store every time.
				db := dbtest.RunTestSQLite(t)
				ts, err := store.NewHumanTaskStore(db, dialect.NewSQLite())
				require.NoError(t, err, "NewHumanTaskStore")
				return ts
			},
		},
		{
			name: "CachingTaskStore over MemTaskStore",
			newStore: func(t *testing.T) humantask.TaskStore {
				cs, err := persistence.NewCachingTaskStore(humantask.NewMemTaskStore(), hotcache.New())
				require.NoError(t, err, "NewCachingTaskStore")
				return cs
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			processtest.RunTaskStoreConformance(t, tc.newStore)
		})
	}
}
