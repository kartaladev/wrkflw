package postgres

import (
	"context"
	"fmt"

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
		a.Kind = engine.TimerKind(kind)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-postgres: iterate armed timers: %w", err)
	}
	return out, nil
}

var _ runtime.TimerStore = (*TimerStore)(nil)
