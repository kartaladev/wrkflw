package postgres_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

const callLinkLeaseTTL = 30 * time.Second

func leaseClockBase() time.Time {
	return time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
}

// newLeaseCallLinkReader returns a CallLinkStore with the given options and a
// freshly migrated pool.
func newLeaseCallLinkReader(t *testing.T, opts ...pg.CallLinkOption) (*pg.CallLinkStore, *pg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	cls := pg.NewCallLinkStore(pool, opts...)
	store := pg.NewStore(pool)
	return cls, store, pool
}

// TestCallLinkStoreLease verifies opt-in lease semantics on the Postgres
// CallLinkStore (ADR-0031).
func TestCallLinkStoreLease(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "leased first claim returns the link and stamps claimed_at/by",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseClockBase())
				cls, store, pool := newLeaseCallLinkReader(t,
					pg.WithCallLinkLease("replica-A", callLinkLeaseTTL),
					pg.WithCallLinkClock(fc),
				)

				seedCompletedLink(t, store, pool, "lease-child-1", runtime.CallOutcome{Completed: true})

				got, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "lease-child-1", got[0].Link.ChildInstanceID)

				// Assert DB row has claimed_at/claimed_by stamped.
				var claimedAt *time.Time
				var claimedBy *string
				err = pool.QueryRow(t.Context(),
					`SELECT claimed_at, claimed_by FROM wrkflw_call_links WHERE child_instance_id = $1`,
					"lease-child-1",
				).Scan(&claimedAt, &claimedBy)
				require.NoError(t, err)
				require.NotNil(t, claimedAt, "claimed_at must be stamped after claim")
				require.NotNil(t, claimedBy, "claimed_by must be stamped after claim")
				assert.Equal(t, "replica-A", *claimedBy)
			},
		},
		{
			name: "immediate second claim by owner B returns nothing while lease is live",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseClockBase())
				clsA, store, pool := newLeaseCallLinkReader(t,
					pg.WithCallLinkLease("replica-A", callLinkLeaseTTL),
					pg.WithCallLinkClock(fc),
				)
				clsB := pg.NewCallLinkStore(pool,
					pg.WithCallLinkLease("replica-B", callLinkLeaseTTL),
					pg.WithCallLinkClock(fc),
				)

				seedCompletedLink(t, store, pool, "lease-child-2", runtime.CallOutcome{Completed: true})

				// Owner A claims.
				first, err := clsA.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, first, 1)

				// Owner B immediately tries — lease is still live.
				second, err := clsB.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, second, "owner B must not see a row held by owner A's lease")
			},
		},
		{
			name: "after fake-clock advance past TTL owner B reclaims",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseClockBase())
				clsA, store, pool := newLeaseCallLinkReader(t,
					pg.WithCallLinkLease("replica-A", callLinkLeaseTTL),
					pg.WithCallLinkClock(fc),
				)
				clsB := pg.NewCallLinkStore(pool,
					pg.WithCallLinkLease("replica-B", callLinkLeaseTTL),
					pg.WithCallLinkClock(fc),
				)

				seedCompletedLink(t, store, pool, "lease-child-3", runtime.CallOutcome{Completed: true})

				// A claims first.
				first, err := clsA.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, first, 1)

				// Advance clock past TTL.
				fc.Advance(callLinkLeaseTTL + time.Second)

				// B reclaims after TTL expiry.
				reclaimed, err := clsB.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, reclaimed, 1, "owner B must reclaim after TTL expires")
				assert.Equal(t, "lease-child-3", reclaimed[0].Link.ChildInstanceID)
			},
		},
		{
			name: "notified row is never returned by leased ClaimPending",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseClockBase())
				cls, store, pool := newLeaseCallLinkReader(t,
					pg.WithCallLinkLease("replica-A", callLinkLeaseTTL),
					pg.WithCallLinkClock(fc),
				)

				seedCompletedLink(t, store, pool, "lease-notif-1", runtime.CallOutcome{Completed: true})

				// Mark notified before any claim attempt.
				require.NoError(t, cls.MarkNotified(t.Context(), "lease-notif-1"))

				got, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, got, "notified row must never be returned")

				// Advance past TTL — still never returned.
				fc.Advance(callLinkLeaseTTL + time.Second)

				got2, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, got2, "notified row must never be returned even after TTL")
			},
		},
		{
			name: "ttl=0 two consecutive claims both return the link (backward-compat)",
			assert: func(t *testing.T) {
				// No options — backward-compat path: no lease.
				cls, store, pool := newLeaseCallLinkReader(t)

				seedCompletedLink(t, store, pool, "lease-noopt-1", runtime.CallOutcome{Completed: true})

				first, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, first, 1, "first claim must return the link")

				second, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, second, 1, "second claim must also return the link (no lease)")
				assert.Equal(t, "lease-noopt-1", second[0].Link.ChildInstanceID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}
