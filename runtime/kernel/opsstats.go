package kernel

import (
	"context"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// OutboxStats summarises the current health of the wrkflw_outbox table for
// operational dashboards. It is produced by OutboxStatsReader.OutboxStats.
type OutboxStats struct {
	// Pending is the number of outbox rows with status='pending' (not yet published).
	Pending int64
	// Dead is the number of quarantined rows with status='dead'.
	Dead int64
	// OldestPendingAge is the wall-clock age of the oldest pending row
	// (now - min(created_at) FILTER WHERE status='pending'). Zero when there are
	// no pending rows.
	OldestPendingAge time.Duration
}

// TimerStats summarises the current state of the wrkflw_timers table for
// operational dashboards. It is produced by TimerStatsReader.Stats.
type TimerStats struct {
	// Armed is the total number of armed timer rows.
	Armed int64
	// NextFireAt is the earliest next_run among all armed timers, or nil when the
	// table is empty.
	NextFireAt *time.Time
}

// OutboxStatsReader is implemented by any component that can report aggregate
// statistics about the outbox table (e.g. the Postgres Relay).
type OutboxStatsReader interface {
	OutboxStats(ctx context.Context) (OutboxStats, error)
}

// TimerStatsReader is implemented by any component that can report aggregate
// statistics about the timers table (e.g. the Postgres TimerStore).
type TimerStatsReader interface {
	Stats(ctx context.Context) (TimerStats, error)
}

// CallLinkRef is a compact reference to a call-linked instance — parent or child
// — returned by the lineage read port. It carries the fields needed to identify
// the instance and its position in the call chain without returning the full
// CallLink record (which includes write-side fields like ParentCommandID).
//
// The related instance's execution status is NOT carried here; an operator must
// fetch the instance's snapshot to learn it.
//
// For a child relation DefID and DefVersion are empty: wrkflw_call_links records
// only the parent definition, not the child's. The child's definition ref must
// be retrieved from the child's own instance record.
type CallLinkRef struct {
	InstanceID string
	DefID      string
	DefVersion int
	Depth      int
}

// ChainLinkRef is a compact reference to a chain-linked instance — predecessor
// or successor — returned by the lineage read port. It carries the definition
// reference and the chaining outcome that connected the two instances.
type ChainLinkRef struct {
	InstanceID    string
	DefinitionRef model.Qualifier
	Outcome       string
}

// InstanceLineage aggregates a single-hop lineage view for one process instance.
// CallParent is nil when the instance was not started by a call activity
// (it is a root). ChainPredecessor is nil when the instance was not started by
// chaining. CallChildren and ChainSuccessors are empty (never nil) when no
// downstream instances exist.
type InstanceLineage struct {
	InstanceID       string
	CallParent       *CallLinkRef
	CallChildren     []CallLinkRef
	ChainPredecessor *ChainLinkRef
	ChainSuccessors  []ChainLinkRef
}

// CallLineageReader is the read port for call-activity lineage lookups. It is
// satisfied by any store that can return the parent and direct children of a
// given instance via the wrkflw_call_links table.
type CallLineageReader interface {
	// ParentOf returns the CallLink describing the parent call relationship for
	// childID. Returns (nil, nil) when childID is a root instance (no parent).
	ParentOf(ctx context.Context, childID string) (*CallLink, error)
	// ChildrenOf returns all CallLinks whose parent_instance_id equals parentID,
	// ordered by (created_at, child_instance_id). Returns an empty slice (never
	// nil) when there are no children.
	ChildrenOf(ctx context.Context, parentID string) ([]CallLink, error)
}

// ChainLineageReader is the read port for process-chaining lineage lookups. It
// is satisfied by any store that can return the predecessor and successors of a
// given instance via the wrkflw_chain_links table.
type ChainLineageReader interface {
	// PredecessorOf returns the ChainLink that produced successorID. Returns
	// (nil, nil) when successorID was not produced by chaining (it is a chain root).
	PredecessorOf(ctx context.Context, successorID string) (*ChainLink, error)
	// SuccessorsOf returns all ChainLinks fanned out from predecessorID, ordered
	// by outcome. Returns an empty slice (never nil) when there are no successors.
	SuccessorsOf(ctx context.Context, predecessorID string) ([]ChainLink, error)
}
