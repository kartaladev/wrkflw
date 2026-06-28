package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Deduper records processed message IDs in wrkflw_processed_message so an
// at-least-once consumer can achieve exactly-once effect (idempotent-consumer
// pattern, ADR-0018). The dedup record is written inside the caller's own
// business transaction so it commits atomically with the side effect.
//
// MySQL uses INSERT IGNORE (vs Postgres's ON CONFLICT DO NOTHING); RowsAffected
// == 1 means first-seen, 0 means duplicate.
type Deduper struct{ db *sql.DB }

// NewDeduper constructs a Deduper backed by db. The db is retained for Prune;
// Seen operates on the caller-supplied *sql.Tx.
func NewDeduper(db *sql.DB) *Deduper { return &Deduper{db: db} }

// Seen records (subscriber, messageID) within the caller's transaction and
// reports whether this is the FIRST time the pair was seen. firstTime==false
// means the message is a duplicate and the caller should skip the side effect.
//
// The insert runs inside tx so the dedup record commits atomically with the
// caller's work. INSERT IGNORE against the PRIMARY KEY (subscriber, message_id)
// resolves concurrent inserts of the same pair without error — the duplicate row
// is silently skipped and RowsAffected returns 0.
func (d *Deduper) Seen(ctx context.Context, tx *sql.Tx, subscriber, messageID string) (firstTime bool, err error) {
	res, err := tx.ExecContext(ctx,
		`INSERT IGNORE INTO wrkflw_processed_message (subscriber, message_id)
		 VALUES (?, ?)`,
		subscriber, messageID)
	if err != nil {
		return false, fmt.Errorf("workflow-persistence-mysql: deduper: seen: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("workflow-persistence-mysql: deduper: seen: rows affected: %w", err)
	}
	return n == 1, nil
}

// Prune deletes all processed-message records with a processed_at strictly
// before before. Callers should supply a cutoff well past the relay
// max-delivery × backoff window so in-flight messages are never evicted.
// Returns the number of rows deleted.
func (d *Deduper) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := d.db.ExecContext(ctx,
		`DELETE FROM wrkflw_processed_message WHERE processed_at < ?`,
		before)
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: deduper: prune: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: deduper: prune: rows affected: %w", err)
	}
	return n, nil
}
