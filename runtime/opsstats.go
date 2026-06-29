package runtime

import (
	"context"
	"time"
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
	// NextFireAt is the earliest fire_at among all armed timers, or nil when the
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
