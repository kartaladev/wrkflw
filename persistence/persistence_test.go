// Package persistence_test is the black-box end-to-end test for the consumer-facing
// persistence façade. It drives a real runtime.ProcessDriver against a Postgres container
// to prove that Tasks 1–8 compose correctly on real Postgres.
package persistence_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// minimalStartEndDefinition returns the simplest possible process: start → end.
// It completes synchronously in a single Run call with no service tasks, so no
// action catalog is required.
func minimalStartEndDefinition() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      "minimal",
		Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// capturingPublisher records every OutboxEvent published to it.
type capturingPublisher struct {
	events []kernel.OutboxEvent
}

func (c *capturingPublisher) Publish(_ context.Context, ev kernel.OutboxEvent) error {
	c.events = append(c.events, ev)
	return nil
}

// TestOpenPostgresEndToEnd is the capstone integration test. It:
//  1. Spins up a real Postgres container via RunTestDatabase.
//  2. Applies the schema with persistence.Migrate.
//  3. Opens a Postgres-backed store with persistence.OpenPostgres.
//  4. Drives a minimal start→end process through runtime.ProcessDriver.
//  5. Asserts: terminal status is Completed, the snapshot round-trips, journal
//     entries are recorded, and the wrkflw_outbox has an instance.completed row.
//
// This test owns coverage of OpenPostgres (options loop and store construction).
// The Option type is unexported config, so external tests cannot construct real
// options; the option loop is exercised here via the store's normal operation.
func TestOpenPostgresEndToEnd(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	store, err := persistence.OpenPostgres(t.Context(), pool)
	require.NoError(t, err)

	def := minimalStartEndDefinition()

	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)
	st, err := r.Run(t.Context(), def, "i-e2e", map[string]any{"k": "v"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, st.Status)

	// The snapshot round-trips to Postgres and back.
	reloaded, _, err := store.Load(t.Context(), "i-e2e")
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, reloaded.Status)
	assert.Equal(t, "i-e2e", reloaded.InstanceID)
	assert.Equal(t, "v", reloaded.Variables["k"], "initial variables must be persisted in snapshot")

	// Journal: start→end fires StartInstance + no other triggers (no service tasks).
	// At minimum StartInstance should be present.
	entries, err := store.Entries(t.Context(), "i-e2e")
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "journal must have at least one entry (StartInstance)")

	// Outbox: instance.completed must be present (written by the transactional store).
	var n int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM wrkflw_outbox WHERE topic = 'instance.completed'`).Scan(&n))
	require.Equal(t, 1, n, "exactly one instance.completed outbox row expected")
}

// TestMigrateIsIdempotent proves that calling Migrate twice does not error
// (goose's versioning makes it a safe no-op on re-run).
func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))
	require.NoError(t, persistence.Migrate(t.Context(), pool), "second Migrate call must be a no-op")
}

// TestOpenPostgresNotFoundSentinel proves that the re-exported ErrInstanceNotFound
// sentinel is reachable and works with errors.Is through the façade.
func TestOpenPostgresNotFoundSentinel(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	store, err := persistence.OpenPostgres(t.Context(), pool)
	require.NoError(t, err)

	_, _, err = store.Load(t.Context(), "nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, persistence.ErrInstanceNotFound,
		"ErrInstanceNotFound must be usable via the persistence façade package")
}

// TestNewDefinitionStoreAndCachingRegistry exercises the NewDefinitionStore and
// NewCachingDefinitionRegistry constructor façades.
func TestNewDefinitionStoreAndCachingRegistry(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	// NewDefinitionStore must return a non-nil *postgres.DefinitionStore that
	// satisfies kernel.DefinitionRegistry.
	ds, err := persistence.NewDefinitionStore(pool)
	require.NoError(t, err)
	require.NotNil(t, ds)

	// Round-trip a definition through the store.
	def := &definition.ProcessDefinition{ID: "d1", Version: 1,
		Nodes: []definition.Node{event.NewStart("start")},
	}
	require.NoError(t, ds.PutDefinition(t.Context(), def))

	got, err := ds.Lookup(t.Context(), "d1")
	require.NoError(t, err)
	assert.Equal(t, "d1", got.ID)

	// NewCachingDefinitionRegistry wraps ds with a TTL cache.
	cached, err := persistence.NewCachingDefinitionRegistry(ds, 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, cached)

	// Lookup through the cache.
	cachedDef, err := cached.Lookup(t.Context(), "d1")
	require.NoError(t, err)
	assert.Equal(t, "d1", cachedDef.ID)
}

// TestRelayOptionsConstructors exercises WithPollInterval and WithBatchSize
// option constructors through the façade.
func TestRelayOptionsConstructors(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	pub := &capturingPublisher{}
	relay, err := persistence.NewRelay(pool, pub,
		persistence.WithPollInterval(50*time.Millisecond),
		persistence.WithBatchSize(10),
		persistence.WithRelayClock(clock.System()),
		persistence.WithMaxDeliveryAttempts(5),
		persistence.WithRelayBackoff(time.Second, time.Minute),
	)
	require.NoError(t, err)
	require.NotNil(t, relay)

	// DrainOnce on an empty outbox must succeed with 0 rows drained.
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

// TestNewRelayDrainsOutbox proves the Relay façade drains the outbox end-to-end.
func TestNewRelayDrainsOutbox(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	store, err := persistence.OpenPostgres(t.Context(), pool)
	require.NoError(t, err)

	// Run a process to generate an outbox event.
	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)
	st, err := r.Run(t.Context(), minimalStartEndDefinition(), "i-relay", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, st.Status)

	// Drain via persistence.NewRelay.
	pub := &capturingPublisher{}
	relay, err := persistence.NewRelay(pool, pub)
	require.NoError(t, err)
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "relay must drain exactly one outbox event")
	require.Len(t, pub.events, 1)
	assert.Equal(t, "instance.completed", pub.events[0].Topic)
}

// TestRelayTelemetryOptions verifies that the three telemetry façade options
// (WithRelayLogger, WithRelayTracerProvider, WithRelayMeterProvider) are accepted
// by NewRelay and that a wrkflw.relay.batch span is emitted through the injected
// TracerProvider when DrainOnce is called.
func TestRelayTelemetryOptions(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	mp := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	relay, err := persistence.NewRelay(pool, &capturingPublisher{},
		persistence.WithRelayLogger(slog.Default()),
		persistence.WithRelayTracerProvider(tp),
		persistence.WithRelayMeterProvider(mp),
	)
	require.NoError(t, err)
	require.NotNil(t, relay)

	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "empty outbox: 0 rows published")

	// A batch span must be emitted even for an empty drain.
	ended := sr.Ended()
	var saw bool
	for _, s := range ended {
		if s.Name() == "wrkflw.relay.batch" {
			saw = true
			break
		}
	}
	assert.True(t, saw, "expected a wrkflw.relay.batch span from the injected TracerProvider")
}
