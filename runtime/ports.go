// Package runtime is the reference driver that performs engine Commands and
// feeds results back as Triggers. It is reference wiring, not the product;
// later sub-projects replace the in-memory ports with real implementations.
package runtime

import (
	"errors"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ErrInstanceNotFound is returned by StateStore.Load when no instance exists for the id.
var ErrInstanceNotFound = errors.New("runtime: instance not found")

// StateStore persists the authoritative instance snapshot.
type StateStore interface {
	Load(id string) (engine.InstanceState, error)
	Save(st engine.InstanceState) error
}

// Journal is the append-only audit ledger of applied triggers.
type Journal interface {
	Append(id string, trg engine.Trigger) error
}

// JournalReader exposes the recorded trigger history for replay/audit.
type JournalReader interface {
	Entries(id string) []engine.Trigger
}

// OutboxWriter records domain events for later relay.
type OutboxWriter interface {
	Write(topic string, payload map[string]any) error
}
