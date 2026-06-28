package mysql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TimerStore is the MySQL-backed runtime.TimerStore. It reads armed timers from
// wrkflw_timers (written transactionally by Store). See ADR-0027.
type TimerStore struct {
	db *sql.DB
}

// NewTimerStore constructs a TimerStore over db. The DB must already have
// migrations applied (see Migrate).
func NewTimerStore(db *sql.DB) *TimerStore {
	return &TimerStore{db: db}
}

// ListArmed implements runtime.TimerStore, ordered by (fire_at, instance_id, timer_id).
func (s *TimerStore) ListArmed(ctx context.Context) ([]runtime.ArmedTimer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT instance_id, def_id, def_version, timer_id, fire_at, kind
		FROM   wrkflw_timers
		ORDER  BY fire_at, instance_id, timer_id`)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: list armed timers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []runtime.ArmedTimer
	for rows.Next() {
		var a runtime.ArmedTimer
		var kind int16
		if err := rows.Scan(&a.InstanceID, &a.DefID, &a.DefVersion, &a.TimerID, &a.FireAt, &kind); err != nil {
			return nil, fmt.Errorf("workflow-persistence-mysql: scan armed timer: %w", err)
		}
		a.Kind = engine.TimerKind(kind)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: iterate armed timers: %w", err)
	}
	return out, nil
}

var _ runtime.TimerStore = (*TimerStore)(nil)
