package persistence_test

// clock_injection_test.go is the CONSUMER-side counterpart to
// internal/persistence/store/clock_injection_test.go. That test proves the
// store honours an injected clock; this one proves a consumer of the public
// facade can actually inject one. The two are not redundant: the store options
// shipped reachable only from inside the module, so their doc promise ("inject
// a clockwork.FakeClock in tests") was unkeepable off-module.

import (
	"database/sql"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// facadeClockInstant is deliberately decades away from the wall clock.
//
// This is load-bearing, not decoration. clockwork.NewFakeClock() SEEDS FROM
// WALL TIME in the pinned clockwork version (clockwork@v0.5.0 NewFakeClock ->
// NewFakeClockAt(time.Now()...)), so a fake-clock assertion written against it
// passes just as well against production code that still calls time.Now().
// Pinning to 1998 makes "did the facade thread the injected clock through?"
// answerable by exact equality.
var facadeClockInstant = time.Date(1998, 7, 12, 13, 14, 15, 160170180, time.UTC)

// TestFacadeClockOptionsReachPersistedTimestamps asserts that each clock
// option exposed by the public persistence facade actually reaches the
// timestamp the corresponding store persists.
//
// What makes these cases fail today: the facade forwards none of the four
// clock options, and three of the four constructors accept no options at all,
// so every case is a compile error until the facade exposes them.
//
// SQLite is sufficient — the clock is threaded above the dialect seam and is
// identical for all three backends — and it needs no Docker daemon. The
// read-back parses RFC3339Nano because that is SQLite's TEXT timestamp codec.
func TestFacadeClockOptionsReachPersistedTimestamps(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// write performs one persisting operation through the public facade,
		// with clk as the constructor's only time source.
		write func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock)
		// query selects the single persisted timestamp column write wrote.
		query string
		// key identifies the row write inserted.
		key string
		// assert checks the timestamp that was actually persisted.
		assert func(t *testing.T, got time.Time)
	}

	equalsInstant := func(column string) func(*testing.T, time.Time) {
		return func(t *testing.T, got time.Time) {
			assert.True(t, got.Equal(facadeClockInstant),
				"%s must be the injected instant %s, got %s", column, facadeClockInstant, got)
		}
	}

	cases := []testCase{
		{
			name: "OpenSQLite + WithStoreClock stamps instances.updated_at",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				s, err := persistence.OpenSQLite(t.Context(), db, persistence.WithStoreClock(clk))
				require.NoError(t, err, "OpenSQLite")
				_, err = s.Create(t.Context(), newFacadeStep("facade-clk-create"))
				require.NoError(t, err, "Create")
			},
			query:  `SELECT updated_at FROM wrkflw_instances WHERE instance_id = ?`,
			key:    "facade-clk-create",
			assert: equalsInstant("updated_at"),
		},
		{
			name: "NewSQLiteDefinitionStore + WithDefinitionClock stamps definitions.created_at",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				ds, err := persistence.NewSQLiteDefinitionStore(db, persistence.WithDefinitionClock(clk))
				require.NoError(t, err, "NewSQLiteDefinitionStore")
				require.NoError(t, ds.PutDefinition(t.Context(), &model.ProcessDefinition{
					ID: "facade-clk-def", Version: 1,
				}), "PutDefinition")
			},
			query:  `SELECT created_at FROM wrkflw_definitions WHERE def_id = ?`,
			key:    "facade-clk-def",
			assert: equalsInstant("created_at"),
		},
		{
			name: "NewSQLiteDeduper + WithDeduperClock stamps processed_message.processed_at",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				d, err := persistence.NewSQLiteDeduper(db, persistence.WithDeduperClock(clk))
				require.NoError(t, err, "NewSQLiteDeduper")
				first, err := d.Seen(t.Context(), "facade-clk-sub", "facade-clk-msg")
				require.NoError(t, err, "Seen")
				require.True(t, first, "Seen must report first-time")
			},
			query:  `SELECT processed_at FROM wrkflw_processed_message WHERE subscriber = ?`,
			key:    "facade-clk-sub",
			assert: equalsInstant("processed_at"),
		},
		{
			name: "NewSQLiteChainLinkStore + WithChainLinkClock stamps chain_links.created_at",
			write: func(t *testing.T, db *sql.DB, clk *clockwork.FakeClock) {
				cls, err := persistence.NewSQLiteChainLinkStore(db, persistence.WithChainLinkClock(clk))
				require.NoError(t, err, "NewSQLiteChainLinkStore")
				// CreatedAt left zero on purpose: that is the branch that falls
				// back to the store's own clock.
				require.NoError(t, cls.Record(t.Context(), kernel.ChainLink{
					PredecessorID: "facade-clk-pred",
					Outcome:       kernel.ChainOutcome("done"),
					SuccessorID:   "facade-clk-succ",
				}), "Record")
			},
			query:  `SELECT created_at FROM wrkflw_chain_links WHERE predecessor_instance_id = ?`,
			key:    "facade-clk-pred",
			assert: equalsInstant("created_at"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := dbtest.RunTestSQLite(t)
			clk := clockwork.NewFakeClockAt(facadeClockInstant)

			tc.write(t, db, clk)

			var raw string
			require.NoError(t,
				db.QueryRowContext(t.Context(), tc.query, tc.key).Scan(&raw),
				"read back persisted timestamp")

			got, err := time.Parse(time.RFC3339Nano, raw)
			require.NoError(t, err, "parse persisted timestamp %q", raw)
			tc.assert(t, got.UTC())
		})
	}
}

// newFacadeStep builds the minimal AppliedStep the facade InstanceStore accepts.
func newFacadeStep(id string) kernel.AppliedStep {
	now := time.Unix(1700000000, 123456789).UTC()
	return kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "facade-clk",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
			Variables:  map[string]any{"str": "hello"},
			Tokens: []engine.Token{
				{ID: "tok-1", NodeID: "start", State: engine.TokenActive, EnteredAt: now},
			},
			CmdSeq:   1,
			TokenSeq: 1,
		},
		Trigger: engine.NewStartInstance(now, map[string]any{"str": "hello"}),
	}
}
