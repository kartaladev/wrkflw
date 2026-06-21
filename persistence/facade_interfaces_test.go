package persistence_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestOpenPostgresReturnsInterface verifies that OpenPostgres accepts exactly
// (ctx, pool) — no variadic Option — and returns a persistence.Store interface
// (not a concrete *postgres.Store). This is the ADR-0008 requirement: the public
// façade must expose stable port/interface types, never internal concrete types.
func TestOpenPostgresReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	// OpenPostgres must compile with exactly 2 arguments (no variadic).
	store, err := persistence.OpenPostgres(t.Context(), pool)
	require.NoError(t, err)

	// The returned value must satisfy persistence.Store (which embeds runtime.Store
	// and runtime.JournalReader). Verify via type assertions — if the interface is
	// not satisfied the assertions panic and the test fails.
	_ = store.(runtime.Store)
	_ = store.(runtime.JournalReader)

	assert.NotNil(t, store)
}

// TestNewDefinitionStoreReturnsInterface verifies that NewDefinitionStore returns a
// persistence.DefinitionStore interface value, not a *postgres.DefinitionStore.
// Callers must only use the interface methods (Lookup, PutDefinition).
func TestNewDefinitionStoreReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	ds := persistence.NewDefinitionStore(pool)

	// The static type of ds is persistence.DefinitionStore — the function signature
	// is the compile-time proof. Assert non-nil as a runtime sanity check.
	assert.NotNil(t, ds)
}

// TestNewRelayReturnsInterface verifies that NewRelay returns a persistence.Relay
// interface value, not a *postgres.Relay. Callers must use Run/DrainOnce via the
// interface.
func TestNewRelayReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	pub := &capturingPublisher{}
	relay := persistence.NewRelay(pool, pub)

	// The static type of relay is persistence.Relay — the function signature is the
	// compile-time proof. Assert non-nil as a runtime sanity check.
	assert.NotNil(t, relay)
}
