package store

import (
	"context"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// Pruner deletes safely-eligible rows from the unbounded-growth tables so a
// consumer's scheduled retention job can keep them from overwhelming the
// database (ADR-0052). Every method deletes only rows older than a
// caller-supplied cutoff that are provably safe to drop, and returns the number
// of rows deleted. Pruning cadence and cutoffs are the consumer's
// responsibility — see docs/retention.md.
//
// All DELETE operations run against the pool directly (not inside a
// transaction) because retention pruning is a background maintenance operation
// that must not join a caller's ambient transaction or hold locks that block
// hot-path writes.
//
// Every timestamp cutoff is encoded via [timeArg] so the value is
// format-compatible with the values written by the store layer on every
// backend. On SQLite (TimestampsAsText) this ensures that the lexicographic
// TEXT comparison is apples-to-apples with the RFC3339Nano strings stored in
// the relevant columns (ADR-0080).
//
// Processed-message dedup records are pruned through [Deduper.Prune];
// [Pruner.PruneProcessedMessages] re-exposes that method for one-stop ergonomics.
//
// Pruner is safe for concurrent use: it carries no mutable state.
type Pruner struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// NewPruner constructs a Pruner over conn using dialect d. conn must be either
// a *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite). Migrate must be
// applied before calling any method.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	p := store.NewPruner(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	p := store.NewPruner(db, dialect.NewSQLite())
func NewPruner(conn any, d dialect.Dialect) *Pruner {
	return &Pruner{conn: conn, dialect: d}
}

// PruneOutbox deletes published outbox rows whose published_at is strictly
// before cutoff. Only status='published' rows are eligible: pending rows (not
// yet drained) and dead-lettered rows (awaiting operator redrive) are never
// touched. Returns the number of rows deleted.
//
// A safe cutoff is well past the relay's poll/backoff window so a row that was
// just published is never reclaimed before any late subscriber or replica has
// drained it.
func (p *Pruner) PruneOutbox(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune outbox: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(
			`DELETE FROM wrkflw_outbox
			  WHERE status = 'published' AND published_at < ?`),
		timeArg(p.dialect, cutoff.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune outbox: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune outbox: rows affected: %w", err)
	}
	return n, nil
}

// PruneCallLinks deletes call-link rows that have already been delivered to
// their parent — status='notified' with a notified_at strictly before cutoff.
//
// This predicate is deliberately conservative. A row is only eligible once the
// parent has been resumed (MarkNotified set status='notified' and stamped
// notified_at), so a row a parent might still need is never deleted:
//   - 'running' children (still executing) survive.
//   - 'completed'/'failed' children that are terminal but NOT yet notified
//     (notified_at IS NULL) survive — the notifier may still have to resume the
//     parent from them.
//
// Returns the number of rows deleted.
func (p *Pruner) PruneCallLinks(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune call links: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(
			`DELETE FROM wrkflw_call_links
			  WHERE status = 'notified' AND notified_at < ?`),
		timeArg(p.dialect, cutoff.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune call links: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune call links: rows affected: %w", err)
	}
	return n, nil
}

// PruneChainLinks deletes process-chaining lineage rows whose created_at is
// strictly before cutoff. Returns the number of rows deleted.
//
// Trade-off: chain links are ancestry (which predecessor produced which
// successor) and double as the exactly-once chaining backstop. Pruning them
// loses that ancestry for the affected hops and removes the backstop, so
// re-fire of a predecessor's terminal event after pruning could re-chain a
// successor. Choose a cutoff far beyond any window in which a terminal event
// could be redelivered. See docs/retention.md.
func (p *Pruner) PruneChainLinks(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune chain links: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(`DELETE FROM wrkflw_chain_links WHERE created_at < ?`),
		timeArg(p.dialect, cutoff.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune chain links: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune chain links: rows affected: %w", err)
	}
	return n, nil
}

// PruneProcessedMessages deletes idempotent-consumer dedup records whose
// processed_at is strictly before cutoff. It delegates to [Deduper.Prune];
// supply a cutoff well past the relay max-delivery × backoff window so
// in-flight messages are never evicted. Returns the number of rows deleted.
func (p *Pruner) PruneProcessedMessages(ctx context.Context, cutoff time.Time) (int64, error) {
	return NewDeduper(p.conn, p.dialect).Prune(ctx, cutoff)
}

// PruneTimers deletes timer rows whose fire_at is strictly before cutoff.
// Returns the number of rows deleted.
//
// Fired timers that are no longer needed can accumulate in wrkflw_timers; this
// method lets a consumer's retention job drop them. Choose a cutoff safely past
// any window in which a timer could still fire or be rescheduled.
//
// This method mirrors the MySQL-specific PruneTimers extension and is available
// on all three dialects in the neutral store.
func (p *Pruner) PruneTimers(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune timers: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(`DELETE FROM wrkflw_timers WHERE fire_at < ?`),
		timeArg(p.dialect, cutoff.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune timers: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune timers: rows affected: %w", err)
	}
	return n, nil
}
