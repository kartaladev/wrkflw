package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time checks that Store satisfies both ports.
var (
	_ runtime.Store         = (*Store)(nil)
	_ runtime.JournalReader = (*Store)(nil)
)

// Store is the MySQL-backed runtime.Store + JournalReader. It performs
// snapshot CAS + journal append + outbox inserts atomically in a single
// database/sql transaction per applied trigger.
type Store struct {
	db         *sql.DB
	historyCap int // <= 0 means no cap (full inline history)

	// staged telemetry option values; assembled into tel after all StoreOptions
	// have been applied in NewStore.
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	tel           observability.Telemetry
	storeDuration metric.Float64Histogram
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithHistoryCap bounds the inline History retained in the snapshot to every
// open visit plus at most n most-recent closed visits (ADR-0021). n <= 0 (the
// default) keeps full inline history. The wrkflw_journal table is unaffected
// and remains the complete audit source.
func WithHistoryCap(n int) StoreOption { return func(s *Store) { s.historyCap = n } }

// WithStoreLogger sets the structured logger used by the store for operation logs.
// Default: slog.Default().
func WithStoreLogger(l *slog.Logger) StoreOption {
	return func(s *Store) { s.logOpt = observability.WithLogger(l) }
}

// WithStoreTracerProvider sets the OTel TracerProvider for store operation spans.
// Default: the OTel global provider.
func WithStoreTracerProvider(tp trace.TracerProvider) StoreOption {
	return func(s *Store) { s.tpOpt = observability.WithTracerProvider(tp) }
}

// WithStoreMeterProvider sets the OTel MeterProvider for store metrics.
// Default: the OTel global provider.
func WithStoreMeterProvider(mp metric.MeterProvider) StoreOption {
	return func(s *Store) { s.mpOpt = observability.WithMeterProvider(mp) }
}

// NewStore constructs a Store over the given *sql.DB. The DB must already have
// migrations applied (see Migrate).
func NewStore(db *sql.DB, opts ...StoreOption) *Store {
	s := &Store{db: db}
	for _, o := range opts {
		o(s)
	}
	s.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/persistence",
		filterNilOpts(s.logOpt, s.tpOpt, s.mpOpt)...,
	)
	s.storeDuration = s.tel.Float64Histogram(
		"wrkflw_store_duration_seconds",
		"Duration of persistence Store operations in seconds",
	)
	return s
}

// filterNilOpts returns only the non-nil observability.Option values from opts.
func filterNilOpts(opts ...observability.Option) []observability.Option {
	out := make([]observability.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}

// endedAt extracts the optional EndedAt pointer from the state for DB writes.
func endedAt(st engine.InstanceState) *time.Time { return st.EndedAt }

// Create inserts a brand-new process instance from its first applied step.
// version is set to 1; journal seq is 1; outbox dedup_key is "<id>:1:<i>".
// All three writes are in one atomic transaction.
func (s *Store) Create(ctx context.Context, step runtime.AppliedStep) (runtime.Token, error) {
	const version int64 = 1

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: create: marshal snapshot: %w", err)
	}

	now := time.Now().UTC()
	_, execErr := tx.ExecContext(ctx,
		`INSERT INTO wrkflw_instances
		   (instance_id, def_id, def_version, status, snapshot, version, started_at, ended_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		step.State.InstanceID,
		step.State.DefID,
		step.State.DefVersion,
		int16(step.State.Status),
		snap,
		version,
		step.State.StartedAt,
		endedAt(step.State),
		now,
	)
	if execErr != nil {
		if isUniqueViolation(execErr) {
			return 0, runtime.ErrInstanceExists
		}
		return 0, fmt.Errorf("workflow-persistence-mysql: create: insert instance: %w", execErr)
	}

	if err := mysqlWriteJournal(ctx, tx, step, version, now); err != nil {
		return 0, mysqlMapConflict(err)
	}
	if err := mysqlWriteOutbox(ctx, tx, step.State.InstanceID, version, step.Events, now); err != nil {
		return 0, mysqlMapConflict(err)
	}

	if step.NewCallLink != nil {
		if err := mysqlInsertCallLink(ctx, tx, *step.NewCallLink, now); err != nil {
			return 0, mysqlMapConflict(err)
		}
	}

	if err := mysqlApplyTimerOps(ctx, tx, step); err != nil {
		return 0, mysqlMapConflict(err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: create: commit: %w", err)
	}
	return runtime.Token(version), nil
}

// Load returns the persisted snapshot and the current optimistic-concurrency
// token for the given instance. Returns runtime.ErrInstanceNotFound when no
// row exists for id.
func (s *Store) Load(ctx context.Context, id string) (engine.InstanceState, runtime.Token, error) {
	ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.load")
	defer span.End()
	start := time.Now()
	defer func() {
		s.storeDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("op", "load")))
	}()

	var snap []byte
	var version int64
	err := s.db.QueryRowContext(ctx,
		`SELECT snapshot, version FROM wrkflw_instances WHERE instance_id = ?`, id,
	).Scan(&snap, &version)
	if errors.Is(err, sql.ErrNoRows) {
		span.RecordError(runtime.ErrInstanceNotFound)
		span.SetStatus(otelcodes.Error, runtime.ErrInstanceNotFound.Error())
		return engine.InstanceState{}, 0, runtime.ErrInstanceNotFound
	}
	if err != nil {
		wrapped := fmt.Errorf("workflow-persistence-mysql: load %q: %w", id, err)
		span.RecordError(wrapped)
		span.SetStatus(otelcodes.Error, wrapped.Error())
		return engine.InstanceState{}, 0, wrapped
	}

	var st engine.InstanceState
	if err := json.Unmarshal(snap, &st); err != nil {
		wrapped := fmt.Errorf("workflow-persistence-mysql: load %q: unmarshal snapshot: %w", id, err)
		span.RecordError(wrapped)
		span.SetStatus(otelcodes.Error, wrapped.Error())
		return engine.InstanceState{}, 0, wrapped
	}
	return st, runtime.Token(version), nil
}

// Commit atomically applies one step against a running instance:
//   - CAS UPDATE on wrkflw_instances (WHERE version = expected → version+1),
//   - INSERT into wrkflw_journal (next seq),
//   - INSERT each event into wrkflw_outbox.
//
// Returns runtime.ErrConcurrentUpdate when the expected token is stale (another
// writer advanced the instance first) or when MySQL raises a deadlock or lock-wait
// timeout (error numbers 1213, 1205).
func (s *Store) Commit(ctx context.Context, expected runtime.Token, step runtime.AppliedStep) (runtime.Token, error) {
	ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.commit")
	defer span.End()
	start := time.Now()
	defer func() {
		s.storeDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("op", "commit")))
	}()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		wrapped := fmt.Errorf("workflow-persistence-mysql: commit: begin: %w", err)
		span.RecordError(wrapped)
		span.SetStatus(otelcodes.Error, wrapped.Error())
		return 0, wrapped
	}
	defer func() { _ = tx.Rollback() }()

	// spanErr records err on span and sets Error status; used on early returns.
	spanErr := func(err error) {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())
	}

	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		wrapped := fmt.Errorf("workflow-persistence-mysql: commit: marshal snapshot: %w", err)
		spanErr(wrapped)
		return 0, wrapped
	}

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx,
		`UPDATE wrkflw_instances
		    SET version=version+1, snapshot=?, status=?, ended_at=?, updated_at=?
		  WHERE instance_id=? AND version=?`,
		snap,
		int16(step.State.Status),
		endedAt(step.State),
		now,
		step.State.InstanceID,
		int64(expected),
	)
	if err != nil {
		if isConcurrencyError(err) {
			span.SetAttributes(attribute.Bool("wrkflw.concurrent_update", true))
			return 0, runtime.ErrConcurrentUpdate
		}
		mapped := mysqlMapConflict(fmt.Errorf("workflow-persistence-mysql: commit: update: %w", err))
		spanErr(mapped)
		return 0, mapped
	}
	rows, err := result.RowsAffected()
	if err != nil {
		wrapped := fmt.Errorf("workflow-persistence-mysql: commit: rows affected: %w", err)
		spanErr(wrapped)
		return 0, wrapped
	}
	if rows == 0 {
		// Version mismatch: another writer advanced the token first. This is
		// expected optimistic-concurrency control flow (the runner retries on
		// ErrConcurrentUpdate), NOT a failure — record it as a contention
		// attribute rather than marking the span as an error.
		span.SetAttributes(attribute.Bool("wrkflw.concurrent_update", true))
		return 0, runtime.ErrConcurrentUpdate
	}

	next := int64(expected) + 1 // 1:1 with journal seq (journal seq == version after commit)

	if err := mysqlWriteJournal(ctx, tx, step, next, now); err != nil {
		mapped := mysqlMapConflict(err)
		spanErr(mapped)
		return 0, mapped
	}
	if err := mysqlWriteOutbox(ctx, tx, step.State.InstanceID, next, step.Events, now); err != nil {
		mapped := mysqlMapConflict(err)
		spanErr(mapped)
		return 0, mapped
	}

	if step.CallOutcome != nil {
		if err := mysqlFlipCallLink(ctx, tx, step.State.InstanceID, *step.CallOutcome, now); err != nil {
			mapped := mysqlMapConflict(err)
			spanErr(mapped)
			return 0, mapped
		}
	}

	if err := mysqlApplyTimerOps(ctx, tx, step); err != nil {
		mapped := mysqlMapConflict(err)
		spanErr(mapped)
		return 0, mapped
	}

	if err := tx.Commit(); err != nil {
		if isConcurrencyError(err) {
			span.SetAttributes(attribute.Bool("wrkflw.concurrent_update", true))
			return 0, runtime.ErrConcurrentUpdate
		}
		mapped := mysqlMapConflict(fmt.Errorf("workflow-persistence-mysql: commit: %w", err))
		spanErr(mapped)
		return 0, mapped
	}
	return runtime.Token(next), nil
}

// Entries returns the recorded trigger history for the given instance id,
// ordered by journal seq ascending.
func (s *Store) Entries(ctx context.Context, id string) ([]engine.Trigger, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, trigger_ FROM wrkflw_journal WHERE instance_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: entries %q: %w", id, err)
	}
	defer func() { _ = rows.Close() }()

	var triggers []engine.Trigger
	for rows.Next() {
		var kind string
		var data []byte
		if err := rows.Scan(&kind, &data); err != nil {
			return nil, fmt.Errorf("workflow-persistence-mysql: entries %q: scan: %w", id, err)
		}
		trg, err := UnmarshalTrigger(kind, data)
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, trg)
	}
	return triggers, rows.Err()
}

// mysqlWriteJournal inserts one row into wrkflw_journal inside the given transaction.
// Note: the column is named trigger_ (not trigger) because trigger is a MySQL reserved word.
func mysqlWriteJournal(ctx context.Context, db DBTX, step runtime.AppliedStep, seq int64, appliedAt time.Time) error {
	data, kind, err := MarshalTrigger(step.Trigger)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_journal (instance_id, seq, kind, trigger_, occurred_at, applied_at)
		 VALUES (?,?,?,?,?,?)`,
		step.State.InstanceID, seq, kind, data, step.Trigger.OccurredAt(), appliedAt,
	); err != nil {
		return fmt.Errorf("workflow-persistence-mysql: write journal: %w", err)
	}
	return nil
}

// mysqlWriteOutbox inserts one row per event into wrkflw_outbox inside the given
// transaction. The dedup_key is "<instanceID>:<seq>:<eventIndex>" — globally
// unique per applied step because (instanceID, seq) is unique per journal row.
func mysqlWriteOutbox(ctx context.Context, db DBTX, instanceID string, seq int64, events []runtime.OutboxEvent, createdAt time.Time) error {
	for i, ev := range events {
		payload, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("workflow-persistence-mysql: write outbox: marshal payload: %w", err)
		}
		dedup := fmt.Sprintf("%s:%d:%d", instanceID, seq, i)
		if _, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at, definition_ref)
			 VALUES (?,?,?,?,?,?)`,
			instanceID, ev.Topic, payload, dedup, createdAt, ev.DefinitionRef,
		); err != nil {
			return fmt.Errorf("workflow-persistence-mysql: write outbox: %w", err)
		}
	}
	return nil
}

// mysqlMapConflict translates a MySQL deadlock/lock-wait timeout into
// runtime.ErrConcurrentUpdate so callers do not need to depend on the MySQL
// driver. All other errors pass through unchanged.
func mysqlMapConflict(err error) error {
	if isConcurrencyError(err) {
		return runtime.ErrConcurrentUpdate
	}
	return err
}

// mysqlInsertCallLink writes a new wrkflw_call_links row with status='running' inside
// the given transaction. It is called during Store.Create when the applied step
// carries a NewCallLink — the insert is atomic with the child instance INSERT
// (ADR-0025 crash-safety seam).
func mysqlInsertCallLink(ctx context.Context, db DBTX, link runtime.CallLink, createdAt time.Time) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO wrkflw_call_links
		   (child_instance_id, parent_instance_id, parent_command_id, parent_def_id, parent_def_version, depth, status, created_at)
		 VALUES (?,?,?,?,?,?,'running',?)`,
		link.ChildInstanceID,
		link.ParentInstanceID,
		link.ParentCommandID,
		link.ParentDefID,
		link.ParentDefVersion,
		link.Depth,
		createdAt,
	); err != nil {
		return fmt.Errorf("workflow-persistence-mysql: create: call link: %w", err)
	}
	return nil
}

// mysqlUpsertTimer writes (or updates) a wrkflw_timers row inside tx, atomic with
// the state commit (ADR-0027). Re-arming the same (instance, timer) overwrites FireAt.
func mysqlUpsertTimer(ctx context.Context, db DBTX, t runtime.ArmedTimer) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO wrkflw_timers (instance_id, timer_id, fire_at, kind, def_id, def_version)
		VALUES (?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE fire_at=VALUES(fire_at), kind=VALUES(kind),
		                        def_id=VALUES(def_id), def_version=VALUES(def_version)`,
		t.InstanceID, t.TimerID, t.FireAt, int16(t.Kind), t.DefID, t.DefVersion)
	if err != nil {
		return fmt.Errorf("workflow-persistence-mysql: upsert timer %q/%q: %w", t.InstanceID, t.TimerID, err)
	}
	return nil
}

// mysqlDeleteTimer removes a wrkflw_timers row inside tx (fired or cancelled). A
// zero-row delete is fine (idempotent / already gone).
func mysqlDeleteTimer(ctx context.Context, db DBTX, instanceID, timerID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM wrkflw_timers WHERE instance_id=? AND timer_id=?`,
		instanceID, timerID)
	if err != nil {
		return fmt.Errorf("workflow-persistence-mysql: delete timer %q/%q: %w", instanceID, timerID, err)
	}
	return nil
}

// mysqlApplyTimerOps applies a step's timer arms and cancels within tx.
func mysqlApplyTimerOps(ctx context.Context, db DBTX, step runtime.AppliedStep) error {
	for _, a := range step.TimerArms {
		if err := mysqlUpsertTimer(ctx, db, a); err != nil {
			return err
		}
	}
	for _, id := range step.TimerCancels {
		if err := mysqlDeleteTimer(ctx, db, step.State.InstanceID, id); err != nil {
			return err
		}
	}
	return nil
}

// mysqlFlipCallLink updates the wrkflw_call_links row for childInstanceID to the
// terminal status implied by outcome (completed or failed) inside the given
// transaction. It is called during Store.Commit when the applied step carries a
// CallOutcome — the flip is atomic with the snapshot UPDATE (ADR-0025).
//
// For a root instance (no link row) the UPDATE affects zero rows, which is a
// clean no-op; the caller must NOT treat zero rows as an error.
func mysqlFlipCallLink(ctx context.Context, db DBTX, childInstanceID string, outcome runtime.CallOutcome, updatedAt time.Time) error {
	status := "failed"
	if outcome.Completed {
		status = "completed"
	}

	var outputJSON []byte
	if outcome.Completed && len(outcome.Output) > 0 {
		b, err := json.Marshal(outcome.Output)
		if err != nil {
			return fmt.Errorf("workflow-persistence-mysql: commit: call link: marshal output: %w", err)
		}
		outputJSON = b
	}

	var errText *string
	if !outcome.Completed && outcome.Err != "" {
		errText = &outcome.Err
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE wrkflw_call_links
		    SET status=?, output=?, error=?
		  WHERE child_instance_id=?`,
		status,
		outputJSON,
		errText,
		childInstanceID,
	); err != nil {
		return fmt.Errorf("workflow-persistence-mysql: commit: call link: %w", err)
	}
	// Zero rows affected = root instance (no link row) — clean no-op, not an error.
	return nil
}
