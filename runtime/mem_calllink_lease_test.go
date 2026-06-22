package runtime_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// leaseTTL is the fixed TTL used across lease sub-tests.
const leaseTTL = 30 * time.Second

// SeedCallLink and SeedTerminal are exported test helpers defined in
// runtime/mem_calllink_export_test.go (package runtime) that expose the
// unexported record and markTerminal methods of MemCallLinkStore to this
// black-box test package.

func leaseBaseTime() time.Time {
	return time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
}

func makeCallLink(childID, parentID string) runtime.CallLink {
	return runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: parentID,
		ParentCommandID:  "cmd-1",
		ParentDefID:      "parent-def",
		ParentDefVersion: 1,
	}
}

func TestMemCallLinkStoreLease(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "leased first claim returns the link and records lease",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseBaseTime())
				s := runtime.NewMemCallLinkStore(
					runtime.WithMemCallLinkLease("replica-A", leaseTTL),
					runtime.WithMemCallLinkClock(fc),
				)
				link := makeCallLink("child-1", "parent-1")
				runtime.SeedCallLink(s, link)
				runtime.SeedTerminal(s, "child-1", runtime.CallOutcome{})

				got, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "child-1", got[0].Link.ChildInstanceID)
			},
		},
		{
			name: "immediate second claim returns nothing (lease live)",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseBaseTime())
				s := runtime.NewMemCallLinkStore(
					runtime.WithMemCallLinkLease("replica-A", leaseTTL),
					runtime.WithMemCallLinkClock(fc),
				)
				link := makeCallLink("child-2", "parent-2")
				runtime.SeedCallLink(s, link)
				runtime.SeedTerminal(s, "child-2", runtime.CallOutcome{})

				// First claim — gets the link.
				first, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, first, 1)

				// Immediate second claim — lease still live.
				second, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, second, "second claim should be empty while lease is live")
			},
		},
		{
			name: "after fake-clock advance past TTL the link is reclaimable",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseBaseTime())
				s := runtime.NewMemCallLinkStore(
					runtime.WithMemCallLinkLease("replica-A", leaseTTL),
					runtime.WithMemCallLinkClock(fc),
				)
				link := makeCallLink("child-3", "parent-3")
				runtime.SeedCallLink(s, link)
				runtime.SeedTerminal(s, "child-3", runtime.CallOutcome{})

				// First claim holds the lease.
				first, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, first, 1)

				// Advance clock past TTL.
				fc.Advance(leaseTTL + time.Second)

				// Now reclaimable.
				reclaimed, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, reclaimed, 1, "link should be reclaimable after TTL expires")
				assert.Equal(t, "child-3", reclaimed[0].Link.ChildInstanceID)
			},
		},
		{
			name: "MarkNotified link is never returned by ClaimPending",
			assert: func(t *testing.T) {
				fc := clockwork.NewFakeClockAt(leaseBaseTime())
				s := runtime.NewMemCallLinkStore(
					runtime.WithMemCallLinkLease("replica-A", leaseTTL),
					runtime.WithMemCallLinkClock(fc),
				)
				link := makeCallLink("child-4", "parent-4")
				runtime.SeedCallLink(s, link)
				runtime.SeedTerminal(s, "child-4", runtime.CallOutcome{})

				// Mark notified before any claim.
				require.NoError(t, s.MarkNotified(t.Context(), "child-4"))

				got, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, got, "notified link must never be claimed")

				// Advance past TTL — still never returned.
				fc.Advance(leaseTTL + time.Second)

				got2, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				assert.Empty(t, got2, "notified link must never be claimed even after TTL")
			},
		},
		{
			name: "default ttl=0 two consecutive claims both return the link",
			assert: func(t *testing.T) {
				// No lease options — backward-compat path.
				s := runtime.NewMemCallLinkStore()
				link := makeCallLink("child-5", "parent-5")
				runtime.SeedCallLink(s, link)
				runtime.SeedTerminal(s, "child-5", runtime.CallOutcome{})

				first, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, first, 1, "first claim must return the link")

				second, err := s.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, second, 1, "second claim must also return the link (no lease)")
				assert.Equal(t, "child-5", second[0].Link.ChildInstanceID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t) })
	}
}
