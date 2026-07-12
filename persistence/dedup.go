package persistence

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// Deduper is the stable public interface for idempotent-consumer deduplication
// (ADR-0018). It records processed message IDs inside the caller's own
// business transaction so the dedup record and the side effect commit
// atomically.
//
// # Breaking change (store unification, Phase 2)
//
// Deduper.Seen no longer takes an explicit transaction handle. It joins the
// ambient transaction stashed in ctx by the internal transaction helper, so the
// dedup record commits or rolls back together with the surrounding business
// unit. When no ambient transaction is present, Seen begins and commits a fresh
// leaf transaction so the call is always atomic. This also unifies the former
// separate Deduper (pgx.Tx) and MySQLDeduper (*sql.Tx) interfaces into one
// backend-neutral type — MySQLDeduper has been removed; use Deduper.
//
// Migration: replace
//
//	tx, _ := pool.Begin(ctx)
//	first, _ := d.Seen(ctx, tx, sub, id)
//	tx.Commit(ctx)
//
// with a call whose ctx already carries the ambient transaction (obtained from
// the engine's own commit path); a bare d.Seen(ctx, sub, id) with no ambient
// transaction transparently begins and commits its own leaf transaction.
type Deduper interface {
	// Seen records (subscriber, messageID) and reports whether this is the FIRST
	// time the pair was seen. firstTime==false means the message is a duplicate
	// and the caller should skip the side effect. Seen joins the ambient
	// transaction in ctx when present so the dedup record and the side effect
	// commit atomically; otherwise it commits its own leaf transaction.
	Seen(ctx context.Context, subscriber, messageID string) (firstTime bool, err error)

	// Prune deletes all processed-message records with a processed_at strictly
	// before before. Callers should supply a cutoff well past the relay
	// max-delivery × backoff window (e.g. retention = relay window + large safety
	// margin) so that in-flight messages are never evicted prematurely.
	// Returns the number of rows deleted.
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// Compile-time check: the neutral store Deduper satisfies the public interface.
var _ Deduper = (*store.Deduper)(nil)

// NewDeduper constructs a Deduper over pool (returns the stable Deduper interface).
// The consumer must call persistence.Migrate before the first Seen call so the
// wrkflw_processed_message table exists.
func NewDeduper(pool *pgxpool.Pool) (Deduper, error) {
	return store.NewDeduper(pool, dialect.NewPostgres())
}

// NewMySQLDeduper constructs a Deduper backed by a MySQL database (ADR-0018),
// using INSERT IGNORE into wrkflw_processed_message. MigrateMySQL must be called
// before the first Seen call so the table exists.
//
// It returns the same unified Deduper interface as NewDeduper (the Postgres
// analog) so the two backends are interchangeable at the consumer site. See the
// Deduper doc for the store-unification breaking change to Seen's signature.
func NewMySQLDeduper(db *sql.DB) (Deduper, error) {
	return store.NewDeduper(db, dialect.NewMySQL())
}
