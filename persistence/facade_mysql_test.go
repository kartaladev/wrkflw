package persistence_test

// facade_mysql_test.go covers the MySQL façade constructors: OpenMySQL,
// MigrateMySQL, NewMySQLTimerStore, NewMySQLRelay, and NewMySQLDeduper. It is
// kept in the black-box persistence_test package to enforce API-boundary tests only.

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// facadePub is a thread-safe recording publisher for facade tests.
type facadePub struct {
	mu     sync.Mutex
	events []kernel.OutboxEvent
}

func (p *facadePub) Publish(_ context.Context, ev kernel.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
	return nil
}

func (p *facadePub) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

// mysqlMinimalDef returns the simplest process definition for MySQL tests.
func mysqlMinimalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "mysql-minimal",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
}

// TestOpenMySQL_RoundTrip drives a minimal start→end process through the MySQL
// façade: MigrateMySQL is auto-run by RunTestMySQL, then OpenMySQL is used to
// Create and Load a process instance through the Store interface.
func TestOpenMySQL_RoundTrip(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t) // auto-migrates

	store, err := persistence.OpenMySQL(t.Context(), db)
	require.NoError(t, err)
	require.NotNil(t, store)

	def := mysqlMinimalDef()
	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)
	st, err := r.Run(t.Context(), def, "mysql-rt-1", map[string]any{"key": "val"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, st.Status)

	// Snapshot round-trips through MySQL.
	reloaded, _, err := store.Load(t.Context(), "mysql-rt-1")
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, reloaded.Status)
	assert.Equal(t, "mysql-rt-1", reloaded.InstanceID)
	assert.Equal(t, "val", reloaded.Variables["key"], "initial variables must round-trip")

	// Journal: at least one entry (StartInstance).
	entries, err := store.Entries(t.Context(), "mysql-rt-1")
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "journal must have at least one entry")
}

// TestMigrateMySQL_Idempotent verifies that MigrateMySQL can be called multiple
// times without error (goose's versioning makes re-runs a no-op).
func TestMigrateMySQL_Idempotent(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t) // auto-migrates once already

	// Second call must be idempotent.
	err := persistence.MigrateMySQL(t.Context(), db)
	require.NoError(t, err, "second MigrateMySQL call must be a no-op")
}

// TestNewMySQLTimerStore_ListArmed arms a timer via the Store and asserts that
// NewMySQLTimerStore(db).ListArmed returns it.
func TestNewMySQLTimerStore_ListArmed(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db)
	require.NoError(t, err)

	// Arm a timer by committing a step that includes a TimerArm.
	now := time.Unix(1700000000, 0).UTC()
	fireAt := now.Add(time.Hour)
	step := kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "timer-instance-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		TimerArms: []kernel.ArmedTimer{
			{
				InstanceID: "timer-instance-1",
				TimerID:    "t1",
				DefID:      "d",
				DefVersion: 1,
				FireAt:     fireAt,
				Kind:       engine.TimerDeadline,
			},
		},
	}
	_, err = store.Create(t.Context(), step)
	require.NoError(t, err)

	ts, err := persistence.NewMySQLTimerStore(db)
	require.NoError(t, err)
	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1, "exactly one armed timer expected")
	assert.Equal(t, "timer-instance-1", armed[0].InstanceID)
	assert.Equal(t, "t1", armed[0].TimerID)
	assert.Equal(t, engine.TimerDeadline, armed[0].Kind)
}

// TestOpenMySQL_WithHistoryCap verifies that the MySQLWithHistoryCap option
// is accepted by OpenMySQL and threads through to the underlying store.
func TestOpenMySQL_WithHistoryCap(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db, persistence.MySQLWithHistoryCap(5))
	require.NoError(t, err)
	require.NotNil(t, store)

	// Drive a minimal process to confirm the option is wired through.
	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)
	st, err := r.Run(t.Context(), &model.ProcessDefinition{
		ID:      "hist-mysql-1",
		Version: 1,
		Nodes:   []model.Node{model.NewStartEvent("start"), model.NewEndEvent("end")},
		Flows:   []model.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}, "hist-mysql-inst-1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}

// TestOpenMySQL_WithStoreObservabilityOptions verifies that the MySQL store
// observability options (logger, tracer, meter) are accepted by OpenMySQL.
func TestOpenMySQL_WithStoreObservabilityOptions(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db,
		persistence.MySQLWithStoreLogger(slog.Default()),
		persistence.MySQLWithStoreTracerProvider(tracenoop.NewTracerProvider()),
		persistence.MySQLWithStoreMeterProvider(metricnoop.NewMeterProvider()),
	)
	require.NoError(t, err)
	require.NotNil(t, store)
}

// seedOutboxForFacadeTest inserts n pending outbox rows directly into wrkflw_outbox.
// next_attempt_at is set to base so fake-clock relays claim the rows.
func seedOutboxForFacadeTest(t *testing.T, db *sql.DB, n int, base time.Time) {
	t.Helper()
	ctx := t.Context()
	for i := range n {
		dedup := "facade-seed-" + time.Now().Format("20060102150405.000000000") + "-" + fmt.Sprintf("%d", i)
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
			 VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)`,
			"facade-inst",
			"facade.event",
			`{"x":"y"}`,
			dedup,
			base.UTC(),
			base.UTC(),
		)
		require.NoError(t, err, "seed outbox row %d", i)
	}
}

// TestNewMySQLRelay_DrainsViaFacade verifies that NewMySQLRelay returns a Relay
// whose DrainOnce publishes seeded outbox rows via the supplied publisher.
func TestNewMySQLRelay_DrainsViaFacade(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	base := time.Now().UTC().Truncate(time.Second)
	seedOutboxForFacadeTest(t, db, 3, base)

	pub := &facadePub{}
	relay, err := persistence.NewMySQLRelay(db, pub,
		persistence.MySQLWithPollInterval(10*time.Millisecond),
		persistence.MySQLWithBatchSize(10),
	)
	require.NoError(t, err)

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, n, "DrainOnce must publish all 3 seeded rows")
	require.Equal(t, 3, pub.count(), "publisher must have received 3 events")
}

// TestNewMySQLDeduper_FirstThenDup verifies that NewMySQLDeduper returns a
// Deduper whose Seen method returns true on first observation and false on
// a repeat within a MySQL transaction.
func TestNewMySQLDeduper_FirstThenDup(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	d, err := persistence.NewMySQLDeduper(db)
	require.NoError(t, err)

	// Helper to call Seen inside a committed tx. Seen joins the ambient
	// transaction stashed in ctx by transaction.Begin, so the dedup record
	// commits atomically with the caller's business unit.
	callSeen := func(subscriber, msgID string) (bool, error) {
		q, ctx, err := transaction.Begin(t.Context(), db)
		require.NoError(t, err)
		first, err := d.Seen(ctx, subscriber, msgID)
		if err != nil {
			_ = q.Rollback(ctx)
			return false, err
		}
		return first, q.Commit(ctx)
	}

	first, err := callSeen("sub-facade", "msg-facade-1")
	require.NoError(t, err)
	require.True(t, first, "first observation must return true")

	dup, err := callSeen("sub-facade", "msg-facade-1")
	require.NoError(t, err)
	require.False(t, dup, "duplicate observation must return false")
}

// ─── MySQL Correlation Facade Tests ────────────────────────────────────────

// staticReg is a simple in-memory DefinitionRegistry for tests.
type staticReg struct {
	defs map[string]*model.ProcessDefinition
}

func (r *staticReg) Lookup(_ context.Context, defRef string) (*model.ProcessDefinition, error) {
	d, ok := r.defs[defRef]
	if !ok {
		return nil, fmt.Errorf("def not found: %s", defRef)
	}
	return d, nil
}

// TestNewMySQLCallLinkStore_ClaimAndMarkNotified seeds a terminal call link via
// the store and asserts that NewMySQLCallLinkStore.ClaimPending returns it,
// and MarkNotified marks it so a second ClaimPending returns nothing.
func TestNewMySQLCallLinkStore_ClaimAndMarkNotified(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db)
	require.NoError(t, err)

	// Seed a parent instance.
	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)
	_, err = r.Run(t.Context(), mysqlMinimalDef(), "parent-cls-1", nil)
	require.NoError(t, err)

	// Seed a terminal call link directly (child terminated, parent waiting).
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO wrkflw_call_links
		  (child_instance_id, parent_instance_id, parent_command_id,
		   parent_def_id, parent_def_version, depth, status, output, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'completed', '{}', NULL, NOW(6))
	`, "child-cls-1", "parent-cls-1", "cmd-1", "mysql-minimal", 1, 0)
	require.NoError(t, err)

	cls, err := persistence.NewMySQLCallLinkStore(db)
	require.NoError(t, err)

	// ClaimPending must return the link.
	pending, err := cls.ClaimPending(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "expected 1 pending link")
	assert.Equal(t, "child-cls-1", pending[0].Link.ChildInstanceID)
	assert.Equal(t, "parent-cls-1", pending[0].Link.ParentInstanceID)
	assert.True(t, pending[0].Outcome.Completed)

	// MarkNotified should succeed.
	err = cls.MarkNotified(t.Context(), "child-cls-1")
	require.NoError(t, err)

	// Second ClaimPending must return nothing.
	pending2, err := cls.ClaimPending(t.Context(), 10)
	require.NoError(t, err)
	assert.Empty(t, pending2, "no pending after MarkNotified")
}

// TestNewMySQLChainLinkStore_RecordAndLookup verifies NewMySQLChainLinkStore
// round-trips a chain link through MySQL.
func TestNewMySQLChainLinkStore_RecordAndLookup(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	links, err := persistence.NewMySQLChainLinkStore(db)
	require.NoError(t, err)
	at := time.Now().UTC().Truncate(time.Millisecond)

	link := kernel.ChainLink{
		PredecessorID:            "pred-1",
		Outcome:                  kernel.Outcome("success"),
		SuccessorID:              "succ-1",
		PredecessorDefinitionRef: "def-a:1",
		SuccessorDefinitionRef:   "def-b:2",
		StartVars:                map[string]any{"k": "v"},
		CreatedAt:                at,
	}
	err = links.Record(t.Context(), link)
	require.NoError(t, err)

	// LookupBySuccessor round-trip.
	got, ok, err := links.LookupBySuccessor(t.Context(), "succ-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "pred-1", got.PredecessorID)
	assert.Equal(t, kernel.Outcome("success"), got.Outcome)
	assert.Equal(t, "succ-1", got.SuccessorID)

	// ListByPredecessor.
	list, err := links.ListByPredecessor(t.Context(), "pred-1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "succ-1", list[0].SuccessorID)
}

// TestNewMySQLLister_ListsInstances verifies NewMySQLLister returns an
// InstanceLister that pages over instances seeded through the store.
func TestNewMySQLLister_ListsInstances(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db)
	require.NoError(t, err)

	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)
	for _, id := range []string{"lst-inst-a", "lst-inst-b"} {
		_, err := r.Run(t.Context(), mysqlMinimalDef(), id, nil)
		require.NoError(t, err)
	}

	lister, err := persistence.NewMySQLLister(db)
	require.NoError(t, err)
	page, err := lister.List(t.Context(), kernel.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "isolated DB must contain exactly the two seeded instances")

	// Collect IDs into a set and assert both seeded IDs are present (order is
	// not guaranteed — keyset pagination orders by StartedAt DESC, InstanceID DESC).
	gotIDs := make(map[string]struct{}, len(page.Items))
	for _, item := range page.Items {
		gotIDs[item.InstanceID] = struct{}{}
	}
	assert.Contains(t, gotIDs, "lst-inst-a", "lst-inst-a must appear in the listing")
	assert.Contains(t, gotIDs, "lst-inst-b", "lst-inst-b must appear in the listing")
}

// TestNewMySQLAdvisoryLockOwnership_AcquireAndClose verifies the facade ctor
// returns a kernel.Ownership that can Acquire an instance and Close cleanly.
func TestNewMySQLAdvisoryLockOwnership_AcquireAndClose(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	owner, closer, err := persistence.NewMySQLAdvisoryLockOwnership(t.Context(), db)
	require.NoError(t, err)
	require.NotNil(t, owner)
	require.NotNil(t, closer)

	acquired, err := owner.Acquire(t.Context(), "facade-lock-inst-1")
	require.NoError(t, err)
	assert.True(t, acquired, "first Acquire must succeed")

	// Close must release cleanly.
	err = closer.Close()
	require.NoError(t, err)
}

// TestNewMySQLAdvisoryLockOwnership_ClosedDBReturnsError verifies that
// NewMySQLAdvisoryLockOwnership returns a non-nil error (and nil owner/closer)
// when the underlying *sql.DB is closed and db.Conn fails. This covers the
// `if err != nil { return nil, nil, err }` branch in the facade constructor.
func TestNewMySQLAdvisoryLockOwnership_ClosedDBReturnsError(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	// Close the db so db.Conn() inside mysqlstore.NewAdvisoryLockOwnership fails.
	require.NoError(t, db.Close())

	owner, closer, err := persistence.NewMySQLAdvisoryLockOwnership(t.Context(), db)
	require.Error(t, err, "closed db must cause an error")
	require.Nil(t, owner, "owner must be nil on error")
	require.Nil(t, closer, "closer must be nil on error")
}

// ─── MySQL DefinitionStore Facade Tests ────────────────────────────────────

// TestNewMySQLDefinitionStore_RoundTrip verifies that NewMySQLDefinitionStore
// returns a DefinitionStore (same interface as NewDefinitionStore/Postgres) that
// PutDefinition-then-Lookup round-trips a definition through MySQL.
func TestNewMySQLDefinitionStore_RoundTrip(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	ds, err := persistence.NewMySQLDefinitionStore(db)
	require.NoError(t, err)
	require.NotNil(t, ds)

	def := &model.ProcessDefinition{
		ID:            "facade-def-1",
		Version:       1,
		CancelActions: []string{"rollback"},
	}
	require.NoError(t, ds.PutDefinition(t.Context(), def))

	// Exact lookup "defID:version".
	got, err := ds.Lookup(t.Context(), "facade-def-1:1")
	require.NoError(t, err)
	require.Equal(t, "facade-def-1", got.ID)
	require.Equal(t, 1, got.Version)
	require.Equal(t, []string{"rollback"}, got.CancelActions)

	// Latest-version lookup "defID".
	got2, err := ds.Lookup(t.Context(), "facade-def-1")
	require.NoError(t, err)
	require.Equal(t, "facade-def-1", got2.ID)
}

// ─── MySQL Pruner Facade Tests ──────────────────────────────────────────────

// TestNewMySQLPruner_PruneOutbox verifies that NewMySQLPruner returns a Pruner
// (same interface as NewPruner/Postgres) whose PruneOutbox removes only published
// rows older than the cutoff.
func TestNewMySQLPruner_PruneOutbox(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	cutoff := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour) // before cutoff → must be deleted
	new_ := cutoff.Add(24 * time.Hour) // after cutoff  → must survive

	ctx := t.Context()

	// Seed an OLD published row.
	_, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_outbox
		 (instance_id, topic, payload, dedup_key, created_at, status, published_at)
		 VALUES (?, ?, '{}', ?, ?, 'published', ?)`,
		"pruner-facade-inst-old", "test.prune.topic", "dk-pruner-facade-old", old, old)
	require.NoError(t, err)

	// Seed a NEW published row.
	_, err = db.ExecContext(ctx,
		`INSERT INTO wrkflw_outbox
		 (instance_id, topic, payload, dedup_key, created_at, status, published_at)
		 VALUES (?, ?, '{}', ?, ?, 'published', ?)`,
		"pruner-facade-inst-new", "test.prune.topic", "dk-pruner-facade-new", new_, new_)
	require.NoError(t, err)

	pr, err := persistence.NewMySQLPruner(db)
	require.NoError(t, err)
	require.NotNil(t, pr)

	n, err := pr.PruneOutbox(ctx, cutoff)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1), "at least the seeded old row must be deleted")

	// Old row must be gone.
	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = 'dk-pruner-facade-old'`).Scan(&count))
	assert.Equal(t, 0, count, "old outbox row must be deleted")

	// New row must survive.
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = 'dk-pruner-facade-new'`).Scan(&count))
	assert.Equal(t, 1, count, "new outbox row must survive")
}

// TestNewMySQLPruner_PruneProcessedMessages verifies PruneProcessedMessages via
// the facade Pruner interface, proving full wiring through to the MySQL backend.
func TestNewMySQLPruner_PruneProcessedMessages(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	cutoff := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour) // before cutoff → must be deleted
	new_ := cutoff.Add(24 * time.Hour) // after cutoff  → must survive

	ctx := t.Context()

	// Seed an OLD processed_message row.
	_, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
		 VALUES ('facade-sub', 'facade-msg-old', ?)`,
		old)
	require.NoError(t, err)

	// Seed a NEW processed_message row.
	_, err = db.ExecContext(ctx,
		`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
		 VALUES ('facade-sub', 'facade-msg-new', ?)`,
		new_)
	require.NoError(t, err)

	pr, err := persistence.NewMySQLPruner(db)
	require.NoError(t, err)
	require.NotNil(t, pr)

	n, err := pr.PruneProcessedMessages(ctx, cutoff)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1), "at least the seeded old row must be deleted")

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_processed_message WHERE message_id = 'facade-msg-old'`).Scan(&count))
	assert.Equal(t, 0, count, "old processed_message row must be deleted")

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wrkflw_processed_message WHERE message_id = 'facade-msg-new'`).Scan(&count))
	assert.Equal(t, 1, count, "new processed_message row must survive")
}

// ─── MySQL CallNotifier Facade Tests ────────────────────────────────────────

// TestNewMySQLCallNotifier_DeliversViaMySQLStore seeds a terminal call link via
// the MySQL call-link store, runs DrainOnce on a CallNotifier built through the
// facade, and asserts the deliver func fired exactly once.
func TestNewMySQLCallNotifier_DeliversViaMySQLStore(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db)
	require.NoError(t, err)

	// Create a parent process instance (the definition "mysql-minimal" id:version 1).
	def := mysqlMinimalDef()
	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)
	_, err = r.Run(t.Context(), def, "notifier-parent-1", nil)
	require.NoError(t, err)

	// Seed a terminal call link so the notifier has something to deliver.
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO wrkflw_call_links
		  (child_instance_id, parent_instance_id, parent_command_id,
		   parent_def_id, parent_def_version, depth, status, output, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'completed', '{}', NULL, NOW(6))
	`, "notifier-child-1", "notifier-parent-1", "cmd-notifier", "mysql-minimal", 1, 0)
	require.NoError(t, err)

	// Build a simple in-memory registry wrapping the definition.
	reg := &staticReg{defs: map[string]*model.ProcessDefinition{
		"mysql-minimal:1": def,
	}}

	var deliverCalled int
	deliverFn := runtime.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
		deliverCalled++
		return nil
	})

	notifier, err := persistence.NewMySQLCallNotifier(db, deliverFn, reg)
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
