// Package runtime is the reference driver that performs engine Commands and
// feeds results back as Triggers. It is reference wiring, not the product;
// later sub-projects replace the in-memory ports with real implementations.
package runtime

import (
	"context"
	"errors"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ErrInstanceNotFound is returned by Store.Load when no instance exists for the id.
var ErrInstanceNotFound = errors.New("runtime: instance not found")

// JournalReader exposes the recorded trigger history for replay/audit.
type JournalReader interface {
	Entries(ctx context.Context, id string) ([]engine.Trigger, error)
}

// Token is an opaque optimistic-concurrency token (Postgres: a bigint version).
type Token int64

// OutboxEvent is one domain event to relay.
type OutboxEvent struct {
	Topic   string
	Payload map[string]any
}

// AppliedStep is the atomic persistence unit for exactly one applied trigger:
// the new snapshot, the trigger that produced it, and the outbox events derived
// from the resulting commands.
type AppliedStep struct {
	State   engine.InstanceState
	Trigger engine.Trigger
	Events  []OutboxEvent
}

// ErrConcurrentUpdate is returned by Store.Commit when the expected token is
// stale (a concurrent writer advanced the instance first).
var ErrConcurrentUpdate = errors.New("runtime: concurrent update")

// Store is the transactional persistence port the Runner depends on. Commit
// persists snapshot + journal + outbox atomically per applied trigger.
type Store interface {
	Create(ctx context.Context, step AppliedStep) (Token, error)
	Load(ctx context.Context, id string) (engine.InstanceState, Token, error)
	Commit(ctx context.Context, expected Token, step AppliedStep) (Token, error)
}
