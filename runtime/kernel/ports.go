// Package runtime is the reference driver that performs engine Commands and
// feeds results back as Triggers. It is reference wiring, not the product;
// later sub-projects replace the in-memory ports with real implementations.
package kernel

import (
	"context"
	"errors"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// ErrInstanceNotFound is returned by Store.Load when no instance exists for the id.
var ErrInstanceNotFound = errors.New("workflow-runtime: instance not found")

// ErrInstanceExists is returned by Store.Create when an instance with the same
// id already exists. It lets a caller distinguish a duplicate start from a real
// failure — process-instance chaining treats it as "already started"
// (a clean no-op ack) under at-least-once terminal-event delivery.
var ErrInstanceExists = errors.New("workflow-runtime: instance already exists")

// JournalReader exposes the recorded trigger history for replay/audit.
type JournalReader interface {
	Entries(ctx context.Context, id string) ([]engine.Trigger, error)
}

// Version is an opaque optimistic-concurrency token (Postgres: a bigint version).
type Version int64

// OutboxEvent is one domain event to relay. DedupKey and InstanceID are
// populated when the event is read back from a persisted outbox row; they let a
// publisher set a stable message identity (DedupKey) and a per-instance
// partition/ordering key (InstanceID). They are empty for events not sourced
// from a persisted row.
type OutboxEvent struct {
	Topic      string
	Payload    map[string]any
	DedupKey   string
	InstanceID string
	// DefinitionRef is the id:version reference of the instance that produced the
	// event, carried through to a consumer (e.g. chaining's PredecessorDefinitionRef).
	// It is best-effort routing context — the zero Qualifier when the producing
	// path supplies no definition reference.
	DefinitionRef model.Qualifier
}

// AppliedStep is the atomic persistence unit for exactly one applied trigger:
// the new snapshot, the trigger that produced it, and the outbox events derived
// from the resulting commands.
type AppliedStep struct {
	State   engine.InstanceState
	Trigger engine.Trigger
	Events  []OutboxEvent
	// NewCallLink, when non-nil, records a parent↔child call link atomically with
	// this step (set on the child's first Create).
	NewCallLink *CallLink
	// CallOutcome, when non-nil, flips THIS instance's call link to terminal
	// atomically with this step (set on the child's terminal Commit).
	CallOutcome *CallOutcome
}

// ErrConcurrentUpdate is returned by Store.Commit when the expected token is
// stale (a concurrent writer advanced the instance first).
var ErrConcurrentUpdate = errors.New("workflow-runtime: concurrent update")

// InstanceStore is the transactional persistence port the ProcessDriver depends on. Commit
// persists snapshot + journal + outbox atomically per applied trigger.
type InstanceStore interface {
	Create(ctx context.Context, step AppliedStep) (Version, error)
	Load(ctx context.Context, id string) (engine.InstanceState, Version, error)
	Commit(ctx context.Context, expected Version, step AppliedStep) (Version, error)
}

// PendingCommandClearer is an OPTIONAL [InstanceStore] capability: it drops the
// [engine.InstanceState.PendingCommands] mark from an instance's durable
// snapshot once the runtime has performed those commands.
//
// It is probed by type assertion, exactly like [TxRunner], so an existing
// InstanceStore implementation keeps compiling and keeps working. A store that
// does not implement it simply leaves the mark in place until the instance's
// next committed step overwrites the snapshot; the runtime's recovery sweep
// stays correct either way, because it re-drives at-least-once and its lease
// window bounds how often, but such a store re-performs a parked instance's
// commands once per lease until something else advances it.
//
// Three properties are contractual, and each is load-bearing:
//
//   - It does NOT advance the optimistic-concurrency token. Clearing the mark
//     records that work already committed has now been performed; it is not a
//     new applied step, and bumping the version would invalidate the token the
//     caller is still holding mid-loop.
//   - It records NO journal entry and NO outbox event. The journal is the
//     replay log of applied triggers, and this applies none.
//   - A STALE expected is not an error. A later step has already rewritten the
//     snapshot, mark included, so there is nothing to clear and nothing to
//     report. Implementations return nil.
type PendingCommandClearer interface {
	ClearPendingCommands(ctx context.Context, id string, expected Version) error
}
