package persistence_test

// facade_constructors_test.go covers thin façade constructors/options that were
// previously untested: NewTimerStore and the WithStore* observability options.
// These are pure delegations to internal/persistence/postgres; the test asserts
// the façade wires through to a working store/timer reader.

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
)

func TestTimerStoreFacade(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	ts := persistence.NewTimerStore(pool)
	require.NotNil(t, ts)

	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed, "a freshly migrated DB has no armed timers")
}

func TestOpenPostgresWithStoreObservabilityOptions(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	store, err := persistence.OpenPostgres(t.Context(), pool,
		persistence.WithStoreLogger(slog.Default()),
		persistence.WithStoreTracerProvider(tracenoop.NewTracerProvider()),
		persistence.WithStoreMeterProvider(metricnoop.NewMeterProvider()),
	)
	require.NoError(t, err)
	require.NotNil(t, store)
}
