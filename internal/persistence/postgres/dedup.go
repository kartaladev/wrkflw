package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deduper records processed message IDs in wrkflw_processed_message so an
// at-least-once consumer can achieve exactly-once effect (idempotent-consumer
// pattern, ADR-0018). The dedup record is written inside the caller's own
// business transaction so it commits atomically with the side effect.
type Deduper struct{ pool *pgxpool.Pool }

// NewDeduper constructs a Deduper backed by pool. The pool is retained for
// symmetry with the other constructors in this package; Seen operates on the
// caller-supplied tx.
func NewDeduper(pool *pgxpool.Pool) *Deduper { return &Deduper{pool: pool} }

// Seen records (subscriber, messageID) within the caller's transaction and
// reports whether this is the FIRST time the pair was seen. firstTime==false
// means the message is a duplicate and the caller should skip the side effect.
//
// The insert runs inside tx so the dedup record commits atomically with the
// caller's work. The underlying SQL uses ON CONFLICT DO NOTHING against the
// PRIMARY KEY (subscriber, message_id), so concurrent inserts of the same pair
// within the same or different transactions resolve without error.
func (d *Deduper) Seen(ctx context.Context, tx pgx.Tx, subscriber, messageID string) (firstTime bool, err error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO wrkflw_processed_message (subscriber, message_id)
		 VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		subscriber, messageID)
	if err != nil {
		return false, fmt.Errorf("workflow-postgres: deduper: seen: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Prune deletes all processed-message records with a processed_at strictly
// before before. Callers should supply a cutoff well past the relay
// max-delivery × backoff window so in-flight messages are never evicted.
// Returns the number of rows deleted.
func (d *Deduper) Prune(ctx context.Context, before time.Time) (int64, error) {
	tag, err := d.pool.Exec(ctx,
		`DELETE FROM wrkflw_processed_message WHERE processed_at < $1`,
		before)
	if err != nil {
		return 0, fmt.Errorf("workflow-postgres: deduper: prune: %w", err)
	}
	return tag.RowsAffected(), nil
}
