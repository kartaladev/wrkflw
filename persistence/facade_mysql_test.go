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

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// facadePub is a thread-safe recording publisher for facade tests.
type facadePub struct {
	mu     sync.Mutex
	events []runtime.OutboxEvent
}

func (p *facadePub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
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
	db := database.RunTestMySQL(t) // auto-migrates

	store, err := persistence.OpenMySQL(t.Context(), db)
	require.NoError(t, err)
	require.NotNil(t, store)

	def := mysqlMinimalDef()
	r := runtime.NewRunner(nil, store)
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
	db := database.RunTestMySQL(t) // auto-migrates once already

	// Second call must be idempotent.
	err := persistence.MigrateMySQL(t.Context(), db)
	require.NoError(t, err, "second MigrateMySQL call must be a no-op")
}

// TestNewMySQLTimerStore_ListArmed arms a timer via the Store and asserts that
// NewMySQLTimerStore(db).ListArmed returns it.
func TestNewMySQLTimerStore_ListArmed(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db)
	require.NoError(t, err)

	// Arm a timer by committing a step that includes a TimerArm.
	now := time.Unix(1700000000, 0).UTC()
	fireAt := now.Add(time.Hour)
	step := runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "timer-instance-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		TimerArms: []runtime.ArmedTimer{
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

	ts := persistence.NewMySQLTimerStore(db)
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
	db := database.RunTestMySQL(t)

	store, err := persistence.OpenMySQL(t.Context(), db, persistence.MySQLWithHistoryCap(5))
	require.NoError(t, err)
	require.NotNil(t, store)

	// Drive a minimal process to confirm the option is wired through.
	r := runtime.NewRunner(nil, store)
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
	db := database.RunTestMySQL(t)

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
	db := database.RunTestMySQL(t)

	base := time.Now().UTC().Truncate(time.Second)
	seedOutboxForFacadeTest(t, db, 3, base)

	pub := &facadePub{}
	relay := persistence.NewMySQLRelay(db, pub,
		persistence.MySQLWithPollInterval(10*time.Millisecond),
		persistence.MySQLWithBatchSize(10),
	)

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, n, "DrainOnce must publish all 3 seeded rows")
	require.Equal(t, 3, pub.count(), "publisher must have received 3 events")
}

// TestNewMySQLDeduper_FirstThenDup verifies that NewMySQLDeduper returns a
// MySQLDeduper whose Seen method returns true on first observation and false on
// a repeat within a MySQL transaction.
func TestNewMySQLDeduper_FirstThenDup(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)

	d := persistence.NewMySQLDeduper(db)

	// Helper to call Seen inside a committed tx.
	callSeen := func(subscriber, msgID string) (bool, error) {
		tx, err := db.BeginTx(t.Context(), nil)
		require.NoError(t, err)
		first, err := d.Seen(t.Context(), tx, subscriber, msgID)
		if err != nil {
			_ = tx.Rollback()
			return false, err
		}
		return first, tx.Commit()
	}

	first, err := callSeen("sub-facade", "msg-facade-1")
	require.NoError(t, err)
	require.True(t, first, "first observation must return true")

	dup, err := callSeen("sub-facade", "msg-facade-1")
	require.NoError(t, err)
	require.False(t, dup, "duplicate observation must return false")
}
