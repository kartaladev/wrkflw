package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TimerStore is the vendor-neutral, dialect-parametrised [runtime.TimerStore].
// It reads armed timers from wrkflw_timers — written transactionally by [Store]
// via AppliedStep.TimerArms and TimerCancels (ADR-0027). The read side is
// intentionally separate so the runtime scheduler can be constructed with just
// the connection and dialect value, without carrying the full [Store].
//
// SQL is written once with ? placeholders and run through
// [dialect.Dialect.Rebind] for the backend's native placeholder style. Timestamp
// codec for the fire_at column is dialect-aware: Postgres and MySQL bind and
// scan time.Time natively; SQLite stores TEXT as RFC3339Nano and needs the
// [parseTimeText] helper on the read side (ADR-0080). The codec is gated on
// [dialect.Dialect.TimestampsAsText] — NEVER compare [dialect.Dialect.Name]
// to "sqlite" directly.
//
// TimerStore is read-only and safe for concurrent use.
type TimerStore struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// Compile-time checks that *TimerStore satisfies both runtime ports.
var (
	_ runtime.TimerStore       = (*TimerStore)(nil)
	_ runtime.TimerStatsReader = (*TimerStore)(nil)
)

// NewTimerStore constructs a TimerStore over conn using dialect d. conn must be
// either a *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite); any other
// type causes [database.From] to return an error when the first query is issued.
// Returns [ErrNilDependency] when conn is nil or d is nil.
//
// NewTimerStore mirrors the [New] and [NewLister] constructor shape so callers
// can pair a TimerStore alongside a Store with the same conn and dialect value.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	ts, err := store.NewTimerStore(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	ts, err := store.NewTimerStore(db, dialect.NewSQLite())
func NewTimerStore(conn any, d dialect.Dialect) (*TimerStore, error) {
	if conn == nil {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if d == nil {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &TimerStore{conn: conn, dialect: d}, nil
}

// ListArmed implements [runtime.TimerStore]. It returns all timers currently
// present in wrkflw_timers, ordered by (fire_at ASC, instance_id ASC,
// timer_id ASC) for deterministic re-arm order on engine startup or
// rehydration. FireAt is always UTC-normalised (ADR-0080).
func (s *TimerStore) ListArmed(ctx context.Context) ([]runtime.ArmedTimer, error) {
	q := s.querier()

	rows, err := q.Query(ctx, s.dialect.Rebind(`
		SELECT instance_id, def_id, def_version, timer_id, fire_at, kind
		FROM   wrkflw_timers
		ORDER  BY fire_at, instance_id, timer_id`))
	if err != nil {
		return nil, fmt.Errorf("workflow-store: list armed timers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []runtime.ArmedTimer
	for rows.Next() {
		a, err := s.scanArmedTimer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: iterate armed timers: %w", err)
	}
	return out, nil
}

// Stats implements [runtime.TimerStatsReader]. It returns the total count of
// armed timers and the earliest fire_at in the wrkflw_timers table.
// NextFireAt is nil when the table is empty. All timestamps are UTC-normalised.
func (s *TimerStore) Stats(ctx context.Context) (runtime.TimerStats, error) {
	q := s.querier()
	if s.dialect.TimestampsAsText() {
		return s.statsText(ctx, q)
	}
	return s.statsNative(ctx, q)
}

// statsNative handles the Stats query for Postgres and MySQL, where fire_at
// is a native time.Time column. MIN(fire_at) scans into a *time.Time directly.
func (s *TimerStore) statsNative(ctx context.Context, q database.Querier) (runtime.TimerStats, error) {
	var armed int64
	var nextFireAt *time.Time
	err := q.QueryRow(ctx, `SELECT count(*), MIN(fire_at) FROM wrkflw_timers`).
		Scan(&armed, &nextFireAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return runtime.TimerStats{}, fmt.Errorf("workflow-store: timer stats: %w", err)
	}
	if nextFireAt != nil {
		t := nextFireAt.UTC()
		nextFireAt = &t
	}
	return runtime.TimerStats{Armed: armed, NextFireAt: nextFireAt}, nil
}

// statsText handles the Stats query for SQLite, where fire_at is an
// RFC3339Nano TEXT column. MIN(fire_at) is scanned into a *string and then
// parsed via [parseTimeText] (ADR-0080).
func (s *TimerStore) statsText(ctx context.Context, q database.Querier) (runtime.TimerStats, error) {
	var armed int64
	var nextStr *string
	err := q.QueryRow(ctx, `SELECT count(*), MIN(fire_at) FROM wrkflw_timers`).
		Scan(&armed, &nextStr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return runtime.TimerStats{}, fmt.Errorf("workflow-store: timer stats (text): %w", err)
	}
	var nextFireAt *time.Time
	if nextStr != nil {
		t, err := parseTimeText(*nextStr)
		if err != nil {
			return runtime.TimerStats{}, fmt.Errorf("workflow-store: timer stats: parse next_fire_at: %w", err)
		}
		nextFireAt = &t
	}
	return runtime.TimerStats{Armed: armed, NextFireAt: nextFireAt}, nil
}

// scanArmedTimer reads one row from the query result into an [runtime.ArmedTimer].
// The fire_at column is handled via the time codec: TEXT-timestamp (SQLite) is
// parsed from the RFC3339Nano string; native paths (Postgres/MySQL) scan into
// time.Time directly and are then normalised to UTC (ADR-0080).
func (s *TimerStore) scanArmedTimer(rows interface {
	Scan(dest ...any) error
}) (runtime.ArmedTimer, error) {
	var (
		instanceID string
		defID      string
		defVersion int
		timerID    string
		kind       int16
	)

	if s.dialect.TimestampsAsText() {
		var fireAtStr string
		if err := rows.Scan(&instanceID, &defID, &defVersion, &timerID, &fireAtStr, &kind); err != nil {
			return runtime.ArmedTimer{}, fmt.Errorf("workflow-store: scan armed timer (text): %w", err)
		}
		fireAt, err := parseTimeText(fireAtStr)
		if err != nil {
			return runtime.ArmedTimer{}, fmt.Errorf("workflow-store: scan armed timer: parse fire_at: %w", err)
		}
		return runtime.ArmedTimer{
			InstanceID: instanceID,
			DefID:      defID,
			DefVersion: defVersion,
			TimerID:    timerID,
			FireAt:     fireAt, // already UTC from parseTimeText
			Kind:       engine.TimerKind(kind),
		}, nil
	}

	// Native time.Time path (Postgres / MySQL).
	var fireAt time.Time
	if err := rows.Scan(&instanceID, &defID, &defVersion, &timerID, &fireAt, &kind); err != nil {
		return runtime.ArmedTimer{}, fmt.Errorf("workflow-store: scan armed timer: %w", err)
	}
	return runtime.ArmedTimer{
		InstanceID: instanceID,
		DefID:      defID,
		DefVersion: defVersion,
		TimerID:    timerID,
		FireAt:     fireAt.UTC(), // normalise TIMESTAMPTZ / DATETIME to UTC
		Kind:       engine.TimerKind(kind),
	}, nil
}

// querier returns a pool-backed [database.Querier]. TimerStore is read-only so
// it never participates in an ambient transaction. Mirrors the [Lister.querier]
// pattern.
func (s *TimerStore) querier() database.Querier {
	q, _ := database.From(s.conn)
	return q
}
