package postgres_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// listArmedDirect queries wrkflw_timers directly, independent of any TimerStore
// implementation, so this test remains self-contained before Task 5 lands.
func listArmedDirect(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT timer_id FROM wrkflw_timers ORDER BY timer_id`)
	require.NoError(t, err)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

// TestPgTimerStoreListArmedClosedPool covers the Query-error branch in
// TimerStore.ListArmed by closing the pool before the call.
func TestPgTimerStoreListArmedClosedPool(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	ts := pg.NewTimerStore(pool)

	pool.Close() // subsequent Query fails immediately

	_, err := ts.ListArmed(t.Context())
	require.Error(t, err, "ListArmed on a closed pool must return an error")
	require.Contains(t, err.Error(), "list armed timers", "error must be wrapped with list context")
}

// TestStorePersistsTimerOpsAtomically verifies that Store.Create writes
// TimerArms rows and Store.Commit removes TimerCancels rows, both atomically
// with the instance state change (ADR-0027).
func TestStorePersistsTimerOpsAtomically(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	store := pg.NewStore(pool)

	at := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "pti-1",
		DefID:      "d",
		DefVersion: 1,
		Status:     engine.StatusRunning,
		StartedAt:  at,
	}

	// Create: arms timer "t1" atomically with the instance insert.
	tok, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(at, nil),
		TimerArms: []runtime.ArmedTimer{{
			InstanceID: "pti-1",
			DefID:      "d",
			DefVersion: 1,
			TimerID:    "t1",
			FireAt:     at.Add(time.Hour),
			Kind:       engine.TimerIntermediate,
		}},
	})
	require.NoError(t, err)

	// Assert timer row was persisted.
	armed := listArmedDirect(t, pool)
	require.Len(t, armed, 1)
	assert.Equal(t, "t1", armed[0])

	// Commit: cancels "t1" atomically with the state transition.
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{
		State:        st,
		Trigger:      engine.NewTimerFired(at.Add(time.Hour), "t1"),
		TimerCancels: []string{"t1"},
	})
	require.NoError(t, err)

	// Assert fired timer row was deleted in the commit tx.
	armed = listArmedDirect(t, pool)
	assert.Empty(t, armed, "fired timer row must be deleted in the commit tx")
}

func TestPgTimerStoreListArmedOrdered(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	store := pg.NewStore(pool)
	ts := pg.NewTimerStore(pool)

	base := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)
	st := engine.InstanceState{InstanceID: "ord-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: base}
	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(base, nil),
		TimerArms: []runtime.ArmedTimer{
			{InstanceID: "ord-1", DefID: "d", DefVersion: 1, TimerID: "later", FireAt: base.Add(2 * time.Hour), Kind: engine.TimerIntermediate},
			{InstanceID: "ord-1", DefID: "d", DefVersion: 1, TimerID: "sooner", FireAt: base.Add(time.Hour), Kind: engine.TimerIntermediate},
		},
	})
	require.NoError(t, err)

	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 2)
	assert.Equal(t, "sooner", armed[0].TimerID, "ordered by FireAt ascending")
	assert.Equal(t, "later", armed[1].TimerID)
	assert.Equal(t, "d", armed[0].DefID)
	assert.Equal(t, 1, armed[0].DefVersion)
}
