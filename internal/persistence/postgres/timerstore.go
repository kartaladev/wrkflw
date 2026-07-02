package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TimerStore is the Postgres-backed runtime.TimerStore. It reads armed timers
// from wrkflw_timers (written transactionally by Store). See ADR-0027.
type TimerStore struct {
	pool *pgxpool.Pool
}

// NewTimerStore constructs a TimerStore over pool. The pool must already have
// migrations applied (see Migrate).
func NewTimerStore(pool *pgxpool.Pool) *TimerStore {
	return &TimerStore{pool: pool}
}

// ListArmed implements runtime.TimerStore, ordered by (fire_at, instance_id, timer_id).
func (s *TimerStore) ListArmed(ctx context.Context) ([]runtime.ArmedTimer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, def_id, def_version, timer_id, fire_at, kind
		FROM   wrkflw_timers
		ORDER  BY fire_at, instance_id, timer_id`)
	if err != nil {
		return nil, fmt.Errorf("workflow-postgres: list armed timers: %w", err)
	}
	defer rows.Close()

	var out []runtime.ArmedTimer
	for rows.Next() {
		var a runtime.ArmedTimer
		var kind int16
		if err := rows.Scan(&a.InstanceID, &a.DefID, &a.DefVersion, &a.TimerID, &a.FireAt, &kind); err != nil {
			return nil, fmt.Errorf("workflow-postgres: scan armed timer: %w", err)
		}
		a.FireAt = a.FireAt.UTC() // normalize TIMESTAMPTZ to UTC-located (pgx may return host zone)
		a.Kind = engine.TimerKind(kind)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-postgres: iterate armed timers: %w", err)
	}
	return out, nil
}

// Stats returns aggregate statistics about the wrkflw_timers table: the total
// count of armed timers and the earliest fire_at timestamp among them.
// NextFireAt is nil when the table is empty.
//
// It implements runtime.TimerStatsReader.
func (s *TimerStore) Stats(ctx context.Context) (runtime.TimerStats, error) {
	var armed int64
	var nextFireAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT count(*), min(fire_at) FROM wrkflw_timers`,
	).Scan(&armed, &nextFireAt)
	if err != nil {
		return runtime.TimerStats{}, fmt.Errorf("workflow-postgres: timer store: stats: %w", err)
	}
	if nextFireAt != nil {
		t := nextFireAt.UTC() // normalize TIMESTAMPTZ to UTC-located (pgx may return host zone)
		nextFireAt = &t
	}
	return runtime.TimerStats{
		Armed:      armed,
		NextFireAt: nextFireAt,
	}, nil
}

var _ runtime.TimerStatsReader = (*TimerStore)(nil)

var _ runtime.TimerStore = (*TimerStore)(nil)
