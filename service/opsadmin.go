package service

//go:generate mockgen -source=opsadmin.go -package=service -destination=opsadmin_mock.go -typed

import (
	"context"

	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// RelayStatsAdmin is the optional admin port for inspecting aggregate statistics
// about the outbox relay. It is intentionally separate from Service: outbox
// relay statistics are an infrastructure concern not available when only using an
// in-memory store (e.g. kernel.MemInstanceStore-only consumers simply never wire it).
//
// The concrete Postgres Relay satisfies RelayStatsAdmin directly — pass the relay
// to a transport's WithRelayStatsAdmin option with no adapter.
type RelayStatsAdmin interface {
	// OutboxStats returns a snapshot of outbox table health metrics.
	OutboxStats(ctx context.Context) (kernel.OutboxStats, error)
}

// TimerAdmin is the optional admin port for inspecting armed timers.
// The concrete SQL TimerStore satisfies TimerAdmin directly.
type TimerAdmin interface {
	// Stats returns aggregate statistics about the armed-timer table.
	Stats(ctx context.Context) (kernel.TimerStats, error)
	// ListArmedPage returns one page of armed timers in
	// (NextRun, InstanceID, TimerID) order.
	//
	// It is deliberately paged rather than whole-table: an admin listing must
	// not be an unbounded read whose cost grows with the timer population.
	// Implementations clamp filter.Limit via [kernel.NormalizeLimit] rather
	// than rejecting it, and issue the total count only when
	// filter.IncludeTotal.
	ListArmedPage(ctx context.Context, filter kernel.ArmedTimerFilter) (kernel.ArmedTimerPage, error)
}

// Note: kernel.MemTimerStore is deliberately NOT a TimerAdmin. It implements
// the read port [kernel.TimerStore] — ListArmed plus the ArmedTimer point
// lookup — but neither Stats nor ListArmedPage. It is a test helper, not an
// admin-inspectable store; only the SQL TimerStore satisfies TimerAdmin.
