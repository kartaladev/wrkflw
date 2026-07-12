package persistence_test

// facade_sqlite_test.go covers the SQLite façade constructors: NewSQLiteTimerStore,
// NewSQLiteRelay, NewSQLiteCallLinkStore, NewSQLiteChainLinkStore, NewSQLiteLister,
// NewSQLiteCallNotifier, NewSQLiteDefinitionStore, and NewSQLitePruner.
//
// All tests use dbtest.RunTestSQLite which opens a fresh in-process SQLite database
// and applies migrations — no Docker daemon required.

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/calllink"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// sqliteMinimalDef returns the simplest process definition for SQLite tests.
func sqliteMinimalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "sqlite-minimal",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
}

// seedSQLiteOutbox inserts n pending outbox rows directly into wrkflw_outbox for
// SQLite. next_attempt_at is set to base so fake-clock relays claim the rows.
func seedSQLiteOutbox(t *testing.T, db *sql.DB, n int, base time.Time) {
	t.Helper()
	ctx := t.Context()
	for i := range n {
		dedup := fmt.Sprintf("sqlite-facade-seed-%d-%d", base.UnixNano(), i)
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
			 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
			"sqlite-facade-inst",
			"sqlite.facade.event",
			`{"x":"y"}`,
			dedup,
			base.UTC().Format("2006-01-02 15:04:05.000000"),
			base.UTC().Format("2006-01-02 15:04:05.000000"),
		)
		require.NoError(t, err, "seed outbox row %d", i)
	}
}

// ─── TimerStore ─────────────────────────────────────────────────────────────

// TestNewSQLiteTimerStore_ListArmed arms a timer via the Store and asserts that
// NewSQLiteTimerStore(db).ListArmed returns it.
func TestNewSQLiteTimerStore_ListArmed(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	store, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err)

	now := time.Unix(1700000000, 0).UTC()
	fireAt := now.Add(time.Hour)
	step := kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "sqlite-timer-instance-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		TimerArms: []kernel.ArmedTimer{
			{
				InstanceID: "sqlite-timer-instance-1",
				TimerID:    "t1",
				DefID:      "d",
				DefVersion: 1,
				NextRun:    fireAt,
				Kind:       engine.TimerDeadline,
			},
		},
	}
	_, err = store.Create(t.Context(), step)
	require.NoError(t, err)

	ts, err := persistence.NewSQLiteTimerStore(db)
	require.NoError(t, err)
	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1, "exactly one armed timer expected")
	assert.Equal(t, "sqlite-timer-instance-1", armed[0].InstanceID)
	assert.Equal(t, "t1", armed[0].TimerID)
	assert.Equal(t, engine.TimerDeadline, armed[0].Kind)
}

// ─── Relay ──────────────────────────────────────────────────────────────────

// TestNewSQLiteRelay_DrainsViaFacade verifies that NewSQLiteRelay returns a Relay
// whose DrainOnce publishes seeded outbox rows via the supplied publisher.
// SQLite relay is poll-only (no LISTEN/NOTIFY).
func TestNewSQLiteRelay_DrainsViaFacade(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	base := time.Now().UTC().Truncate(time.Second)
	seedSQLiteOutbox(t, db, 3, base)

	pub := &facadePub{}
	relay, err := persistence.NewSQLiteRelay(db, pub,
		persistence.MySQLWithPollInterval(10*time.Millisecond),
		persistence.MySQLWithBatchSize(10),
	)
	require.NoError(t, err)

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, n, "DrainOnce must publish all 3 seeded rows")
	require.Equal(t, 3, pub.count(), "publisher must have received 3 events")
}

// TestNewSQLiteRelay_OutboxStatsViaFacadeInterface verifies that a persistence.Relay
// assigned to the interface variable exposes OutboxStats without a type assertion.
// Seeding N pending rows then calling relay.OutboxStats(ctx) must return Pending==N
// and Dead==0.
func TestNewSQLiteRelay_OutboxStatsViaFacadeInterface(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	base := time.Now().UTC().Truncate(time.Second)
	const pending = 4
	seedSQLiteOutbox(t, db, pending, base)

	pub := &facadePub{} // no-op publisher — rows stay pending

	// callViaInterface drives the relay through the persistence.Relay interface
	// to confirm OutboxStats is reachable without a type assertion.
	callViaInterface := func(r persistence.Relay) (kernel.OutboxStats, error) {
		return r.OutboxStats(t.Context())
	}

	sqliteRelay, err := persistence.NewSQLiteRelay(db, pub)
	require.NoError(t, err)
	stats, err := callViaInterface(sqliteRelay)
	require.NoError(t, err)
	assert.Equal(t, int64(pending), stats.Pending, "Pending must equal seeded row count")
	assert.Equal(t, int64(0), stats.Dead, "Dead must be 0 when no rows have been quarantined")
}

// ─── CallLinkStore ──────────────────────────────────────────────────────────

// TestNewSQLiteCallLinkStore_ClaimAndMarkNotified seeds a terminal call link via
// the store and asserts that NewSQLiteCallLinkStore.ClaimPending returns it,
// and MarkNotified marks it so a second ClaimPending returns nothing.
func TestNewSQLiteCallLinkStore_ClaimAndMarkNotified(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	store, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err)

	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
	require.NoError(t, err)
	_, err = driver.Drive(t.Context(), sqliteMinimalDef(), "sqlite-parent-cls-1", nil)
	require.NoError(t, err)

	// Seed a terminal call link directly.
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO wrkflw_call_links
		  (child_instance_id, parent_instance_id, parent_command_id,
		   parent_def_id, parent_def_version, depth, status, output, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'completed', '{}', NULL, CURRENT_TIMESTAMP)
	`, "sqlite-child-cls-1", "sqlite-parent-cls-1", "cmd-sqlite-1", "sqlite-minimal", 1, 0)
	require.NoError(t, err)

	cls, err := persistence.NewSQLiteCallLinkStore(db)
	require.NoError(t, err)

	// ClaimPending must return the link.
	pending, err := cls.ClaimPending(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "expected 1 pending link")
	assert.Equal(t, "sqlite-child-cls-1", pending[0].Link.ChildInstanceID)
	assert.Equal(t, "sqlite-parent-cls-1", pending[0].Link.ParentInstanceID)
	assert.True(t, pending[0].Outcome.Completed)

	// MarkNotified should succeed.
	err = cls.MarkNotified(t.Context(), "sqlite-child-cls-1")
	require.NoError(t, err)

	// Second ClaimPending must return nothing.
	pending2, err := cls.ClaimPending(t.Context(), 10)
	require.NoError(t, err)
	assert.Empty(t, pending2, "no pending after MarkNotified")
}

// ─── ChainLinkStore ─────────────────────────────────────────────────────────

// TestNewSQLiteChainLinkStore_RecordAndLookup verifies NewSQLiteChainLinkStore
// round-trips a chain link through SQLite.
func TestNewSQLiteChainLinkStore_RecordAndLookup(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	links, err := persistence.NewSQLiteChainLinkStore(db)
	require.NoError(t, err)
	at := time.Now().UTC().Truncate(time.Millisecond)

	link := kernel.ChainLink{
		PredecessorID:            "sqlite-pred-1",
		Outcome:                  kernel.ChainOutcome("success"),
		SuccessorID:              "sqlite-succ-1",
		PredecessorDefinitionRef: model.Version("def-a", 1),
		SuccessorDefinitionRef:   model.Version("def-b", 2),
		StartVars:                map[string]any{"k": "v"},
		CreatedAt:                at,
	}
	err = links.Record(t.Context(), link)
	require.NoError(t, err)

	// LookupBySuccessor round-trip.
	got, ok, err := links.LookupBySuccessor(t.Context(), "sqlite-succ-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "sqlite-pred-1", got.PredecessorID)
	assert.Equal(t, kernel.ChainOutcome("success"), got.Outcome)
	assert.Equal(t, "sqlite-succ-1", got.SuccessorID)

	// ListByPredecessor.
	list, err := links.ListByPredecessor(t.Context(), "sqlite-pred-1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "sqlite-succ-1", list[0].SuccessorID)
}

// ─── Lister ─────────────────────────────────────────────────────────────────

// TestNewSQLiteLister_ListsInstances verifies NewSQLiteLister returns an
// InstanceLister that pages over instances seeded through the store.
func TestNewSQLiteLister_ListsInstances(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	store, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err)

	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
	require.NoError(t, err)
	for _, id := range []string{"sqlite-lst-inst-a", "sqlite-lst-inst-b"} {
		_, err := driver.Drive(t.Context(), sqliteMinimalDef(), id, nil)
		require.NoError(t, err)
	}

	lister, err := persistence.NewSQLiteLister(db)
	require.NoError(t, err)
	page, err := lister.List(t.Context(), kernel.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "isolated DB must contain exactly the two seeded instances")

	gotIDs := make(map[string]struct{}, len(page.Items))
	for _, item := range page.Items {
		gotIDs[item.InstanceID] = struct{}{}
	}
	assert.Contains(t, gotIDs, "sqlite-lst-inst-a", "sqlite-lst-inst-a must appear in the listing")
	assert.Contains(t, gotIDs, "sqlite-lst-inst-b", "sqlite-lst-inst-b must appear in the listing")
}

// ─── CallNotifier ───────────────────────────────────────────────────────────

// TestNewSQLiteCallNotifier_DeliversViaSQLiteStore seeds a terminal call link via
// the SQLite call-link store, runs DrainOnce on a CallNotifier built through the
// facade, and asserts the deliver func fired exactly once.
func TestNewSQLiteCallNotifier_DeliversViaSQLiteStore(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	store, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err)

	def := sqliteMinimalDef()
	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
	require.NoError(t, err)
	_, err = driver.Drive(t.Context(), def, "sqlite-notifier-parent-1", nil)
	require.NoError(t, err)

	// Seed a terminal call link so the notifier has something to deliver.
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO wrkflw_call_links
		  (child_instance_id, parent_instance_id, parent_command_id,
		   parent_def_id, parent_def_version, depth, status, output, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'completed', '{}', NULL, CURRENT_TIMESTAMP)
	`, "sqlite-notifier-child-1", "sqlite-notifier-parent-1", "cmd-sqlite-notifier", "sqlite-minimal", 1, 0)
	require.NoError(t, err)

	reg := &staticReg{defs: map[string]*model.ProcessDefinition{
		"sqlite-minimal:1": def,
	}}

	var deliverCalled int
	deliverFn := calllink.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
		deliverCalled++
		return nil
	})

	notifier, err := persistence.NewSQLiteCallNotifier(db, deliverFn, reg)
	require.NoError(t, err)

	notified, err := notifier.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, notified, "DrainOnce must report 1 notified link")
	assert.Equal(t, 1, deliverCalled, "deliver func must be called exactly once")

	// Second DrainOnce is a no-op (link is marked notified).
	notified2, err := notifier.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, notified2, "second DrainOnce must be a no-op")
}

// ─── DefinitionStore ────────────────────────────────────────────────────────

// TestNewSQLiteDefinitionStore_RoundTrip verifies that NewSQLiteDefinitionStore
// returns a DefinitionStore that PutDefinition-then-Lookup round-trips a definition
// through SQLite.
func TestNewSQLiteDefinitionStore_RoundTrip(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	ds, err := persistence.NewSQLiteDefinitionStore(db)
	require.NoError(t, err)
	require.NotNil(t, ds)

	def := &model.ProcessDefinition{
		ID:            "sqlite-facade-def-1",
		Version:       1,
		CancelActions: []string{"rollback"},
	}
	require.NoError(t, ds.PutDefinition(t.Context(), def))

	// Pinned lookup Version(id, version).
	got, err := ds.Lookup(t.Context(), model.Version("sqlite-facade-def-1", 1))
	require.NoError(t, err)
	require.Equal(t, "sqlite-facade-def-1", got.ID)
	require.Equal(t, 1, got.Version)
	require.Equal(t, []string{"rollback"}, got.CancelActions)

	// Latest-version lookup Latest(id).
	got2, err := ds.Lookup(t.Context(), model.Latest("sqlite-facade-def-1"))
	require.NoError(t, err)
	require.Equal(t, "sqlite-facade-def-1", got2.ID)
}

// ─── Pruner ─────────────────────────────────────────────────────────────────

// TestNewSQLitePruner_PruneOutbox verifies that NewSQLitePruner returns a Pruner
// whose PruneOutbox removes only published rows older than the cutoff.
func TestNewSQLitePruner_PruneOutbox(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	cutoff := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour) // before cutoff → must be deleted
	new_ := cutoff.Add(24 * time.Hour) // after cutoff  → must survive

	ctx := t.Context()

	// Seed an OLD published row.
	_, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_outbox
		 (instance_id, topic, payload, dedup_key, created_at, status, published_at)
		 VALUES (?, ?, '{}', ?, ?, 'published', ?)`,
		"sqlite-pruner-facade-inst-old", "test.prune.topic", "dk-sqlite-pruner-facade-old",
		old.Format("2006-01-02 15:04:05"), old.Format("2006-01-02 15:04:05"))
	require.NoError(t, err)

	// Seed a NEW published row.
	_, err = db.ExecContext(ctx,
		`INSERT INTO wrkflw_outbox
		 (instance_id, topic, payload, dedup_key, created_at, status, published_at)
		 VALUES (?, ?, '{}', ?, ?, 'published', ?)`,
		"sqlite-pruner-facade-inst-new", "test.prune.topic", "dk-sqlite-pruner-facade-new",
		new_.Format("2006-01-02 15:04:05"), new_.Format("2006-01-02 15:04:05"))
	require.NoError(t, err)

	pr, err := persistence.NewSQLitePruner(db)
	require.NoError(t, err)
	require.NotNil(t, pr)

	n, err := pr.PruneOutbox(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "exactly the one seeded old row must be deleted")

	// Old row must be gone.
	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = 'dk-sqlite-pruner-facade-old'`).Scan(&count))
	assert.Equal(t, 0, count, "old outbox row must be deleted")

	// New row must survive.
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = 'dk-sqlite-pruner-facade-new'`).Scan(&count))
	assert.Equal(t, 1, count, "new outbox row must survive")
}

// TestNewSQLitePruner_PruneProcessedMessages verifies PruneProcessedMessages via
// the facade Pruner interface, proving full wiring through to the SQLite backend.
func TestNewSQLitePruner_PruneProcessedMessages(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	cutoff := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour) // before cutoff → must be deleted
	new_ := cutoff.Add(24 * time.Hour) // after cutoff  → must survive

	ctx := t.Context()

	// Seed OLD processed_message row.
	_, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
		 VALUES ('sqlite-facade-sub', 'sqlite-facade-msg-old', ?)`,
		old.Format("2006-01-02 15:04:05"))
	require.NoError(t, err)

	// Seed NEW processed_message row.
	_, err = db.ExecContext(ctx,
		`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
		 VALUES ('sqlite-facade-sub', 'sqlite-facade-msg-new', ?)`,
		new_.Format("2006-01-02 15:04:05"))
	require.NoError(t, err)

	pr, err := persistence.NewSQLitePruner(db)
	require.NoError(t, err)
	require.NotNil(t, pr)

	n, err := pr.PruneProcessedMessages(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "exactly the one seeded old row must be deleted")

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_processed_message WHERE message_id = 'sqlite-facade-msg-old'`).Scan(&count))
	assert.Equal(t, 0, count, "old processed_message row must be deleted")

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_processed_message WHERE message_id = 'sqlite-facade-msg-new'`).Scan(&count))
	assert.Equal(t, 1, count, "new processed_message row must survive")
}
