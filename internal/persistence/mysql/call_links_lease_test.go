package mysql_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

const mysqlCallLinkLeaseTTL = 30 * time.Second

func mysqlLeaseClockBase() time.Time {
	return time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
}

// newMySQLLeaseCallLinkStore returns a CallLinkStore with the given options and a freshly migrated db.
func newMySQLLeaseCallLinkStore(t *testing.T, opts ...mypkg.CallLinkOption) (*mypkg.CallLinkStore, *mypkg.Store) {
	t.Helper()
	db := database.RunTestMySQL(t)
	cls := mypkg.NewCallLinkStore(db, opts...)
	store := mypkg.NewStore(db)
	return cls, store
}

// TestCallLinkStore_Lease_HidesClaimedRows verifies opt-in lease semantics on the MySQL
// CallLinkStore (two stores, distinct owners → SKIP LOCKED exclusivity; lease expiry re-claim).
func TestCallLinkStore_Lease_HidesClaimedRows(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "leased first claim stamps claimed_at/claimed_by",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(mysqlLeaseClockBase())
				db := database.RunTestMySQL(t)
				cls := mypkg.NewCallLinkStore(db,
					mypkg.WithCallLinkLease("replica-A", mysqlCallLinkLeaseTTL),
					mypkg.WithCallLinkClock(fc),
				)
				store := mypkg.NewStore(db)

				seedMySQLCompletedLink(t, store, "lease-child-1", runtime.CallOutcome{Completed: true})

				got, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "lease-child-1", got[0].Link.ChildInstanceID)

				// Assert DB row has claimed_at/claimed_by stamped.
				var claimedAt *time.Time
				var claimedBy *string
				err = db.QueryRowContext(t.Context(),
					`SELECT claimed_at, claimed_by FROM wrkflw_call_links WHERE child_instance_id = ?`,
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
				fc := clockwork.NewFakeClockAt(mysqlLeaseClockBase())
				db := database.RunTestMySQL(t)
				clsA := mypkg.NewCallLinkStore(db,
					mypkg.WithCallLinkLease("replica-A", mysqlCallLinkLeaseTTL),
					mypkg.WithCallLinkClock(fc),
				)
				clsB := mypkg.NewCallLinkStore(db,
					mypkg.WithCallLinkLease("replica-B", mysqlCallLinkLeaseTTL),
					mypkg.WithCallLinkClock(fc),
				)
				store := mypkg.NewStore(db)

				seedMySQLCompletedLink(t, store, "lease-child-2", runtime.CallOutcome{Completed: true})

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
				fc := clockwork.NewFakeClockAt(mysqlLeaseClockBase())
				db := database.RunTestMySQL(t)
				clsA := mypkg.NewCallLinkStore(db,
					mypkg.WithCallLinkLease("replica-A", mysqlCallLinkLeaseTTL),
					mypkg.WithCallLinkClock(fc),
				)
				clsB := mypkg.NewCallLinkStore(db,
					mypkg.WithCallLinkLease("replica-B", mysqlCallLinkLeaseTTL),
					mypkg.WithCallLinkClock(fc),
				)
				store := mypkg.NewStore(db)

				seedMySQLCompletedLink(t, store, "lease-child-3", runtime.CallOutcome{Completed: true})

				// A claims first.
				first, err := clsA.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, first, 1)

				// Advance clock past TTL.
				fc.Advance(mysqlCallLinkLeaseTTL + time.Second)

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
				fc := clockwork.NewFakeClockAt(mysqlLeaseClockBase())
				db := database.RunTestMySQL(t)
				cls := mypkg.NewCallLinkStore(db,
					mypkg.WithCallLinkLease("replica-A", mysqlCallLinkLeaseTTL),
					mypkg.WithCallLinkClock(fc),
				)
				store := mypkg.NewStore(db)

				seedMySQLCompletedLink(t, store, "lease-notif-1", runtime.CallOutcome{Completed: true})

				// Mark notified before any claim attempt.
				require.NoError(t, cls.MarkNotified(t.Context(), "lease-notif-1"))

				got, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, got, "notified row must never be returned")

				// Advance past TTL — still never returned.
				fc.Advance(mysqlCallLinkLeaseTTL + time.Second)

				got2, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, got2, "notified row must never be returned even after TTL")
			},
		},
		{
			name: "ttl=0 two consecutive claims both return the link (backward-compat)",
			assert: func(t *testing.T) {
				// No options — backward-compat path: no lease.
				cls, store := newMySQLLeaseCallLinkStore(t)

				seedMySQLCompletedLink(t, store, "lease-noopt-1", runtime.CallOutcome{Completed: true})

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
