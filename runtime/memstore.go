package runtime

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// Compile-time checks: MemStore satisfies both ports.
var (
	_ Store         = (*MemStore)(nil)
	_ JournalReader = (*MemStore)(nil)
)

// memInstance is the in-memory record for one instance.
type memInstance struct {
	state   engine.InstanceState
	version Token
}

// MemStore is an in-memory transactional Store + JournalReader for tests and
// reference wiring. Its Commit performs an in-memory CAS on a per-instance
// version and BUFFERS all writes so a failed step never half-applies.
type MemStore struct {
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
	const initial Token = 1
	m.instances[step.State.InstanceID] = &memInstance{state: step.State.Clone(), version: initial}
	m.journal[step.State.InstanceID] = append(m.journal[step.State.InstanceID], step.Trigger)
	m.events = append(m.events, step.Events...)
	return initial, nil
}

// Load returns the current snapshot and its concurrency token.
func (m *MemStore) Load(_ context.Context, id string) (engine.InstanceState, Token, error) {
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
	return m.journal[id], nil
}

// Events returns all buffered outbox events, in append order (test accessor).
func (m *MemStore) Events() []OutboxEvent { return m.events }
