// Package runtime is the reference driver that performs engine Commands and
// feeds results back as Triggers. It is reference wiring, not the product;
// later sub-projects replace the in-memory ports with real implementations.
package kernel

import (
	"context"
	"errors"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ErrInstanceNotFound is returned by Store.Load when no instance exists for the id.
var ErrInstanceNotFound = errors.New("workflow-runtime: instance not found")

// ErrInstanceExists is returned by Store.Create when an instance with the same
// id already exists. It lets a caller distinguish a duplicate start from a real
// failure — process-instance chaining (ADR-0045) treats it as "already started"
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
	// It is best-effort routing context — the zero Qualifier for events/rows
	// produced before ADR-0047.
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
	// this step (set on the child's first Create). ADR-0025.
	NewCallLink *CallLink
	// CallOutcome, when non-nil, flips THIS instance's call link to terminal
	// atomically with this step (set on the child's terminal Commit). ADR-0025.
	CallOutcome *CallOutcome
	// TimerArms are timers armed by this step (one per ScheduleTimer command).
	// The Store upserts them into the armed-timers table atomically with the
	// state commit (ADR-0027). Empty unless a TimerStore is wired.
	TimerArms []ArmedTimer
	// TimerCancels are timer IDs disarmed by this step (CancelTimer commands and
	// the fired timer on a TimerFired trigger). The Store deletes them atomically
	// with the state commit. Empty unless a TimerStore is wired.
	TimerCancels []string
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
