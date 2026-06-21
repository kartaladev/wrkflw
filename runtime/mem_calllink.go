package runtime

import (
	"context"
	"sync"
)

// memLink is the in-memory record for one call link + its terminal outcome.
type memLink struct {
	link     CallLink
	terminal bool
	outcome  CallOutcome
	notified bool
}

// MemCallLinkStore is the in-memory CallLinkStore for the pure-runtime/test path.
type MemCallLinkStore struct {
	mu    sync.Mutex
	links map[string]*memLink // keyed by ChildInstanceID
}

var _ CallLinkStore = (*MemCallLinkStore)(nil)

// NewMemCallLinkStore constructs an empty MemCallLinkStore.
func NewMemCallLinkStore() *MemCallLinkStore {
	return &MemCallLinkStore{links: make(map[string]*memLink)}
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
func (m *MemCallLinkStore) ClaimPending(_ context.Context, limit int) ([]PendingNotify, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PendingNotify
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
