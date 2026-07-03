package persistence_test

// calllink_lease_test.go tests the façade-level WithCallLinkLease and
// WithCallLinkClock options on persistence.NewCallLinkStore (ADR-0031).
// It exercises only the public façade constructors, verifying the thin
// delegation wires through to the underlying store options.

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

const facadeLeaseTTL = 30 * time.Second

// seedFacadeTerminalLink inserts a terminal call link via the internal
// postgres.Store (the write side), so the read-side façade lease tests have a
// seeded row without duplicating the write path in the public API.
func seedFacadeTerminalLink(t *testing.T, pgStore *store.Store, pool *pgxpool.Pool, childID string, outcome kernel.CallOutcome) {
	t.Helper()
	_ = pool // accepted to mirror the internal test helper signature

	link := &kernel.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: "parent-" + childID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-facade-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	now := time.Unix(1700000000, 0).UTC()

	createStep := kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: childID,
			DefID:      "def-facade-parent",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger:     engine.NewStartInstance(now, map[string]any{"k": "v"}),
		NewCallLink: link,
	}
	tok, err := pgStore.Create(t.Context(), createStep)
	require.NoError(t, err)

	termStep := kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: childID,
			DefID:      "def-facade-parent",
			DefVersion: 1,
			Status:     engine.StatusCompleted,
			StartedAt:  now,
		},
		Trigger:     engine.NewStartInstance(now, nil),
		CallOutcome: &outcome,
	}
	_, err = pgStore.Commit(t.Context(), tok, termStep)
	require.NoError(t, err)
}

// TestCallLinkStoreFacadeLeaseOptions verifies the persistence façade exposes
// the WithCallLinkLease and WithCallLinkClock options and that they wire through
// to the Postgres lease machinery (ADR-0031).
func TestCallLinkStoreFacadeLeaseOptions(t *testing.T) {
	t.Run("WithCallLinkLease options compile and a leased store is non-nil", func(t *testing.T) {
		pool := dbtest.RunTestDatabase(t)
		require.NoError(t, persistence.Migrate(t.Context(), pool))

		fc := clockwork.NewFakeClockAt(time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC))

		cls, err := persistence.NewCallLinkStore(pool,
			persistence.WithCallLinkLease("facade-A", facadeLeaseTTL),
			persistence.WithCallLinkClock(fc),
		)
		require.NoError(t, err)
		require.NotNil(t, cls, "NewCallLinkStore with lease options must return a non-nil store")
	})

	t.Run("leased ClaimPending reserves row from a concurrent second owner", func(t *testing.T) {
		pool := dbtest.RunTestDatabase(t)
		require.NoError(t, persistence.Migrate(t.Context(), pool))

		fc := clockwork.NewFakeClockAt(time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC))

		// Owner A: leased store via the public façade.
		storeA, err := persistence.NewCallLinkStore(pool,
			persistence.WithCallLinkLease("facade-owner-A", facadeLeaseTTL),
			persistence.WithCallLinkClock(fc),
		)
		require.NoError(t, err)
		// Owner B: different owner, same pool, same lease TTL.
		storeB, err := persistence.NewCallLinkStore(pool,
			persistence.WithCallLinkLease("facade-owner-B", facadeLeaseTTL),
			persistence.WithCallLinkClock(fc),
		)
		require.NoError(t, err)

		// Seed a terminal link via the internal store write path.
		pgStore, err := store.New(pool, dialect.NewPostgres())
		require.NoError(t, err)
		seedFacadeTerminalLink(t, pgStore, pool, "facade-lease-child-1",
			kernel.CallOutcome{Completed: true})

		// Owner A claims — must see the seeded link.
		first, err := storeA.ClaimPending(t.Context(), 10)
		require.NoError(t, err)
		require.Len(t, first, 1, "owner A must claim the seeded terminal link")
		assert.Equal(t, "facade-lease-child-1", first[0].Link.ChildInstanceID)

		// Owner B immediately claims — the lease is still live, must return nothing.
		second, err := storeB.ClaimPending(t.Context(), 10)
		require.NoError(t, err)
		assert.Empty(t, second,
			"owner B must not see the row while owner A's lease is live")
	})

	t.Run("zero-option NewCallLinkStore call still compiles (backward compat)", func(t *testing.T) {
		pool := dbtest.RunTestDatabase(t)
		require.NoError(t, persistence.Migrate(t.Context(), pool))

		cls, err := persistence.NewCallLinkStore(pool)
		require.NoError(t, err)
		require.NotNil(t, cls, "zero-option NewCallLinkStore must still return a non-nil store")
	})
}
