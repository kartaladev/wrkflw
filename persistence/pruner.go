package persistence

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
)

// Pruner is the stable public interface for data-lifecycle retention pruning
// (ADR-0052). Each method deletes only safely-eligible rows older than the
// caller-supplied cutoff and returns the number of rows deleted, so a consumer
// can drive retention from their own scheduled job (they own the cron). Pruning
// is the consumer's responsibility — the library ships no daemon. See
// docs/retention.md for recommended cadences and cutoffs.
type Pruner interface {
	// PruneOutbox deletes published outbox rows (status='published') whose
	// published_at is strictly before cutoff. Pending and dead-lettered rows are
	// never touched. Returns the number of rows deleted.
	PruneOutbox(ctx context.Context, cutoff time.Time) (int64, error)

	// PruneCallLinks deletes call-link rows already delivered to their parent
	// (status='notified') whose notified_at is strictly before cutoff. Running
	// children and terminal-but-undelivered children survive — a parent may still
	// need to be resumed from them. Returns the number of rows deleted.
	PruneCallLinks(ctx context.Context, cutoff time.Time) (int64, error)

	// PruneChainLinks deletes process-chaining lineage rows whose created_at is
	// strictly before cutoff. Trade-off: this loses ancestry and the exactly-once
	// chaining backstop for the affected hops — choose a cutoff far beyond any
	// terminal-event redelivery window. Returns the number of rows deleted.
	PruneChainLinks(ctx context.Context, cutoff time.Time) (int64, error)

	// PruneProcessedMessages deletes idempotent-consumer dedup records whose
	// processed_at is strictly before cutoff (equivalent to Deduper.Prune). Supply
	// a cutoff well past the relay max-delivery × backoff window so in-flight
	// messages are never evicted. Returns the number of rows deleted.
	PruneProcessedMessages(ctx context.Context, cutoff time.Time) (int64, error)
}

// Compile-time check: the neutral store concrete type satisfies the public interface.
var _ Pruner = (*store.Pruner)(nil)

// NewPruner constructs a Pruner over pool (returns the stable Pruner interface).
// Migrate must have been applied before calling any method.
//
// Wire it into a scheduled job the consumer owns, e.g.:
//
//	pruner := persistence.NewPruner(pool)
//	// every hour, drop outbox events published more than 7 days ago:
//	_, err := pruner.PruneOutbox(ctx, time.Now().Add(-7*24*time.Hour))
func NewPruner(pool *pgxpool.Pool) Pruner {
	return store.NewPruner(pool, dialect.NewPostgres())
}
