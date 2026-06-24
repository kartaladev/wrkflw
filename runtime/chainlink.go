package runtime

import (
	"context"
	"errors"
	"maps"
	"sort"
	"sync"
	"time"
)

// Outcome is the terminal outcome that triggered a chaining decision (ADR-0045).
// It mirrors the status-accurate terminal outbox topics (ADR-0046):
// instance.completed -> OutcomeCompleted, instance.failed -> OutcomeFailed,
// instance.terminated -> OutcomeTerminated.
type Outcome string

const (
	// OutcomeCompleted is the predecessor reaching StatusCompleted.
	OutcomeCompleted Outcome = "completed"
	// OutcomeFailed is the predecessor reaching StatusFailed (unhandled error).
	OutcomeFailed Outcome = "failed"
	// OutcomeTerminated is the predecessor reaching StatusTerminated (cancel /
	// full rollback).
	OutcomeTerminated Outcome = "terminated"
)

// ChainLink is the durable predecessor→successor correlation for one chaining
// hop (ADR-0045). It is keyed by (PredecessorID, Outcome): at most one successor
// per terminal outcome of a predecessor, which doubles as the exactly-once
// backstop under at-least-once terminal-event delivery.
type ChainLink struct {
	PredecessorID  string
	PredecessorDef string
	Outcome        Outcome
	SuccessorID    string
	SuccessorDef   string
	StartVars      map[string]any
	CreatedAt      time.Time
}

// ErrChainLinkExists signals an already-recorded (PredecessorID, Outcome) hop —
// the exactly-once backstop. The Chainer treats it as "already chained" (skip,
// ack).
var ErrChainLinkExists = errors.New("workflow-runtime: chain link already exists")

// ChainLinkStore persists predecessor→successor chaining lineage. Record is the
// write side; LookupBySuccessor and ListByPredecessor are the read/ancestry
// side an admin or audit surface uses.
type ChainLinkStore interface {
	// Record durably stores one predecessor→successor hop. It MUST be idempotent
	// on (PredecessorID, Outcome): a duplicate returns ErrChainLinkExists and does
	// not overwrite the existing link.
	Record(ctx context.Context, link ChainLink) error
	// LookupBySuccessor returns the link that produced successorID (ancestry).
	// ok=false (no error) when successorID was not produced by chaining.
	LookupBySuccessor(ctx context.Context, successorID string) (ChainLink, bool, error)
	// ListByPredecessor returns all hops fanned out from predecessorID, ordered
	// by Outcome for deterministic results (admin/audit).
	ListByPredecessor(ctx context.Context, predecessorID string) ([]ChainLink, error)
}

// chainKey is the (PredecessorID, Outcome) uniqueness key.
type chainKey struct {
	pred    string
	outcome Outcome
}

// MemChainLinkStore is the in-memory reference ChainLinkStore for tests and
// embedded consumers. It is safe for concurrent use.
type MemChainLinkStore struct {
	mu    sync.RWMutex
	links map[chainKey]ChainLink
}

// Compile-time check: MemChainLinkStore satisfies ChainLinkStore.
var _ ChainLinkStore = (*MemChainLinkStore)(nil)

// NewMemChainLinkStore constructs an empty in-memory ChainLinkStore.
func NewMemChainLinkStore() *MemChainLinkStore {
	return &MemChainLinkStore{links: map[chainKey]ChainLink{}}
}

// Record stores link, rejecting a duplicate (PredecessorID, Outcome) with
// ErrChainLinkExists. StartVars is defensively copied so a later caller mutation
// cannot corrupt the stored lineage.
func (m *MemChainLinkStore) Record(_ context.Context, link ChainLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := chainKey{pred: link.PredecessorID, outcome: link.Outcome}
	if _, exists := m.links[key]; exists {
		return ErrChainLinkExists
	}
	if link.StartVars != nil {
		link.StartVars = maps.Clone(link.StartVars)
	}
	m.links[key] = link
	return nil
}

// LookupBySuccessor returns the link whose SuccessorID equals successorID.
func (m *MemChainLinkStore) LookupBySuccessor(_ context.Context, successorID string) (ChainLink, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, l := range m.links {
		if l.SuccessorID == successorID {
			return cloneChainLink(l), true, nil
		}
	}
	return ChainLink{}, false, nil
}

// ListByPredecessor returns all hops from predecessorID, ordered by Outcome.
func (m *MemChainLinkStore) ListByPredecessor(_ context.Context, predecessorID string) ([]ChainLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ChainLink
	for _, l := range m.links {
		if l.PredecessorID == predecessorID {
			out = append(out, cloneChainLink(l))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Outcome < out[j].Outcome })
	return out, nil
}

// cloneChainLink returns a copy of l with an independent StartVars map.
func cloneChainLink(l ChainLink) ChainLink {
	if l.StartVars != nil {
		l.StartVars = maps.Clone(l.StartVars)
	}
	return l
}
