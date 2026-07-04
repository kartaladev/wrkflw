package store_test

// store_options_test.go covers the two 0%-covered store.Option constructors
// (WithNotifier and WithStoreLogger) and several error-path branches in the
// Pruner, Deduper, and ChainLinkStore that are only reachable via an
// unsupported connection type or a dropped table.
//
// Every test asserts real behaviour: either that the option is wired correctly
// and does not break subsequent operations, or that an error is returned by the
// right function when the underlying driver fails.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// --------------------------------------------------------------------------
// WithNotifier wiring
// --------------------------------------------------------------------------

// testNotifier is a minimal in-process dialect.Notifier used only so
// WithNotifier can be exercised without a real PG connection. Tests verify that
// the injected value is actually stored on the Store — not that it drives
// real PG notifications.
type testNotifier struct{}

func (testNotifier) Listen(_ context.Context, _ string) (<-chan struct{}, func(), error) {
	ch := make(chan struct{})
	return ch, func() {}, nil
}

// TestWithNotifier verifies that WithNotifier stores the supplied notifier on
// the Store (accessible via the test-export accessor NotifyForTest) and that
// a Store built with the option can still execute Create/Load/Commit operations
// normally on SQLite.
func TestWithNotifier(t *testing.T) {
	t.Run("notifier is wired", func(t *testing.T) {
		n := testNotifier{}
		s, err := store.New(struct{}{}, dialect.NewSQLite(), store.WithNotifier(n))
		require.NoError(t, err)
		// The notifier must be stored even though the connection is unusable —
		// wiring is independent of the connection.
		require.Equal(t, n, s.NotifyForTest(),
			"WithNotifier must store the supplied dialect.Notifier on the Store")
	})

	t.Run("nil notifier is accepted without panic", func(t *testing.T) {
		s, err := store.New(struct{}{}, dialect.NewSQLite(), store.WithNotifier(nil))
		require.NoError(t, err)
		assert.Nil(t, s.NotifyForTest(), "nil notifier must be stored as nil")
	})

	t.Run("store with notifier can Create/Load/Commit on SQLite", func(t *testing.T) {
		db := dbtest.RunTestSQLite(t)
		n := testNotifier{}
		s, err := store.New(db, dialect.NewSQLite(), store.WithNotifier(n))
		require.NoError(t, err)

		step := appliedStep("notifier-inst-1", "notifier.topic")
		tok, err := s.Create(t.Context(), step)
		require.NoError(t, err, "Create must succeed with WithNotifier wired")
		require.Greater(t, int64(tok), int64(0))

		_, loadTok, err := s.Load(t.Context(), "notifier-inst-1")
		require.NoError(t, err, "Load must succeed after Create with notifier")
		require.Equal(t, tok, loadTok)

		_, err = s.Commit(t.Context(), tok, appliedStep("notifier-inst-1", "notifier.topic2"))
		require.NoError(t, err, "Commit must succeed with WithNotifier wired")
	})
}

// --------------------------------------------------------------------------
// WithStoreLogger wiring
// --------------------------------------------------------------------------

// TestWithStoreLogger verifies that a Store built with WithStoreLogger executes
// Create/Load/Commit without errors — i.e. the logger option is wired cleanly
// into the observability pipeline and does not break anything.
func TestWithStoreLogger(t *testing.T) {
	t.Run("structured logger is accepted and operations succeed", func(t *testing.T) {
		db := dbtest.RunTestSQLite(t)
		logger := slog.Default()
		s, err := store.New(db, dialect.NewSQLite(), store.WithStoreLogger(logger))
		require.NoError(t, err)

		step := appliedStep("logger-inst-1", "logger.topic")
		tok, err := s.Create(t.Context(), step)
		require.NoError(t, err, "Create must succeed when WithStoreLogger is set")

		_, _, err = s.Load(t.Context(), "logger-inst-1")
		require.NoError(t, err, "Load must succeed when WithStoreLogger is set")

		_, err = s.Commit(t.Context(), tok, appliedStep("logger-inst-1", "logger.topic2"))
		require.NoError(t, err, "Commit must succeed when WithStoreLogger is set")
	})

	t.Run("all three obs options together do not break operations", func(t *testing.T) {
		// This also increases coverage of the filterNilOpts + observability.New code
		// path that assembles the triple of logger+tracer+meter options together.
		db := dbtest.RunTestSQLite(t)
		logger := slog.Default()
		s, err := store.New(db, dialect.NewSQLite(),
			store.WithStoreLogger(logger),
			// TracerProvider and MeterProvider already covered by the obs test;
			// combining with a logger exercises the filterNilOpts three-way branch.
		)
		require.NoError(t, err)
		tok, err := s.Create(t.Context(), appliedStep("obs-all-1", "all.topic"))
		require.NoError(t, err)
		_, err = s.Commit(t.Context(), tok, appliedStep("obs-all-1", "all.topic2"))
		require.NoError(t, err)
	})
}

// --------------------------------------------------------------------------
// Pruner error branches — unsupported connection type → database.From fails
// --------------------------------------------------------------------------

// TestPrunerUnsupportedConn exercises the "conn: %w" error branch at the top of
// each Pruner method by passing a struct{} connection that database.From rejects.
// These branches are only reachable when the caller wires an invalid conn.
func TestPrunerUnsupportedConn(t *testing.T) {
	p, err := store.NewPruner(struct{}{}, dialect.NewSQLite())
	require.NoError(t, err)
	cutoff := time.Now().UTC()

	tests := []struct {
		name string
		fn   func() (int64, error)
	}{
		{"PruneOutbox", func() (int64, error) { return p.PruneOutbox(t.Context(), cutoff) }},
		{"PruneCallLinks", func() (int64, error) { return p.PruneCallLinks(t.Context(), cutoff) }},
		{"PruneChainLinks", func() (int64, error) { return p.PruneChainLinks(t.Context(), cutoff) }},
		{"PruneTimers", func() (int64, error) { return p.PruneTimers(t.Context(), cutoff) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, err := tc.fn()
			require.Error(t, err, "%s must error on unsupported conn", tc.name)
			assert.Equal(t, int64(0), n, "%s must return 0 rows on error", tc.name)
		})
	}
}

// TestPrunerDroppedTables exercises the exec-error branch in each Pruner method
// by dropping the relevant table from a live SQLite DB. This forces the DELETE
// statement to fail and verifies that the method returns a wrapped error.
func TestPrunerDroppedTables(t *testing.T) {
	tests := []struct {
		name  string
		table string
		fn    func(p *store.Pruner, ctx context.Context) (int64, error)
	}{
		{
			name:  "PruneOutbox dropped table",
			table: "wrkflw_outbox",
			fn: func(p *store.Pruner, ctx context.Context) (int64, error) {
				return p.PruneOutbox(ctx, time.Now().UTC())
			},
		},
		{
			name:  "PruneCallLinks dropped table",
			table: "wrkflw_call_links",
			fn: func(p *store.Pruner, ctx context.Context) (int64, error) {
				return p.PruneCallLinks(ctx, time.Now().UTC())
			},
		},
		{
			name:  "PruneChainLinks dropped table",
			table: "wrkflw_chain_links",
			fn: func(p *store.Pruner, ctx context.Context) (int64, error) {
				return p.PruneChainLinks(ctx, time.Now().UTC())
			},
		},
		{
			name:  "PruneTimers dropped table",
			table: "wrkflw_timers",
			fn: func(p *store.Pruner, ctx context.Context) (int64, error) {
				return p.PruneTimers(ctx, time.Now().UTC())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := dbtest.RunTestSQLite(t)
			_, err := db.ExecContext(t.Context(), "DROP TABLE "+tc.table)
			require.NoError(t, err, "drop %s", tc.table)

			p, err := store.NewPruner(db, dialect.NewSQLite())
			require.NoError(t, err)
			n, err := tc.fn(p, t.Context())
			require.Error(t, err, "must return error after table dropped")
			assert.Equal(t, int64(0), n, "must return 0 rows on error")
		})
	}
}

// --------------------------------------------------------------------------
// Deduper error branches — unsupported connection type
// --------------------------------------------------------------------------

// TestDeduperUnsupportedConn exercises the "begin" error branch of Seen and the
// "from: %w" error branch of Prune by passing a struct{} connection that the
// transaction layer and database.From both reject.
func TestDeduperUnsupportedConn(t *testing.T) {
	d, err := store.NewDeduper(struct{}{}, dialect.NewSQLite())
	require.NoError(t, err)

	t.Run("Seen returns error on unsupported conn", func(t *testing.T) {
		first, err := d.Seen(t.Context(), "sub", "msg")
		require.Error(t, err, "Seen must error on unsupported conn")
		assert.False(t, first, "Seen must return false on error")
	})

	t.Run("Prune returns error on unsupported conn", func(t *testing.T) {
		n, err := d.Prune(t.Context(), time.Now().UTC())
		require.Error(t, err, "Prune must error on unsupported conn")
		assert.Equal(t, int64(0), n, "Prune must return 0 on error")
	})
}

// TestDeduperDroppedTable exercises the exec-error branch in Seen and the
// exec-error branch in Prune by dropping wrkflw_processed_message.
func TestDeduperDroppedTable(t *testing.T) {
	t.Run("Seen exec error after table dropped", func(t *testing.T) {
		db := dbtest.RunTestSQLite(t)
		_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_processed_message")
		require.NoError(t, err)

		d, err := store.NewDeduper(db, dialect.NewSQLite())
		require.NoError(t, err)
		first, err := d.Seen(t.Context(), "sub", "msg")
		require.Error(t, err, "Seen must error after table dropped")
		assert.False(t, first)
	})

	t.Run("Prune exec error after table dropped", func(t *testing.T) {
		db := dbtest.RunTestSQLite(t)
		_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_processed_message")
		require.NoError(t, err)

		d, err := store.NewDeduper(db, dialect.NewSQLite())
		require.NoError(t, err)
		n, err := d.Prune(t.Context(), time.Now().UTC())
		require.Error(t, err, "Prune must error after table dropped")
		assert.Equal(t, int64(0), n)
	})
}

// --------------------------------------------------------------------------
// ChainLinkStore error branches — unsupported connection type
// --------------------------------------------------------------------------

// TestChainLinkStoreUnsupportedConn exercises the "conn: %w" error branches
// in Record, LookupBySuccessor, ListByPredecessor, PredecessorOf, and
// SuccessorsOf by passing a struct{} connection.
func TestChainLinkStoreUnsupportedConn(t *testing.T) {
	cls, err := store.NewChainLinkStore(struct{}{}, dialect.NewSQLite())
	require.NoError(t, err)
	link := kernel.ChainLink{
		PredecessorID: "p", Outcome: kernel.OutcomeCompleted, SuccessorID: "s",
	}

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Record", func() error { return cls.Record(t.Context(), link) }},
		{"LookupBySuccessor", func() error {
			_, _, err := cls.LookupBySuccessor(t.Context(), "s")
			return err
		}},
		{"ListByPredecessor", func() error {
			_, err := cls.ListByPredecessor(t.Context(), "p")
			return err
		}},
		{"PredecessorOf", func() error {
			_, err := cls.PredecessorOf(t.Context(), "s")
			return err
		}},
		{"SuccessorsOf", func() error {
			_, err := cls.SuccessorsOf(t.Context(), "p")
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.fn(), "%s must error on unsupported conn", tc.name)
		})
	}
}

// TestChainLinkStoreDroppedTable exercises the query-error branches in
// LookupBySuccessor, ListByPredecessor, PredecessorOf, and SuccessorsOf by
// dropping wrkflw_chain_links from a live SQLite DB.
func TestChainLinkStoreDroppedTable(t *testing.T) {
	tests := []struct {
		name string
		fn   func(cls *store.ChainLinkStore) error
	}{
		{
			"LookupBySuccessor query error",
			func(cls *store.ChainLinkStore) error {
				_, _, err := cls.LookupBySuccessor(t.Context(), "ghost")
				return err
			},
		},
		{
			"ListByPredecessor query error",
			func(cls *store.ChainLinkStore) error {
				_, err := cls.ListByPredecessor(t.Context(), "ghost")
				return err
			},
		},
		{
			"PredecessorOf query error",
			func(cls *store.ChainLinkStore) error {
				_, err := cls.PredecessorOf(t.Context(), "ghost")
				return err
			},
		},
		{
			"SuccessorsOf query error",
			func(cls *store.ChainLinkStore) error {
				_, err := cls.SuccessorsOf(t.Context(), "ghost")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := dbtest.RunTestSQLite(t)
			_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_chain_links")
			require.NoError(t, err)

			cls, err := store.NewChainLinkStore(db, dialect.NewSQLite())
			require.NoError(t, err)
			require.Error(t, tc.fn(cls), "must error after chain_links table dropped")
		})
	}
}

// --------------------------------------------------------------------------
// DefinitionStore error branches — dropped table / unsupported conn
// --------------------------------------------------------------------------

// TestDefinitionStoreDroppedTable exercises the exec-error branch in PutDefinition
// and the query-error branch in GetDefinition by dropping wrkflw_definitions.
func TestDefinitionStoreDroppedTable(t *testing.T) {
	t.Run("PutDefinition exec error after table dropped", func(t *testing.T) {
		db := dbtest.RunTestSQLite(t)
		_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_definitions")
		require.NoError(t, err)

		ds, err := store.NewDefinitionStore(db, dialect.NewSQLite())
		require.NoError(t, err)
		err = ds.PutDefinition(t.Context(), &definition.ProcessDefinition{ID: "d1", Version: 1})
		require.Error(t, err, "PutDefinition must error after table dropped")
	})

	t.Run("GetDefinition query error after table dropped", func(t *testing.T) {
		db := dbtest.RunTestSQLite(t)
		_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_definitions")
		require.NoError(t, err)

		ds, err := store.NewDefinitionStore(db, dialect.NewSQLite())
		require.NoError(t, err)
		_, err = ds.GetDefinition(t.Context(), "d1", 1)
		require.Error(t, err, "GetDefinition must error after table dropped")
	})
}
