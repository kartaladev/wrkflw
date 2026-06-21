package persistence

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// Deduper is the stable public interface for idempotent-consumer deduplication
// (ADR-0018). It records processed message IDs inside the caller's own
// business transaction so the dedup record and the side effect commit
// atomically.
//
// The pgx.Tx parameter is a third-party DB type the consumer already owns —
// this is acceptable in the public interface because the consumer supplies their
// own transaction (obtained from pgxpool.Pool.Begin).
type Deduper interface {
	// Seen records (subscriber, messageID) within tx and reports whether this is
	// the FIRST time the pair was seen. firstTime==false means the message is a
	// duplicate and the caller should skip the side effect.
	Seen(ctx context.Context, tx pgx.Tx, subscriber, messageID string) (firstTime bool, err error)
}

// Compile-time check: internal concrete type must satisfy the public interface.
var _ Deduper = (*postgres.Deduper)(nil)

// NewDeduper constructs a Deduper over pool (returns the stable Deduper interface).
// The consumer must call persistence.Migrate before the first Seen call so the
// wrkflw_processed_message table exists.
func NewDeduper(pool *pgxpool.Pool) Deduper {
	return postgres.NewDeduper(pool)
}
