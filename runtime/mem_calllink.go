package runtime

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zakyalvan/krtlwrkflw/clock"
)

// memLink is the in-memory record for one call link + its terminal outcome.
type memLink struct {
	link      CallLink
	terminal  bool
	outcome   CallOutcome
	notified  bool
	claimedAt time.Time // zero when unclaimed or lease disabled
	claimedBy string    // owner string from WithMemCallLinkLease
}

// MemCallLinkOption is a functional option for MemCallLinkStore.
type MemCallLinkOption func(*MemCallLinkStore)

// WithMemCallLinkLease configures an opt-in claim lease on the store. When
// ttl > 0, ClaimPending stamps each claimed link with claimedAt=now and
// claimedBy=owner, hiding it from subsequent claims until the lease expires.
// When ttl <= 0 (the default), ClaimPending behaves exactly as before.
func WithMemCallLinkLease(owner string, ttl time.Duration) MemCallLinkOption {
	return func(s *MemCallLinkStore) {
		s.leaseOwner = owner
		s.leaseTTL = ttl
	}
}

// WithMemCallLinkClock overrides the clock used for lease timestamps. The
// default is clock.System(). Inject a fake clock in tests.
// A nil clock is ignored (the default is kept).
func WithMemCallLinkClock(clk clock.Clock) MemCallLinkOption {
	return func(s *MemCallLinkStore) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// MemCallLinkStore is the in-memory CallLinkStore for the pure-runtime/test path.
type MemCallLinkStore struct {
	mu         sync.Mutex
	links      map[string]*memLink // keyed by ChildInstanceID
	leaseOwner string
	leaseTTL   time.Duration
	clk        clock.Clock
}

var _ CallLinkStore = (*MemCallLinkStore)(nil)

// NewMemCallLinkStore constructs an empty MemCallLinkStore. Zero-argument call
// sites continue to compile unchanged; pass MemCallLinkOption values to opt in
// to lease-based multi-replica exclusivity.
func NewMemCallLinkStore(opts ...MemCallLinkOption) *MemCallLinkStore {
	s := &MemCallLinkStore{
		links: make(map[string]*memLink),
		clk:   clock.System(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// record inserts a new link (called by MemStore on Create with NewCallLink).
func (m *MemCallLinkStore) record(link CallLink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[link.ChildInstanceID] = &memLink{link: link}
}

// markTerminal flips a child's link to terminal (called by MemStore on Commit with
// CallOutcome). No-op when the instance has no link (root instance).
func (m *MemCallLinkStore) markTerminal(childID string, out CallOutcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[childID]; ok {
		l.terminal = true
		l.outcome = out
	}
}

// ClaimPending returns up to limit terminal-but-unnotified links.
//
// When leaseTTL > 0 (lease mode), only links whose claimedAt is zero or has
// expired (claimedAt <= now-leaseTTL) are eligible. Each returned link has its
// claimedAt stamped to now and claimedBy set to the configured owner, hiding it
// from subsequent claims until the lease expires.
//
// When leaseTTL <= 0 (default), behaviour is identical to the original
// implementation: all terminal-but-unnotified links are returned with no
// claim stamp.
func (m *MemCallLinkStore) ClaimPending(_ context.Context, limit int) ([]PendingNotify, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []PendingNotify

	if m.leaseTTL <= 0 {
		// Original behaviour: no lease, return all eligible links.
		for _, l := range m.links {
			if l.terminal && !l.notified {
				out = append(out, PendingNotify{Link: l.link, Outcome: l.outcome})
				if limit > 0 && len(out) >= limit {
					break
				}
			}
		}
		return out, nil
	}

	// Lease mode: only claim links whose lease has expired or was never set.
	now := m.clk.Now()
	leaseExpiry := now.Add(-m.leaseTTL)

	for _, l := range m.links {
		if !l.terminal || l.notified {
			continue
		}
		// A link is claimable if it has never been claimed, or its lease has
		// expired (claimedAt is at or before the expiry threshold).
		if l.claimedAt.IsZero() || !l.claimedAt.After(leaseExpiry) {
			l.claimedAt = now
			l.claimedBy = m.leaseOwner
			out = append(out, PendingNotify{Link: l.link, Outcome: l.outcome})
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// MarkNotified records that the parent for childInstanceID has been resumed.
func (m *MemCallLinkStore) MarkNotified(_ context.Context, childID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[childID]; ok {
		l.notified = true
	}
	return nil
}

// LookupChild returns the link for a child instance; ok=false when the instance
// is a root (no parent).
func (m *MemCallLinkStore) LookupChild(_ context.Context, childID string) (CallLink, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[childID]; ok {
		return l.link, true, nil
	}
	return CallLink{}, false, nil
}

// ListRunningChildren returns all non-terminal child links whose ParentInstanceID
// equals parentInstanceID, ordered by ChildInstanceID for determinism.
func (m *MemCallLinkStore) ListRunningChildren(_ context.Context, parentInstanceID string) ([]CallLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CallLink
	for _, l := range m.links {
		if !l.terminal && l.link.ParentInstanceID == parentInstanceID {
			out = append(out, l.link)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ChildInstanceID < out[j].ChildInstanceID
	})
	return out, nil
}
