package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestDeduperSeen verifies the idempotent-consumer Seen semantics (ADR-0018):
//   - First call for (subscriber, messageID) returns firstTime==true.
//   - Duplicate call within the same tx returns firstTime==false.
//   - A different subscriber with the same messageID is a distinct key → true.
//   - After commit, a new tx sees the already-persisted pair → false (durability).
func TestDeduperSeen(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	d := pg.NewDeduper(pool)

	// --- tx 1: in-progress transaction ---
	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	defer tx.Rollback(t.Context()) //nolint:errcheck // best-effort cleanup

	// First time we see (sub, m-1) must return firstTime==true.
	first, err := d.Seen(t.Context(), tx, "sub", "m-1")
	require.NoError(t, err)
	assert.True(t, first, "first Seen for (sub, m-1) must return true")

	// Duplicate within the same tx must return firstTime==false.
	again, err := d.Seen(t.Context(), tx, "sub", "m-1")
	require.NoError(t, err)
	assert.False(t, again, "second Seen for (sub, m-1) within same tx must return false")

	// Different subscriber, same messageID — distinct primary key, must return true.
	other, err := d.Seen(t.Context(), tx, "sub2", "m-1")
	require.NoError(t, err)
	assert.True(t, other, "Seen for (sub2, m-1) must return true — different subscriber")

	// Commit tx so the rows are durable.
	require.NoError(t, tx.Commit(t.Context()))

	// --- tx 2: new transaction after commit proves durability ---
	tx2, err := pool.Begin(t.Context())
	require.NoError(t, err)
	defer tx2.Rollback(t.Context()) //nolint:errcheck

	persisted, err := d.Seen(t.Context(), tx2, "sub", "m-1")
	require.NoError(t, err)
	assert.False(t, persisted, "Seen for already-committed (sub, m-1) must return false across transactions")

	require.NoError(t, tx2.Rollback(t.Context()))
}
