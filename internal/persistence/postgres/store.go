package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time checks that Store satisfies both ports.
var (
	_ runtime.Store         = (*Store)(nil)
	_ runtime.JournalReader = (*Store)(nil)
)

// outboxNotifyChannel is the Postgres NOTIFY channel the relay listens on
// (ADR-0022). The notification carries no payload — it is a bare wakeup; the
// relay still claims rows via FOR UPDATE SKIP LOCKED.
const outboxNotifyChannel = "wrkflw_outbox"

// Store is the Postgres-backed runtime.Store + JournalReader. It performs
// snapshot CAS + journal append + outbox inserts atomically in a single pgx.Tx
// per applied trigger.
type Store struct {
	pool       *pgxpool.Pool
	historyCap int  // <= 0 means no cap (full inline history)
	notify     bool // emit NOTIFY wrkflw_outbox on outbox insert when true
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithHistoryCap bounds the inline History retained in the snapshot to every
// open visit plus at most n most-recent closed visits (ADR-0021). n <= 0 (the
// default) keeps full inline history. The wrkflw_journal table is unaffected
// and remains the complete audit source.
func WithHistoryCap(n int) StoreOption { return func(s *Store) { s.historyCap = n } }

// WithOutboxNotify makes Create/Commit emit NOTIFY wrkflw_outbox inside the
// committing transaction whenever the step inserted at least one outbox row, so
// a listening relay (WithListenNotify) wakes immediately instead of waiting for
// its next poll tick. Steps that produce no events emit no notification.
func WithOutboxNotify() StoreOption { return func(s *Store) { s.notify = true } }

// maybeNotify issues a transactional NOTIFY when notify is enabled and the step
// produced outbox events. Errors propagate so the whole step rolls back.
func (s *Store) maybeNotify(ctx context.Context, db DBTX, events []runtime.OutboxEvent) error {
	if !s.notify || len(events) == 0 {
		return nil
	}
	// Channel name cannot be parameterized; it is a fixed constant.
	if _, err := db.Exec(ctx, "NOTIFY "+outboxNotifyChannel); err != nil {
		return fmt.Errorf("postgres: notify outbox: %w", err)
	}
	return nil
}

// NewStore constructs a Store over the given pool. The pool must already have
// migrations applied (see Migrate).
func NewStore(pool *pgxpool.Pool, opts ...StoreOption) *Store {
	s := &Store{pool: pool}
	for _, o := range opts {
		o(s)
	}
	return s
}

// endedAt extracts the optional EndedAt pointer from the state for DB writes.
func endedAt(st engine.InstanceState) *time.Time { return st.EndedAt }

// Create inserts a brand-new process instance from its first applied step.
// version is set to 1; journal seq is 1; outbox dedup_key is "<id>:1:<i>".
// All three writes are in one atomic transaction.
func (s *Store) Create(ctx context.Context, step runtime.AppliedStep) (runtime.Token, error) {
	const version int64 = 1

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		return 0, fmt.Errorf("postgres: create: marshal snapshot: %w", err)
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(ctx,
		`INSERT INTO wrkflw_instances
		   (instance_id, def_id, def_version, status, snapshot, version, started_at, ended_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		step.State.InstanceID,
		step.State.DefID,
		step.State.DefVersion,
		int16(step.State.Status),
		snap,
		version,
		step.State.StartedAt,
		endedAt(step.State),
		now,
	); err != nil {
		return 0, fmt.Errorf("postgres: create: insert instance: %w", err)
	}

	if err := writeJournal(ctx, tx, step, version, now); err != nil {
		return 0, mapConflict(err)
	}
	if err := writeOutbox(ctx, tx, step.State.InstanceID, version, step.Events, now); err != nil {
		return 0, mapConflict(err)
	}
	if err := s.maybeNotify(ctx, tx, step.Events); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: create: commit: %w", err)
	}
	return runtime.Token(version), nil
}

// Load returns the persisted snapshot and the current optimistic-concurrency
// token for the given instance. Returns runtime.ErrInstanceNotFound when no
// row exists for id.
func (s *Store) Load(ctx context.Context, id string) (engine.InstanceState, runtime.Token, error) {
	var snap []byte
	var version int64
	err := s.pool.QueryRow(ctx,
		`SELECT snapshot, version FROM wrkflw_instances WHERE instance_id = $1`, id,
	).Scan(&snap, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.InstanceState{}, 0, runtime.ErrInstanceNotFound
	}
	if err != nil {
		return engine.InstanceState{}, 0, fmt.Errorf("postgres: load %q: %w", id, err)
	}

	var st engine.InstanceState
	if err := json.Unmarshal(snap, &st); err != nil {
		return engine.InstanceState{}, 0, fmt.Errorf("postgres: load %q: unmarshal snapshot: %w", id, err)
	}
	return st, runtime.Token(version), nil
}

// Commit atomically applies one step against a running instance:
//   - CAS UPDATE on wrkflw_instances (WHERE version = expected → version+1),
//   - INSERT into wrkflw_journal (next seq),
//   - INSERT each event into wrkflw_outbox.
//
// Returns runtime.ErrConcurrentUpdate when the expected token is stale (another
// writer advanced the instance first) or when Postgres raises SQLSTATE 40001
// (serialization failure).
func (s *Store) Commit(ctx context.Context, expected runtime.Token, step runtime.AppliedStep) (runtime.Token, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: commit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		return 0, fmt.Errorf("postgres: commit: marshal snapshot: %w", err)
	}

	now := time.Now().UTC()
	tag, err := tx.Exec(ctx,
		`UPDATE wrkflw_instances
		    SET snapshot = $1, version = version + 1, status = $2, ended_at = $3, updated_at = $4
		  WHERE instance_id = $5 AND version = $6`,
		snap,
		int16(step.State.Status),
		endedAt(step.State),
		now,
		step.State.InstanceID,
		int64(expected),
	)
	if err != nil {
		return 0, mapConflict(fmt.Errorf("postgres: commit: update: %w", err))
	}
	if tag.RowsAffected() == 0 {
		// version mismatch: another writer advanced the token first.
		return 0, runtime.ErrConcurrentUpdate
	}

	next := int64(expected) + 1 // 1:1 with journal seq (journal seq == version after commit)

	if err := writeJournal(ctx, tx, step, next, now); err != nil {
		return 0, mapConflict(err)
	}
	if err := writeOutbox(ctx, tx, step.State.InstanceID, next, step.Events, now); err != nil {
		return 0, mapConflict(err)
	}
	if err := s.maybeNotify(ctx, tx, step.Events); err != nil {
		return 0, mapConflict(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, mapConflict(fmt.Errorf("postgres: commit: %w", err))
	}
	return runtime.Token(next), nil
}

// Entries returns the recorded trigger history for the given instance id,
// ordered by journal seq ascending.
func (s *Store) Entries(ctx context.Context, id string) ([]engine.Trigger, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT kind, trigger FROM wrkflw_journal WHERE instance_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: entries %q: %w", id, err)
	}
	defer rows.Close()

	var triggers []engine.Trigger
	for rows.Next() {
		var kind string
		var data []byte
		if err := rows.Scan(&kind, &data); err != nil {
			return nil, fmt.Errorf("postgres: entries %q: scan: %w", id, err)
		}
		trg, err := UnmarshalTrigger(kind, data)
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, trg)
	}
	return triggers, rows.Err()
}

// writeJournal inserts one row into wrkflw_journal inside the given transaction.
// seq must equal the new version written to wrkflw_instances by Create/Commit.
func writeJournal(ctx context.Context, db DBTX, step runtime.AppliedStep, seq int64, appliedAt time.Time) error {
	data, kind, err := MarshalTrigger(step.Trigger)
	if err != nil {
		return err
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO wrkflw_journal (instance_id, seq, kind, trigger, occurred_at, applied_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		step.State.InstanceID, seq, kind, data, step.Trigger.OccurredAt(), appliedAt,
	); err != nil {
		return fmt.Errorf("postgres: write journal: %w", err)
	}
	return nil
}

// writeOutbox inserts one row per event into wrkflw_outbox inside the given
// transaction. The dedup_key is "<instanceID>:<seq>:<eventIndex>" — globally
// unique per applied step because (instanceID, seq) is unique per journal row.
func writeOutbox(ctx context.Context, db DBTX, instanceID string, seq int64, events []runtime.OutboxEvent, createdAt time.Time) error {
	for i, ev := range events {
		payload, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("postgres: write outbox: marshal payload: %w", err)
		}
		dedup := fmt.Sprintf("%s:%d:%d", instanceID, seq, i)
		if _, err := db.Exec(ctx,
			`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
			 VALUES ($1,$2,$3,$4,$5)`,
			instanceID, ev.Topic, payload, dedup, createdAt,
		); err != nil {
			return fmt.Errorf("postgres: write outbox: %w", err)
		}
	}
	return nil
}

// mapConflict translates a Postgres serialization failure (SQLSTATE 40001) into
// runtime.ErrConcurrentUpdate so callers do not need to depend on pgconn. All
// other errors pass through unchanged.
func mapConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40001" {
		return runtime.ErrConcurrentUpdate
	}
	return err
}
