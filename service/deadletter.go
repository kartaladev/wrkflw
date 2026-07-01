package service

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// DeadLetterAdmin is the optional admin port for inspecting and redriving
// dead-lettered outbox events. It is intentionally separate from Service: the
// dead-letter queue is an outbox-relay concern, not an engine/runtime one, and a
// consumer without the Postgres outbox relay (e.g. MemStore-only) simply never
// wires it.
//
// Its methods are a subset of persistence.Relay's (which also has Run and
// DrainOnce), so persistence.Relay satisfies DeadLetterAdmin directly — pass the
// relay straight to a transport's WithDeadLetterAdmin option with no adapter.
type DeadLetterAdmin interface {
	// ListDeadLettered returns up to limit dead-lettered outbox rows, oldest first.
	ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
	// Redrive resets the given dead rows back to pending and returns the count
	// re-queued. Passing no ids is a no-op (returns 0, nil).
	Redrive(ctx context.Context, ids ...int64) (int, error)
}
