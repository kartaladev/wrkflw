package runtime

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// Compile-time checks: MemStore satisfies both ports.
var (
	_ Store          = (*MemStore)(nil)
	_ JournalReader  = (*MemStore)(nil)
	_ InstanceLister = (*MemStore)(nil)
)

// memInstance is the in-memory record for one instance.
type memInstance struct {
	state   engine.InstanceState
	version Token
}

// MemStore is an in-memory transactional Store + JournalReader for tests and
// reference wiring. Its Commit performs an in-memory CAS on a per-instance
// version and BUFFERS all writes so a failed step never half-applies.
// MemStore is safe for concurrent use.
type MemStore struct {
	mu        sync.RWMutex
	instances map[string]*memInstance
	journal   map[string][]engine.Trigger
	events    []OutboxEvent
}

// NewMemStore constructs an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{
		instances: map[string]*memInstance{},
		journal:   map[string][]engine.Trigger{},
	}
}

// Create inserts a brand-new instance from its first applied step and returns
// its initial token.
func (m *MemStore) Create(_ context.Context, step AppliedStep) (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	const initial Token = 1
	m.instances[step.State.InstanceID] = &memInstance{state: step.State.Clone(), version: initial}
	m.journal[step.State.InstanceID] = append(m.journal[step.State.InstanceID], step.Trigger)
	m.events = append(m.events, step.Events...)
	return initial, nil
}

// Load returns the current snapshot and its concurrency token.
func (m *MemStore) Load(_ context.Context, id string) (engine.InstanceState, Token, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	if !ok {
		return engine.InstanceState{}, 0, ErrInstanceNotFound
	}
	return inst.state.Clone(), inst.version, nil
}

// Commit atomically applies one step under an optimistic CAS on expected.
// It buffers the snapshot, journal append, and outbox events, applying them
// only after the CAS succeeds, so a stale token leaves the store untouched.
func (m *MemStore) Commit(_ context.Context, expected Token, step AppliedStep) (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[step.State.InstanceID]
	if !ok {
		return 0, ErrInstanceNotFound
	}
	if inst.version != expected {
		return 0, ErrConcurrentUpdate
	}
	next := inst.version + 1
	inst.state = step.State.Clone()
	inst.version = next
	m.journal[step.State.InstanceID] = append(m.journal[step.State.InstanceID], step.Trigger)
	m.events = append(m.events, step.Events...)
	return next, nil
}

// Entries returns the recorded trigger history for id (JournalReader).
func (m *MemStore) Entries(_ context.Context, id string) ([]engine.Trigger, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.journal[id]), nil
}

// Events returns all buffered outbox events, in append order (test accessor).
func (m *MemStore) Events() []OutboxEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.events)
}

// List returns a keyset-cursor-paginated page of instance summaries.
//
// Items are ordered deterministically by (StartedAt DESC, InstanceID DESC).
// When filter.Status is non-nil, only instances with that status are included.
// Cursor encodes the last-seen (StartedAt, InstanceID); items at-or-after that
// position (under DESC ordering) are skipped. Limit is clamped via normalizeLimit.
func (m *MemStore) List(_ context.Context, filter InstanceFilter) (InstancePage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// snapshot + filter
	all := make([]InstanceSummary, 0, len(m.instances))
	for _, inst := range m.instances {
		st := inst.state
		if filter.Status != nil && st.Status != *filter.Status {
			continue
		}
		all = append(all, InstanceSummary{
			InstanceID:    st.InstanceID,
			DefID:         st.DefID,
			DefVersion:    st.DefVersion,
			Status:        st.Status,
			StartedAt:     st.StartedAt,
			EndedAt:       st.EndedAt,
			IncidentCount: len(st.Incidents),
		})
	}

	// sort DESC by (StartedAt, InstanceID)
	slices.SortFunc(all, func(a, b InstanceSummary) int {
		if c := cmp.Compare(b.StartedAt.UnixNano(), a.StartedAt.UnixNano()); c != 0 {
			return c
		}
		return cmp.Compare(b.InstanceID, a.InstanceID)
	})

	// apply cursor: skip items that are at or before the cursor position
	// under DESC ordering (i.e. items with StartedAt > cursorTime, or equal
	// time with InstanceID >= cursorID are already included; we drop those
	// with StartedAt == cursorTime && InstanceID >= cursorID, and all items
	// with StartedAt > cursorTime have a LATER time, i.e. come BEFORE the
	// cursor in desc order).
	//
	// Keyset semantics for DESC order:
	//   next page contains rows WHERE (started_at, instance_id) < (cursorTime, cursorID)
	//   i.e. started_at < cursorTime, OR started_at==cursorTime AND instance_id < cursorID
	if filter.Cursor != "" {
		cursorTime, cursorID, err := DecodeCursor(filter.Cursor)
		if err != nil {
			return InstancePage{}, err
		}
		start := 0
		for start < len(all) {
			it := all[start]
			// item is strictly less than (cursorTime, cursorID) in the keyset sense?
			lessThan := it.StartedAt.Before(cursorTime) ||
				(it.StartedAt.Equal(cursorTime) && it.InstanceID < cursorID)
			if lessThan {
				break
			}
			start++
		}
		all = all[start:]
	}

	limit := NormalizeLimit(filter.Limit)
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}

	var nextCursor string
	if hasMore && len(all) > 0 {
		last := all[len(all)-1]
		nextCursor = EncodeCursor(last.StartedAt, last.InstanceID)
	}

	return InstancePage{
		Items:      all,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}
