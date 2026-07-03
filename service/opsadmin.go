package service

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// RelayStatsAdmin is the optional admin port for inspecting aggregate statistics
// about the outbox relay. It is intentionally separate from Service: outbox
// relay statistics are an infrastructure concern not available when only using an
// in-memory store (e.g. kernel.MemStore-only consumers simply never wire it).
//
// The concrete Postgres Relay satisfies RelayStatsAdmin directly — pass the relay
// to a transport's WithRelayStatsAdmin option with no adapter.
type RelayStatsAdmin interface {
	// OutboxStats returns a snapshot of outbox table health metrics.
	OutboxStats(ctx context.Context) (kernel.OutboxStats, error)
}

// TimerAdmin is the optional admin port for inspecting armed timers.
// The concrete Postgres TimerStore satisfies TimerAdmin directly.
type TimerAdmin interface {
	// Stats returns aggregate statistics about the armed-timer table.
	Stats(ctx context.Context) (kernel.TimerStats, error)
	// ListArmed returns all currently armed timers in (FireAt, InstanceID, TimerID)
	// order.
	ListArmed(ctx context.Context) ([]kernel.ArmedTimer, error)
}

// Note: kernel.MemTimerStore implements only ListArmed (it has no Stats method),
// so it does NOT satisfy the full TimerAdmin — only the Postgres/MySQL TimerStore
// does. MemTimerStore is a test helper, not a full TimerAdmin implementation.
