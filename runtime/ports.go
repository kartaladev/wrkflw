// Package runtime is the reference driver that performs engine Commands and
// feeds results back as Triggers. It is reference wiring, not the product;
// later sub-projects replace the in-memory ports with real implementations.
package runtime

import "github.com/zakyalvan/krtlwrkflw/engine"

// StateStore persists the authoritative instance snapshot.
type StateStore interface {
	Load(id string) (engine.InstanceState, bool)
	Save(st engine.InstanceState)
}

// Journal is the append-only audit ledger of applied triggers.
type Journal interface {
	Append(id string, trg engine.Trigger)
}

// OutboxWriter records domain events for later relay (no-op in memory here).
type OutboxWriter interface {
	Write(topic string, payload map[string]any)
}
