package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time checks that *Store satisfies both persistence ports.
var (
	_ runtime.Store         = (*Store)(nil)
	_ runtime.JournalReader = (*Store)(nil)
)

// outboxNotifyChannel is the wake channel the relay listens on (ADR-0022). The
// notification carries no payload — it is a bare wakeup; the relay still claims
// rows via FOR UPDATE SKIP LOCKED. Only backends whose dialect returns a
// non-empty NotifyStatement (Postgres) emit it.
const outboxNotifyChannel = "wrkflw_outbox"

// timeArg converts a time.Time into the correct bind argument for the store's
// dialect. Postgres (TIMESTAMPTZ) and MySQL (DATETIME, DSN loc=UTC) bind
// time.Time natively. SQLite timestamp columns are TEXT: the modernc.org/sqlite
// driver stringifies a bound time.Time via its default String() form, which is
// NOT ISO8601 (julianday() returns NULL for it) and cannot be scanned back.
// For SQLite the value is therefore formatted as an RFC3339Nano UTC string,
// which is julianday-compatible and round-trips exactly (ADR-0080).
//
// The TEXT path is activated by [dialect.Dialect.TimestampsAsText]; callers
// must not compare [dialect.Dialect.Name] to "sqlite" directly.
func (s *Store) timeArg(t time.Time) any {
	if s.dialect.TimestampsAsText() {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return t
}

// timeArgP converts an optional time.Time (nil-safe) for a dialect-correct bind.
// A nil pointer stays nil so the column is written NULL on every backend.
func (s *Store) timeArgP(t *time.Time) any {
	if t == nil {
		return nil
	}
	return s.timeArg(*t)
}

// Create inserts a brand-new process instance from its first applied step.
// version is set to 1; journal seq is 1; outbox dedup_key is "<id>:1:<i>".
// All writes are performed atomically in one transaction (joining an ambient
// one if present, otherwise beginning a fresh leaf).
func (s *Store) Create(ctx context.Context, step runtime.AppliedStep) (runtime.Token, error) {
	const version int64 = 1

	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: create: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		return 0, fmt.Errorf("workflow-store: create: marshal snapshot: %w", err)
	}

	now := time.Now().UTC()
	if _, err := q.Exec(ctx, s.dialect.Rebind(
		`INSERT INTO wrkflw_instances
		   (instance_id, def_id, def_version, status, snapshot, version, started_at, ended_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`),
		step.State.InstanceID,
		step.State.DefID,
		step.State.DefVersion,
		int16(step.State.Status),
		snap,
		version,
		s.timeArg(step.State.StartedAt),
		s.timeArgP(step.State.EndedAt),
		s.timeArg(now),
	); err != nil {
		if s.dialect.IsUniqueViolation(err) {
			return 0, runtime.ErrInstanceExists
		}
		return 0, fmt.Errorf("workflow-store: create: insert instance: %w", err)
	}

	if err := s.writeJournal(ctx, q, step, version, now); err != nil {
		return 0, s.mapConflict(err)
	}
	if err := s.writeOutbox(ctx, q, step.State.InstanceID, version, step.Events, now); err != nil {
		return 0, s.mapConflict(err)
	}
	if err := s.maybeNotify(ctx, q, step.Events); err != nil {
		return 0, err
	}

	if step.NewCallLink != nil {
		if err := s.insertCallLink(ctx, q, *step.NewCallLink, now); err != nil {
			return 0, s.mapConflict(err)
		}
	}

	if err := s.applyTimerOps(ctx, q, step); err != nil {
		return 0, s.mapConflict(err)
	}

	if err := q.Commit(ctx); err != nil {
		return 0, s.mapConflict(fmt.Errorf("workflow-store: create: commit: %w", err))
	}
	committed = true
	return runtime.Token(version), nil
}

// Load returns the persisted snapshot and the current optimistic-concurrency
// token for the given instance. Returns runtime.ErrInstanceNotFound when no row
// exists for id. It reads through the pool (no ambient transaction).
//
// Emits a "wrkflw.store.load" OTel span and records a data point in the
// wrkflw_store_duration_seconds histogram with attribute op=load.
func (s *Store) Load(ctx context.Context, id string) (engine.InstanceState, runtime.Token, error) {
	ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.load")
	defer span.End()
	start := time.Now()
	defer func() {
		s.storeDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("op", "load")))
	}()

	q := s.querier(ctx)

	var snap []byte
	var version int64
	err := q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT snapshot, version FROM wrkflw_instances WHERE instance_id = ?`), id,
	).Scan(&snap, &version)
	if errors.Is(err, sql.ErrNoRows) {
		// Both drivers surface a no-rows scan error that matches sql.ErrNoRows:
		// database/sql returns it directly; pgx.ErrNoRows wraps it (pgx v5).
		span.RecordError(runtime.ErrInstanceNotFound)
		span.SetStatus(otelcodes.Error, runtime.ErrInstanceNotFound.Error())
		return engine.InstanceState{}, 0, runtime.ErrInstanceNotFound
	}
	if err != nil {
		wrapped := fmt.Errorf("workflow-store: load %q: %w", id, err)
		span.RecordError(wrapped)
		span.SetStatus(otelcodes.Error, wrapped.Error())
		return engine.InstanceState{}, 0, wrapped
	}

	var stateOut engine.InstanceState
	if err := json.Unmarshal(snap, &stateOut); err != nil {
		wrapped := fmt.Errorf("workflow-store: load %q: unmarshal snapshot: %w", id, err)
		span.RecordError(wrapped)
		span.SetStatus(otelcodes.Error, wrapped.Error())
		return engine.InstanceState{}, 0, wrapped
	}
	return stateOut, runtime.Token(version), nil
}

// Commit atomically applies one step against a running instance:
//   - CAS UPDATE on wrkflw_instances (WHERE version = expected → version+1),
//   - INSERT into wrkflw_journal (next seq),
//   - INSERT each event into wrkflw_outbox,
//   - flip call link / apply timer ops when the step carries them.
//
// Returns runtime.ErrConcurrentUpdate when the expected token is stale (another
// writer advanced the instance first) or when the backend raises a transient
// serialization/deadlock error (classified via dialect.IsRetryableConflict).
//
// Emits a "wrkflw.store.commit" OTel span and records a data point in the
// wrkflw_store_duration_seconds histogram with attribute op=commit. A version
// mismatch (optimistic-CAS conflict) is recorded as attribute
// wrkflw.concurrent_update=true and does NOT mark the span as Error — it is
// expected, retryable control flow that must not pollute error-rate dashboards.
func (s *Store) Commit(ctx context.Context, expected runtime.Token, step runtime.AppliedStep) (runtime.Token, error) {
	ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.commit")
	defer span.End()
	start := time.Now()
	defer func() {
		s.storeDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("op", "commit")))
	}()

	// spanErr records err on the span and sets Error status; used on early returns.
	spanErr := func(err error) {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())
	}

	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		wrapped := fmt.Errorf("workflow-store: commit: begin: %w", err)
		spanErr(wrapped)
		return 0, wrapped
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		wrapped := fmt.Errorf("workflow-store: commit: marshal snapshot: %w", err)
		spanErr(wrapped)
		return 0, wrapped
	}

	now := time.Now().UTC()
	res, err := q.Exec(ctx, s.dialect.Rebind(
		`UPDATE wrkflw_instances
		    SET snapshot = ?, version = version + 1, status = ?, ended_at = ?, updated_at = ?
		  WHERE instance_id = ? AND version = ?`),
		snap,
		int16(step.State.Status),
		s.timeArgP(step.State.EndedAt),
		s.timeArg(now),
		step.State.InstanceID,
		int64(expected),
	)
	if err != nil {
		mapped := s.mapConflict(fmt.Errorf("workflow-store: commit: update: %w", err))
		spanErr(mapped)
		return 0, mapped
	}
	rows, err := res.RowsAffected()
	if err != nil {
		wrapped := fmt.Errorf("workflow-store: commit: rows affected: %w", err)
		spanErr(wrapped)
		return 0, wrapped
	}
	if rows == 0 {
		// Version mismatch: another writer advanced the token first. This is
		// expected optimistic-concurrency control flow (the runner retries on
		// ErrConcurrentUpdate), NOT a failure — record it as a contention
		// attribute rather than marking the span as an error, so normal retries
		// don't pollute trace-derived error-rate dashboards.
		span.SetAttributes(attribute.Bool("wrkflw.concurrent_update", true))
		return 0, runtime.ErrConcurrentUpdate
	}

	next := int64(expected) + 1 // 1:1 with journal seq (journal seq == version after commit)

	if err := s.writeJournal(ctx, q, step, next, now); err != nil {
		mapped := s.mapConflict(err)
		spanErr(mapped)
		return 0, mapped
	}
	if err := s.writeOutbox(ctx, q, step.State.InstanceID, next, step.Events, now); err != nil {
		mapped := s.mapConflict(err)
		spanErr(mapped)
		return 0, mapped
	}
	if err := s.maybeNotify(ctx, q, step.Events); err != nil {
		mapped := s.mapConflict(err)
		spanErr(mapped)
		return 0, mapped
	}

	if step.CallOutcome != nil {
		if err := s.flipCallLink(ctx, q, step.State.InstanceID, *step.CallOutcome, now); err != nil {
			mapped := s.mapConflict(err)
			spanErr(mapped)
			return 0, mapped
		}
	}

	if err := s.applyTimerOps(ctx, q, step); err != nil {
		spanErr(err)
		return 0, err
	}

	if err := q.Commit(ctx); err != nil {
		mapped := s.mapConflict(fmt.Errorf("workflow-store: commit: %w", err))
		spanErr(mapped)
		return 0, mapped
	}
	committed = true
	return runtime.Token(next), nil
}

// Entries returns the recorded trigger history for the given instance id,
// ordered by journal seq ascending. It reads through the pool.
func (s *Store) Entries(ctx context.Context, id string) ([]engine.Trigger, error) {
	q := s.querier(ctx)

	rows, err := q.Query(ctx, s.dialect.Rebind(
		`SELECT kind, `+s.dialect.JournalTriggerColumn()+` FROM wrkflw_journal WHERE instance_id = ? ORDER BY seq`), id)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: entries %q: %w", id, err)
	}
	defer func() { _ = rows.Close() }()

	var triggers []engine.Trigger
	for rows.Next() {
		var kind string
		var data []byte
		if err := rows.Scan(&kind, &data); err != nil {
			return nil, fmt.Errorf("workflow-store: entries %q: scan: %w", id, err)
		}
		trg, err := UnmarshalTrigger(kind, data)
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, trg)
	}
	return triggers, rows.Err()
}

// mapConflict translates a transient serialization/deadlock error into
// runtime.ErrConcurrentUpdate (via dialect.IsRetryableConflict) so callers do
// not depend on any driver package. All other errors pass through unchanged.
func (s *Store) mapConflict(err error) error {
	if s.dialect.IsRetryableConflict(err) {
		return runtime.ErrConcurrentUpdate
	}
	return err
}

// maybeNotify issues a transactional wake statement when the dialect supports
// it (Postgres), notify emission is enabled, and the step produced outbox
// events. Errors propagate so the whole step rolls back.
func (s *Store) maybeNotify(ctx context.Context, q database.Querier, events []runtime.OutboxEvent) error {
	if !s.emitNotify || len(events) == 0 {
		return nil
	}
	stmt := s.dialect.NotifyStatement(outboxNotifyChannel)
	if stmt == "" {
		return nil // backend has no native pub/sub (MySQL, SQLite)
	}
	if _, err := q.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("workflow-store: notify outbox: %w", err)
	}
	return nil
}

// writeJournal inserts one row into wrkflw_journal on q. seq must equal the new
// version written to wrkflw_instances by Create/Commit. The payload column name
// is dialect-specific ("trigger" on PG/SQLite, "trigger_" on MySQL).
func (s *Store) writeJournal(ctx context.Context, q database.Querier, step runtime.AppliedStep, seq int64, appliedAt time.Time) error {
	data, kind, err := MarshalTrigger(step.Trigger)
	if err != nil {
		return err
	}
	col := s.dialect.JournalTriggerColumn()
	if _, err := q.Exec(ctx, s.dialect.Rebind(
		`INSERT INTO wrkflw_journal (instance_id, seq, kind, `+col+`, occurred_at, applied_at)
		 VALUES (?,?,?,?,?,?)`),
		step.State.InstanceID, seq, kind, data,
		s.timeArg(step.Trigger.OccurredAt()), s.timeArg(appliedAt),
	); err != nil {
		return fmt.Errorf("workflow-store: write journal: %w", err)
	}
	return nil
}

// writeOutbox inserts one row per event into wrkflw_outbox on q. The dedup_key
// is "<instanceID>:<seq>:<eventIndex>" — globally unique per applied step
// because (instanceID, seq) is unique per journal row.
func (s *Store) writeOutbox(ctx context.Context, q database.Querier, instanceID string, seq int64, events []runtime.OutboxEvent, createdAt time.Time) error {
	for i, ev := range events {
		payload, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("workflow-store: write outbox: marshal payload: %w", err)
		}
		dedup := fmt.Sprintf("%s:%d:%d", instanceID, seq, i)
		if _, err := q.Exec(ctx, s.dialect.Rebind(
			`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at, definition_ref)
			 VALUES (?,?,?,?,?,?)`),
			instanceID, ev.Topic, payload, dedup, s.timeArg(createdAt), ev.DefinitionRef,
		); err != nil {
			return fmt.Errorf("workflow-store: write outbox: %w", err)
		}
	}
	return nil
}

// insertCallLink writes a new wrkflw_call_links row with status='running' on q.
// Called during Create when the applied step carries a NewCallLink — atomic with
// the child instance INSERT (ADR-0025 crash-safety seam).
func (s *Store) insertCallLink(ctx context.Context, q database.Querier, link runtime.CallLink, createdAt time.Time) error {
	if _, err := q.Exec(ctx, s.dialect.Rebind(
		`INSERT INTO wrkflw_call_links
		   (child_instance_id, parent_instance_id, parent_command_id, parent_def_id, parent_def_version, depth, status, created_at)
		 VALUES (?,?,?,?,?,?,'running',?)`),
		link.ChildInstanceID,
		link.ParentInstanceID,
		link.ParentCommandID,
		link.ParentDefID,
		link.ParentDefVersion,
		link.Depth,
		s.timeArg(createdAt),
	); err != nil {
		return fmt.Errorf("workflow-store: create: call link: %w", err)
	}
	return nil
}

// upsertTimer writes (or updates) a wrkflw_timers row on q, atomic with the
// state commit (ADR-0027). Re-arming the same (instance, timer) overwrites the
// row via the dialect's UpsertTimer conflict clause.
func (s *Store) upsertTimer(ctx context.Context, q database.Querier, tm runtime.ArmedTimer) error {
	_, err := q.Exec(ctx, s.dialect.Rebind(
		`INSERT INTO wrkflw_timers (instance_id, timer_id, fire_at, kind, def_id, def_version)
		 VALUES (?,?,?,?,?,?)`+s.dialect.UpsertTimer()),
		tm.InstanceID, tm.TimerID, s.timeArg(tm.FireAt), int16(tm.Kind), tm.DefID, tm.DefVersion)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert timer %q/%q: %w", tm.InstanceID, tm.TimerID, err)
	}
	return nil
}

// deleteTimer removes a wrkflw_timers row on q (fired or cancelled). A zero-row
// delete is fine (idempotent / already gone).
func (s *Store) deleteTimer(ctx context.Context, q database.Querier, instanceID, timerID string) error {
	_, err := q.Exec(ctx, s.dialect.Rebind(
		`DELETE FROM wrkflw_timers WHERE instance_id = ? AND timer_id = ?`),
		instanceID, timerID)
	if err != nil {
		return fmt.Errorf("workflow-store: delete timer %q/%q: %w", instanceID, timerID, err)
	}
	return nil
}

// applyTimerOps applies a step's timer arms and cancels on q.
func (s *Store) applyTimerOps(ctx context.Context, q database.Querier, step runtime.AppliedStep) error {
	for _, a := range step.TimerArms {
		if err := s.upsertTimer(ctx, q, a); err != nil {
			return err
		}
	}
	for _, id := range step.TimerCancels {
		if err := s.deleteTimer(ctx, q, step.State.InstanceID, id); err != nil {
			return err
		}
	}
	return nil
}

// flipCallLink updates the wrkflw_call_links row for childInstanceID to the
// terminal status implied by outcome (completed or failed) on q. Called during
// Commit when the applied step carries a CallOutcome — atomic with the snapshot
// UPDATE (ADR-0025). For a root instance (no link row) the UPDATE affects zero
// rows, which is a clean no-op; zero rows must NOT be treated as an error.
func (s *Store) flipCallLink(ctx context.Context, q database.Querier, childInstanceID string, outcome runtime.CallOutcome, updatedAt time.Time) error {
	_ = updatedAt // no updated_at column on call_links flip; retained for signature parity
	status := "failed"
	if outcome.Completed {
		status = "completed"
	}

	var outputJSON []byte
	if outcome.Completed && len(outcome.Output) > 0 {
		b, err := json.Marshal(outcome.Output)
		if err != nil {
			return fmt.Errorf("workflow-store: commit: call link: marshal output: %w", err)
		}
		outputJSON = b
	}

	var errText *string
	if !outcome.Completed && outcome.Err != "" {
		errText = &outcome.Err
	}

	if _, err := q.Exec(ctx, s.dialect.Rebind(
		`UPDATE wrkflw_call_links
		    SET status = ?, output = ?, error = ?
		  WHERE child_instance_id = ?`),
		status,
		outputJSON,
		errText,
		childInstanceID,
	); err != nil {
		return fmt.Errorf("workflow-store: commit: call link: %w", err)
	}
	return nil
}
