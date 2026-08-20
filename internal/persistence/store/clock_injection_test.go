package store_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// clockPinnedInstant is deliberately decades away from the wall clock.
//
// This is load-bearing, not decoration. clockwork.NewFakeClock() SEEDS FROM
// WALL TIME in the pinned clockwork version — measured here: a fake clock and
// time.Now() read 417ns apart. A regression test built on NewFakeClock() would
// therefore pass even with the production code still calling time.Now(),
// because the two instants are indistinguishable at any realistic assertion
// tolerance. Pinning the fake clock to 1999 makes "did the store use the
// injected clock?" answerable by exact equality (backlog item 126).
var clockPinnedInstant = time.Date(1999, 3, 4, 5, 6, 7, 890123456, time.UTC)

// TestPersistedTimestampsUseInjectedClock is the ADR-0138 conformance test for
// the store layer: every persisted wall-clock stamp must come from the injected
// clockwork.Clock, not from time.Now().
//
// It covers the five persisted wall-clock sites in this package (the two
// remaining time.Now() calls in store_core.go are latency stopwatches whose
// values are never persisted, and are deliberately out of scope):
//
//   - Store.Create        → wrkflw_instances.updated_at
//   - Store.Commit        → wrkflw_instances.updated_at
//   - DefinitionStore.PutDefinition → wrkflw_definitions.created_at
//   - Deduper.Seen        → wrkflw_processed_message.processed_at
//   - ChainLinkStore.Record → wrkflw_chain_links.created_at
//
// SQLite suffices: the clock is threaded identically for all three dialects and
// the stamping happens above the dialect seam. The TEXT timestamp encoding is
// SQLite-specific, so the read-back parses the column as RFC3339Nano.
func TestPersistedTimestampsUseInjectedClock(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// write performs one persisting operation against db using clk as its
		// only time source.
		write func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock)
		// query selects the single persisted timestamp column written by write.
		query string
		// key is the bind argument identifying the row write inserted.
		key string
		// assert checks the timestamp that was actually persisted.
		assert func(t *testing.T, got time.Time)
	}

	sqliteDialect := dialect.NewSQLite()

	cases := []testCase{
		{
			name: "Store.Create stamps updated_at from the injected clock",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				s, err := store.New(db, sqliteDialect, store.WithStoreClock(clk))
				require.NoError(t, err)
				_, err = s.Create(t.Context(), newTestInstance(t, "clk-create"))
				require.NoError(t, err, "Create")
			},
			query: `SELECT updated_at FROM wrkflw_instances WHERE instance_id = ?`,
			key:   "clk-create",
			assert: func(t *testing.T, got time.Time) {
				assert.True(t, got.Equal(clockPinnedInstant),
					"updated_at must be the injected instant %s, got %s", clockPinnedInstant, got)
			},
		},
		{
			name: "Store.Commit stamps updated_at from the injected clock",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				s, err := store.New(db, sqliteDialect, store.WithStoreClock(clk))
				require.NoError(t, err)
				v, err := s.Create(t.Context(), newTestInstance(t, "clk-commit"))
				require.NoError(t, err, "Create")

				// Advance so Commit's stamp is distinguishable from Create's:
				// a shared instant could not tell the two sites apart.
				clk.Advance(90 * time.Minute)

				_, err = s.Commit(t.Context(), v, newTestInstance(t, "clk-commit"))
				require.NoError(t, err, "Commit")
			},
			query: `SELECT updated_at FROM wrkflw_instances WHERE instance_id = ?`,
			key:   "clk-commit",
			assert: func(t *testing.T, got time.Time) {
				want := clockPinnedInstant.Add(90 * time.Minute)
				assert.True(t, got.Equal(want),
					"updated_at must be the advanced injected instant %s, got %s", want, got)
			},
		},
		{
			name: "DefinitionStore.PutDefinition stamps created_at from the injected clock",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				ds, err := store.NewDefinitionStore(db, sqliteDialect, store.WithDefinitionClock(clk))
				require.NoError(t, err)
				require.NoError(t, ds.PutDefinition(t.Context(), &model.ProcessDefinition{
					ID: "clk-def", Version: 1,
				}), "PutDefinition")
			},
			query: `SELECT created_at FROM wrkflw_definitions WHERE def_id = ?`,
			key:   "clk-def",
			assert: func(t *testing.T, got time.Time) {
				assert.True(t, got.Equal(clockPinnedInstant),
					"created_at must be the injected instant %s, got %s", clockPinnedInstant, got)
			},
		},
		{
			name: "Deduper.Seen stamps processed_at from the injected clock",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				d, err := store.NewDeduper(db, sqliteDialect, store.WithDeduperClock(clk))
				require.NoError(t, err)
				first, err := d.Seen(t.Context(), "clk-sub", "clk-msg")
				require.NoError(t, err, "Seen")
				require.True(t, first, "Seen must report first-time")
			},
			query: `SELECT processed_at FROM wrkflw_processed_message WHERE subscriber = ?`,
			key:   "clk-sub",
			assert: func(t *testing.T, got time.Time) {
				assert.True(t, got.Equal(clockPinnedInstant),
					"processed_at must be the injected instant %s, got %s", clockPinnedInstant, got)
			},
		},
		{
			name: "ChainLinkStore.Record stamps created_at from the injected clock",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				cls, err := store.NewChainLinkStore(db, sqliteDialect, store.WithChainLinkClock(clk))
				require.NoError(t, err)
				// CreatedAt left zero on purpose: that is the branch that falls
				// back to the store's own clock.
				require.NoError(t, cls.Record(t.Context(), kernel.ChainLink{
					PredecessorID: "clk-pred",
					Outcome:       kernel.ChainOutcome("done"),
					SuccessorID:   "clk-succ",
				}), "Record")
			},
			query: `SELECT created_at FROM wrkflw_chain_links WHERE predecessor_instance_id = ?`,
			key:   "clk-pred",
			assert: func(t *testing.T, got time.Time) {
				assert.True(t, got.Equal(clockPinnedInstant),
					"created_at must be the injected instant %s, got %s", clockPinnedInstant, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := dbtest.RunTestSQLite(t)
			clk := clockwork.NewFakeClockAt(clockPinnedInstant)

			tc.write(t, db, clk)

			q, err := database.From(db)
			require.NoError(t, err, "database.From")

			var raw string
			require.NoError(t,
				q.QueryRow(t.Context(), sqliteDialect.Rebind(tc.query), tc.key).Scan(&raw),
				"read back persisted timestamp")

			got, err := time.Parse(time.RFC3339Nano, raw)
			require.NoError(t, err, "parse persisted timestamp %q", raw)
			tc.assert(t, got.UTC())
		})
	}
}
