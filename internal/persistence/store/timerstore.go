package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TimerStore is the vendor-neutral, dialect-parametrised [kernel.TimerStore].
// It reads armed timers from wrkflw_timers — written transactionally via the
// standalone [kernel.TimerWriter] capability (ADR-0134). The read side is
// intentionally separate so the runtime scheduler can be constructed with just
// the connection and dialect value, without carrying the full [Store].
//
// SQL is written once with ? placeholders and run through
// [dialect.Dialect.Rebind] for the backend's native placeholder style. Timestamp
// codec for the next_run column is dialect-aware: Postgres and MySQL bind and
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

// Compile-time checks that *TimerStore satisfies all three runtime ports.
var (
	_ kernel.TimerStore       = (*TimerStore)(nil)
	_ kernel.TimerStatsReader = (*TimerStore)(nil)
	_ kernel.TimerWriter      = (*TimerStore)(nil)
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
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &TimerStore{conn: conn, dialect: d}, nil
}

// ListArmed implements [kernel.TimerStore]. It returns all timers currently
// present in wrkflw_timers, ordered by (next_run ASC, instance_id ASC,
// timer_id ASC) for deterministic re-arm order on engine startup or
// rehydration. FireAt is always UTC-normalised (ADR-0080).
func (s *TimerStore) ListArmed(ctx context.Context) ([]kernel.ArmedTimer, error) {
	q := s.querier()

	rows, err := q.Query(ctx, s.dialect.Rebind(`
		SELECT instance_id, def_id, def_version, timer_id, next_run, kind, trigger_payload
		FROM   wrkflw_timers
		ORDER  BY next_run, instance_id, timer_id`))
	if err != nil {
		return nil, fmt.Errorf("workflow-store: list armed timers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []kernel.ArmedTimer
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

// UpsertJob implements [kernel.TimerWriter]. It writes (or updates) spec's
// wrkflw_timers row via the shared [upsertTimer] SQL, joining the ambient
// ctx-transaction if one is present ([transaction.JoinOrBegin], ADR-0134) so
// the runtime JobStore can persist atomically with the state commit.
//
// NewTimerStore takes its own caller-supplied conn — nothing shares it
// automatically with a Store constructed separately. Correctness does NOT
// depend on same-conn: JoinOrBegin joins the ambient handle stashed in ctx
// regardless of the conn argument, and only begins a fresh transaction over
// s.conn when ctx carries no ambient handle. The wiring requirement this
// implies is same-DATABASE (the join must resolve to the same physical
// database the Store commits into), not same-connection/pool identity.
func (s *TimerStore) UpsertJob(ctx context.Context, spec kernel.JobSpec) error {
	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert job %q/%q: begin: %w", spec.InstanceID, spec.TimerID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	if err := upsertTimer(ctx, q, s.dialect, jobSpecToArmedTimer(spec)); err != nil {
		return err
	}

	if err := q.Commit(ctx); err != nil {
		return fmt.Errorf("workflow-store: upsert job %q/%q: commit: %w", spec.InstanceID, spec.TimerID, err)
	}
	committed = true
	return nil
}

// DeleteJob implements [kernel.TimerWriter]. It removes the wrkflw_timers row
// for (instanceID, timerID) via the shared [deleteTimer] SQL, joining the
// ambient ctx-transaction on the same terms as [TimerStore.UpsertJob].
func (s *TimerStore) DeleteJob(ctx context.Context, instanceID, timerID string) error {
	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: delete job %q/%q: begin: %w", instanceID, timerID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	if err := deleteTimer(ctx, q, s.dialect, instanceID, timerID); err != nil {
		return err
	}

	if err := q.Commit(ctx); err != nil {
		return fmt.Errorf("workflow-store: delete job %q/%q: commit: %w", instanceID, timerID, err)
	}
	committed = true
	return nil
}

// DeleteJobByTimerID implements [kernel.TimerWriter]. It removes the
// wrkflw_timers row for timerID alone (no instanceID scope) via the shared
// [deleteTimerByTimerID] SQL, joining the ambient ctx-transaction on the same
// terms as [TimerStore.UpsertJob]. Engine timer ids are globally unique, so
// this is unambiguous; the runtime JobStore's Delete(id) (Task 10) uses it
// when only the timer id is on hand.
func (s *TimerStore) DeleteJobByTimerID(ctx context.Context, timerID string) error {
	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: delete job %q: begin: %w", timerID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	if err := deleteTimerByTimerID(ctx, q, s.dialect, timerID); err != nil {
		return err
	}

	if err := q.Commit(ctx); err != nil {
		return fmt.Errorf("workflow-store: delete job %q: commit: %w", timerID, err)
	}
	committed = true
	return nil
}

// jobSpecToArmedTimer projects a [kernel.JobSpec] onto the [kernel.ArmedTimer]
// shape [upsertTimer] persists — the two are field-for-field equivalent by
// design (ADR-0134), so this is a straight copy.
func jobSpecToArmedTimer(spec kernel.JobSpec) kernel.ArmedTimer {
	return kernel.ArmedTimer{
		InstanceID: spec.InstanceID,
		DefID:      spec.DefID,
		DefVersion: spec.DefVersion,
		TimerID:    spec.TimerID,
		Trigger:    spec.Trigger,
		NextRun:    spec.NextRun,
		Kind:       spec.Kind,
	}
}

// Stats implements [kernel.TimerStatsReader]. It returns the total count of
// armed timers and the earliest next_run in the wrkflw_timers table.
// NextFireAt is nil when the table is empty. All timestamps are UTC-normalised.
func (s *TimerStore) Stats(ctx context.Context) (kernel.TimerStats, error) {
	q := s.querier()
	if s.dialect.TimestampsAsText() {
		return s.statsText(ctx, q)
	}
	return s.statsNative(ctx, q)
}

// statsNative handles the Stats query for Postgres and MySQL, where next_run
// is a native time.Time column. MIN(next_run) scans into a *time.Time directly.
func (s *TimerStore) statsNative(ctx context.Context, q database.Querier) (kernel.TimerStats, error) {
	var armed int64
	var nextFireAt *time.Time
	err := q.QueryRow(ctx, `SELECT count(*), MIN(next_run) FROM wrkflw_timers`).
		Scan(&armed, &nextFireAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return kernel.TimerStats{}, fmt.Errorf("workflow-store: timer stats: %w", err)
	}
	if nextFireAt != nil {
		t := nextFireAt.UTC()
		nextFireAt = &t
	}
	return kernel.TimerStats{Armed: armed, NextFireAt: nextFireAt}, nil
}

// statsText handles the Stats query for SQLite, where next_run is an
// RFC3339Nano TEXT column. MIN(next_run) is scanned into a *string and then
// parsed via [parseTimeText] (ADR-0080).
func (s *TimerStore) statsText(ctx context.Context, q database.Querier) (kernel.TimerStats, error) {
	var armed int64
	var nextStr *string
	err := q.QueryRow(ctx, `SELECT count(*), MIN(next_run) FROM wrkflw_timers`).
		Scan(&armed, &nextStr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return kernel.TimerStats{}, fmt.Errorf("workflow-store: timer stats (text): %w", err)
	}
	var nextFireAt *time.Time
	if nextStr != nil {
		t, err := parseTimeText(*nextStr)
		if err != nil {
			return kernel.TimerStats{}, fmt.Errorf("workflow-store: timer stats: parse next_fire_at: %w", err)
		}
		nextFireAt = &t
	}
	return kernel.TimerStats{Armed: armed, NextFireAt: nextFireAt}, nil
}

// scanArmedTimer reads one row from the query result into an [kernel.ArmedTimer].
// The next_run column is handled via the time codec: TEXT-timestamp (SQLite) is
// parsed from the RFC3339Nano string; native paths (Postgres/MySQL) scan into
// time.Time directly and are then normalised to UTC (ADR-0080). The
// trigger_payload column (JSONB/JSON/TEXT, nullable) is unmarshalled into a
// [model.TriggerWire] and decoded back to a [schedule.TriggerSpec] via
// [model.ReadTrigger] — the authoritative descriptor RehydrateTimers re-arms
// from. A NULL payload yields the zero (unset) trigger.
func (s *TimerStore) scanArmedTimer(rows interface {
	Scan(dest ...any) error
}) (kernel.ArmedTimer, error) {
	var (
		instanceID string
		defID      string
		defVersion int
		timerID    string
		kind       int16
		payload    []byte
	)

	var nextRun time.Time
	if s.dialect.TimestampsAsText() {
		var nextRunStr string
		if err := rows.Scan(&instanceID, &defID, &defVersion, &timerID, &nextRunStr, &kind, &payload); err != nil {
			return kernel.ArmedTimer{}, fmt.Errorf("workflow-store: scan armed timer (text): %w", err)
		}
		parsed, err := parseTimeText(nextRunStr)
		if err != nil {
			return kernel.ArmedTimer{}, fmt.Errorf("workflow-store: scan armed timer: parse next_run: %w", err)
		}
		nextRun = parsed // already UTC from parseTimeText
	} else {
		// Native time.Time path (Postgres / MySQL).
		var t time.Time
		if err := rows.Scan(&instanceID, &defID, &defVersion, &timerID, &t, &kind, &payload); err != nil {
			return kernel.ArmedTimer{}, fmt.Errorf("workflow-store: scan armed timer: %w", err)
		}
		nextRun = t.UTC() // normalise TIMESTAMPTZ / DATETIME to UTC
	}

	trig, err := decodeTriggerPayload(payload)
	if err != nil {
		return kernel.ArmedTimer{}, fmt.Errorf("workflow-store: scan armed timer %q/%q: %w", instanceID, timerID, err)
	}

	return kernel.ArmedTimer{
		InstanceID: instanceID,
		DefID:      defID,
		DefVersion: defVersion,
		TimerID:    timerID,
		Trigger:    trig,
		NextRun:    nextRun, // the next_run column carries the authoritative next-run instant
		Kind:       engine.TimerKind(kind),
	}, nil
}

// decodeTriggerPayload reconstructs a [schedule.TriggerSpec] from the raw
// trigger_payload column bytes. A nil/empty payload (NULL column, or a row
// written before this column existed) yields the zero trigger. Non-empty bytes
// are unmarshalled into a [model.TriggerWire] and decoded via
// [model.ReadTrigger] — the inverse of [triggerPayloadArg].
func decodeTriggerPayload(payload []byte) (schedule.TriggerSpec, error) {
	if len(payload) == 0 {
		return schedule.TriggerSpec{}, nil
	}
	var w model.TriggerWire
	if err := json.Unmarshal(payload, &w); err != nil {
		return schedule.TriggerSpec{}, fmt.Errorf("unmarshal trigger payload: %w", err)
	}
	return model.ReadTrigger(&w, "", false), nil
}

// querier returns a pool-backed [database.Querier]. TimerStore is read-only so
// it never participates in an ambient transaction. Mirrors the [Lister.querier]
// pattern.
func (s *TimerStore) querier() database.Querier {
	q, _ := database.From(s.conn)
	return q
}
