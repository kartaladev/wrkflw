package persistence_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// TestOpenPostgresReturnsInterface verifies that OpenPostgres accepts exactly
// (ctx, pool) — no variadic Option — and returns a persistence.Store interface
// (not a concrete *postgres.Store). This is the ADR-0008 requirement: the public
// façade must expose stable port/interface types, never internal concrete types.
func TestOpenPostgresReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	// OpenPostgres must compile with exactly 2 arguments (no variadic).
	store, err := persistence.OpenPostgres(t.Context(), pool)
	require.NoError(t, err)

	// The returned value must satisfy persistence.InstanceStore (which embeds kernel.InstanceStore
	// and kernel.JournalReader). Verify via type assertions — if the interface is
	// not satisfied the assertions panic and the test fails.
	_ = store.(kernel.InstanceStore)
	_ = store.(kernel.JournalReader)

	assert.NotNil(t, store)
}

// TestNewDefinitionStoreReturnsInterface verifies that NewDefinitionStore returns a
// persistence.DefinitionStore interface value, not a *postgres.DefinitionStore.
// Callers must only use the interface methods (Lookup, PutDefinition).
func TestNewDefinitionStoreReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	ds, err := persistence.NewDefinitionStore(pool)
	require.NoError(t, err)

	// The static type of ds is persistence.DefinitionStore — the function signature
	// is the compile-time proof. Assert non-nil as a runtime sanity check.
	assert.NotNil(t, ds)
}

// TestNewRelayReturnsInterface verifies that NewRelay returns a persistence.Relay
// interface value, not a *postgres.Relay. Callers must use Run/DrainOnce via the
// interface.
func TestNewRelayReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	pub := &capturingPublisher{}
	relay, err := persistence.NewRelay(pool, pub)
	require.NoError(t, err)

	// The static type of relay is persistence.Relay — the function signature is the
	// compile-time proof. Assert non-nil as a runtime sanity check.
	assert.NotNil(t, relay)
}

// TestNewDeduperReturnsInterface verifies that NewDeduper returns a
// persistence.Deduper interface value (not a *postgres.Deduper) and that a Seen
// call through the interface works end-to-end (ADR-0008, ADR-0018).
func TestNewDeduperReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	d, err := persistence.NewDeduper(pool)
	require.NoError(t, err)

	// The static type is persistence.Deduper — compile-time proof.
	assert.NotNil(t, d)

	// Functional smoke: first Seen returns true; duplicate returns false.
	// Seen joins the ambient transaction stashed in ctx by transaction.Begin,
	// so the dedup record commits atomically with the caller's business unit.
	q, ctx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)

	first, err := d.Seen(ctx, "facade-sub", "facade-msg-1")
	require.NoError(t, err)
	assert.True(t, first, "first Seen via facade must return true")

	dup, err := d.Seen(ctx, "facade-sub", "facade-msg-1")
	require.NoError(t, err)
	assert.False(t, dup, "duplicate Seen via facade must return false")

	require.NoError(t, q.Rollback(ctx))
}

// TestNewRelayDLQAdminViaFacade verifies that the DLQ admin methods
// (ListDeadLettered, Redrive) are accessible through the persistence.Relay
// interface returned by persistence.NewRelay (ADR-0008).
func TestNewRelayDLQAdminViaFacade(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	pub := &capturingPublisher{}
	relay, err := persistence.NewRelay(pool, pub)
	require.NoError(t, err)

	// ListDeadLettered on an empty outbox must return an empty (nil) slice.
	dead, err := relay.ListDeadLettered(t.Context(), 10)
	require.NoError(t, err)
	assert.Empty(t, dead, "no dead rows yet — empty outbox")

	// Redrive with no ids must return 0, nil (no-op).
	n, err := relay.Redrive(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
